package server_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"loom/internal/store"
)

// A viewer must never see the approval capability token: /hook/approval/{token}
// is unauthenticated, so reading it is equivalent to an operator decision.
func TestRunResponsesHideApprovalTokenFromViewers(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@x.io", "admin", "password-123456")
	addUser(t, st, "viewer@x.io", "viewer", "password-123456")

	run := store.Run{
		ID: "run_waiting", WorkflowID: "wf", WorkflowVersion: "1",
		Status: "waiting", Trigger: "manual", StartedAt: time.Now().UTC(),
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.SetRunWaitingUntil(run.ID, "approve", "capability-token", []byte(`{}`), time.Time{}, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("set waiting: %v", err)
	}

	viewer := loginAs(t, srv, "viewer@x.io", "password-123456")
	for _, path := range []string{"/api/runs", "/api/runs/" + run.ID} {
		code, body := viewer.do(t, srv, http.MethodGet, path, nil, false)
		if code != http.StatusOK {
			t.Fatalf("%s = %d: %s", path, code, body)
		}
		if bytes.Contains(body, []byte("capability-token")) || bytes.Contains(body, []byte("resumeToken")) {
			t.Fatalf("%s leaked the approval token to a viewer: %s", path, body)
		}
	}

	admin := loginAs(t, srv, "admin@x.io", "password-123456")
	code, body := admin.do(t, srv, http.MethodGet, "/api/runs/"+run.ID, nil, false)
	if code != http.StatusOK {
		t.Fatalf("admin get = %d: %s", code, body)
	}
	if !bytes.Contains(body, []byte("capability-token")) {
		t.Fatalf("operator should still receive the approval token: %s", body)
	}
}
