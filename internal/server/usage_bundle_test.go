package server_test

import (
	"net/http"
	"testing"

	"loom/internal/graph"
	"loom/internal/store"
)

func TestBundleUsageAppearsOnEveryAssetFamily(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	if _, err := st.CreateMapping(&store.Mapping{
		ID: "map-bundle", Name: "Bundled mapping", Kind: "item",
		Visibility: store.VisibilityShared, Rows: []store.MappingRow{},
	}, "test"); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if _, err := st.CreateDataSchema(&store.DataSchema{
		ID: "schema-bundle", Name: "Bundled contract",
		Visibility: store.VisibilityShared,
	}, "test"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := st.CreateScript(&store.Script{
		ID: "script-bundle", Name: "Bundled script",
		Language: store.ScriptLanguageJavaScript, Visibility: store.VisibilityShared,
		Source: "return $json;",
	}, "test"); err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		Name: "Bundle profile", NodeType: "http.request",
		Visibility: store.VisibilityShared,
		Config:     map[string]any{"method": "GET", "url": "https://api.example.com"},
		Bundle: &store.ProfileBundle{
			Mappings: map[string]string{"body": "map-bundle"},
			Schemas:  map[string]string{"input": "schema-bundle"},
			Script:   "script-bundle",
		},
	}, "test"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	cases := []struct {
		path string
	}{
		{"/api/mappings/map-bundle/usage"},
		{"/api/data-schemas/schema-bundle/usage"},
		{"/api/scripts/script-bundle/usage"},
	}
	for _, tc := range cases {
		status, data := call(t, srv, http.MethodGet, tc.path, "test-token", nil)
		if status != http.StatusOK {
			t.Fatalf("%s = %d: %s", tc.path, status, data)
		}
		body := decode(t, data)
		profiles, _ := body["profiles"].([]any)
		if len(profiles) != 1 {
			t.Fatalf("%s profiles = %s, want one bundle profile", tc.path, data)
		}
		profile := profiles[0].(map[string]any)
		if profile["name"] != "Bundle profile" || profile["nodeType"] != "http.request" {
			t.Fatalf("%s profile = %s", tc.path, data)
		}
		if body["pins"].(float64) != 0 || body["count"].(float64) != 1 {
			t.Fatalf("%s counts = %s", tc.path, data)
		}
	}
}

func TestWorkflowDeleteMissingIsNotFoundAndAudited(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodDelete, "/api/workflows/missing", "test-token", nil)
	if status != http.StatusNotFound {
		t.Fatalf("delete missing workflow = %d, want 404: %s", status, data)
	}

	if err := st.SaveWorkflow(&graph.Workflow{ID: "wf-delete", Name: "Delete me"}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	status, data = call(t, srv, http.MethodDelete, "/api/workflows/wf-delete", "test-token", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete workflow = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/auth/events", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("auth events = %d: %s", status, data)
	}
	events := decode(t, data)["events"].([]any)
	found := false
	for _, raw := range events {
		event := raw.(map[string]any)
		if event["type"] == "workflow.deleted" && event["detail"] == "wf-delete" {
			found = true
		}
	}
	if !found {
		t.Fatalf("workflow.deleted audit event missing: %s", data)
	}
}
