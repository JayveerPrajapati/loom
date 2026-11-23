package store

import (
	"testing"
	"time"
)

// StorageStats is the read-only operational view; these tests pin its
// accounting against a hand-seeded store where every number is
// derivable by hand.
func newOpsStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStorageStatsEmpty(t *testing.T) {
	s := newOpsStore(t)
	stats, err := s.StorageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Runs != 0 || stats.Events != 0 || stats.Blobs != 0 || stats.BlobBytes != 0 {
		t.Fatalf("empty store must report zeroes, got %+v", stats)
	}
	if len(stats.EventsPerDay) != 0 {
		t.Fatalf("empty store has no daily growth, got %v", stats.EventsPerDay)
	}
	if stats.ProjectedEvents30d != 0 {
		t.Fatalf("no trend to project on an empty store, got %d", stats.ProjectedEvents30d)
	}
	// Reclaimable horizons still exist (all zero on an empty store).
	if len(stats.Reclaimable) != 3 {
		t.Fatalf("expected 7/30/90-day horizons, got %v", stats.Reclaimable)
	}
}

func TestStorageStatsCountsAndReclaimable(t *testing.T) {
	s := newOpsStore(t)

	// The run ledger joins the workflow name so the UI can show it.
	if _, err := s.db.Exec(`INSERT INTO workflows (id, name, version, published_version, doc, updated_at)
		VALUES (?, ?, 1, 0, '{}', ?)`, "wf-ops", "Ops workflow", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	// One terminal run a year ago (reclaimable at every horizon) and
	// one active run now (never reclaimable).
	old := mustInsertRunForOps(t, s, "old-run", "wf-ops", "done", time.Now().UTC().AddDate(-1, 0, 0))
	mustInsertEventForOps(t, s, old, "run.started", "b1", "")
	mustInsertEventForOps(t, s, s.insertActiveRun(t), "run.started", "", "b2")

	stats, err := s.StorageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.Runs != 2 || stats.ActiveRuns != 1 {
		t.Fatalf("runs: want 2 total / 1 active, got %d/%d", stats.Runs, stats.ActiveRuns)
	}
	if stats.Events != 2 {
		t.Fatalf("events: want 2, got %d", stats.Events)
	}
	if stats.Blobs != 2 || stats.BlobBytes != 20 {
		t.Fatalf("blobs: want 2 blobs / 20 bytes, got %d/%d", stats.Blobs, stats.BlobBytes)
	}
	if stats.DatabaseBytes <= 0 {
		t.Fatalf("database size must be positive, got %d", stats.DatabaseBytes)
	}

	// The year-old run is reclaimable at 7/30/90 days; the active run
	// is not. Its single event references b1 only.
	for _, horizon := range stats.Reclaimable {
		if horizon.Days != 7 && horizon.Days != 30 && horizon.Days != 90 {
			t.Fatalf("unexpected horizon %+v", horizon)
		}
		if horizon.Runs != 1 || horizon.Events != 1 {
			t.Fatalf("horizon %d: want 1 run / 1 event reclaimable, got %+v", horizon.Days, horizon)
		}
		if horizon.Bytes != 10 {
			t.Fatalf("horizon %d: want 10 bytes reclaimable, got %d", horizon.Days, horizon.Bytes)
		}
	}

	// Largest run: both runs reference 10 bytes; order falls back to
	// started_at (most recent first).
	if len(stats.LargestRuns) != 2 {
		t.Fatalf("want 2 largest runs, got %d", len(stats.LargestRuns))
	}
	if stats.LargestRuns[0].RunID != "active-run" && stats.LargestRuns[0].RunID != "old-run" {
		t.Fatalf("unexpected top run %+v", stats.LargestRuns[0])
	}
	if stats.LargestRuns[0].Bytes != 10 {
		t.Fatalf("top run bytes: want 10, got %d", stats.LargestRuns[0].Bytes)
	}
	// The workflow name rides the run row; the active run uses its own id.
	for _, run := range stats.LargestRuns {
		if run.WorkflowID == "wf-ops" && run.WorkflowName != "Ops workflow" {
			t.Fatalf("workflow name for %s = %q", run.RunID, run.WorkflowName)
		}
	}

	// Per-workflow: one workflow, both runs, both events.
	if len(stats.LargestWorkflows) != 1 {
		t.Fatalf("want 1 workflow, got %d", len(stats.LargestWorkflows))
	}
	wf := stats.LargestWorkflows[0]
	if wf.WorkflowID != "wf-ops" || wf.Runs != 2 || wf.Events != 2 {
		t.Fatalf("workflow aggregate wrong: %+v", wf)
	}
}

func TestStorageStatsSharedBlobs(t *testing.T) {
	s := newOpsStore(t)

	// One blob referenced by two runs' events (shared), one referenced
	// once (not shared).
	runA := mustInsertRunForOps(t, s, "run-a", "wf-shared", "done", time.Now().UTC().AddDate(0, 0, -2))
	runB := mustInsertRunForOps(t, s, "run-b", "wf-shared", "done", time.Now().UTC().AddDate(0, 0, -1))
	mustInsertEventForOps(t, s, runA, "run.started", "shared", "only-a")
	mustInsertEventForOps(t, s, runB, "run.started", "shared", "")

	stats, err := s.StorageStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.SharedBlobs != 1 {
		t.Fatalf("want 1 shared blob, got %d", stats.SharedBlobs)
	}
	if stats.SharedBlobBytes != 10 {
		t.Fatalf("shared blob bytes: want 10, got %d", stats.SharedBlobBytes)
	}
}

func TestStorageStatsGrowthAndProjection(t *testing.T) {
	s := newOpsStore(t)

	// Two days of history with 1 event then 3 events: the linear
	// trend extends at +2/day.
	day1 := time.Now().UTC().AddDate(0, 0, -1)
	run1 := mustInsertRunForOps(t, s, "grow-1", "wf-grow", "done", day1)
	mustInsertEventForOps(t, s, run1, "run.started", "", "")
	today := mustInsertRunForOps(t, s, "grow-2", "wf-grow", "done", time.Now().UTC())
	for i := 0; i < 3; i++ {
		mustInsertEventForOps(t, s, today, "run.started", "", "")
	}

	stats, err := s.StorageStats()
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.EventsPerDay) != 2 {
		t.Fatalf("want 2 growth days, got %d (%v)", len(stats.EventsPerDay), stats.EventsPerDay)
	}
	if stats.EventsPerDay[0].Events != 1 || stats.EventsPerDay[1].Events != 3 {
		t.Fatalf("daily counts wrong: %v", stats.EventsPerDay)
	}
	// Fit over (0→1, 1→3): intercept 1, slope +2. Future daily levels
	// are 5, 7, …; thirty of them sum to 1020.
	if stats.ProjectedEvents30d != 1020 {
		t.Fatalf("projection: want 1020, got %d", stats.ProjectedEvents30d)
	}
}

