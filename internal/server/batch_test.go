package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"loom/internal/graph"
	"loom/internal/store"
)

func batchWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "batch-flow", Name: "Batch flow", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call",
				Config:      map[string]any{"method": "GET", "url": "https://example.test/flaky"},
				Credentials: map[string]string{"http": "test"},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
}

func waitRun(t *testing.T, srv *httptest.Server, runID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, data := call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
		if status != http.StatusOK {
			t.Fatalf("get run status = %d: %s", status, data)
		}
		run := decode(t, data)
		if terminalRun(run["status"]) {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s did not finish: %s", runID, data)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func terminalRun(value any) bool {
	status, _ := value.(string)
	return status == "done" || status == "failed" || status == "cancelled"
}

func TestBatchRunAndRetryFailedItems(t *testing.T) {
	srv, _ := newTestServer(t, "")

	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", batchWorkflow()); status != http.StatusCreated {
		t.Fatalf("create workflow status = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/batch-flow/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish status = %d: %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/jobs", "", map[string]any{
		"name": "Batch job", "templateId": "batch-flow", "templateVersion": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("create job status = %d: %s", status, data)
	}
	jobID := decode(t, data)["id"].(string)

	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/batch", "", map[string]any{
		"items": []any{
			map[string]any{"name": "one"},
			map[string]any{"name": "two"},
			map[string]any{"name": "three"},
			map[string]any{"name": "four"},
		},
		"concurrency": 1,
	})
	if status != http.StatusAccepted {
		t.Fatalf("start batch status = %d: %s", status, data)
	}
	started := decode(t, data)
	parentID, _ := started["runId"].(string)
	if parentID == "" || started["total"] != float64(4) {
		t.Fatalf("batch start response = %s", data)
	}

	parent := waitRun(t, srv, parentID)
	if parent["status"] != "failed" {
		t.Fatalf("parent status = %v, want failed: %s", parent["status"], parent)
	}
	if message, _ := parent["error"].(string); !strings.Contains(message, "1 of 4 items failed") {
		t.Fatalf("parent error = %q", message)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs?parent="+parentID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("list children status = %d: %s", status, data)
	}
	children := decode(t, data)["runs"].([]any)
	if len(children) != 4 {
		t.Fatalf("children = %d, want 4: %s", len(children), data)
	}
	failed := 0
	for _, raw := range children {
		child := raw.(map[string]any)
		if child["parentRunId"] != parentID {
			t.Fatalf("child parent = %v, want %s", child["parentRunId"], parentID)
		}
		if child["status"] == "failed" {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed children = %d, want 1: %s", failed, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs?job="+jobID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("list job runs status = %d: %s", status, data)
	}
	jobRuns := decode(t, data)["runs"].([]any)
	if len(jobRuns) != 1 {
		t.Fatalf("job runs = %d, want only the parent batch: %s", len(jobRuns), data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+parentID+"/retry", "", nil)
	if status != http.StatusAccepted {
		t.Fatalf("retry status = %d: %s", status, data)
	}
	retried := decode(t, data)
	retryID, _ := retried["runId"].(string)
	if retryID == "" || retried["total"] != float64(1) || retried["retryOf"] != parentID {
		t.Fatalf("retry response = %s", data)
	}

	retryParent := waitRun(t, srv, retryID)
	if retryParent["status"] != "done" {
		t.Fatalf("retry parent status = %v, want done: %s", retryParent["status"], retryParent)
	}
	status, data = call(t, srv, http.MethodGet, "/api/runs?parent="+retryID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("list retry children status = %d: %s", status, data)
	}
	retryChildren := decode(t, data)["runs"].([]any)
	if len(retryChildren) != 1 || retryChildren[0].(map[string]any)["status"] != "done" {
		t.Fatalf("retry children = %s", data)
	}
}

func TestBatchCSVInput(t *testing.T) {
	srv, _ := newTestServer(t, "")

	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow()); status != http.StatusCreated {
		t.Fatalf("create workflow status = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish status = %d: %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/jobs", "", map[string]any{
		"name": "CSV batch", "templateId": "linear", "templateVersion": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("create job status = %d: %s", status, data)
	}
	jobID := decode(t, data)["id"].(string)

	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/batch", "", map[string]any{
		"csv":         "name,city\nAda,London\nGrace,Paris\n",
		"concurrency": 2,
	})
	if status != http.StatusAccepted {
		t.Fatalf("start CSV batch status = %d: %s", status, data)
	}
	started := decode(t, data)
	parentID := started["runId"].(string)
	if started["total"] != float64(2) {
		t.Fatalf("CSV batch total = %v, want 2: %s", started["total"], data)
	}
	parent := waitRun(t, srv, parentID)
	if parent["status"] != "done" {
		t.Fatalf("CSV parent status = %v: %s", parent["status"], parent)
	}
	status, data = call(t, srv, http.MethodGet, "/api/runs?parent="+parentID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("list CSV children status = %d: %s", status, data)
	}
	children := decode(t, data)["runs"].([]any)
	if len(children) != 2 {
		t.Fatalf("CSV children = %d, want 2: %s", len(children), data)
	}
	for _, raw := range children {
		if raw.(map[string]any)["status"] != "done" {
			t.Fatalf("CSV child not done: %s", data)
		}
	}
}

func TestBatchParentWaitsForExpectedItems(t *testing.T) {
	srv, st := newTestServer(t, "")
	state, err := json.Marshal(map[string]any{"items": []any{"one", "two", "three"}, "concurrency": 3})
	if err != nil {
		t.Fatalf("marshal batch state: %v", err)
	}
	parentID := "run_batch_pending"
	if err := st.CreateRun(store.Run{ID: parentID, WorkflowID: "batch-flow", WorkflowVersion: "1", Status: "running", Trigger: "batch", StartedAt: time.Now().UTC(), ResumeData: state}); err != nil {
		t.Fatalf("create parent run: %v", err)
	}

	assertStatus := func(want string) map[string]any {
		t.Helper()
		status, data := call(t, srv, http.MethodGet, "/api/runs/"+parentID, "", nil)
		if status != http.StatusOK {
			t.Fatalf("get parent status = %d: %s", status, data)
		}
		run := decode(t, data)
		if run["status"] != want {
			t.Fatalf("parent status = %v, want %s: %s", run["status"], want, data)
		}
		return run
	}
	assertStatus("running")

	addChild := func(index int, status string) {
		t.Helper()
		run := store.Run{ID: "run_batch_item_" + strconv.Itoa(index), WorkflowID: "batch-flow", WorkflowVersion: "1", Status: status, Trigger: "batch", StartedAt: time.Now().UTC(), ParentRunID: parentID, ParentNodeID: "batch:" + strconv.Itoa(index)}
		if err := st.CreateRun(run); err != nil {
			t.Fatalf("create child %d: %v", index, err)
		}
	}
	addChild(0, "done")
	assertStatus("running")

	addChild(1, "done")
	addChild(2, "done")
	run := assertStatus("done")
	if run["finishedAt"] == nil {
		t.Fatalf("finished parent missing finishedAt: %s", run)
	}
}
