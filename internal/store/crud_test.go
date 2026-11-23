package store_test

import (
	"context"
	"testing"
	"time"

	"loom/internal/graph"
	"loom/internal/store"
)

func TestPingAndRunStats(t *testing.T) {
	st := openStore(t)
	if err := st.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	for _, status := range []string{"queued", "running", "waiting", "done", "failed"} {
		if err := st.CreateRun(store.Run{
			ID: "run-" + status, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: status, Trigger: "manual", StartedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create %s: %v", status, err)
		}
	}
	stats, err := st.RunStats()
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Total != 5 || stats.Done != 1 || stats.Failed != 1 || stats.Waiting != 1 || stats.Active != 2 {
		t.Fatalf("stats = %#v", stats)
	}
}

func TestWorkflowCRUDAndVersions(t *testing.T) {
	st := openStore(t)
	wf := &graph.Workflow{
		ID: "wf", Name: "WF", Version: 1,
		Nodes: []graph.Node{{ID: "a", Type: "data.set", TypeVersion: 1, Name: "A"}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := st.GetWorkflow("wf")
	if err != nil || got.Name != "WF" {
		t.Fatalf("get = %#v, %v", got, err)
	}
	list, err := st.ListWorkflows()
	if err != nil || len(list) != 1 || list[0].ID != "wf" {
		t.Fatalf("list = %#v, %v", list, err)
	}

	ver, err := st.PublishWorkflow(wf)
	if err != nil || ver.Version != 1 || ver.NodeCount != 1 {
		t.Fatalf("publish = %#v, %v", ver, err)
	}
	versions, err := st.ListWorkflowVersions("wf")
	if err != nil || len(versions) != 1 || versions[0].Version != 1 {
		t.Fatalf("versions = %#v, %v", versions, err)
	}
	snapshot, err := st.GetWorkflowVersion("wf", 1)
	if err != nil || snapshot.ID != "wf" {
		t.Fatalf("version snapshot = %#v, %v", snapshot, err)
	}
	state, err := st.WorkflowState("wf")
	if err != nil || state.PublishedVersion != 1 || state.Draft || len(state.Versions) != 1 {
		t.Fatalf("state = %#v, %v", state, err)
	}

	wf.Name = "changed"
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	state, err = st.WorkflowState("wf")
	if err != nil || !state.Draft {
		t.Fatalf("draft state = %#v, %v", state, err)
	}

	second, err := st.PublishWorkflow(wf)
	if err != nil || second.Version != 2 {
		t.Fatalf("second publish = %#v, %v", second, err)
	}

	if err := st.DeleteWorkflow("wf"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetWorkflow("wf"); err == nil {
		t.Fatal("deleted workflow still readable")
	}
}

func TestJobCRUD(t *testing.T) {
	st := openStore(t)
	job := &store.Job{
		ID: "job-1", Name: "Job", TemplateID: "wf", TemplateVersion: 1,
		Enabled: true, TriggerType: "manual",
		InputDefaults: map[string]any{"a": 1},
	}
	if err := st.CreateJob(job); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.GetJob("job-1")
	if err != nil || got.Name != "Job" || !got.Enabled {
		t.Fatalf("get = %#v, %v", got, err)
	}
	if n, err := st.CountJobsByTemplate("wf"); err != nil || n != 1 {
		t.Fatalf("count = %d, %v", n, err)
	}
	list, err := st.ListJobs()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, %v", list, err)
	}

	if err := st.SetJobEnabled("job-1", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	got, _ = st.GetJob("job-1")
	if got.Enabled {
		t.Fatal("job still enabled")
	}
	if err := st.UpdateJobVersion("job-1", 2); err != nil {
		t.Fatalf("update version: %v", err)
	}
	got, _ = st.GetJob("job-1")
	if got.TemplateVersion != 2 {
		t.Fatalf("template version = %d", got.TemplateVersion)
	}
	got.Name = "Renamed"
	if err := st.UpdateJob(got); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = st.GetJob("job-1")
	if got.Name != "Renamed" {
		t.Fatalf("name = %q", got.Name)
	}

	if err := st.DeleteJob("job-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetJob("job-1"); err == nil {
		t.Fatal("deleted job still readable")
	}
}

func TestRunQueriesAndWaitingToken(t *testing.T) {
	st := openStore(t)
	if err := st.CreateRun(store.Run{
		ID: "run-1", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "waiting", Trigger: "manual", StartedAt: time.Now(),
		JobID: "job-1",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID: "run-2", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: time.Now(),
		ParentRunID: "parent-1",
	}); err != nil {
		t.Fatalf("create child run: %v", err)
	}
	if err := st.SetRunWaiting("run-1", "node-1", "token-1", []byte(`{"k":"v"}`)); err != nil {
		t.Fatalf("set waiting: %v", err)
	}
	run, err := st.GetRunByResumeToken("token-1")
	if err != nil || run.ID != "run-1" || run.ResumeNode != "node-1" {
		t.Fatalf("by token = %#v, %v", run, err)
	}

	runs, err := st.ListRuns("wf", 10)
	if err != nil || len(runs) != 2 {
		t.Fatalf("list runs = %#v, %v", runs, err)
	}
	runs, err = st.ListRunsByJob("job-1", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("by job = %#v, %v", runs, err)
	}
	runs, err = st.ListChildRuns("parent-1", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("children = %#v, %v", runs, err)
	}
	runs, err = st.ListRunsByStatus("waiting", 10)
	if err != nil || len(runs) != 1 || runs[0].ID != "run-1" {
		t.Fatalf("by status waiting = %#v, %v", runs, err)
	}
	runs, err = st.ListRunsByStatus("done", 10)
	if err != nil || len(runs) != 1 || runs[0].ID != "run-2" {
		t.Fatalf("by status done = %#v, %v", runs, err)
	}
	if err := st.CreateRun(store.Run{
		ID: "run-3", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create running run: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID: "run-4", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "queued", Trigger: "manual", StartedAt: time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("create queued run: %v", err)
	}
	if n, err := st.CountActiveRuns("wf"); err != nil || n != 1 {
		t.Fatalf("active count = %d, %v", n, err)
	}
	oldest, err := st.OldestQueuedRun("wf")
	if err != nil || oldest == nil || oldest.ID != "run-4" {
		t.Fatalf("oldest queued = %#v, %v", oldest, err)
	}
	if ok, err := st.ClaimQueuedRun("run-4", time.Now()); err != nil || !ok {
		t.Fatalf("claim = %v, %v", ok, err)
	}
	if run, err := st.GetRun("run-4"); err != nil || run.Status != "running" {
		t.Fatalf("claimed run status = %s, %v", run.Status, err)
	}
	if ok, err := st.ClaimQueuedRun("run-4", time.Now()); err != nil || ok {
		t.Fatalf("double claim = %v, %v", ok, err)
	}
}

func TestCredentialListAndDelete(t *testing.T) {
	st := openStore(t)
	if err := st.SaveCredential("cred-1", "http", []byte(`{"token":"x"}`)); err != nil {
		t.Fatalf("save: %v", err)
	}
	list, err := st.ListCredentials()
	if err != nil || len(list) != 1 || list[0].ID != "cred-1" || list[0].Type != "http" {
		t.Fatalf("list = %#v, %v", list, err)
	}
	if err := st.DeleteCredential("cred-1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	list, err = st.ListCredentials()
	if err != nil || len(list) != 0 {
		t.Fatalf("after delete = %#v, %v", list, err)
	}
}
