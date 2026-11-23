package store

import (
	"fmt"
	"testing"
	"time"
)

func openPerfStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// P2-10 scale regressions. Each test pins a property that must not degrade
// as data grows; query-plan guards (queryplan_test.go, prune tests) cover the
// index side, these cover behavior and per-row cost at volume. Local
// baselines live in docs/performance.md.

// TestQueueAdmissionScalesHundredRuns admits a full FIFO queue of 100 parked
// runs one slot at a time. The admission count subquery (CountActiveRuns)
// and OldestQueuedRun must stay index-driven as the run table grows — the
// loop would visibly stall (test timeout) if either degraded to a scan per
// admission.
func TestQueueAdmissionScalesHundredRuns(t *testing.T) {
	st := openPerfStore(t)
	for i := 0; i < 100; i++ {
		if err := st.CreateRun(Run{
			ID:              fmt.Sprintf("run-q%03d", i),
			WorkflowID:      "wf-scale",
			WorkflowVersion: "v1", Status: "queued", Trigger: "manual",
			StartedAt: time.Now().Add(-time.Duration(100-i) * time.Second),
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	admitted := 0
	for i := 0; i < 100; i++ {
		next, err := st.OldestQueuedRun("wf-scale")
		if err != nil {
			t.Fatalf("oldest: %v", err)
		}
		if next == nil {
			break
		}
		ok, err := st.AdmitRun(next.ID, "wf-scale", 1)
		if err != nil {
			t.Fatalf("admit %s: %v", next.ID, err)
		}
		if !ok {
			t.Fatalf("admit %s refused with a free slot", next.ID)
		}
		if next.ID != fmt.Sprintf("run-q%03d", i) {
			t.Fatalf("admitted %s, want run-q%03d (FIFO order)", next.ID, i)
		}
		// Simulate the run finishing and freeing the slot.
		fin := time.Now()
		if err := st.UpdateRunStatus(next.ID, "done", "", &fin); err != nil {
			t.Fatalf("finish %s: %v", next.ID, err)
		}
		admitted++
	}
	if admitted != 100 {
		t.Fatalf("admitted %d of 100 queued runs", admitted)
	}
	if next, err := st.OldestQueuedRun("wf-scale"); err != nil || next != nil {
		t.Fatalf("queue not drained: %#v, %v", next, err)
	}
}

// TestListRunsPageDeepHistoryStaysBounded pages through 1000 runs purely by
// cursor: every page must be full, strictly older than the last, and the
// final cursor must be empty. Keyset pagination keeps per-page cost constant
// no matter how deep the history goes — the benchmark below records the
// cursor-vs-offset difference as a baseline.
func TestListRunsPageDeepHistoryStaysBounded(t *testing.T) {
	st := openPerfStore(t)
	// One base timestamp: the -1ms-per-run offsets guarantee strictly
	// distinct, descending order regardless of how slow the inserts are
	// (a per-row time.Now() can locally ascend and tie under -race).
	base := time.Now()
	for i := 0; i < 1000; i++ {
		if err := st.CreateRun(Run{
			ID:              fmt.Sprintf("run-p%04d", i),
			WorkflowID:      "wf-page",
			WorkflowVersion: "v1", Status: "done", Trigger: "manual",
			StartedAt: base.Add(-time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	var cursor string
	pages := 0
	seen := 0
	lastStarted := time.Now().Add(time.Hour)
	for {
		page, err := st.ListRunsPage(RunsFilter{WorkflowID: "wf-page", Limit: 100, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if page.Total != 1000 {
			t.Fatalf("total = %d, want 1000", page.Total)
		}
		for _, run := range page.Runs {
			if !run.StartedAt.Before(lastStarted) {
				t.Fatalf("page %d run %s started %s, not strictly older than the previous", pages, run.ID, run.StartedAt)
			}
			lastStarted = run.StartedAt
			seen++
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 20 {
			t.Fatal("cursor pagination did not terminate")
		}
	}
	if seen != 1000 {
		t.Fatalf("walked %d runs across %d pages, want 1000", seen, pages)
	}
	if pages != 10 {
		t.Fatalf("pages = %d, want 10", pages)
	}
}

// BenchmarkListRunsPageDeepVsOffset records the per-page cost of the
// keyset predicate versus OFFSET at increasing depth on the same 1000-run
// history. The keyset page stays flat while OFFSET re-reads everything it
// skips.
func BenchmarkListRunsPageDeepVsOffset(b *testing.B) {
	st, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer st.Close()
	base := time.Now()
	for i := 0; i < 1000; i++ {
		if err := st.CreateRun(Run{
			ID:              fmt.Sprintf("run-b%04d", i),
			WorkflowID:      "wf-bench",
			WorkflowVersion: "v1", Status: "done", Trigger: "manual",
			StartedAt: base.Add(-time.Duration(i) * time.Millisecond),
		}); err != nil {
			b.Fatalf("create %d: %v", i, err)
		}
	}
	// A cursor to the last (oldest) page, and the equivalent deep offset.
	page, err := st.ListRunsPage(RunsFilter{WorkflowID: "wf-bench", Limit: 100})
	if err != nil {
		b.Fatalf("first page: %v", err)
	}
	deepCursor := page.NextCursor
	for i := 0; i < 8; i++ {
		page, err = st.ListRunsPage(RunsFilter{WorkflowID: "wf-bench", Limit: 100, Cursor: deepCursor})
		if err != nil {
			b.Fatalf("walk: %v", err)
		}
	}
	b.Run("cursor-deep", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, err := st.ListRunsPage(RunsFilter{WorkflowID: "wf-bench", Limit: 100, Cursor: deepCursor}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("offset-deep-900", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			if _, _, err := st.ListRunsFiltered(RunsFilter{WorkflowID: "wf-bench", Limit: 100, Offset: 900}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
