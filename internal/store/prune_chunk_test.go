package store

import (
	"testing"
	"time"
)

// Pruning more runs than one deletion chunk (500) must still delete exactly
// the candidate set, and the dry run must keep agreeing across chunk borders.
func TestPruneChunksLargeHistories(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	const runs = pruneChunk + 37
	now := time.Now()
	for i := 0; i < runs; i++ {
		id := "bulk-" + itoa(i)
		at := now.Add(-48 * time.Hour)
		if err := s.CreateRun(Run{ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE runs SET finished_at = ? WHERE id = ?`,
			at.UTC().Format(time.RFC3339Nano), id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendEvent(id, Event{Type: "node.finished"}); err != nil {
			t.Fatal(err)
		}
	}
	// A young run must survive the same prune.
	if err := s.CreateRun(Run{ID: "young", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET finished_at = ? WHERE id = 'young'`,
		now.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("young", Event{Type: "node.finished"}); err != nil {
		t.Fatal(err)
	}

	cutoff := time.Now().Add(-24 * time.Hour)
	estimate, err := s.PruneEstimate(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if estimate.Runs != runs || estimate.Events != runs {
		t.Fatalf("estimate = %+v, want %d runs and events", estimate, runs)
	}

	result, err := s.Prune(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if result.Runs != runs || result.Events != runs {
		t.Fatalf("prune = %+v, want %d runs and events", result, runs)
	}
	if result.Runs != estimate.Runs || result.Events != estimate.Events {
		t.Fatalf("dry run %+v disagrees with prune %+v", estimate, result)
	}

	var n int
	if err := s.readRow(`SELECT COUNT(*) FROM runs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("runs remaining = %d, want 1 (the young run)", n)
	}
	if err := s.readRow(`SELECT COUNT(*) FROM events`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("events remaining = %d, want 1", n)
	}

	after, err := s.PruneEstimate(cutoff)
	if err != nil {
		t.Fatal(err)
	}
	if after.Runs != 0 || after.Events != 0 {
		t.Fatalf("post-prune estimate = %+v, want empty", after)
	}
}

// BenchmarkPrune measures a full prune of 5000 terminal runs (10 chunks)
// against an empty prune over the same store, so the chunked-deletion cost
// stays observable as the schema evolves.
func BenchmarkPrune(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	old := now.Add(-48 * time.Hour).UTC().Format(time.RFC3339Nano)
	for i := 0; i < 5000; i++ {
		id := "bench-" + itoa(i)
		if err := s.CreateRun(Run{ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: now.Add(-48 * time.Hour)}); err != nil {
			b.Fatal(err)
		}
		if _, err := s.db.Exec(`UPDATE runs SET finished_at = ? WHERE id = ?`, old, id); err != nil {
			b.Fatal(err)
		}
		if _, err := s.AppendEvent(id, Event{Type: "node.finished"}); err != nil {
			b.Fatal(err)
		}
	}
	cutoff := time.Now().Add(-24 * time.Hour)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := s.PruneEstimate(cutoff); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if _, err := s.Prune(cutoff); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		// Re-seed the history the prune just deleted.
		for j := 0; j < 5000; j++ {
			id := "bench-" + itoa(j)
			if err := s.CreateRun(Run{ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
				Status: "done", Trigger: "manual", StartedAt: now.Add(-48 * time.Hour)}); err != nil {
				b.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE runs SET finished_at = ? WHERE id = ?`, old, id); err != nil {
				b.Fatal(err)
			}
			if _, err := s.AppendEvent(id, Event{Type: "node.finished"}); err != nil {
				b.Fatal(err)
			}
		}
		b.StartTimer()
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var digits []byte
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
