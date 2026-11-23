package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"loom/internal/graph"
	"loom/internal/schemas"
	"loom/internal/store"
)

func validSchemaBody() map[string]any {
	return map[string]any{
		"name":        "Inbound lead",
		"description": "Webhook lead payload",
		"fields": []map[string]any{
			{"name": "email", "type": "string", "required": true},
			{"name": "name", "type": "string"},
			{"name": "vip", "type": "boolean"},
			{"name": "address", "type": "object", "fields": []map[string]any{
				{"name": "city", "type": "string", "required": true},
			}},
			{"name": "tags", "type": "array", "items": map[string]any{"name": "tag", "type": "string"}},
		},
		"tags":  []string{"webhook", "leads"},
		"owner": "team-a",
	}
}

func TestDataSchemasLifecycle(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", validSchemaBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var created store.DataSchema
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Version != 1 || len(created.Fields) != 5 {
		t.Fatalf("created = %#v", created)
	}
	if len(created.Tags) != 2 || created.Owner != "team-a" {
		t.Fatalf("governance fields = %#v", created)
	}

	// List and get agree.
	status, data = call(t, srv, http.MethodGet, "/api/data-schemas", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d: %s", status, data)
	}
	var list struct {
		Schemas []store.DataSchema `json:"schemas"`
		Count   int                `json:"count"`
	}
	if err := json.Unmarshal(data, &list); err != nil || list.Count != 1 {
		t.Fatalf("list decode: %v (%s)", err, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/data-schemas/"+created.ID, "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get = %d: %s", status, data)
	}

	// Update with the right base version succeeds and bumps to v2.
	body := validSchemaBody()
	body["version"] = created.Version
	body["name"] = "Inbound lead v2"
	status, data = call(t, srv, http.MethodPut, "/api/data-schemas/"+created.ID, "test-token", body)
	if status != http.StatusOK {
		t.Fatalf("update = %d: %s", status, data)
	}
	var updated store.DataSchema
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Version != 2 || updated.Name != "Inbound lead v2" {
		t.Fatalf("updated = %#v", updated)
	}

	// A second update from stale version 1 is a 409.
	stale := validSchemaBody()
	stale["version"] = 1
	status, data = call(t, srv, http.MethodPut, "/api/data-schemas/"+created.ID, "test-token", stale)
	if status != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409: %s", status, data)
	}

	// Version history is readable and append-only.
	status, data = call(t, srv, http.MethodGet, "/api/data-schemas/"+created.ID+"/versions", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("versions = %d: %s", status, data)
	}
	var versions struct {
		Versions []store.DataSchemaVersion `json:"versions"`
	}
	if err := json.Unmarshal(data, &versions); err != nil || len(versions.Versions) != 2 {
		t.Fatalf("versions decode: %v (%s)", err, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/data-schemas/"+created.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 = %d: %s", status, data)
	}
	var v1 store.DataSchema
	if err := json.Unmarshal(data, &v1); err != nil || v1.Name != "Inbound lead" {
		t.Fatalf("version 1 = %v (%s)", err, data)
	}

	// Duplicate copies the current fields under a new id.
	status, data = call(t, srv, http.MethodPost, "/api/data-schemas/"+created.ID+"/duplicate", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicate = %d: %s", status, data)
	}
	var dup store.DataSchema
	if err := json.Unmarshal(data, &dup); err != nil || dup.ID == created.ID || dup.Version != 1 {
		t.Fatalf("duplicate = %v (%s)", err, data)
	}

	// Usage pins guard deletes.
	if err := st.RecordDataSchemaUsage(created.ID, 2, "wf-guard", 1); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	status, data = call(t, srv, http.MethodDelete, "/api/data-schemas/"+created.ID, "test-token", nil)
	if status != http.StatusConflict {
		t.Fatalf("delete in-use = %d, want 409: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/data-schemas/"+created.ID+"/usage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("usage = %d: %s", status, data)
	}
	var usage struct {
		Usage []store.DataSchemaUsage `json:"usage"`
	}
	if err := json.Unmarshal(data, &usage); err != nil || len(usage.Usage) != 1 || usage.Usage[0].WorkflowID != "wf-guard" {
		t.Fatalf("usage decode: %v (%s)", err, data)
	}

	// The unpinned duplicate deletes; a missing id is a 404.
	if status, data = call(t, srv, http.MethodDelete, "/api/data-schemas/"+dup.ID, "test-token", nil); status != http.StatusNoContent {
		t.Fatalf("delete duplicate = %d: %s", status, data)
	}
	if status, _ = call(t, srv, http.MethodDelete, "/api/data-schemas/missing", "test-token", nil); status != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", status)
	}
}

