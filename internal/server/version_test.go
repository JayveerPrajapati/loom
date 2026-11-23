package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// S1: /api/version is intentionally public and returns only the build
// identity — no workspace, user, run, configuration or credential data.
// This pins the exposure documented in docs/security.md#public-endpoints.
func TestVersionEndpointIsPublicAndMinimal(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodGet, "/api/version", "", nil)
	if status != http.StatusOK {
		t.Fatalf("unauthenticated /api/version status = %d: %s", status, data)
	}

	var body map[string]string
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode /api/version: %v (%s)", err, data)
	}
	if len(body) != 2 {
		t.Fatalf("/api/version must expose only name and version, got %s", data)
	}
	if body["name"] != "loom" {
		t.Fatalf("/api/version name = %q, want loom", body["name"])
	}
	if _, ok := body["version"]; !ok {
		t.Fatalf("/api/version must include version, got %s", data)
	}
}
