package store_test

import (
	"fmt"
	"testing"
	"time"

	"loom/internal/store"
)

func TestListRunsKeysetPagination(t *testing.T) {
	st := openStore(t)
	base := time.Now().UTC().Add(-time.Hour)
	// 25 runs, five per distinct timestamp, so the id tiebreaker decides
	// order inside every timestamp group.
	for i := 0; i < 25; i++ {
		started := base.Add(time.Duration(i/5) * time.Second)
		if err := st.CreateRun(store.Run{
			ID: fmt.Sprintf("run-k%02d", i), WorkflowID: "wf-k", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: started,
		}); err != nil {
			t.Fatalf("create run %d: %v", i, err)
		}
	}

	var got []store.Run
	cursor := ""
	pages := 0
	for {
		page, err := st.ListRunsPage(store.RunsFilter{WorkflowID: "wf-k", Limit: 4, Cursor: cursor})
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		if page.Total != 25 {
			t.Fatalf("page %d total = %d, want 25", pages, page.Total)
		}
		got = append(got, page.Runs...)
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 10 {
			t.Fatal("pagination did not terminate")
		}
	}
	if pages != 7 {
		t.Fatalf("paged through %d pages, want 7 (25 runs at 4 per page)", pages)
	}
	if len(got) != 25 {
		t.Fatalf("collected %d runs, want 25", len(got))
	}
	seen := map[string]bool{}
	for _, run := range got {
		if run.WorkflowID != "wf-k" {
			t.Fatalf("run %s from workflow %q leaked into the page", run.ID, run.WorkflowID)
		}
		if seen[run.ID] {
			t.Fatalf("run %s appeared on more than one page", run.ID)
		}
		seen[run.ID] = true
	}
	// Pages must concatenate into the exact global order: started_at
	// descending, then id descending within a timestamp.
	for i := 1; i < len(got); i++ {
		prev, cur := got[i-1], got[i]
		if prev.StartedAt.After(cur.StartedAt) {
			continue
		}
		if prev.StartedAt.Equal(cur.StartedAt) && prev.ID > cur.ID {
			continue
		}
		t.Fatalf("order broke at %d: %s(%s) before %s(%s)", i, prev.ID, prev.StartedAt, cur.ID, cur.StartedAt)
	}
}

func TestListRunsPageRejectsMalformedCursor(t *testing.T) {
	st := openStore(t)
	if err := st.CreateRun(store.Run{
		ID: "run-bad", WorkflowID: "wf-bad", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		cursor string
	}{
		{name: "not base64", cursor: "!!!not-base64!!!"},
		{name: "no separator", cursor: "cnVuLWthMDA"},
		{name: "empty position", cursor: "Lw=="},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := st.ListRunsPage(store.RunsFilter{Cursor: tc.cursor}); err == nil {
				t.Fatalf("cursor %q accepted, want error", tc.cursor)
			}
		})
	}
	// A first page without a cursor still works and reports the total.
	page, err := st.ListRunsPage(store.RunsFilter{WorkflowID: "wf-bad"})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if page.Total != 1 || len(page.Runs) != 1 || page.Runs[0].ID != "run-bad" || page.NextCursor != "" {
		t.Fatalf("first page = %+v, want the single run and no next cursor", page)
	}
}
