package store

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func pruneFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	now := time.Now()
	mk := func(id, status string, age time.Duration) {
		at := now.Add(-age)
		if err := s.CreateRun(Run{ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: status, Trigger: "manual", StartedAt: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE runs SET finished_at = ? WHERE id = ?`,
			at.UTC().Format(time.RFC3339Nano), id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendEvent(id, Event{Type: "node.finished",
			Data: map[string]any{"i": 1}}); err != nil {
			t.Fatal(err)
		}
	}
	mk("old-done", "done", 48*time.Hour)
	mk("old-failed", "failed", 48*time.Hour)
	mk("recent-done", "done", time.Hour)
	mk("running", "running", 48*time.Hour)
	// A blob from an old run (prunable) and one referenced by the running
	// run's event (must survive). Both are backdated past the cutoff so the
	// reference — not the age — is what keeps the live one.
	oldSum := sha256.Sum256([]byte("old payload"))
	oldHash := hex.EncodeToString(oldSum[:])
	if _, err := s.SaveBlob([]byte("old payload")); err != nil {
		t.Fatal(err)
	}
	liveSum := sha256.Sum256([]byte("live payload"))
	liveHash := hex.EncodeToString(liveSum[:])
	if _, err := s.SaveBlob([]byte("live payload")); err != nil {
		t.Fatal(err)
	}
	backdated := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(`UPDATE blobs SET created_at = ? WHERE hash IN (?, ?)`,
		backdated, oldHash, liveHash); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE events SET input_hash = ? WHERE run_id = 'running'`, liveHash); err != nil {
		t.Fatal(err)
	}
	return s
}

// The dry run must predict the real prune exactly, and delete nothing itself.
func TestPruneEstimateMatchesPrune(t *testing.T) {
	s := pruneFixture(t)
	cutoff := time.Now().Add(-24 * time.Hour)

	estimate, err := s.PruneEstimate(cutoff)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if estimate.Runs != 2 {
		t.Fatalf("estimated runs: %d, want 2", estimate.Runs)
	}
	if estimate.Events != 2 {
		t.Fatalf("estimated events: %d, want 2", estimate.Events)
	}
	if estimate.Blobs != 1 {
		t.Fatalf("estimated blobs: %d, want 1", estimate.Blobs)
	}

	// A second estimate must return the same numbers: dry runs never mutate.
	again, err := s.PruneEstimate(cutoff)
	if err != nil {
		t.Fatalf("second estimate: %v", err)
	}
	if again != estimate {
		t.Fatalf("second estimate drifted: %+v vs %+v", again, estimate)
	}
	var runs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 4 {
		t.Fatalf("dry run changed the runs table: %d rows", runs)
	}

	result, err := s.Prune(cutoff)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if result.Runs != estimate.Runs || result.Events != estimate.Events ||
		result.Blobs != estimate.Blobs || result.Bytes != estimate.Bytes {
		t.Fatalf("prune %+v disagrees with estimate %+v", result, estimate)
	}

	// The blob referenced by the running run survives.
	var liveBlobs int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&liveBlobs); err != nil {
		t.Fatal(err)
	}
	if liveBlobs != 1 {
		t.Fatalf("surviving blobs: %d, want 1", liveBlobs)
	}
}

// Running the estimate after a prune must report zero: everything eligible is
// gone, so the two commands compose predictably.
func TestPruneEstimateAfterPruneIsEmpty(t *testing.T) {
	s := pruneFixture(t)
	cutoff := time.Now().Add(-24 * time.Hour)
	if _, err := s.Prune(cutoff); err != nil {
		t.Fatalf("prune: %v", err)
	}
	estimate, err := s.PruneEstimate(cutoff)
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if estimate.Runs != 0 || estimate.Events != 0 || estimate.Blobs != 0 {
		t.Fatalf("post-prune estimate not empty: %+v", estimate)
	}
}
