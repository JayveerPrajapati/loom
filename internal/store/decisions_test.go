package store_test

import (
	"testing"
	"time"

	"loom/internal/store"
)

func parkRun(t *testing.T, st *store.Store, id, workflowID string, requested, expires time.Time) {
	t.Helper()
	run := store.Run{
		ID: id, WorkflowID: workflowID, WorkflowVersion: "v1", Status: "running",
		Trigger: "manual", StartedAt: time.Now().UTC(),
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	payload := []byte(`{"requestedAt":"` + requested.UTC().Format(time.RFC3339) + `","channel":"#ops"}`)
	if err := st.SetRunWaitingUntil(id, "approve", "tok-"+id, payload, time.Time{}, expires); err != nil {
		t.Fatalf("park run: %v", err)
	}
}

func TestResumeWithDecisionIsAtomicAndQueryable(t *testing.T) {
	st := openStore(t)
	requested := time.Now().UTC().Add(-2 * time.Hour)
	parkRun(t, st, "run-d1", "wf-d", requested, time.Now().UTC().Add(time.Hour))

	decision := store.Decision{
		RunID: "run-d1", WorkflowID: "wf-d", NodeID: "approve",
		Decision: "rejected", Note: "out of budget",
		ActorID: "u1", ActorLabel: "ada@example.com", Source: "api",
		RequestedAt: requested, DecidedAt: time.Now().UTC(),
		WaitedMs: time.Since(requested).Milliseconds(),
	}
	claimed, err := st.ResumeWaitingWithDecision("run-d1", decision)
	if err != nil || !claimed {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	// The second claim must lose the CAS and must not add a second decision.
	again, err := st.ResumeWaitingWithDecision("run-d1", decision)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if again {
		t.Fatal("second claim succeeded; the wait was not atomic")
	}

	rows, total, err := st.ListDecisions("wf-d", "", "", 50, 0)
	if err != nil {
		t.Fatalf("list decisions: %v", err)
	}
	if total != 1 || len(rows) != 1 {
		t.Fatalf("decisions = %d/%d, want 1/1", total, len(rows))
	}
	got := rows[0]
	if got.Note != "out of budget" || got.ActorLabel != "ada@example.com" || got.Source != "api" {
		t.Fatalf("decision = %+v", got)
	}
	if got.WaitedMs < 2*time.Hour.Milliseconds()-1000 {
		t.Fatalf("waitedMs = %d, want about two hours", got.WaitedMs)
	}

	// Filters narrow, and an unknown source yields nothing.
	if _, n, err := st.ListDecisions("wf-d", "slack", "", 50, 0); err != nil || n != 0 {
		t.Fatalf("source filter = %d, %v", n, err)
	}

	stats, err := st.DecisionMetrics(24 * time.Hour)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if stats.Total != 1 || stats.Rejected != 1 || stats.Approved != 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if stats.WaitedP50Ms <= 0 || stats.WaitedP95Ms < stats.WaitedP50Ms {
		t.Fatalf("percentiles = %+v", stats)
	}
}

func TestResumeTokenExpiryAndRevoke(t *testing.T) {
	st := openStore(t)
	past := time.Now().UTC().Add(-time.Minute)
	parkRun(t, st, "run-exp", "wf-exp", time.Now().UTC().Add(-2*time.Minute), past)

	if _, err := st.GetRunByResumeToken("tok-run-exp"); err == nil {
		t.Fatal("expired token still resolves")
	}
	run, err := st.GetRun("run-exp")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.ResumeExpiresAt == nil || !run.ResumeExpiresAt.Before(time.Now()) {
		t.Fatalf("resume expiry = %v", run.ResumeExpiresAt)
	}

	parkRun(t, st, "run-live", "wf-exp", time.Now().UTC(), time.Now().UTC().Add(time.Hour))
	if _, err := st.GetRunByResumeToken("tok-run-live"); err != nil {
		t.Fatalf("live token should resolve: %v", err)
	}
	ok, err := st.ClearResumeToken("run-live")
	if err != nil || !ok {
		t.Fatalf("revoke = %v, %v", ok, err)
	}
	if _, err := st.GetRunByResumeToken("tok-run-live"); err == nil {
		t.Fatal("revoked token still resolves")
	}
	if run, _ := st.GetRun("run-live"); run.Status != "waiting" {
		t.Fatalf("revoke changed status to %q", run.Status)
	}
}

func TestListRunsFilteredPagesAndSearches(t *testing.T) {
	st := openStore(t)
	now := time.Now().UTC()
	for i, status := range []string{"done", "failed", "done"} {
		id := "run-f" + string(rune('a'+i))
		run := store.Run{
			ID: id, WorkflowID: "wf-f", WorkflowVersion: "v1", Status: status,
			Trigger: "manual", StartedAt: now.Add(time.Duration(i) * time.Minute),
		}
		if status == "failed" {
			run.Error = "boom: disk full"
		}
		if err := st.CreateRun(run); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	runs, total, err := st.ListRunsFiltered(store.RunsFilter{WorkflowID: "wf-f", Status: "done", Limit: 1})
	if err != nil {
		t.Fatalf("filtered: %v", err)
	}
	if total != 2 || len(runs) != 1 {
		t.Fatalf("done filter = %d/%d, want 2/1", total, len(runs))
	}
	if runs[0].ID != "run-fc" {
		t.Fatalf("newest first: got %s", runs[0].ID)
	}

	page2, total2, err := st.ListRunsFiltered(store.RunsFilter{WorkflowID: "wf-f", Status: "done", Limit: 1, Offset: 1})
	if err != nil || total2 != 2 || len(page2) != 1 || page2[0].ID != "run-fa" {
		t.Fatalf("page 2 = %+v (%d), %v", page2, total2, err)
	}

	failed, total3, err := st.ListRunsFiltered(store.RunsFilter{WorkflowID: "wf-f", Query: "disk"})
	if err != nil || total3 != 1 || len(failed) != 1 || failed[0].ID != "run-fb" {
		t.Fatalf("search = %+v (%d), %v", failed, total3, err)
	}
}
