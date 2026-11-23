package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"loom/internal/store"
)

// GET /api/ops/storage serves the operational view (storage-operations
// campaign). The service token carries admin so the happy path is the
// token; the role gate is pinned with a real viewer session.
func TestStorageOpsEndpoint(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodGet, "/api/ops/storage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("storage ops = %d: %s", status, data)
	}
	var body struct {
		Stats store.StorageStats `json:"stats"`
		Hint  string             `json:"hint"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	// The empty test store still reports all sections coherently.
	if body.Stats.Runs != 0 || len(body.Stats.Reclaimable) != 3 {
		t.Fatalf("empty-store stats wrong: %#v", body.Stats)
	}
	if body.Hint == "" {
		t.Fatal("expected the retention hint")
	}

	// Seed one finished run with one event referencing one blob, via
	// the store's public API (chain-signed event, content-addressed
	// blob).
	if err := st.CreateRun(store.Run{ID: "ops-run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual"}); err != nil {
		t.Fatal(err)
	}
	hash, err := st.SaveBlob([]byte("0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AppendEvent("ops-run", store.Event{
		Type: "run.started", At: time.Now().UTC(), InputHash: hash, Actor: "ops-test",
	}); err != nil {
		t.Fatal(err)
	}
	status, data = call(t, srv, http.MethodGet, "/api/ops/storage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("storage ops after seed = %d", status)
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	if body.Stats.Runs != 1 || body.Stats.Events != 1 || body.Stats.Blobs != 1 {
		t.Fatalf("seeded stats wrong: %#v", body.Stats)
	}
}

func TestStorageOpsEditorGate(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	addUser(t, st, "ops-viewer@example.com", "viewer", "viewer-password")
	session := loginAs(t, srv, "ops-viewer@example.com", "viewer-password")

	status, _ := session.do(t, srv, http.MethodGet, "/api/ops/storage", nil, false)
	if status != http.StatusForbidden {
		t.Fatalf("viewer must be forbidden from ops metrics, got %d", status)
	}
}
