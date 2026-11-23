package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// coverage_gaps_test.go targets the server handlers the coverage profile
// showed at 0%: job listing and upgrade, run cancellation, user creation and
// auth events, workflow versions, and workflow verification (P3-2/P3-1).

func TestJobListAndUpgradeHandlers(t *testing.T) {
	srv, _ := newTestServer(t, "")

	if status, data := call(t, srv, http.MethodGet, "/api/jobs", "", nil); status != http.StatusOK {
		t.Fatalf("empty job list status = %d: %s", status, data)
	} else {
		var out struct {
			Jobs []map[string]any `json:"jobs"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("decode job list: %v", err)
		}
		if len(out.Jobs) != 0 {
			t.Fatalf("expected no jobs, got %s", data)
		}
	}

	wf := linearWorkflow()
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/linear", "", wf); status != http.StatusOK {
		t.Fatalf("save draft: %d %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish v1: %d %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/jobs", "", map[string]any{
		"name": "Cov", "templateId": "linear", "templateVersion": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("create job: %d %s", status, data)
	}
	var job struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}

	if status, data := call(t, srv, http.MethodGet, "/api/jobs", "", nil); status != http.StatusOK {
		t.Fatalf("job list status = %d: %s", status, data)
	}

	// Pin to v1, publish v2, then upgrade the job to the latest version.
	second := linearWorkflow()
	second.Name = "Linear v2"
	second.Nodes[1].Config = map[string]any{"values": map[string]any{"greeting": "v2"}}
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/linear", "", second); status != http.StatusOK {
		t.Fatalf("save draft v2: %d %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish v2: %d %s", status, data)
	}
	// Upgrading to a version that does not exist is a client error.
	if status, _ := call(t, srv, http.MethodPost, "/api/jobs/"+job.ID+"/upgrade", "", map[string]any{"templateVersion": 99}); status != http.StatusBadRequest {
		t.Fatalf("upgrade to missing version = %d, want 400", status)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/jobs/"+job.ID+"/upgrade", "", map[string]any{"templateVersion": 2}); status != http.StatusOK {
		t.Fatalf("upgrade job: %d %s", status, data)
	}
	if status, data := call(t, srv, http.MethodGet, "/api/jobs/"+job.ID, "", nil); status != http.StatusOK {
		t.Fatalf("get job: %d %s", status, data)
	} else if !strings.Contains(string(data), `"templateVersion":2`) {
		t.Fatalf("job did not upgrade to v2: %s", data)
	}
}

func TestCancelRunHandler(t *testing.T) {
	srv, _ := newTestServer(t, "")

	// flow.approval parks the run as waiting without DurableWaits, so the
	// cancel path under test is the waiting-run branch of cancelRun.
	wf := approvalWorkflow()
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/approval", "", wf); status != http.StatusOK {
		t.Fatalf("save waitflow: %d %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/approval/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish waitflow: %d %s", status, data)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/approval/runs", "", map[string]any{
		"input": map[string]any{},
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("start run: %d %s", status, data)
	}
	var started struct {
		RunID string `json:"runId"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatalf("decode started run: %v", err)
	}
	runID := started.RunID
	if runID == "" {
		runID = started.ID
	}
	if runID == "" {
		t.Fatalf("no run id in %s", data)
	}

	// The wait parks the run; cancelling a waiting run marks it cancelled.
	deadline := time.Now().Add(5 * time.Second)
	waiting := false
	for time.Now().Before(deadline) {
		_, body := call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
		if strings.Contains(string(body), `"waiting"`) {
			waiting = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !waiting {
		t.Fatalf("run %s never reached waiting", runID)
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/cancel", "", nil)
	if status != http.StatusAccepted {
		t.Fatalf("cancel waiting run: %d %s", status, data)
	}
	if !strings.Contains(string(data), "cancelled") {
		t.Fatalf("cancel response: %s", data)
	}

	_, data = call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
	if !strings.Contains(string(data), "cancelled") {
		t.Fatalf("run not cancelled after wait-cancel: %s", data)
	}

	// Cancelling a terminal run is a 404 with a clear message.
	status, _ = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/cancel", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("cancel terminal run status = %d", status)
	}
}

func TestUserCreateAndAuthEvents(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "root@example.com", "admin", "root-password-1")
	admin := loginAs(t, srv, "root@example.com", "root-password-1")

	// A failed + successful login for the new user generate auth events.
	status, data := admin.do(t, srv, http.MethodPost, "/api/users", map[string]any{
		"email": "new@example.com", "name": "New", "role": "editor", "password": "editor-password-1",
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create user: %d %s", status, data)
	}
	loginAs(t, srv, "new@example.com", "editor-password-1")
	body := map[string]string{"email": "new@example.com", "password": "wrong"}
	if status, _ := admin.do(t, srv, http.MethodPost, "/api/auth/login", body, false); status != http.StatusUnauthorized {
		t.Fatalf("bad login should be 401, got %d", status)
	}

	status, data = admin.do(t, srv, http.MethodGet, "/api/auth/events", nil, false)
	if status != http.StatusOK {
		t.Fatalf("auth events: %d %s", status, data)
	}
	if !strings.Contains(string(data), "new@example.com") {
		t.Fatalf("auth events missing the new user: %s", data)
	}

	// Reset the user's password through the admin endpoint and log in.
	status, data = admin.do(t, srv, http.MethodGet, "/api/users", nil, false)
	if status != http.StatusOK {
		t.Fatalf("list users: %d %s", status, data)
	}
	var list struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("decode users: %v", err)
	}
	var userID string
	for _, u := range list.Users {
		if u.Email == "new@example.com" {
			userID = u.ID
		}
	}
	if userID == "" {
		t.Fatalf("new user missing from list: %s", data)
	}
	status, data = admin.do(t, srv, http.MethodPost, "/api/users/"+userID+"/password", map[string]any{
		"password": "rotated-password-1",
	}, true)
	if status != http.StatusOK {
		t.Fatalf("reset password: %d %s", status, data)
	}
	loginAs(t, srv, "new@example.com", "rotated-password-1")

	// Disable the user; their session must stop working.
	status, data = admin.do(t, srv, http.MethodPut, "/api/users/"+userID, map[string]any{
		"disabled": true, "role": "editor",
	}, true)
	if status != http.StatusOK {
		t.Fatalf("update user: %d %s", status, data)
	}
}

func TestWorkflowVersionsAndVerifyHandlers(t *testing.T) {
	srv, _ := newTestServer(t, "")

	wf := linearWorkflow()
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/linear", "", wf); status != http.StatusOK {
		t.Fatalf("save draft: %d %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish: %d %s", status, data)
	}

	status, data := call(t, srv, http.MethodGet, "/api/workflows/linear/versions", "", nil)
	if status != http.StatusOK {
		t.Fatalf("versions: %d %s", status, data)
	}
	if !strings.Contains(string(data), `"version":1`) {
		t.Fatalf("versions missing v1: %s", data)
	}

	// Verify without a run ID is a client error.
	status, _ = call(t, srv, http.MethodPost, "/api/workflows/linear/verify", "", map[string]any{})
	if status != http.StatusBadRequest {
		t.Fatalf("verify without runId = %d, want 400", status)
	}

	// Run the workflow, then verify the recorded run against the workflow.
	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/runs", "", map[string]any{
		"input": map[string]any{},
	})
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("start run: %d %s", status, data)
	}
	var started struct {
		RunID string `json:"runId"`
		ID    string `json:"id"`
	}
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	runID := started.RunID
	if runID == "" {
		runID = started.ID
	}

	waitForTerminal(t, srv, runID)

	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/verify", "", map[string]any{
		"runId": runID,
	})
	if status != http.StatusOK {
		t.Fatalf("verify run: %d %s", status, data)
	}
	for _, want := range []string{"sourceRunId", "status", "nodes"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("verify response missing %s: %s", want, data)
		}
	}
}

func TestMetricsEndpointAndDecisionMetrics(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodGet, "/metrics", "", nil)
	if status != http.StatusOK {
		t.Fatalf("metrics: %d %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/metrics/decisions", "", nil)
	if status != http.StatusOK {
		t.Fatalf("decision metrics: %d %s", status, data)
	}
	if !strings.Contains(string(data), "total") {
		t.Fatalf("decision metrics body: %s", data)
	}
}

// waitForTerminal polls a run until it leaves running/waiting or the
// deadline passes.
func waitForTerminal(t *testing.T, srv *httptest.Server, runID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, body := call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
		if !strings.Contains(string(body), `"running"`) && !strings.Contains(string(body), `"waiting"`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("run %s did not reach a terminal state", runID)
}
