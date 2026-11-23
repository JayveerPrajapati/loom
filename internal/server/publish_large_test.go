package server_test

import (
	"fmt"
	"net/http"
	"testing"

	"loom/internal/graph"
	"loom/internal/schemas"
	"loom/internal/store"
)

// serverLargePublishReadBudget covers the whole publish request: the
// workflow read, validation, profile/schema/mapping/script resolution and
// the publish writes. The store-level gate
// (TestPublishLargeWorkflowStaysWithinReadBudget) pins resolver work to 20
// reads; this gate catches an N+1 anywhere along the HTTP path. A 1,000-node
// workflow that resolved per node would issue >1,000 reads.
// The server fixture is 1,000 nodes including the trigger (999 asset refs):
// the catalog rejects workflows above 1,000 nodes with workflow_too_large.
// The store-level gate (TestPublishLargeWorkflowStaysWithinReadBudget) covers
// the exact 1,000-ref shape without HTTP validation.
const serverLargePublishReadBudget = 30

const (
	serverLargeProfileRefs = 500
	serverLargeSchemaRefs  = 299
	serverLargeContractCnt = 10
	serverLargeMappingRefs = 100
	serverLargeScriptRefs  = 100
	serverLargeFields      = 100
	serverLargeRows        = 200
)

func serverLargeSchemaID(i int) string {
	return fmt.Sprintf("api-schema-%02d", i)
}

func seedServerLargeAssets(t *testing.T, st *store.Store) {
	t.Helper()
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "api-large-profile", Name: "API large profile", NodeType: "http.request",
		Visibility: store.VisibilityShared,
		Config:     map[string]any{"method": "GET", "url": "https://api.example.com/large"},
	}, "test"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	fields := make([]schemas.Field, 0, serverLargeFields)
	for i := 0; i < serverLargeFields; i++ {
		fields = append(fields, schemas.Field{Name: fmt.Sprintf("field_%03d", i), Type: "string"})
	}
	for i := 0; i < serverLargeContractCnt; i++ {
		if _, err := st.CreateDataSchema(&store.DataSchema{
			ID: serverLargeSchemaID(i), Name: fmt.Sprintf("API contract %02d", i),
			Visibility: store.VisibilityShared, Fields: fields,
		}, "test"); err != nil {
			t.Fatalf("create schema %d: %v", i, err)
		}
	}
	rows := make([]store.MappingRow, 0, serverLargeRows)
	for i := 0; i < serverLargeRows; i++ {
		rows = append(rows, store.MappingRow{
			Target: fmt.Sprintf("target_%03d", i),
			Source: fmt.Sprintf("{{ $json.source_%03d }}", i),
		})
	}
	if _, err := st.CreateMapping(&store.Mapping{
		ID: "api-large-mapping", Name: "API large mapping", Kind: "item",
		Visibility: store.VisibilityShared, Rows: rows,
	}, "test"); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if _, err := st.CreateScript(&store.Script{
		ID: "api-large-script", Name: "API large script",
		Language: store.ScriptLanguageJavaScript, Visibility: store.VisibilityShared,
		Source: "return $json;",
	}, "test"); err != nil {
		t.Fatalf("create script: %v", err)
	}
}

func serverLargeWorkflow() *graph.Workflow {
	nodes := []graph.Node{{
		ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start",
		Config: map[string]any{},
	}}
	for i := 0; i < serverLargeProfileRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("profile-%03d", i), Type: "http.request", TypeVersion: 1,
			Name: fmt.Sprintf("Profile %03d", i), ProfileRef: "api-large-profile",
			Config: map[string]any{},
		})
	}
	for i := 0; i < serverLargeSchemaRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("schema-%03d", i), Type: "http.request", TypeVersion: 1,
			Name:           fmt.Sprintf("Schema %03d", i),
			InputSchemaRef: serverLargeSchemaID(i % serverLargeContractCnt),
			Config:         map[string]any{"method": "GET", "url": "https://api.example.com/schema"},
		})
	}
	for i := 0; i < serverLargeMappingRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("mapping-%03d", i), Type: "data.map", TypeVersion: 1,
			Name: fmt.Sprintf("Mapping %03d", i),
			Config: map[string]any{
				"mappingRef": "api-large-mapping",
			},
		})
	}
	for i := 0; i < serverLargeScriptRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("script-%03d", i), Type: "code.run", TypeVersion: 1,
			Name: fmt.Sprintf("Script %03d", i),
			Config: map[string]any{
				"scriptRef": "api-large-script",
			},
		})
	}
	edges := make([]graph.Edge, 0, len(nodes))
	previous := "start"
	for i := 1; i < len(nodes); i++ {
		edges = append(edges, graph.Edge{
			From: graph.Endpoint{Node: previous, Port: "main"},
			To:   graph.Endpoint{Node: nodes[i].ID, Port: "main"},
		})
		previous = nodes[i].ID
	}
	return &graph.Workflow{
		ID: "api-large-publish", Name: "Large API publish", Version: 1,
		Nodes: nodes, Edges: edges,
	}
}

