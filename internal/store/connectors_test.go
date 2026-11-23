package store

import (
	"errors"
	"fmt"
	"testing"

	"loom/internal/graph"
	"loom/internal/nodes"
)

func testConnector() Connector {
	return Connector{
		ID:   "conn-e2e",
		Name: "Example API",
		Operations: []ConnectorOperation{
			{
				Op:     "list",
				Method: "GET",
				URL:    "{{cred.baseUrl}}/things?owner={{config.owner}}",
				Query:  map[string]string{"limit": "50"},
				Headers: map[string]string{
					"Authorization": "Bearer {{cred.token}}",
				},
				Fields: map[string]nodes.Field{
					"owner": {Type: nodes.FieldString, Required: true},
				},
			},
			{
				Op:     "create",
				Label:  "Create a thing",
				Method: "POST",
				URL:    "{{cred.baseUrl}}/things",
				Body:   `{"name": "{{config.name}}"}`,
				Fields: map[string]nodes.Field{
					"name": {Type: nodes.FieldString, Required: true},
				},
			},
		},
	}
}

func TestConnectorCRUDAndVersions(t *testing.T) {
	s := testProfileStore(t)

	tc := testConnectorFor()
	created, err := s.CreateConnector(&tc, "actor-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d", created.Version)
	}

	got, err := s.GetConnector("conn-crud")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Operations) != 2 || got.Operations[0].Op != "list" {
		t.Fatalf("operations did not round-trip: %+v", got.Operations)
	}
	if got.Owner != "connector-owner" || len(got.Tags) != 2 {
		t.Fatalf("governance did not round-trip: owner=%q tags=%v", got.Owner, got.Tags)
	}

	updated := testConnectorFor()
	updated.Operations = updated.Operations[:1]
	updated.Name = "Example API v2"
	next, err := s.UpdateConnector(&updated, got.Version, "actor-1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if next.Version != 2 {
		t.Fatalf("updated version = %d", next.Version)
	}

	if _, err := s.UpdateConnector(&updated, got.Version, "actor-1"); !errors.Is(err, ErrStaleConnector) {
		t.Fatalf("stale update: want ErrStaleConnector, got %v", err)
	}

	old, err := s.GetConnectorVersion("conn-crud", 1)
	if err != nil {
		t.Fatalf("get version 1: %v", err)
	}
	if len(old.Operations) != 2 {
		t.Fatalf("version 1 must keep both operations (append-only), have %d", len(old.Operations))
	}

	versions, err := s.ListConnectorVersions("conn-crud")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions: %v %d", err, len(versions))
	}

	list, err := s.ListConnectors()
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %d", err, len(list))
	}
	if list[0].Owner != "connector-owner" || len(list[0].Tags) != 2 {
		t.Fatalf("list governance: owner=%q tags=%v", list[0].Owner, list[0].Tags)
	}

	if err := s.DeleteConnector("conn-crud"); err != nil {
		t.Fatalf("delete unused: %v", err)
	}
	if _, err := s.GetConnector("conn-crud"); !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("deleted get: %v", err)
	}
}

func testConnectorFor() Connector {
	c := testConnector()
	c.ID = "conn-crud"
	c.Owner = "connector-owner"
	c.Tags = []string{"connectors", "e2e"}
	return c
}

func TestConnectorCheckRejectsBadDocs(t *testing.T) {
	bad := []func(c *Connector){
		func(c *Connector) { c.Name = "  " },
		func(c *Connector) { c.Operations = nil },
		func(c *Connector) { c.Operations[0].Op = "Bad Op" },
		func(c *Connector) { c.Operations[1].Op = c.Operations[0].Op },
		func(c *Connector) { c.Operations[0].Method = "TELEPORT" },
		func(c *Connector) { c.Operations[0].URL = "" },
		func(c *Connector) { c.Operations[0].Fields["owner"] = nodes.Field{Type: nodes.FieldType("matrix")} },
		func(c *Connector) {
			c.Operations[0].Fields["token"] = nodes.Field{Type: nodes.FieldString, Secret: true, Profileable: true}
		},
	}
	for i, mutate := range bad {
		c := testConnector()
		mutate(&c)
		if err := CheckConnector(&c); err == nil {
			t.Fatalf("case %d: CheckConnector accepted a bad doc", i)
		}
	}
}