func TestDataSchemasValidateContracts(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"empty name", func() map[string]any {
			b := validSchemaBody()
			b["name"] = " "
			return b
		}()},
		{"no fields", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{}
			return b
		}()},
		{"unknown type", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{{"name": "x", "type": "datetime"}}
			return b
		}()},
		{"duplicate names", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{{"name": "x", "type": "string"}, {"name": "x", "type": "number"}}
			return b
		}()},
		{"bad field name", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{{"name": "bad-name", "type": "string"}}
			return b
		}()},
		{"empty object", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{{"name": "obj", "type": "object"}}
			return b
		}()},
		{"array with fields", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{{"name": "arr", "type": "array", "fields": []map[string]any{{"name": "x", "type": "string"}}}}
			return b
		}()},
		{"too deep", func() map[string]any {
			b := validSchemaBody()
			b["fields"] = []map[string]any{deepFieldBody(8)}
			return b
		}()},
	}
	for _, tc := range cases {
		if status, data := call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", tc.body); status != http.StatusBadRequest {
			t.Fatalf("%s: create = %d, want 400: %s", tc.name, status, data)
		}
	}
}

func deepFieldBody(level int) map[string]any {
	if level <= 0 {
		return map[string]any{"name": "leaf", "type": "string"}
	}
	return map[string]any{"name": "l", "type": "object", "fields": []map[string]any{deepFieldBody(level - 1)}}
}

