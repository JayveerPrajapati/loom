package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Audit 4E: a publish can be followed live through the in-memory progress
// registry. Publish stays a synchronous POST; the status endpoint only
// exposes the phases it walked.
func TestPublishProgressTracking(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "editor@example.com", "editor", "correct-horse-battery")
	addUser(t, st, "viewer@example.com", "viewer", "correct-horse-battery")
	if err := st.SaveWorkflow(linearWorkflow()); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	editor := loginAs(t, srv, "editor@example.com", "correct-horse-battery")
	viewer := loginAs(t, srv, "viewer@example.com", "correct-horse-battery")

	status, data := editor.do(t, srv, http.MethodPost,
		"/api/workflows/linear/publish?publishId=progress-test-1", nil, true)
	if status != http.StatusCreated {
		t.Fatalf("publish status = %d: %s", status, data)
	}

	type phase struct {
		Name   string `json:"name"`
		Status string `json:"status"`
		Ms     int64  `json:"ms"`
	}
	var snap struct {
		PublishID  string  `json:"publishId"`
		WorkflowID string  `json:"workflowId"`
		Done       bool    `json:"done"`
		Error      string  `json:"error"`
		Phases     []phase `json:"phases"`
	}
	status, data = editor.do(t, srv, http.MethodGet,
		"/api/workflows/linear/publish/status?publishId=progress-test-1", nil, false)
	if status != http.StatusOK {
		t.Fatalf("status lookup = %d: %s", status, data)
	}
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	if snap.PublishID != "progress-test-1" {
		t.Fatalf("publishId = %q", snap.PublishID)
	}
	if snap.WorkflowID != "linear" {
		t.Fatalf("workflowId = %q", snap.WorkflowID)
	}
	if !snap.Done {
		t.Fatal("done = false after a completed publish")
	}
	if snap.Error != "" {
		t.Fatalf("error = %q", snap.Error)
	}
	want := []string{"validate", "resolve", "environments", "publish", "finalize"}
	if len(snap.Phases) != len(want) {
		t.Fatalf("phases = %+v, want %v", snap.Phases, want)
	}
	for index, name := range want {
		got := snap.Phases[index]
		if got.Name != name {
			t.Fatalf("phase[%d].name = %q, want %q", index, got.Name, name)
		}
		if got.Status != "done" {
			t.Fatalf("phase[%d].status = %q, want done", index, got.Status)
		}
		if got.Ms < 0 {
			t.Fatalf("phase[%d].ms = %d, want non-negative", index, got.Ms)
		}
	}

	// The route is editor-only, so a viewer is rejected before ownership.
	status, data = viewer.do(t, srv, http.MethodGet,
		"/api/workflows/linear/publish/status?publishId=progress-test-1", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer status lookup = %d: %s", status, data)
	}

	// Unknown publish id.
	status, _ = editor.do(t, srv, http.MethodGet,
		"/api/workflows/linear/publish/status?publishId=no-such-publish", nil, false)
	if status != http.StatusNotFound {
		t.Fatalf("unknown publish status = %d, want 404", status)
	}

	// Progress belongs to the workflow that started it.
	status, _ = editor.do(t, srv, http.MethodGet,
		"/api/workflows/other/publish/status?publishId=progress-test-1", nil, false)
	if status != http.StatusNotFound {
		t.Fatalf("wrong workflow status = %d, want 404", status)
	}

	// Malformed publish ids are rejected before anything is tracked.
	status, _ = editor.do(t, srv, http.MethodPost,
		"/api/workflows/linear/publish?publishId=short", nil, true)
	if status != http.StatusBadRequest {
		t.Fatalf("short publishId = %d, want 400", status)
	}

	// Untracked publishes keep the old synchronous behavior.
	status, data = editor.do(t, srv, http.MethodPost, "/api/workflows/linear/publish", nil, true)
	if status != http.StatusCreated {
		t.Fatalf("untracked publish = %d: %s", status, data)
	}
}
