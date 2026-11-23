package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loom/internal/graph"
)

// sparseProfileWorkflow builds a draft whose HTTP node carries no config of
// its own: every value comes from the attached profile, exactly like a node
// created through the editor.
func sparseProfileWorkflow(id, profileID string) *graph.Workflow {
	return &graph.Workflow{
		ID: id, Name: "Sparse profile run", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "HTTP",
				ProfileRef: profileID, ProfileOverrides: map[string]bool{},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "http", Port: "main"}},
		},
	}
}

func createHTTPProfile(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "", map[string]any{
		"name":     "run-prep-profile",
		"nodeType": "http.request",
		"config":   map[string]any{"method": "GET", "url": "https://example.com/from-profile"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	id, _ := decode(t, data)["id"].(string)
	if id == "" {
		t.Fatalf("profile id missing: %s", data)
	}
	return id
}

// A manual run of a never-published draft must execute the same effective
// config a publish would snapshot. Before this, the engine validated the raw
// node config and rejected profile-supplied required fields with
// missing_config.
func TestDraftRunResolvesProfileConfig(t *testing.T) {
	srv, _ := newTestServer(t, "")
	profileID := createHTTPProfile(t, srv)

	wf := sparseProfileWorkflow("run-profile-wf", profileID)
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/run-profile-wf/runs", "", map[string]any{
		"input": map[string]any{},
	})
	if status != http.StatusOK {
		t.Fatalf("run = %d: %s (profile-supplied url must resolve before validation)", status, data)
	}
	result := decode(t, data)
	if result["status"] != "done" {
		t.Fatalf("run status = %v: %s", result["status"], data)
	}
}

// A draft data.map node stores only mappingRef; the rows arrive at publish.
// A run before that publish must resolve them too, or the node fails with
// "no mapping rows resolved".
func TestDraftRunResolvesMappingRef(t *testing.T) {
	srv, _ := newTestServer(t, "")
	status, data := call(t, srv, http.MethodPost, "/api/mappings", "", map[string]any{
		"name": "run-prep-mapping",
		"kind": "item",
		"rows": []map[string]any{{"target": "label", "source": "$json.name"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create mapping = %d: %s", status, data)
	}
	mappingID, _ := decode(t, data)["id"].(string)

	wf := &graph.Workflow{
		ID: "run-mapping-wf", Name: "Mapping run", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "map", Type: "data.map", TypeVersion: 1, Name: "Map", Config: map[string]any{"mappingRef": mappingID}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "map", Port: "main"}},
		},
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/run-mapping-wf/runs", "", map[string]any{
		"input": map[string]any{"name": "loom"},
	})
	if status != http.StatusOK {
		t.Fatalf("run = %d: %s (draft mappingRef must resolve rows)", status, data)
	}
	result := decode(t, data)
	if result["status"] != "done" {
		t.Fatalf("run status = %v: %s", result["status"], data)
	}
	outputs, _ := result["outputs"].(map[string]any)
	main, _ := outputs["map"].(map[string]any)
	if main["main"] == nil {
		// Outputs are keyed by port; the mapped item carries the label.
		t.Logf("outputs: %#v", outputs)
	}
	if !strings.Contains(string(data), `"loom"`) {
		t.Fatalf("mapped output missing label: %s", data)
	}
}

// A draft code node references a script; publish inlines the pinned source.
// A run before that publish must resolve the script as well.
func TestDraftRunResolvesScriptRef(t *testing.T) {
	srv, _ := newTestServer(t, "")
	status, data := call(t, srv, http.MethodPost, "/api/scripts", "", map[string]any{
		"name":     "run-prep-script",
		"language": "javascript",
		"source":   "return {answer: 42};",
	})
	if status != http.StatusCreated {
		t.Fatalf("create script = %d: %s", status, data)
	}
	scriptID, _ := decode(t, data)["id"].(string)

	wf := &graph.Workflow{
		ID: "run-script-wf", Name: "Script run", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "code", Type: "code.run", TypeVersion: 1, Name: "Code", Config: map[string]any{"scriptRef": scriptID}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "code", Port: "main"}},
		},
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/run-script-wf/runs", "", map[string]any{
		"input": map[string]any{},
	})
	if status != http.StatusOK {
		t.Fatalf("run = %d: %s (draft scriptRef must resolve source)", status, data)
	}
	result := decode(t, data)
	if result["status"] != "done" {
		t.Fatalf("run status = %v: %s", result["status"], data)
	}
	if !strings.Contains(string(data), "42") {
		t.Fatalf("script output missing: %s", data)
	}
}