func TestDataSchemaTestEndpoint(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", validSchemaBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var created store.DataSchema
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// A conforming sample passes.
	status, data = call(t, srv, http.MethodPost, "/api/data-schemas/"+created.ID+"/test", "test-token",
		map[string]any{"sample": map[string]any{
			"email": "ada@example.com", "name": "Ada", "vip": true,
			"address": map[string]any{"city": "london"},
			"tags":    []any{"a"},
		}})
	if status != http.StatusOK {
		t.Fatalf("test conforming = %d: %s", status, data)
	}
	var result struct {
		OK         bool                `json:"ok"`
		Violations []schemas.Violation `json:"violations"`
	}
	if err := json.Unmarshal(data, &result); err != nil || !result.OK || len(result.Violations) != 0 {
		t.Fatalf("conforming result = %v (%s)", err, data)
	}

	// A broken sample names every violation with its path.
	status, data = call(t, srv, http.MethodPost, "/api/data-schemas/"+created.ID+"/test", "test-token",
		map[string]any{"sample": map[string]any{
			"email": 7, "vip": "yes",
			"address": map[string]any{"city": 9},
			"tags":    []any{"ok", 3},
		}})
	if status != http.StatusOK {
		t.Fatalf("test broken = %d: %s", status, data)
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	paths := map[string]bool{}
	for _, v := range result.Violations {
		paths[v.Path] = true
	}
	for _, want := range []string{"email", "vip", "address.city", "tags[1]"} {
		if !paths[want] {
			t.Fatalf("missing violation for %q in %v", want, result.Violations)
		}
	}
	if result.OK {
		t.Fatalf("broken sample reported ok")
	}

	// A missing schema is a 404, not a 500.
	if status, _ = call(t, srv, http.MethodPost, "/api/data-schemas/missing/test", "test-token",
		map[string]any{"sample": map[string]any{}}); status != http.StatusNotFound {
		t.Fatalf("test missing = %d, want 404", status)
	}
}

// V2: schema references resolve inline at publish and pin usage (D4/D5
// pattern), same as profiles.
func TestPublishResolvesSchemaRefsInline(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	contractBody := validSchemaBody()
	contractBody["fields"] = []map[string]any{
		{"name": "email", "type": "string", "required": true},
		{"name": "city", "type": "string"},
	}
	status, data := call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", contractBody)
	if status != http.StatusCreated {
		t.Fatalf("create schema = %d: %s", status, data)
	}
	var contract store.DataSchema
	_ = json.Unmarshal(data, &contract)

	wf := linearWorkflow()
	wf.ID = "schema-resolve"
	wf.Nodes[0].Type = "trigger.webhook"
	wf.Nodes[0].Config = map[string]any{"path": "schemas", "method": "POST"}
	wf.Nodes[0].InputSchemaRef = contract.ID
	wf.Nodes[1].OutputSchemaRef = contract.ID
	wf.InputSchemaRef = contract.ID
	wf.OutputSchemaRef = contract.ID
	status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
	if status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 = %d: %s", status, data)
	}
	var snap graph.Workflow
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snap.Nodes[0].InputSchemaFields) != 2 || !snap.Nodes[0].InputSchemaFields[0].Required {
		t.Fatalf("node input fields not resolved: %+v", snap.Nodes[0].InputSchemaFields)
	}
	if len(snap.Nodes[1].OutputSchemaFields) != 2 {
		t.Fatalf("node output fields not resolved: %+v", snap.Nodes[1].OutputSchemaFields)
	}
	if len(snap.InputSchemaFields) != 2 || len(snap.OutputSchemaFields) != 2 {
		t.Fatalf("workflow-level fields not resolved: in=%d out=%d",
			len(snap.InputSchemaFields), len(snap.OutputSchemaFields))
	}
	// Refs stay on the doc for display.
	if snap.Nodes[0].InputSchemaRef != contract.ID || snap.InputSchemaRef != contract.ID {
		t.Fatalf("refs stripped from snapshot: %+v", snap)
	}

	usage, err := st.DataSchemaUsage(contract.ID)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %v, %v", usage, err)
	}
	if usage[0].WorkflowID != wf.ID || usage[0].SchemaVersion != 1 {
		t.Fatalf("usage pin = %+v", usage[0])
	}

	// In-use contracts cannot be deleted.
	status, _ = call(t, srv, http.MethodDelete, "/api/data-schemas/"+contract.ID, "test-token", nil)
	if status != http.StatusConflict {
		t.Fatalf("delete in-use schema = %d, want 409", status)
	}
}

// V2: dangling schema refs are named 422 errors at draft save.
func TestWorkflowSaveRejectsMissingSchemaRef(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")
	wf := linearWorkflow()
	wf.ID = "schema-ghost"
	wf.Nodes[1].OutputSchemaRef = "ghost-schema"
	status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("create with ghost ref = %d: %s", status, data)
	}
	if !strings.Contains(string(data), "ghost-schema") {
		t.Fatalf("error does not name the missing schema: %s", data)
	}
}

