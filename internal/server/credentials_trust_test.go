package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"loom/internal/graph"
	"loom/internal/store"
)

func credentialByID(t *testing.T, data []byte, id string) map[string]any {
	t.Helper()
	body := decode(t, data)
	credentials, _ := body["credentials"].([]any)
	for _, raw := range credentials {
		credential, _ := raw.(map[string]any)
		if credential["id"] == id {
			return credential
		}
	}
	t.Fatalf("credential %q missing from %s", id, data)
	return nil
}

func TestCredentialUsageGuardsDeletionAndRotation(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/credentials", "test-token", map[string]any{
		"type": "http",
		"data": map[string]any{"token": "old-secret"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create credential = %d: %s", status, data)
	}
	id := decode(t, data)["id"].(string)

	status, data = call(t, srv, http.MethodGet, "/api/credentials", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials = %d: %s", status, data)
	}
	if got := credentialByID(t, data, id)["usageCount"].(float64); got != 0 {
		t.Fatalf("initial usageCount = %v, want 0", got)
	}

	profile, err := st.CreateConfigProfile(&store.ConfigProfile{
		Name: "HTTP profile", NodeType: "http.request",
		Config:         map[string]any{"method": "GET", "url": "https://api.example.com"},
		CredentialRefs: map[string]string{"auth": id},
	}, "test")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	wf := &graph.Workflow{
		ID: "wf-credential", Name: "Credential workflow",
		Nodes: []graph.Node{{
			ID: "http", Type: "http.request",
			Config:      map[string]any{"method": "GET", "url": "https://api.example.com"},
			Credentials: map[string]string{"auth": id},
		}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatalf("publish workflow: %v", err)
	}

	status, data = call(t, srv, http.MethodGet, "/api/credentials", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials = %d: %s", status, data)
	}
	if got := credentialByID(t, data, id)["usageCount"].(float64); got != 2 {
		t.Fatalf("usageCount = %v, want 2 (profile + published version)", got)
	}
	if bytes.Contains(data, []byte("old-secret")) {
		t.Fatalf("list leaked secret material: %s", data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/credentials/"+id+"/usage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("credential usage = %d: %s", status, data)
	}
	usage := decode(t, data)
	profiles, _ := usage["profiles"].([]any)
	workflows, _ := usage["workflows"].([]any)
	if len(profiles) != 1 || len(workflows) != 1 {
		t.Fatalf("usage = %s", data)
	}
	if profiles[0].(map[string]any)["profileId"] != profile.ID {
		t.Fatalf("profile usage = %s", data)
	}
	if workflows[0].(map[string]any)["workflowId"] != "wf-credential" {
		t.Fatalf("workflow usage = %s", data)
	}

	status, data = call(t, srv, http.MethodDelete, "/api/credentials/"+id, "test-token", nil)
	if status != http.StatusConflict {
		t.Fatalf("in-use delete = %d, want 409: %s", status, data)
	}
	message := decode(t, data)["error"].(string)
	if !strings.Contains(message, "HTTP profile") || !strings.Contains(message, "Credential workflow") {
		t.Fatalf("delete guard message = %q", message)
	}

	status, data = call(t, srv, http.MethodPost, "/api/credentials/"+id+"/rotate", "test-token", map[string]any{
		"data": map[string]any{"token": "new-secret"},
	})
	if status != http.StatusOK {
		t.Fatalf("rotate = %d: %s", status, data)
	}
	if bytes.Contains(data, []byte("new-secret")) {
		t.Fatalf("rotate response leaked secret material: %s", data)
	}
	if got := decode(t, data)["references"].(float64); got != 2 {
		t.Fatalf("rotate references = %v, want 2", got)
	}
	_, plaintext, err := st.GetCredential(id)
	if err != nil {
		t.Fatalf("get rotated credential: %v", err)
	}
	if !strings.Contains(string(plaintext), "new-secret") {
		t.Fatalf("rotated value = %s", plaintext)
	}

	status, data = call(t, srv, http.MethodGet, "/api/auth/events", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("auth events = %d: %s", status, data)
	}
	events := decode(t, data)["events"].([]any)
	types := map[string]bool{}
	for _, raw := range events {
		event := raw.(map[string]any)
		types[event["type"].(string)] = true
	}
	for _, want := range []string{"credential.created", "credential.rotated"} {
		if !types[want] {
			t.Fatalf("audit events missing %q: %s", want, data)
		}
	}

	status, data = call(t, srv, http.MethodPost, "/api/credentials", "test-token", map[string]any{
		"type": "postgres", "data": map[string]any{"url": "postgres://example"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create second credential = %d: %s", status, data)
	}
	second := decode(t, data)["id"].(string)
	status, data = call(t, srv, http.MethodDelete, "/api/credentials/"+second, "test-token", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete unused credential = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/credentials/"+id+"/rotate", "test-token", map[string]any{
		"data": []any{"not", "an", "object"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("array rotate = %d, want 400: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/credentials/missing/rotate", "test-token", map[string]any{
		"data": map[string]any{"token": "x"},
	})
	if status != http.StatusNotFound {
		t.Fatalf("missing rotate = %d, want 404: %s", status, data)
	}
	status, data = call(t, srv, http.MethodDelete, "/api/credentials/missing", "test-token", nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing delete = %d, want 404: %s", status, data)
	}
}

func TestCredentialRoutesAreAdminOnly(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	if err := st.SaveCredential("cred-rbac", "http", []byte(`{"token":"secret"}`)); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	session := loginAs(t, srv, "editor@example.com", "editor-password")

	cases := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodGet, "/api/credentials", nil},
		{http.MethodGet, "/api/credentials/cred-rbac/usage", nil},
		{http.MethodPost, "/api/credentials", map[string]any{"type": "http", "data": map[string]any{"token": "x"}}},
		{http.MethodPost, "/api/credentials/cred-rbac/rotate", map[string]any{"data": map[string]any{"token": "x"}}},
		{http.MethodDelete, "/api/credentials/cred-rbac", nil},
	}
	for _, tc := range cases {
		status, data := session.do(t, srv, tc.method, tc.path, tc.body, tc.method != http.MethodGet)
		if status != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want 403: %s", tc.method, tc.path, status, data)
		}
	}
	if _, _, err := st.GetCredential("cred-rbac"); err != nil {
		t.Fatalf("editor request deleted the credential: %v", err)
	}
}

func TestCredentialUsageIsJSONSafe(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	if err := st.SaveCredential("cred-json", "http", []byte(`{"token":"secret"}`)); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	status, data := call(t, srv, http.MethodGet, "/api/credentials/cred-json/usage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("usage = %d: %s", status, data)
	}
	var usage store.CredentialUsage
	if err := json.Unmarshal(data, &usage); err != nil {
		t.Fatalf("usage is not JSON-safe: %v (%s)", err, data)
	}
	if usage.Profiles == nil || usage.Workflows == nil {
		t.Fatalf("usage arrays must be present, got %s", data)
	}
}
