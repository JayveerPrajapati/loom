package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// buildLegacyDataDir creates a data directory in the pre-migration format:
// no chain, key, parent or resume columns on runs; no hash columns on
// events; no published_version on workflows; schema_version 1; a legacy
// secret.key; and one blob referenced by an event's input_hash.
func buildLegacyDataDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), secret, 0o600); err != nil {
		t.Fatal(err)
	}

	payload := []byte("legacy payload")
	sum := sha256.Sum256(payload)
	blobHash := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(filepath.Join(dir, "blobs", blobHash[:2]), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "blobs", blobHash[:2], blobHash), payload, 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version (version) VALUES (1);

CREATE TABLE workflows (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE runs (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	workflow_version TEXT NOT NULL,
	status TEXT NOT NULL,
	trigger TEXT NOT NULL,
	started_at TEXT NOT NULL,
	finished_at TEXT,
	error TEXT,
	resume_node TEXT,
	resume_token TEXT,
	resume_data BLOB
);

CREATE TABLE events (
	run_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	type TEXT NOT NULL,
	at TEXT NOT NULL,
	node_id TEXT,
	attempt INTEGER,
	input_hash TEXT,
	output_hash TEXT,
	duration_ms INTEGER,
	error TEXT,
	data BLOB,
	PRIMARY KEY (run_id, seq)
);

CREATE TABLE blobs (
	hash TEXT PRIMARY KEY,
	size INTEGER NOT NULL,
	created_at TEXT NOT NULL
);

INSERT INTO workflows (id, name, version, doc, updated_at) VALUES
	('wf', 'legacy workflow', 3, '{"id":"wf","name":"legacy workflow","nodes":[],"edges":[]}', '2026-01-01T00:00:00Z');

INSERT INTO runs (id, workflow_id, workflow_version, status, trigger, started_at, finished_at)
	VALUES ('run-old', 'wf', '3', 'done', 'manual',
		'2026-01-01T00:00:01Z', '2026-01-01T00:00:02Z');

INSERT INTO events (run_id, seq, type, at, node_id, input_hash, data) VALUES
	('run-old', 1, 'run.started', '2026-01-01T00:00:01Z', NULL, NULL, X'7B7D'),
	('run-old', 2, 'node.finished', '2026-01-01T00:00:02Z', 'news', '` + blobHash + `', X'7B7D');

INSERT INTO blobs (hash, size, created_at) VALUES ('` + blobHash + `', ` + strconv.Itoa(len(payload)) + `, '2026-01-01T00:00:02Z');
`); err != nil {
		t.Fatal(err)
	}
	return dir, blobHash
}

// Opening a pre-migration data directory must upgrade it in place: the old
// rows survive, the legacy signing key is stamped, the previously unsigned
// events become a verifying chain, and the store passes the same health
// check `loom doctor` runs.
func TestOpenUpgradesLegacyDataDir(t *testing.T) {
	dir, blobHash := buildLegacyDataDir(t)

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open legacy dir: %v", err)
	}
	defer s.Close()

	run, err := s.GetRun("run-old")
	if err != nil {
		t.Fatalf("get upgraded run: %v", err)
	}
	if run.Status != "done" || run.WorkflowID != "wf" || run.Trigger != "manual" {
		t.Fatalf("upgraded run lost its data: %+v", run)
	}

	// The keyring stamped the legacy key id onto the pre-keyring run.
	var keyID string
	if err := s.db.QueryRow(`SELECT key_id FROM runs WHERE id = 'run-old'`).Scan(&keyID); err != nil {
		t.Fatalf("read key_id: %v", err)
	}
	if keyID == "" {
		t.Fatal("legacy run was not stamped with the legacy key id")
	}

	events, err := s.ListEvents("run-old")
	if err != nil {
		t.Fatalf("list upgraded events: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("upgraded events: %d, want 2", len(events))
	}

	// The previously unsigned events must verify under the migrated chain.
	if err := s.VerifyEventChain("run-old", events); err != nil {
		t.Fatalf("legacy events do not verify after upgrade: %v", err)
	}

	// New writes must continue the chain cleanly.
	if _, err := s.AppendEvent("run-old", Event{Type: "node.finished",
		NodeID: "summarize", Data: map[string]any{"i": 1}}); err != nil {
		t.Fatalf("append after upgrade: %v", err)
	}
	events, err = s.ListEvents("run-old")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEventChain("run-old", events); err != nil {
		t.Fatalf("chain broken by post-upgrade append: %v", err)
	}

	// The referenced blob survived and the full doctor scan passes.
	if _, err := os.Stat(filepath.Join(s.blobDir, blobHash[:2], blobHash)); err != nil {
		t.Fatalf("legacy blob missing after upgrade: %v", err)
	}
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatalf("health check after upgrade: %v", err)
	}
	if !report.OK() {
		t.Fatalf("upgraded store is unhealthy: %+v", report)
	}

	// The workflow keeps its id and gains the published_version column.
	wf, err := s.GetWorkflow("wf")
	if err != nil {
		t.Fatalf("get upgraded workflow: %v", err)
	}
	if wf.ID != "wf" {
		t.Fatalf("upgraded workflow id: %q", wf.ID)
	}

	// The version stamp advanced.
	var version int
	if err := s.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version < 7 {
		t.Fatalf("schema version after upgrade: %d, want >= 7", version)
	}
}

// Upgrading must be idempotent: opening the same legacy directory a second
// time changes nothing observable.
func TestUpgradeIsIdempotent(t *testing.T) {
	dir, _ := buildLegacyDataDir(t)
	first, err := Open(dir)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	events1, err := first.ListEvents("run-old")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := Open(dir)
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	defer second.Close()
	events2, err := second.ListEvents("run-old")
	if err != nil {
		t.Fatal(err)
	}
	if len(events1) != len(events2) {
		t.Fatalf("second upgrade changed the event count: %d vs %d", len(events1), len(events2))
	}
	for i := range events1 {
		if events1[i].Hash != events2[i].Hash {
			t.Fatalf("second upgrade re-signed event %d", i)
		}
	}
	if err := second.VerifyEventChain("run-old", events2); err != nil {
		t.Fatalf("chain invalid after second upgrade: %v", err)
	}
}
