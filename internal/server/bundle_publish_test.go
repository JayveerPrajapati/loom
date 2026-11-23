package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"loom/internal/graph"
	"loom/internal/store"
)

// TestPublishResolvesBundleInline (Phase 1 P0-2): a profile bundle composes
// at publish — the data.map item mapping arrives via the bundle, the
// published snapshot is self-contained, and every bundled asset is pinned.
func TestPublishResolvesBundleInline(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/mappings", "test-token", map[string]any{
		"name": "Identity", "kind": "item",
		"rows": []store.MappingRow{{Target: "email", Source: "{{ $json.email }}"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create mapping = %d: %s", status, data)
	}
	var mapping store.Mapping
	_ = json.Unmarshal(data, &mapping)

	status, data = call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", map[string]any{
		"name":   "Payload",
		"fields": []map[string]any{{"name": "email", "type": "string"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create schema = %d: %s", status, data)
	}
	var schema store.DataSchema
	_ = json.Unmarshal(data, &schema)

	// A data.map profile composed of exactly one item mapping (bundle as
	// the whole content) plus an input schema.
	status, data = call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "Email mapper", "nodeType": "data.map",
		"bundle": map[string]any{
			"mappings": map[string]any{"item": mapping.ID},
			"schemas":  map[string]any{"input": schema.ID},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create data.map profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)

	// The workflow's data.map node references only the profile — the
	// mapping and schema arrive through the bundle.
	wf := &graph.Workflow{
		ID: "wf-bundle", Name: "Bundle workflow", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "map", Type: "data.map", TypeVersion: 1, Name: "Map", Config: map[string]any{}, ProfileRef: profile.ID},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "map", Port: "main"}}},
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	// The snapshot is self-contained: rows and schema fields inline.
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 = %d: %s", status, data)
	}
	var snap graph.Workflow
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	node := snap.Nodes[1]
	rows, _ := node.Config["mappingRows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("snapshot mappingRows = %#v", node.Config["mappingRows"])
	}
	if node.InputSchemaRef != schema.ID || len(node.InputSchemaFields) != 1 {
		t.Fatalf("snapshot input schema = %q fields=%#v", node.InputSchemaRef, node.InputSchemaFields)
	}

	// Every bundled asset is pinned at publish.
	mUsage, err := st.MappingUsage(mapping.ID)
	if err != nil || len(mUsage) != 1 {
		t.Fatalf("mapping usage = %v (%v)", mUsage, err)
	}
	sUsage, err := st.DataSchemaUsage(schema.ID)
	if err != nil || len(sUsage) != 1 {
		t.Fatalf("schema usage = %v (%v)", sUsage, err)
	}
	pUsage, err := st.ConfigProfileUsage(profile.ID)
	if err != nil || len(pUsage) != 1 {
		t.Fatalf("profile usage = %v (%v)", pUsage, err)
	}

	// The mapping is now guarded by both the publish pin and the bundle
	// reference.
	if status, data = call(t, srv, http.MethodDelete, "/api/mappings/"+mapping.ID, "test-token", nil); status != http.StatusConflict {
		t.Fatalf("delete pinned mapping = %d, want 409: %s", status, data)
	}
}