// The webhook trigger resolves the stored row before its own method/secret
// checks and before the engine starts, so a profile-supplied url works and
// an open hook answers 200.
func TestWebhookRunResolvesProfileConfig(t *testing.T) {
	srv, _ := newTestServer(t, "")
	profileID := createHTTPProfile(t, srv)

	wf := &graph.Workflow{
		ID: "run-hook-wf", Name: "Hook run", Version: 1,
		Nodes: []graph.Node{
			{
				ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
				Config: map[string]any{"path": "run-prep-hook", "method": "POST"},
			},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "HTTP",
				ProfileRef: profileID, ProfileOverrides: map[string]bool{},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "http", Port: "main"}},
		},
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}

	status, data := call(t, srv, http.MethodPost, "/hook/run-prep-hook", "", map[string]any{"name": "loom"})
	if status != http.StatusOK {
		t.Fatalf("webhook = %d: %s (profile-supplied url must resolve before validation)", status, data)
	}
	if runID, _ := decode(t, data)["runId"].(string); runID == "" {
		t.Fatalf("webhook did not start a run: %s", data)
	}
}

// A published row is already resolved. Running it must re-resolve without
// changing stored values (the node's resolved values match the profile).
func TestPublishedRunStaysIdempotent(t *testing.T) {
	srv, _ := newTestServer(t, "")
	profileID := createHTTPProfile(t, srv)

	wf := sparseProfileWorkflow("run-published-wf", profileID)
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}

	if status, data := call(t, srv, http.MethodPost, "/api/workflows/run-published-wf/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	stored := func() map[string]any {
		status, data := call(t, srv, http.MethodGet, "/api/workflows/run-published-wf", "", nil)
		if status != http.StatusOK {
			t.Fatalf("get workflow = %d: %s", status, data)
		}
		nodes, _ := decode(t, data)["nodes"].([]any)
		for _, raw := range nodes {
			node, _ := raw.(map[string]any)
			if node["id"] == "http" {
				return node
			}
		}
		t.Fatalf("http node missing: %s", data)
		return nil
	}
	before := stored()
	cfg, _ := before["config"].(map[string]any)
	if cfg["url"] != "https://example.com/from-profile" {
		t.Fatalf("published url = %v, want the profile value", cfg["url"])
	}

	for attempt := 0; attempt < 2; attempt++ {
		status, data := call(t, srv, http.MethodPost, "/api/workflows/run-published-wf/runs", "", map[string]any{
			"input": map[string]any{},
		})
		if status != http.StatusOK {
			t.Fatalf("run %d = %d: %s", attempt+1, status, data)
		}
		if result := decode(t, data); result["status"] != "done" {
			t.Fatalf("run %d status = %v: %s", attempt+1, result["status"], data)
		}
	}

	after := stored()
	cfgAfter, _ := after["config"].(map[string]any)
	if cfgAfter["url"] != "https://example.com/from-profile" {
		t.Fatalf("run mutated the stored url: %v", cfgAfter["url"])
	}
	if _, ok := after["profileOverrides"].(map[string]any); !ok {
		t.Fatalf("run dropped profileOverrides: %#v", after)
	}
}

// A deleted profile is a named, actionable error, not an engine-level
// missing_config on the node.
func TestDraftRunMissingProfileIsNamed(t *testing.T) {
	srv, _ := newTestServer(t, "")
	profileID := createHTTPProfile(t, srv)

	wf := sparseProfileWorkflow("run-missing-profile-wf", profileID)
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodDelete, "/api/config-profiles/"+profileID, "", nil); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("delete profile = %d: %s", status, data)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/run-missing-profile-wf/runs", "", map[string]any{
		"input": map[string]any{},
	})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("run = %d: %s, want 422", status, data)
	}
	if !strings.Contains(string(data), `"missing_profile"`) || !strings.Contains(string(data), `"invalid workflow"`) {
		t.Fatalf("run error = %s, want a named missing_profile issue", data)
	}
}
