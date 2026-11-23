package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliWorkflow is a two-node manual workflow: trigger -> data.set. It records
// a replayable run under the simulated host without touching the network.
const cliWorkflow = `{
  "id": "cli-lifecycle", "name": "CLI lifecycle", "version": 1,
  "settings": {},
  "nodes": [
    { "id": "start", "type": "trigger.manual", "typeVersion": 1, "name": "Start",
      "position": { "x": 0, "y": 0 }, "config": { "fields": [] } },
    { "id": "fetch", "type": "http.request", "typeVersion": 1, "name": "Fetch status",
      "position": { "x": 220, "y": 0 },
      "config": { "method": "GET", "url": "https://api.example.test/status" } },
    { "id": "set", "type": "data.set", "typeVersion": 1, "name": "Build item",
      "position": { "x": 440, "y": 0 },
      "config": { "values": { "ok": true }, "keepOnlySet": true } }
  ],
  "edges": [
    { "from": { "node": "start", "port": "main" }, "to": { "node": "fetch", "port": "main" } },
    { "from": { "node": "fetch", "port": "main" }, "to": { "node": "set", "port": "main" } }
  ]
}`

// seedRun records one run of the shared workflow and returns its run ID.
// --record-effects captures the effect tape so verify and hermetic replay
// work against the recorded run.
func seedRun(t *testing.T, dataDir, workflowPath string) string {
	t.Helper()
	if err := runFlow([]string{"--data", dataDir, "--record-effects", workflowPath}, true); err != nil {
		t.Fatalf("loom test: %v", err)
	}
	st, err := openStore(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	runs, err := st.ListRuns("cli-lifecycle", 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) == 0 {
		t.Fatal("recorded run not found")
	}
	return runs[0].ID
}

func writeCLIWorkflow(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cli-lifecycle.json")
	if err := os.WriteFile(path, []byte(cliWorkflow), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestBackupVerifyRestoreEndToEnd covers the whole backup loop: create,
// verify, restore into a clean directory, and the failure modes (existing
// target, verification while a pinned secret would use the wrong key).
func TestBackupVerifyRestoreEndToEnd(t *testing.T) {
	t.Setenv("LOOM_SECRET", "")
	t.Setenv("LOOM_SECRET_KEY_FILE", "")
	dataDir := t.TempDir()
	workflow := writeCLIWorkflow(t)
	seedRun(t, dataDir, workflow)

	archive := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := runBackup([]string{"--data", dataDir, "--out", archive}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := runBackupVerify([]string{"--backup", archive}); err != nil {
		t.Fatalf("backup verify: %v", err)
	}

	restored := filepath.Join(t.TempDir(), "restored")
	if err := runBackupRestore([]string{"--backup", archive, "--to", restored}); err != nil {
		t.Fatalf("backup restore: %v", err)
	}
	for _, name := range []string{"loom.db", "keys"} {
		if _, serr := os.Stat(filepath.Join(restored, name)); serr != nil {
			t.Fatalf("restore missing %s: %v", name, serr)
		}
	}
	// The restored directory must be a working store: the recorded run
	// still verifies from it.
	st, err := openStore(restored)
	if err != nil {
		t.Fatalf("open restored store: %v", err)
	}
	runs, err := st.ListRuns("cli-lifecycle", 5)
	if err != nil || len(runs) != 1 {
		st.Close()
		t.Fatalf("restored store runs = %v (err %v), want the recorded run", runs, err)
	}
	runID := runs[0].ID
	st.Close()
	if err := runVerify([]string{"--data", restored, "--from-run", runID}); err != nil {
		t.Fatalf("verify on restored data: %v", err)
	}

	// Restoring onto an existing directory must refuse.
	if err := runBackupRestore([]string{"--backup", archive, "--to", restored}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("restore onto existing dir: err = %v, want already-exists refusal", err)
	}

	// Verification with a pinned secret would use the wrong key: refuse.
	t.Setenv("LOOM_SECRET", "pinned-secret-value")
	if err := runBackupVerify([]string{"--backup", archive}); err == nil || !strings.Contains(err.Error(), "LOOM_SECRET") {
		t.Fatalf("verify with pinned secret: err = %v, want pinned-secret refusal", err)
	}
}

// TestKeyRotationAcrossRuns rotates the signing key between two recorded
// runs; both must keep verifying afterwards, and rotation must refuse while
// a secret is pinned.
func TestKeyRotationAcrossRuns(t *testing.T) {
	t.Setenv("LOOM_SECRET", "")
	t.Setenv("LOOM_SECRET_KEY_FILE", "")
	dataDir := t.TempDir()
	workflow := writeCLIWorkflow(t)
	first := seedRun(t, dataDir, workflow)

	if err := runKey([]string{"rotate", "--data", dataDir}); err != nil {
		t.Fatalf("key rotate: %v", err)
	}
	second := seedRun(t, dataDir, workflow)

	if err := runVerify([]string{"--data", dataDir, "--from-run", first}); err != nil {
		t.Fatalf("pre-rotation run no longer verifies: %v", err)
	}
	if err := runVerify([]string{"--data", dataDir, "--from-run", second}); err != nil {
		t.Fatalf("post-rotation run does not verify: %v", err)
	}

	// Rotation must refuse in single-key mode.
	t.Setenv("LOOM_SECRET", "pinned-secret-value")
	if err := runKey([]string{"rotate", "--data", dataDir}); err == nil {
		t.Fatal("rotation with pinned secret succeeded; want refusal")
	}
}

// TestDoctorDetectsTamperedEvent proves the CLI surface of tamper
// detection: doctor passes on a healthy store and fails after an event
// row is modified behind the store's back.
func TestDoctorDetectsTamperedEvent(t *testing.T) {
	t.Setenv("LOOM_SECRET", "")
	t.Setenv("LOOM_SECRET_KEY_FILE", "")
	dataDir := t.TempDir()
	workflow := writeCLIWorkflow(t)
	runID := seedRun(t, dataDir, workflow)

	if err := runDoctor([]string{"--data", dataDir}); err != nil {
		t.Fatalf("doctor on healthy store: %v", err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dataDir, "loom.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec(`UPDATE events SET output_hash = 'tampered' WHERE run_id = ?`, runID); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	db.Close()

	err = runDoctor([]string{"--data", dataDir})
	if err == nil {
		t.Fatal("doctor accepted a tampered event")
	}
	if !strings.Contains(err.Error(), "chain") {
		t.Fatalf("doctor error should report chain findings: %v", err)
	}
}

// TestReplayModesAndFlagValidation covers the replay flag matrix and the
// three step modes (recorded, hermetic, from-node hermetic).
func TestReplayModesAndFlagValidation(t *testing.T) {
	dataDir := t.TempDir()
	workflow := writeCLIWorkflow(t)
	runID := seedRun(t, dataDir, workflow)

	if err := runReplay([]string{"--data", dataDir, runID}); err != nil {
		t.Fatalf("full replay: %v", err)
	}
	if err := runReplay([]string{"--data", dataDir, "--step", "fetch", runID}); err != nil {
		t.Fatalf("step replay: %v", err)
	}
	if err := runReplay([]string{"--data", dataDir, "--step", "fetch", "--hermetic", runID}); err != nil {
		t.Fatalf("hermetic step replay: %v", err)
	}
	if err := runReplay([]string{"--data", dataDir, "--from", "start", "--hermetic", runID}); err != nil {
		t.Fatalf("hermetic from-node replay: %v", err)
	}

	for name, args := range map[string][]string{
		"step and from together":  {"--data", dataDir, "--step", "fetch", "--from", "start", runID},
		"execute without step":    {"--data", dataDir, "--execute", runID},
		"hermetic without target": {"--data", dataDir, "--hermetic", runID},
		"hermetic and execute":    {"--data", dataDir, "--step", "fetch", "--hermetic", "--execute", runID},
	} {
		if err := runReplay(args); err == nil {
			t.Fatalf("%s: expected an error", name)
		}
	}
}

// TestPruneDryRunThenPrune covers retention: dry-run reports and deletes
// nothing, the real prune removes the recorded runs.
func TestPruneDryRunThenPrune(t *testing.T) {
	dataDir := t.TempDir()
	workflow := writeCLIWorkflow(t)
	seedRun(t, dataDir, workflow)

	if err := runPrune([]string{"--data", dataDir, "--before", "1h", "--dry-run"}); err != nil {
		t.Fatalf("prune dry-run: %v", err)
	}
	st, err := openStore(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	runs, err := st.ListRuns("cli-lifecycle", 5)
	st.Close()
	if err != nil || len(runs) != 1 {
		t.Fatalf("dry-run must not delete: runs=%d err=%v", len(runs), err)
	}

	future := "2999-01-01T00:00:00Z"
	if err := runPrune([]string{"--data", dataDir, "--before", future}); err != nil {
		t.Fatalf("prune: %v", err)
	}
	st, err = openStore(dataDir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	runs, err = st.ListRuns("cli-lifecycle", 5)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 0 {
		t.Fatalf("prune left %d runs behind", len(runs))
	}
}