// --- seed helpers -------------------------------------------------------

func mustInsertRunForOps(t *testing.T, s *Store, id, workflowID, status string, started time.Time) string {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO runs (id, workflow_id, workflow_version, status, trigger, started_at)
		VALUES (?, ?, '1', ?, 'manual', ?)`,
		id, workflowID, status, started.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return id
}

func (s *Store) insertActiveRun(t *testing.T) string {
	t.Helper()
	id := "active-run"
	if _, err := s.db.Exec(`INSERT INTO runs (id, workflow_id, workflow_version, status, trigger, started_at)
		VALUES (?, 'wf-ops', '1', 'running', 'manual', ?)`,
		id, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustInsertEventForOps(t *testing.T, s *Store, runID, typ, inputHash, outputHash string) {
	t.Helper()
	at := time.Now().UTC()
	var runAt string
	_ = s.readRow(`SELECT started_at FROM runs WHERE id = ?`, runID).Scan(&runAt)
	if parsed, err := time.Parse(time.RFC3339Nano, runAt); err == nil {
		at = parsed
	}
	// Blobs must exist in the index for size accounting.
	for _, hash := range []string{inputHash, outputHash} {
		if hash == "" {
			continue
		}
		if _, err := s.db.Exec(`INSERT INTO blobs (hash, size, created_at) VALUES (?, 10, ?)
			ON CONFLICT(hash) DO NOTHING`, hash, at.Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO events (run_id, seq, type, at, input_hash, output_hash, actor)
		VALUES (?, (SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id = ?), ?, ?, ?, ?, 'ops-test')`,
		runID, runID, typ, at.Format(time.RFC3339Nano), inputHash, outputHash); err != nil {
		t.Fatal(err)
	}
}
