package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestApprovalRevokeDecisionsAndMetrics covers the approval lifecycle the UI
// depends on: a link with an expiry, revoking it without deciding, the
// decision record, and the metrics/backlog endpoint.
func TestApprovalRevokeDecisionsAndMetrics(t *testing.T) {
	srv, _ := newTestServer(t, "")
	const wfID = "appr-lifecycle"

	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", approvalInboxWorkflow(wfID)); status != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", status, body)
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wfID+"/runs", "", map[string]any{"input": map[string]any{}})
	if status != http.StatusOK {
		t.Fatalf("start run: %d %s", status, data)
	}
	var started struct {
		RunID  string `json:"runId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &started); err != nil || started.Status != "waiting" {
		t.Fatalf("start = %s (%v)", data, err)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+started.RunID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get run: %d %s", status, data)
	}
	var run map[string]any
	if err := json.Unmarshal(data, &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	token, _ := run["resumeToken"].(string)
	if token == "" {
		t.Fatal("waiting run has no resume token")
	}
	if run["resumeTokenExpiresAt"] == nil {
		t.Fatal("waiting run has no link expiry")
	}
	if run["resumeRequestedAt"] == nil {
		t.Fatal("waiting run has no requestedAt")
	}

	if status, body := call(t, srv, http.MethodPost, "/api/runs/"+started.RunID+"/revoke-approval", "", nil); status != http.StatusOK {
		t.Fatalf("revoke: %d %s", status, body)
	}
	if status, _ := call(t, srv, http.MethodGet, "/hook/approval/"+token, "", nil); status != http.StatusNotFound {
		t.Fatalf("revoked link status = %d, want 404", status)
	}
	status, data = call(t, srv, http.MethodGet, "/api/runs/"+started.RunID, "", nil)
	if status != http.StatusOK || json.Unmarshal(data, &run) != nil || run["status"] != "waiting" {
		t.Fatalf("revoke changed the run: %s", data)
	}

	// No decision has been made yet.
	status, data = call(t, srv, http.MethodGet, "/api/decisions?workflowId="+wfID, "", nil)
	var before struct {
		Decisions []map[string]any `json:"decisions"`
		Count     int              `json:"count"`
	}
	if err := json.Unmarshal(data, &before); err != nil || status != http.StatusOK {
		t.Fatalf("decisions: %d %s", status, data)
	}
	if before.Count != 0 {
		t.Fatalf("decisions before deciding = %d, want 0", before.Count)
	}

	if status, body := call(t, srv, http.MethodPost, "/api/runs/"+started.RunID+"/resume", "",
		map[string]any{"decision": "approved", "note": "ship it"}); status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, body)
	}

	status, data = call(t, srv, http.MethodGet, "/api/decisions?workflowId="+wfID, "", nil)
	var after struct {
		Decisions []map[string]any `json:"decisions"`
		Count     int              `json:"count"`
	}
	if err := json.Unmarshal(data, &after); err != nil || status != http.StatusOK {
		t.Fatalf("decisions after: %d %s", status, data)
	}
	if after.Count != 1 || len(after.Decisions) != 1 {
		t.Fatalf("decisions after = %d/%d, want 1", after.Count, len(after.Decisions))
	}
	d := after.Decisions[0]
	if d["decision"] != "approved" || d["note"] != "ship it" || d["source"] != "api" {
		t.Fatalf("decision = %v", d)
	}
	if label, _ := d["actorLabel"].(string); label == "" {
		t.Fatalf("decision has no actor label: %v", d)
	}

	status, data = call(t, srv, http.MethodGet, "/api/metrics/decisions", "", nil)
	var stats map[string]any
	if err := json.Unmarshal(data, &stats); err != nil || status != http.StatusOK {
		t.Fatalf("metrics: %d %s", status, data)
	}
	if stats["total"] != float64(1) || stats["approved"] != float64(1) {
		t.Fatalf("metrics = %v", stats)
	}
	if _, ok := stats["waitedP50Ms"]; !ok {
		t.Fatalf("metrics missing percentiles: %v", stats)
	}

	// The runs list can search and page, and reports the true total.
	status, data = call(t, srv, http.MethodGet, "/api/runs?workflow="+wfID+"&limit=1", "", nil)
	var listed struct {
		Runs  []map[string]any `json:"runs"`
		Count int              `json:"count"`
	}
	if err := json.Unmarshal(data, &listed); err != nil || status != http.StatusOK {
		t.Fatalf("list runs: %d %s", status, data)
	}
	if listed.Count != 1 || len(listed.Runs) != 1 {
		t.Fatalf("listed = %d/%d", listed.Count, len(listed.Runs))
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs?workflow="+wfID+"&q=no-such-run", "", nil)
	if err := json.Unmarshal(data, &listed); err != nil || status != http.StatusOK {
		t.Fatalf("search: %d %s", status, data)
	}
	if listed.Count != 0 || len(listed.Runs) != 0 {
		t.Fatalf("search matched %d runs, want 0", listed.Count)
	}
}
