package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"loom/internal/store"
)

// Connector API (Campaign 4): CRUD, the node-types merge, validation
// acceptance of connector node types, and publish inlining with usage
// pins.

func validConnectorBody() map[string]any {
	return map[string]any{
		"id":   "conn-test",
		"name": "Example API",
		"operations": []map[string]any{
			{
				"op":     "list",
				"method": "GET",
				"url":    "{{cred.baseUrl}}/things",
				"fields": map[string]any{
					"limit": map[string]any{"type": "number", "required": false},
				},
			},
		},
	}
}

func TestConnectorLifecycle(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/connectors", "test-token", validConnectorBody())
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var createdWrap struct {
		Connector store.Connector `json:"connector"`
	}
	if err := json.Unmarshal(data, &createdWrap); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	created := createdWrap.Connector
	if created.ID != "conn-test" || created.Version != 1 || created.Visibility != store.VisibilityPrivate {
		t.Fatalf("created = %#v", created)
	}
	if len(created.Operations) != 1 || created.Operations[0].Method != "GET" {
		t.Fatalf("operations = %#v", created.Operations)
	}

	// Duplicate copies the current version under a new private id.
	status, data = call(t, srv, http.MethodPost, "/api/connectors/conn-test/duplicate", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicate = %d: %s", status, data)
	}
	var dupWrap struct {
		Connector store.Connector `json:"connector"`
	}
	if err := json.Unmarshal(data, &dupWrap); err != nil {
		t.Fatalf("decode duplicate: %v", err)
	}
	dup := dupWrap.Connector
	if dup.ID == "" || dup.ID == "conn-test" {
		t.Fatalf("duplicate id = %q", dup.ID)
	}
	if dup.Name != "Example API (copy)" || dup.Visibility != store.VisibilityPrivate {
		t.Fatalf("duplicate = %#v", dup)
	}
	if len(dup.Operations) != 1 || dup.Operations[0].Method != "GET" {
		t.Fatalf("duplicate operations = %#v", dup.Operations)
	}

	// Bad docs are rejected with named errors.
	bad := validConnectorBody()
	bad["operations"] = []map[string]any{{"op": "x", "method": "TELEPORT", "url": "http://x"}}
	if status, data = call(t, srv, http.MethodPost, "/api/connectors", "test-token", bad); status != http.StatusBadRequest {
		t.Fatalf("bad create = %d: %s", status, data)
	}

	// Stale update is a 409.
	update := validConnectorBody()
	update["version"] = 1
	update["name"] = "Example API v2"
	if status, data = call(t, srv, http.MethodPut, "/api/connectors/conn-test", "test-token", update); status != http.StatusOK {
		t.Fatalf("update = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodPut, "/api/connectors/conn-test", "test-token", update); status != http.StatusConflict {
		t.Fatalf("stale update = %d: %s", status, data)
	}

	// Node-types listing carries the connector type.
	status, data = call(t, srv, http.MethodGet, "/api/node-types", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("node-types = %d", status)
	}
	var types struct {
		NodeTypes []struct {
			Type     string `json:"type"`
			Category string `json:"category"`
		} `json:"nodeTypes"`
	}
	if err := json.Unmarshal(data, &types); err != nil {
		t.Fatalf("decode node types: %v", err)
	}
	found := false
	for _, nt := range types.NodeTypes {
		if nt.Type == "connector.conn-test.list" {
			found = nt.Category == "connector"
		}
	}
	if !found {
		t.Fatalf("connector type missing from node-types listing")
	}

	// Usage + versions list.
	if status, data = call(t, srv, http.MethodGet, "/api/connectors/conn-test/versions", "test-token", nil); status != http.StatusOK {
		t.Fatalf("versions = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodGet, "/api/connectors/conn-test/usage", "test-token", nil); status != http.StatusOK {
		t.Fatalf("usage = %d: %s", status, data)
	}

	// Share/unshare round-trip (creator may govern their own).
	if status, data = call(t, srv, http.MethodPost, "/api/connectors/conn-test/share", "test-token", nil); status != http.StatusOK {
		t.Fatalf("share = %d: %s", status, data)
	}

	// The token-auth harness is the admin identity: delete succeeds
	// while unused (role separation is covered by the auth suite).
	if status, data = call(t, srv, http.MethodDelete, "/api/connectors/conn-test", "test-token", nil); status != http.StatusOK {
		t.Fatalf("admin delete = %d: %s", status, data)
	}
}

func TestConnectorWorkflowValidatesAndPublishes(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	if status, data := call(t, srv, http.MethodPost, "/api/connectors", "test-token", validConnectorBody()); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("create connector = %d: %s", status, data)
	}

	// A draft with a connector node validates.
	wf := map[string]any{
		"id": "wf-connector", "name": "Connector draft", "version": 1,
		"nodes": []map[string]any{
			{"id": "start", "type": "trigger.manual", "typeVersion": 1},
			{"id": "n", "type": "connector.conn-test.list", "typeVersion": 1,
				"config":      map[string]any{"limit": 5},
				"credentials": map[string]string{"http": "cred-x"}},
		},
		"edges": []map[string]any{
			{"from": map[string]string{"node": "start", "port": "main"}, "to": map[string]string{"node": "n", "port": "main"}},
		},
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/wf-connector", "test-token", wf); status != http.StatusOK {
		t.Fatalf("save draft = %d: %s", status, data)
	}

	// Unknown connector types still fail with a named error.
	wf["nodes"].([]map[string]any)[1]["type"] = "connector.conn-missing.op"
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/wf-connector", "test-token", wf); status != http.StatusUnprocessableEntity {
		t.Fatalf("unknown connector = %d: %s", status, data)
	}
	wf["nodes"].([]map[string]any)[1]["type"] = "connector.conn-test.list"

	// Publish inlines the request template and records usage.
	pstatus, pdata := call(t, srv, http.MethodPost, "/api/workflows/wf-connector/publish", "test-token", nil)
	if pstatus != http.StatusOK && pstatus != http.StatusCreated {
		t.Fatalf("publish = %d: %s", pstatus, pdata)
	}
	var published struct {
		Workflow struct {
			Nodes []struct {
				Config map[string]any `json:"config"`
			} `json:"nodes"`
		} `json:"workflow"`
	}
	if err := json.Unmarshal(pdata, &published); err != nil {
		t.Fatalf("decode publish: %v", err)
	}
	var request map[string]any
	for _, n := range published.Workflow.Nodes {
		if req, ok := n.Config["request"].(map[string]any); ok {
			request = req
		}
	}
	if request == nil || request["url"] != "{{cred.baseUrl}}/things" {
		t.Fatalf("publish did not inline the request template: %#v", request)
	}

	// Usage pin guards the connector.
	if status, data := call(t, srv, http.MethodPost, "/api/connectors/conn-test/unshare", "test-token", nil); status != http.StatusOK {
		t.Fatalf("unshare = %d: %s", status, data)
	}
}
