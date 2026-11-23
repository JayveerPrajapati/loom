package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCapabilitiesAreAdminOnly(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	addUser(t, st, "admin@example.com", "admin", "admin-password")
	editor := loginAs(t, srv, "editor@example.com", "editor-password")
	admin := loginAs(t, srv, "admin@example.com", "admin-password")

	if status, data := editor.do(t, srv, http.MethodGet, "/api/capabilities", nil, false); status != http.StatusForbidden {
		t.Fatalf("editor capabilities = %d, want 403: %s", status, data)
	}
	status, data := admin.do(t, srv, http.MethodGet, "/api/capabilities", nil, false)
	if status != http.StatusOK {
		t.Fatalf("admin capabilities = %d: %s", status, data)
	}
	var body struct {
		Prod       bool `json:"prod"`
		Anonymous  bool `json:"anonymous"`
		Subprocess struct {
			Enabled bool `json:"enabled"`
		} `json:"subprocess"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("capabilities JSON: %v (%s)", err, data)
	}
	if body.Prod || body.Anonymous || body.Subprocess.Enabled {
		t.Fatalf("zero capability state = %s", data)
	}
	if body.Warnings == nil {
		t.Fatalf("warnings must be present: %s", data)
	}
}