func TestPublishLargeWorkflowThroughAPI(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	seedServerLargeAssets(t, st)
	wf := serverLargeWorkflow()

	status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
	if status != http.StatusCreated {
		t.Fatalf("create large workflow = %d: %s", status, data)
	}

	st.ResetReadQueryCount()
	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish large workflow = %d: %s", status, data)
	}
	reads := st.ReadQueryCount()
	if reads > serverLargePublishReadBudget {
		t.Fatalf("server publish issued %d reads, budget is %d", reads, serverLargePublishReadBudget)
	}
	t.Logf("server publish: %d nodes, %d reads (budget %d)", len(wf.Nodes), reads, serverLargePublishReadBudget)

	body := decode(t, data)
	version, ok := body["version"].(map[string]any)
	if !ok || int(version["nodeCount"].(float64)) != len(wf.Nodes) {
		t.Fatalf("publish version = %#v, want nodeCount %d", body["version"], len(wf.Nodes))
	}
	resolved, ok := body["workflow"].(map[string]any)
	if !ok {
		t.Fatalf("publish response lost the resolved workflow: %s", data)
	}
	nodeList, ok := resolved["nodes"].([]any)
	if !ok || len(nodeList) != len(wf.Nodes) {
		t.Fatalf("resolved nodes = %#v, want %d", resolved["nodes"], len(wf.Nodes))
	}
	byID := func(id string) map[string]any {
		t.Helper()
		for _, raw := range nodeList {
			node, _ := raw.(map[string]any)
			if node["id"] == id {
				return node
			}
		}
		t.Fatalf("resolved node %q missing", id)
		return nil
	}
	configOf := func(node map[string]any) map[string]any {
		t.Helper()
		config, ok := node["config"].(map[string]any)
		if !ok {
			t.Fatalf("node %v has no config object", node["id"])
		}
		return config
	}

	if got := configOf(byID("profile-000"))["url"]; got != "https://api.example.com/large" {
		t.Fatalf("profile value not resolved: url = %#v", got)
	}
	if fields, ok := byID("schema-000")["inputSchemaFields"].([]any); !ok || len(fields) != serverLargeFields {
		t.Fatalf("contract fields = %#v, want %d", byID("schema-000")["inputSchemaFields"], serverLargeFields)
	}
	if rows, ok := configOf(byID("mapping-000"))["mappingRows"].([]any); !ok || len(rows) != serverLargeRows {
		t.Fatalf("mapping rows = %#v, want %d", configOf(byID("mapping-000"))["mappingRows"], serverLargeRows)
	}
	if got := configOf(byID("script-000"))["source"]; got != "return $json;" {
		t.Fatalf("script source not resolved: %#v", got)
	}

	status, data = call(t, srv, http.MethodGet, "/api/config-profiles/api-large-profile/usage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("profile usage = %d: %s", status, data)
	}
	if count := int(decode(t, data)["count"].(float64)); count != 1 {
		t.Fatalf("profile usage count = %d, want 1", count)
	}
	status, data = call(t, srv, http.MethodGet, "/api/scripts/api-large-script/usage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("script usage = %d: %s", status, data)
	}
	if count := int(decode(t, data)["count"].(float64)); count != 1 {
		t.Fatalf("script usage count = %d, want 1", count)
	}
}
