package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func healthFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateRun(Run{ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("run", Event{Type: "node.finished",
		InputHash:  "",
		OutputHash: "",
		Data:       map[string]any{"i": 1}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveBlob([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCheckHealthPassesOnIntactStore(t *testing.T) {
	s := healthFixture(t)
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if report.RunsChecked != 1 {
		t.Fatalf("runs checked: %d", report.RunsChecked)
	}
	if !report.OK() {
		t.Fatalf("healthy store reported failures: %+v", report)
	}
}

func TestCheckHealthDetectsBlobTamper(t *testing.T) {
	s := healthFixture(t)
	// Find the blob file on disk and corrupt it in place.
	found := false
	err := filepath.WalkDir(s.blobDir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		found = true
		return os.WriteFile(path, []byte("tampered"), 0o600)
	})
	if err != nil || !found {
		t.Fatalf("blob file: found=%v err=%v", found, err)
	}
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(report.BlobFailures) == 0 {
		t.Fatalf("blob tamper not detected: %+v", report)
	}
}

func TestCheckHealthDetectsMissingBlobFile(t *testing.T) {
	s := healthFixture(t)
	err := filepath.WalkDir(s.blobDir, func(path string, d os.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		return os.Remove(path)
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(report.BlobFailures) == 0 {
		t.Fatalf("missing blob file not detected: %+v", report)
	}
}

func TestCheckHealthReportsOrphanBlobFiles(t *testing.T) {
	s := healthFixture(t)
	orphan := filepath.Join(s.blobDir, "ab", "ab"+"000000000000000000000000000000000000000000000000000000000000cd")
	if err := os.MkdirAll(filepath.Dir(orphan), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("orphan"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(report.OrphanBlobFiles) == 0 {
		t.Fatalf("orphan blob file not reported: %+v", report)
	}
}

func TestCheckHealthFailsOnChainTamper(t *testing.T) {
	s := healthFixture(t)
	if _, err := s.db.Exec(`UPDATE events SET error = 'tampered' WHERE run_id = 'run' AND seq = 1`); err != nil {
		t.Fatal(err)
	}
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(report.ChainFailures) == 0 {
		t.Fatalf("chain tamper not detected: %+v", report)
	}
}

// The health report surfaces an exported bootstrap password (P3-5) so
// operators get a persistent signal, not just the startup log line.
func TestCheckHealthReportsBootstrapPassword(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	t.Setenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD", "")
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatal(err)
	}
	if report.BootstrapPasswordSet {
		t.Fatal("report says bootstrap password is set with the variable empty")
	}

	t.Setenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD", "supersecret")
	report, err = s.CheckHealth()
	if err != nil {
		t.Fatal(err)
	}
	if !report.BootstrapPasswordSet {
		t.Fatal("report does not flag the exported bootstrap password")
	}
	if !report.OK() {
		t.Fatal("hygiene warning must not fail an otherwise healthy store")
	}
}
