package store

import (
	"errors"
	"testing"
	"time"
)

func TestListRunsPageDashboardFilters(t *testing.T) {
	s := newOpsStore(t)
	base := time.Now().UTC()
	create := func(id, status, trigger, job, parent string, offset int) {
		t.Helper()
		if err := s.CreateRun(Run{
			ID: id, WorkflowID: "wf-runs", WorkflowVersion: "v1",
			Status: status, Trigger: trigger, JobID: job, ParentRunID: parent,
			StartedAt: base.Add(time.Duration(offset) * time.Second),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	create("run-root-manual", "done", "manual", "job-a", "", 0)
	create("run-child", "done", "manual", "job-a", "run-root-manual", 1)
	create("run-root-cron", "failed", "cron", "job-b", "", 2)
	create("run-root-webhook", "done", "webhook", "job-a", "", 3)

	roots, err := s.ListRunsPage(RunsFilter{RootsOnly: true, Limit: 10})
	if err != nil || roots.Total != 3 || len(roots.Runs) != 3 {
		t.Fatalf("roots page = %+v, err=%v", roots, err)
	}
	for _, run := range roots.Runs {
		if run.ParentRunID != "" {
			t.Fatalf("roots page leaked child run %s", run.ID)
		}
	}

	job, err := s.ListRunsPage(RunsFilter{JobID: "job-a", RootsOnly: true, Limit: 10})
	if err != nil || job.Total != 2 {
		t.Fatalf("job page = %+v, err=%v", job, err)
	}
	cron, err := s.ListRunsPage(RunsFilter{Trigger: "cron", RootsOnly: true, Limit: 10})
	if err != nil || cron.Total != 1 || len(cron.Runs) != 1 || cron.Runs[0].ID != "run-root-cron" {
		t.Fatalf("trigger page = %+v, err=%v", cron, err)
	}
	searched, err := s.ListRunsPage(RunsFilter{Query: "cron", RootsOnly: true, Limit: 10})
	if err != nil || searched.Total != 1 || searched.Runs[0].ID != "run-root-cron" {
		t.Fatalf("search page = %+v, err=%v", searched, err)
	}
}

func TestConfigProfileListPageCursorSearchVisibility(t *testing.T) {
	s := newOpsStore(t)
	create := func(id, name, visibility, creator string) {
		t.Helper()
		if _, err := s.CreateConfigProfile(&ConfigProfile{
			ID: id, Name: name, NodeType: "http.request",
			Visibility: visibility, CreatorID: creator,
			Config: map[string]any{"method": "GET", "url": "https://api.example.com/" + id},
		}, "test"); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	create("p-alpha", "Alpha", VisibilityPrivate, "u1")
	create("p-beta", "Beta", VisibilityPrivate, "u2")
	create("p-gamma", "Gamma", VisibilityShared, "")
	create("p-percent", "100% coverage", VisibilityShared, "")

	admin := VisibilityScope{Admin: true}
	u1 := VisibilityScope{UserID: "u1"}
	u2 := VisibilityScope{UserID: "u2"}
	anon := VisibilityScope{}

	if page, err := s.ListConfigProfilesPage("", "", "", 10, admin); err != nil || page.Total != 4 || len(page.Items) != 4 {
		t.Fatalf("admin page = %+v, err=%v", page, err)
	}
	if page, err := s.ListConfigProfilesPage("", "", "", 10, u1); err != nil || page.Total != 3 {
		t.Fatalf("u1 page total = %+v, err=%v", page, err)
	}
	if page, err := s.ListConfigProfilesPage("", "", "", 10, u2); err != nil || page.Total != 3 {
		t.Fatalf("u2 page total = %+v, err=%v", page, err)
	}
	if page, err := s.ListConfigProfilesPage("", "", "", 10, anon); err != nil || page.Total != 2 {
		t.Fatalf("anon page total = %+v, err=%v", page, err)
	}

	// Search is literal: "%" must not act as a wildcard.
	page, err := s.ListConfigProfilesPage("", "%", "", 10, admin)
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "p-percent" {
		t.Fatalf("percent search = %+v, err=%v", page, err)
	}
	// Search respects visibility: Beta is private to u2.
	if page, err := s.ListConfigProfilesPage("", "beta", "", 10, u1); err != nil || page.Total != 0 {
		t.Fatalf("u1 beta search = %+v, err=%v", page, err)
	}
	if page, err := s.ListConfigProfilesPage("", "beta", "", 10, u2); err != nil || page.Total != 1 || page.Items[0].ID != "p-beta" {
		t.Fatalf("u2 beta search = %+v, err=%v", page, err)
	}

	// Keyset pages are stable and do not repeat the boundary row.
	first, err := s.ListConfigProfilesPage("", "", "", 2, admin)
	if err != nil || len(first.Items) != 2 || first.NextCursor == "" || first.Total != 4 {
		t.Fatalf("first page = %+v, err=%v", first, err)
	}
	second, err := s.ListConfigProfilesPage("", "", first.NextCursor, 2, admin)
	if err != nil || len(second.Items) != 2 || second.NextCursor != "" || second.Total != 4 {
		t.Fatalf("second page = %+v, err=%v", second, err)
	}
	if first.Items[1].ID == second.Items[0].ID {
		t.Fatalf("cursor repeated %s across pages", first.Items[1].ID)
	}
	if _, err := s.ListConfigProfilesPage("", "", "not-base64", 2, admin); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("malformed cursor err = %v", err)
	}
}
