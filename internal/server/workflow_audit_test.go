package server_test

import (
	"net/http"
	"strings"
	"testing"

	"loom/internal/store"
)

func findAuditEvent(t *testing.T, data []byte, eventType, substring string) map[string]any {
	t.Helper()
	body := decode(t, data)
	events, _ := body["events"].([]any)
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		if event["type"] != eventType {
			continue
		}
		if detail, _ := event["detail"].(string); substring == "" || strings.Contains(detail, substring) {
			return event
		}
	}
	t.Fatalf("audit event %q containing %q missing: %s", eventType, substring, data)
	return nil
}

func TestWorkflowPublishAuditNamesPrivateAssets(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	profile, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-audit-private", Name: "Private audit profile", NodeType: "http.request",
		Visibility: store.VisibilityPrivate,
		Config:     map[string]any{"method": "GET", "url": "https://api.example.com/audit"},
	}, "test")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	wf := httpWorkflow()
	wf.ID = "wf-audit-publish"
	wf.Name = "Audit publish"
	wf.Nodes[1].ProfileRef = profile.ID
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	stored, err := st.GetWorkflow(wf.ID)
	if err != nil || stored.Nodes[1].ProfileRef != profile.ID {
		t.Fatalf("stored profileRef = %q, err=%v", stored.Nodes[1].ProfileRef, err)
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/auth/events", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("auth events = %d: %s", status, data)
	}
	event := findAuditEvent(t, data, "workflow.published", wf.ID+" v1")
	detail := event["detail"].(string)
	if !strings.Contains(detail, "private=profile:"+profile.ID) {
		t.Fatalf("publish detail = %q, want private asset id", detail)
	}
}

func TestWorkflowUpgradeAudit(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	profile, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-audit-upgrade", Name: "Upgrade profile", NodeType: "http.request",
		Visibility: store.VisibilityShared,
		Config:     map[string]any{"method": "GET", "url": "https://api.example.com/v1"},
	}, "test")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	wf := httpWorkflow()
	wf.ID = "wf-audit-upgrade"
	wf.Name = "Audit upgrade"
	wf.Nodes[1].ProfileRef = profile.ID
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	profile.Config["url"] = "https://api.example.com/v2"
	if _, err := st.UpdateConfigProfile(&profile, profile.Version, "test"); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/upgrade-preview", "test-token", map[string]any{"apply": true})
	if status != http.StatusCreated {
		t.Fatalf("upgrade apply = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/auth/events", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("auth events = %d: %s", status, data)
	}
	findAuditEvent(t, data, "workflow.profile_upgrade", wf.ID+" v2")
}

func TestWorkflowProfileDetachAudit(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	profile, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-audit-detach", Name: "Detach profile", NodeType: "http.request",
		Visibility: store.VisibilityShared,
		Config:     map[string]any{"method": "GET", "url": "https://api.example.com/detach"},
	}, "test")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	wf := httpWorkflow()
	wf.ID = "wf-audit-detach"
	wf.Name = "Audit detach"
	wf.Nodes[1].ProfileRef = profile.ID
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	stored, err := st.GetWorkflow(wf.ID)
	if err != nil || stored.Nodes[1].ProfileRef != profile.ID {
		t.Fatalf("stored profileRef = %q, err=%v", stored.Nodes[1].ProfileRef, err)
	}
	wf.Nodes[1].ProfileRef = ""
	status, data := call(t, srv, http.MethodPut, "/api/workflows/"+wf.ID, "test-token", wf)
	if status != http.StatusOK {
		t.Fatalf("detach update = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/auth/events", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("auth events = %d: %s", status, data)
	}
	findAuditEvent(t, data, "workflow.profile_detached", "http:"+profile.ID)
}