// V2: the webhook input contract annotates by default and rejects in strict
// mode (D11).
func TestWebhookInputContract(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	contractBody := validSchemaBody()
	contractBody["fields"] = []map[string]any{
		{"name": "email", "type": "string", "required": true},
	}
	status, data := call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", contractBody)
	if status != http.StatusCreated {
		t.Fatalf("create schema = %d: %s", status, data)
	}
	var contract store.DataSchema
	_ = json.Unmarshal(data, &contract)

	seed := func(strict bool) string {
		wf := &graph.Workflow{
			ID: "hook-contract-" + fmt.Sprint(strict), Name: "Hook contract", Version: 1,
			Nodes: []graph.Node{
				{
					ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
					Config:         map[string]any{"path": "contract" + fmt.Sprint(strict), "method": "POST"},
					InputSchemaRef: contract.ID,
				},
				{
					ID: "echo", Type: "data.set", TypeVersion: 1, Name: "Echo",
					Config: map[string]any{"values": map[string]any{
						"valid":     "{{ $json.schemaValid }}",
						"violation": "{{ $json.schemaViolations[0].path }}",
					}},
				},
			},
			Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "echo", Port: "main"}}},
		}
		if strict {
			wf.Nodes[0].Config["strictSchema"] = true
		}
		status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
		if status != http.StatusCreated {
			t.Fatalf("create %v = %d: %s", strict, status, data)
		}
		status, _ = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
		if status != http.StatusCreated {
			t.Fatalf("publish %v = %d", strict, status)
		}
		return wf.ID
	}

	// Annotate mode: a non-conforming payload still runs, and the verdict
	// rides the trigger item downstream.
	_ = seed(false)
	status, data = call(t, srv, http.MethodPost, "/hook/contractfalse", "test-token", map[string]any{"email": 7})
	if status != http.StatusOK {
		t.Fatalf("annotate-mode hook = %d: %s", status, data)
	}
	var accepted struct {
		RunID string `json:"runId"`
	}
	_ = json.Unmarshal(data, &accepted)
	status, data = call(t, srv, http.MethodGet, "/api/runs/"+accepted.RunID+"/events?format=json", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get events = %d: %s", status, data)
	}
	var eventList struct {
		Events []struct {
			Type       string         `json:"type"`
			NodeID     string         `json:"nodeId"`
			OutputHash string         `json:"outputHash"`
			Data       map[string]any `json:"data"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &eventList); err != nil {
		t.Fatalf("decode events: %v", err)
	}
	echoHash := ""
	for _, ev := range eventList.Events {
		if ev.Type == "run.started" {
			fmt.Printf("RUN STARTED DATA: %v\n", ev.Data)
		}
		if ev.Type == "node.finished" && ev.NodeID == "echo" {
			echoHash = ev.OutputHash
		}
	}
	if echoHash == "" {
		t.Fatalf("echo node.finished missing: %+v", eventList.Events)
	}
	status, data = call(t, srv, http.MethodGet, "/api/blobs/"+echoHash, "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("fetch echo output blob = %d: %s", status, data)
	}
	var echoOut struct {
		Main map[string]any `json:"main"`
	}
	if err := json.Unmarshal(data, &echoOut); err != nil {
		t.Fatalf("decode echo output: %v", err)
	}
	// The echo node rendered the verdict from the annotated trigger item
	// (template interpolation renders values as strings).
	if echoOut.Main["valid"] != "false" {
		t.Fatalf("echo valid = %v (%T), want \"false\"", echoOut.Main["valid"], echoOut.Main["valid"])
	}
	if echoOut.Main["violation"] != "email" {
		t.Fatalf("echo violation = %v, want email", echoOut.Main["violation"])
	}

	// Strict mode: a non-conforming payload is rejected at the door.
	_ = seed(true)
	status, data = call(t, srv, http.MethodPost, "/hook/contracttrue", "test-token", map[string]any{"email": 7})
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("strict-mode hook = %d: %s", status, data)
	}
	if !strings.Contains(string(data), "email") {
		t.Fatalf("strict rejection does not name the violation: %s", data)
	}
	// A conforming payload passes strict mode.
	status, data = call(t, srv, http.MethodPost, "/hook/contracttrue", "test-token", map[string]any{"email": "a@b.c"})
	if status != http.StatusOK {
		t.Fatalf("strict-mode conforming hook = %d: %s", status, data)
	}
}