func TestConnectorDeleteGuardedWhileInUse(t *testing.T) {
	s := testProfileStore(t)
	c := testConnectorFor()
	if _, err := s.CreateConnector(&c, "actor-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.RecordConnectorUsage("conn-crud", 1, "wf-a", 3); err != nil {
		t.Fatalf("usage: %v", err)
	}
	if err := s.DeleteConnector("conn-crud"); !errors.Is(err, ErrConnectorInUse) {
		t.Fatalf("delete in use: want ErrConnectorInUse, got %v", err)
	}
	usage, err := s.ConnectorUsage("conn-crud")
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage list: %v %d", err, len(usage))
	}
}

func TestResolveConnectorRefsInlinesTemplate(t *testing.T) {
	s := testProfileStore(t)
	c := testConnector()
	c.ID = "conn-resolve"
	if _, err := s.CreateConnector(&c, "actor-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Version 2 changes the create op's template; version 3 adds another
	// op. A draft resolution pins the CURRENT version (2), and a later
	// re-resolve of the same doc must not drift to 3 (D4-style pinning).
	v2 := c
	v2.Operations[1].URL = "{{cred.baseUrl}}/things/v2"
	if _, err := s.UpdateConnector(&v2, 1, "actor-1"); err != nil {
		t.Fatalf("update: %v", err)
	}

	wf := &graph.Workflow{
		Version: 1,
		Nodes: []graph.Node{
			{ID: "n", Type: "connector.conn-resolve.create", Config: map[string]any{"name": "x"}},
		},
	}
	refs, err := s.ResolveConnectorRefs(wf)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if refs["conn-resolve"] != 2 {
		t.Fatalf("draft resolution must pin the current version, got %d", refs["conn-resolve"])
	}
	req, _ := wf.Nodes[0].Config["request"].(map[string]any)
	if req == nil || req["method"] != "POST" || req["url"] != "{{cred.baseUrl}}/things/v2" {
		t.Fatalf("request template not inlined: %+v", req)
	}
	pin, _ := wf.Nodes[0].Config["_connector"].(map[string]any)
	if fmt.Sprint(pin["id"]) != "conn-resolve" || fmt.Sprint(pin["version"]) != "2" {
		t.Fatalf("connector pin wrong: %+v", pin)
	}

	// Republish of an already-pinned doc keeps version 2 even after a
	// version 3 exists (D4-style forward-follow only on upgrade).
	v3 := v2
	v3.Operations = append(v3.Operations, ConnectorOperation{Op: "extra", Method: "GET", URL: "{{cred.baseUrl}}/x"})
	if _, err := s.UpdateConnector(&v3, 2, "actor-1"); err != nil {
		t.Fatalf("update 3: %v", err)
	}
	if _, err := s.ResolveConnectorRefs(wf); err != nil {
		t.Fatalf("re-resolve: %v", err)
	}
	if pin, _ := wf.Nodes[0].Config["_connector"].(map[string]any); fmt.Sprint(pin["version"]) != "2" {
		t.Fatalf("pinned version must not drift without an upgrade: %+v", pin)
	}
}

func TestResolveConnectorRefsNamesUnknownConnector(t *testing.T) {
	s := testProfileStore(t)
	wf := &graph.Workflow{
		Version: 1,
		Nodes: []graph.Node{
			{ID: "n", Type: "connector.conn-missing.op", Config: map[string]any{}},
		},
	}
	if _, err := s.ResolveConnectorRefs(wf); err == nil || !errors.Is(err, ErrConnectorNotFound) {
		t.Fatalf("unknown connector: want ErrConnectorNotFound, got %v", err)
	}
}

func TestConnectorNodeTypesShape(t *testing.T) {
	c := testConnector()
	defs := ConnectorNodeTypes(&c)
	if len(defs) != 2 {
		t.Fatalf("defs = %d", len(defs))
	}
	d := defs[0]
	if d.Type != "connector.conn-e2e.create" {
		t.Fatalf("type = %s", d.Type)
	}
	if d.Category != "connector" || d.Credentials[0] != "http" {
		t.Fatalf("def shape wrong: %+v", d)
	}
	if _, ok := d.Config["name"]; !ok {
		t.Fatalf("author fields missing from def config")
	}
}
