package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"loom/internal/graph"
	"loom/internal/store"
)

func validMappingBody() map[string]any {
	return map[string]any{
		"name":        "Lead to customer",
		"description": "Normalizes inbound leads",
		"rows": []map[string]any{
			{"target": "email", "source": "{{ $json.email }}", "required": true},
			{"target": "city", "source": "{{ $json.address.city }}"},
			{"target": "fullName", "source": "{{ $json.first }} {{ $json.last }}", "required": true},
		},
		"tags":  []string{"crm", "normalize"},
		"owner": "team-a",
	}
}

func mapWorkflow(id, mappingRef string) *graph.Workflow {
	return &graph.Workflow{
		ID: id, Name: "Map", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "map", Type: "data.map", TypeVersion: 1, Name: "Map data",
				Config: map[string]any{"mappingRef": mappingRef},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "map", Port: "main"}},
		},
	}
}

// The mapping API mirrors the contract and profile surfaces: CRUD,
// append-only versions, 409 on stale writes, delete guards, RBAC.
func TestMappingCRUDVersioningAndGuards(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/mappings", "test-token", validMappingBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var mapping store.Mapping
	if err := json.Unmarshal(data, &mapping); err != nil {
		t.Fatal(err)
	}
	if mapping.Version != 1 || len(mapping.Rows) != 3 {
		t.Fatalf("mapping = %+v", mapping)
	}

	// Duplicate targets and empty sources are rejected.
	for _, body := range []map[string]any{
		{"name": "X", "rows": []map[string]any{{"target": "a", "source": "1"}, {"target": "a", "source": "2"}}},
		{"name": "X", "rows": []map[string]any{{"target": "a", "source": ""}}},
		{"name": "X", "rows": []map[string]any{}},
	} {
		status, data = call(t, srv, http.MethodPost, "/api/mappings", "test-token", body)
		if status != http.StatusBadRequest {
			t.Fatalf("invalid body accepted = %d: %s", status, data)
		}
	}

	// A second version appends; a stale write 409s.
	body := validMappingBody()
	body["rows"] = []map[string]any{{"target": "email", "source": "{{ $json.email }}", "required": true}}
	body["version"] = 1
	status, data = call(t, srv, http.MethodPut, "/api/mappings/"+mapping.ID, "test-token", body)
	if status != http.StatusOK {
		t.Fatalf("update = %d: %s", status, data)
	}
	body["version"] = 1
	status, data = call(t, srv, http.MethodPut, "/api/mappings/"+mapping.ID, "test-token", body)
	if status != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409", status)
	}

	// Versions list both snapshots.
	status, data = call(t, srv, http.MethodGet, "/api/mappings/"+mapping.ID+"/versions", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("versions = %d", status)
	}
	var listed struct {
		Versions []store.MappingVersion `json:"versions"`
	}
	_ = json.Unmarshal(data, &listed)
	if len(listed.Versions) != 2 {
		t.Fatalf("versions = %+v", listed.Versions)
	}

	// Not found is 404.
	status, _ = call(t, srv, http.MethodGet, "/api/mappings/ghost", "test-token", nil)
	if status != http.StatusNotFound {
		t.Fatalf("ghost mapping = %d", status)
	}

	// Delete works when unused.
	status, data = call(t, srv, http.MethodPost, "/api/mappings/"+mapping.ID+"/duplicate", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicate = %d: %s", status, data)
	}
	var copy store.Mapping
	_ = json.Unmarshal(data, &copy)
	status, _ = call(t, srv, http.MethodDelete, "/api/mappings/"+copy.ID, "test-token", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete unused = %d", status)
	}
}

// A data.map node's mappingRef resolves inline at publish and pins usage
// (D4/D5 pattern), and dangling refs are named errors at draft save.
func TestPublishResolvesMappingRefsInline(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/mappings", "test-token", validMappingBody())
	if status != http.StatusCreated {
		t.Fatalf("create mapping = %d: %s", status, data)
	}
	var mapping store.Mapping
	_ = json.Unmarshal(data, &mapping)

	wf := mapWorkflow("mapping-resolve", mapping.ID)
	status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
	if status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/mapping-resolve/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/workflows/mapping-resolve/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version = %d: %s", status, data)
	}
	var snap struct {
		Nodes []struct {
			Config map[string]any `json:"config"`
		} `json:"nodes"`
	}
	_ = json.Unmarshal(data, &snap)
	rows, _ := snap.Nodes[1].Config["mappingRows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("mappingRows not resolved: %+v", snap.Nodes[1].Config)
	}
	if snap.Nodes[1].Config["mappingRef"] != mapping.ID {
		t.Fatalf("ref not preserved: %+v", snap.Nodes[1].Config)
	}

	usage, err := st.MappingUsage(mapping.ID)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %v, %v", usage, err)
	}
	status, _ = call(t, srv, http.MethodDelete, "/api/mappings/"+mapping.ID, "test-token", nil)
	if status != http.StatusConflict {
		t.Fatalf("delete in-use mapping = %d, want 409", status)
	}
}

func TestWorkflowSaveRejectsMissingMappingRef(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")
	wf := mapWorkflow("mapping-ghost", "ghost-mapping")
	status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("create with ghost mapping = %d: %s", status, data)
	}
}

// v3 plan D4/D6: an http.request node's parameter groups publish as plain
// compiled config — mapping rows resolved, the node's own fields rows on
// top — with usage pins recorded and delete guards armed. Kind mismatches
// and bad row shapes are named 422s at draft save.
func TestPublishResolvesHTTPParamGroups(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	qBody := validMappingBody()
	qBody["name"] = "Paging params"
	qBody["kind"] = "query"
	qBody["rows"] = []map[string]any{
		{"target": "limit", "source": "20"},
		{"target": "source", "source": "web"},
	}
	status, data := call(t, srv, http.MethodPost, "/api/mappings", "test-token", qBody)
	if status != http.StatusCreated {
		t.Fatalf("create query mapping = %d: %s", status, data)
	}
	var qMap store.Mapping
	_ = json.Unmarshal(data, &qMap)
	if qMap.Kind != "query" {
		t.Fatalf("created kind = %q", qMap.Kind)
	}

	iBody := validMappingBody()
	iBody["name"] = "Item mapping stays item"
	status, data = call(t, srv, http.MethodPost, "/api/mappings", "test-token", iBody)
	if status != http.StatusCreated {
		t.Fatalf("create item mapping = %d: %s", status, data)
	}
	var iMap store.Mapping
	_ = json.Unmarshal(data, &iMap)

	httpWF := func(mappingRefs, queryFields any) *graph.Workflow {
		return &graph.Workflow{
			ID: "http-groups", Name: "HTTP groups", Version: 1,
			Nodes: []graph.Node{
				{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
				{
					ID: "http", Type: "http.request", TypeVersion: 1, Name: "Call API",
					Config: map[string]any{
						"method": "GET", "url": "https://api.example.test/x",
						"mappingRefs": mappingRefs, "queryFields": queryFields,
					},
				},
			},
			Edges: []graph.Edge{
				{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "http", Port: "main"}},
			},
		}
	}

	// Kind mismatch is rejected at draft save with the group named.
	status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token",
		httpWF(map[string]any{"query": iMap.ID}, nil))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("kind mismatch accepted = %d: %s", status, data)
	}
	if !strings.Contains(string(data), "query group") {
		t.Fatalf("kind mismatch error = %s", data)
	}

	// Bad row shapes are rejected at draft save.
	status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token",
		httpWF(nil, []any{map[string]any{"name": "", "mode": "static", "value": "x"}}))
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("bad rows accepted = %d: %s", status, data)
	}

	// The good case: node rows win per name, mapping rows fill the rest.
	status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token",
		httpWF(map[string]any{"query": qMap.ID}, []any{
			map[string]any{"name": "limit", "mode": "static", "type": "number", "value": 50},
		}))
	if status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/http-groups/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/workflows/http-groups/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version = %d: %s", status, data)
	}
	var snap struct {
		Nodes []struct {
			Config map[string]any `json:"config"`
		} `json:"nodes"`
	}
	_ = json.Unmarshal(data, &snap)
	cfg := snap.Nodes[1].Config
	query, _ := cfg["query"].(map[string]any)
	if query["limit"] != float64(50) || query["source"] != "web" {
		t.Fatalf("published query = %#v, want node limit 50 over mapping 20 plus mapping source web", query)
	}
	refs, _ := cfg["mappingRefs"].(map[string]any)
	if refs["query"] != qMap.ID {
		t.Fatalf("mappingRefs not preserved for display: %#v", refs)
	}

	usage, err := st.MappingUsage(qMap.ID)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %v, %v — publish must pin the query mapping", usage, err)
	}
	status, _ = call(t, srv, http.MethodDelete, "/api/mappings/"+qMap.ID, "test-token", nil)
	if status != http.StatusConflict {
		t.Fatalf("delete in-use query mapping = %d, want 409", status)
	}
}
