package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"loom/internal/store"
)

// Profile-Centric UX Phase 1 (P0-3/P0-6): the server-side visibility
// matrix (D-P0a) and bundle reference validation.

// visibilityFixture is a fully wired auth server with two editors and an
// admin, all signed in.
type visibilityFixture struct {
	srv                  *httptest.Server
	editor1              testSession
	editor2              testSession
	admin                testSession
	editor1ProfileID     string
	editor1ProfileVer    int
	editor1MappingID     string
	editor1MappingVer    int
	editor1SchemaID      string
	editor1SchemaVer     int
	editor1ScriptID      string
	editor1ScriptVersion int
}

func newVisibilityFixture(t *testing.T) *visibilityFixture {
	t.Helper()
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "one@example.com", "editor", "one-password")
	addUser(t, st, "two@example.com", "editor", "two-password")
	addUser(t, st, "root@example.com", "admin", "root-password")
	f := &visibilityFixture{
		srv:     srv,
		editor1: loginAs(t, srv, "one@example.com", "one-password"),
		editor2: loginAs(t, srv, "two@example.com", "two-password"),
		admin:   loginAs(t, srv, "root@example.com", "root-password"),
	}

	// Editor 1 owns a profile, a mapping and a schema — all default to
	// private on creation (D-P0c).
	status, data := f.editor1.do(t, srv, http.MethodPost, "/api/config-profiles", validProfileBody(), true)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)
	f.editor1ProfileID = profile.ID
	f.editor1ProfileVer = profile.Version
	if profile.Visibility != store.VisibilityPrivate {
		t.Fatalf("new profile visibility = %q, want private", profile.Visibility)
	}
	if profile.CreatorID == "" {
		t.Fatal("new profile has no creator id")
	}

	status, data = f.editor1.do(t, srv, http.MethodPost, "/api/mappings", map[string]any{
		"name": "Query mapping", "kind": "query",
		"rows": []store.MappingRow{{Target: "limit", Source: "50"}},
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create mapping = %d: %s", status, data)
	}
	var mapping store.Mapping
	_ = json.Unmarshal(data, &mapping)
	f.editor1MappingID = mapping.ID
	f.editor1MappingVer = mapping.Version

	status, data = f.editor1.do(t, srv, http.MethodPost, "/api/data-schemas", map[string]any{
		"name":   "Order schema",
		"fields": []map[string]any{{"name": "id", "type": "string"}},
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create schema = %d: %s", status, data)
	}
	var schema store.DataSchema
	_ = json.Unmarshal(data, &schema)
	f.editor1SchemaID = schema.ID
	f.editor1SchemaVer = schema.Version

	status, data = f.editor1.do(t, srv, http.MethodPost, "/api/scripts", map[string]any{
		"name": "Owned script", "language": "javascript", "source": "return 1;",
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create script = %d: %s", status, data)
	}
	var script store.Script
	_ = json.Unmarshal(data, &script)
	f.editor1ScriptID = script.ID
	f.editor1ScriptVersion = script.Version
	return f
}

func TestAssetVisibilityMatrix(t *testing.T) {
	f := newVisibilityFixture(t)
	srv := f.srv

	// The creator sees all three of their assets.
	for path, id := range map[string]string{
		"/api/config-profiles/": f.editor1ProfileID,
		"/api/mappings/":        f.editor1MappingID,
		"/api/data-schemas/":    f.editor1SchemaID,
	} {
		if status, _ := f.editor1.do(t, srv, http.MethodGet, path+id, nil, false); status != http.StatusOK {
			t.Fatalf("creator get %s* = %d, want 200", path, status)
		}
	}

	// Another editor gets 404s — the assets must not exist for them.
	for path, id := range map[string]string{
		"/api/config-profiles/": f.editor1ProfileID,
		"/api/mappings/":        f.editor1MappingID,
		"/api/data-schemas/":    f.editor1SchemaID,
	} {
		if status, _ := f.editor2.do(t, srv, http.MethodGet, path+id, nil, false); status != http.StatusNotFound {
			t.Fatalf("stranger get %s* = %d, want 404", path, status)
		}
	}

	// Lists exclude foreign private assets but include shared ones.
	status, data := f.editor2.do(t, srv, http.MethodGet, "/api/config-profiles", nil, false)
	if status != http.StatusOK {
		t.Fatalf("list = %d", status)
	}
	if bytes.Contains(data, []byte(f.editor1ProfileID)) {
		t.Fatalf("foreign private profile leaked into list: %s", data)
	}
	// The hub overview counts exclude it too.
	status, data = f.editor2.do(t, srv, http.MethodGet, "/api/hub", nil, false)
	if status != http.StatusOK || bytes.Contains(data, []byte("http.request")) {
		t.Fatalf("hub overview leaked private profile: %d %s", status, data)
	}
	status, data = f.editor2.do(t, srv, http.MethodGet, "/api/hub/http.request", nil, false)
	if status != http.StatusOK || bytes.Contains(data, []byte(f.editor1ProfileID)) || bytes.Contains(data, []byte(f.editor1MappingID)) {
		t.Fatalf("hub node-type page leaked private assets: %d %s", status, data)
	}

	// Sharing (promote) makes an asset visible; unshare hides it again.
	if status, data = f.editor1.do(t, srv, http.MethodPost, "/api/config-profiles/"+f.editor1ProfileID+"/share", nil, true); status != http.StatusOK {
		t.Fatalf("share = %d: %s", status, data)
	}
	if status, data = f.editor2.do(t, srv, http.MethodGet, "/api/config-profiles/"+f.editor1ProfileID, nil, false); status != http.StatusOK {
		t.Fatalf("stranger get after share = %d: %s", status, data)
	}
	// A shared asset cannot be governed by a non-creator.
	if status, _ = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles/"+f.editor1ProfileID+"/unshare", nil, true); status != http.StatusForbidden {
		t.Fatalf("stranger unshare = %d, want 403", status)
	}
	if status, _ = f.editor1.do(t, srv, http.MethodPost, "/api/config-profiles/"+f.editor1ProfileID+"/unshare", nil, true); status != http.StatusOK {
		t.Fatalf("creator unshare = %d, want 200", status)
	}
	if status, _ = f.editor2.do(t, srv, http.MethodGet, "/api/config-profiles/"+f.editor1ProfileID, nil, false); status != http.StatusNotFound {
		t.Fatalf("stranger get after unshare = %d, want 404", status)
	}

	// The share endpoint on a foreign private asset 404s — it never
	// reveals the asset exists.
	if status, _ = f.editor2.do(t, srv, http.MethodPost, "/api/mappings/"+f.editor1MappingID+"/share", nil, true); status != http.StatusNotFound {
		t.Fatalf("stranger share hidden = %d, want 404", status)
	}

	// Admins see everything (D-P0a).
	if status, _ = f.admin.do(t, srv, http.MethodGet, "/api/config-profiles/"+f.editor1ProfileID, nil, false); status != http.StatusOK {
		t.Fatalf("admin get = %d, want 200", status)
	}
	// ... and can govern without owning. End private so the workflow
	// checks below exercise the hidden state.
	if status, _ = f.admin.do(t, srv, http.MethodPost, "/api/config-profiles/"+f.editor1ProfileID+"/share", nil, true); status != http.StatusOK {
		t.Fatalf("admin share = %d, want 200", status)
	}
	if status, _ = f.admin.do(t, srv, http.MethodPost, "/api/config-profiles/"+f.editor1ProfileID+"/unshare", nil, true); status != http.StatusOK {
		t.Fatalf("admin unshare = %d, want 200", status)
	}

	// A foreign private profile reference fails workflow save with a
	// named error (D-P0a).
	wf := httpWorkflow()
	wf.Nodes[1].ProfileRef = f.editor1ProfileID
	status, data = f.editor2.do(t, srv, http.MethodPut, "/api/workflows/"+wf.ID, wf, true)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("save with foreign private profile = %d, want 422: %s", status, data)
	}
	if !bytes.Contains(data, []byte("private")) {
		t.Fatalf("error does not explain privacy: %s", data)
	}
	// The same workflow saves for the creator.
	if status, data = f.editor1.do(t, srv, http.MethodPut, "/api/workflows/"+wf.ID, wf, true); status != http.StatusOK {
		t.Fatalf("creator save = %d: %s", status, data)
	}
}

func TestProfileBundleValidation(t *testing.T) {
	f := newVisibilityFixture(t)
	srv := f.srv

	// A shared mapping and schema of the other editor, plus editor 1's
	// private item mapping and editor 2's own item mapping.
	if status, data := f.editor1.do(t, srv, http.MethodPost, "/api/mappings/"+f.editor1MappingID+"/share", nil, true); status != http.StatusOK {
		t.Fatalf("share mapping = %d: %s", status, data)
	}
	if status, data := f.editor1.do(t, srv, http.MethodPost, "/api/data-schemas/"+f.editor1SchemaID+"/share", nil, true); status != http.StatusOK {
		t.Fatalf("share schema = %d: %s", status, data)
	}
	status, data := f.editor1.do(t, srv, http.MethodPost, "/api/mappings", map[string]any{
		"name": "Private item", "kind": "item",
		"rows": []store.MappingRow{{Target: "out", Source: "item"}},
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create private item mapping = %d: %s", status, data)
	}
	var privateItem store.Mapping
	_ = json.Unmarshal(data, &privateItem)
	status, data = f.editor2.do(t, srv, http.MethodPost, "/api/mappings", map[string]any{
		"name": "Own item", "kind": "item",
		"rows": []store.MappingRow{{Target: "out", Source: "item"}},
	}, true)
	if status != http.StatusCreated {
		t.Fatalf("create own item mapping = %d: %s", status, data)
	}
	var ownItem store.Mapping
	_ = json.Unmarshal(data, &ownItem)

	// A valid bundle composes the shared mapping and schema.
	body := validProfileBody()
	body["bundle"] = map[string]any{
		"mappings": map[string]any{"query": f.editor1MappingID},
		"schemas":  map[string]any{"input": f.editor1SchemaID},
	}
	status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", body, true)
	if status != http.StatusCreated {
		t.Fatalf("create bundled profile = %d: %s", status, data)
	}
	var created store.ConfigProfile
	_ = json.Unmarshal(data, &created)
	if created.Bundle == nil || created.Bundle.Mappings["query"] != f.editor1MappingID {
		t.Fatalf("created bundle = %#v", created.Bundle)
	}

	// The bundle round-trips on update and appears in the response.
	body["version"] = created.Version
	body["bundle"] = map[string]any{"schemas": map[string]any{"output": f.editor1SchemaID}}
	status, data = f.editor2.do(t, srv, http.MethodPut, "/api/config-profiles/"+created.ID, body, true)
	if status != http.StatusOK {
		t.Fatalf("update bundled profile = %d: %s", status, data)
	}
	var updated store.ConfigProfile
	_ = json.Unmarshal(data, &updated)
	if updated.Bundle == nil || updated.Bundle.Schemas["output"] != f.editor1SchemaID || updated.Bundle.Mappings != nil {
		t.Fatalf("updated bundle = %#v", updated.Bundle)
	}

	// Group not supported by the node type: item on http.request.
	bad := validProfileBody()
	bad["bundle"] = map[string]any{"mappings": map[string]any{"item": privateItem.ID}}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", bad, true); status != http.StatusBadRequest || !bytes.Contains(data, []byte("not supported")) {
		t.Fatalf("item group on http.request = %d: %s", status, data)
	}

	// Kind mismatch: a query mapping in the body group.
	bad = validProfileBody()
	bad["bundle"] = map[string]any{"mappings": map[string]any{"body": f.editor1MappingID}}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", bad, true); status != http.StatusBadRequest || !bytes.Contains(data, []byte("kind")) {
		t.Fatalf("kind mismatch = %d: %s", status, data)
	}

	// Unknown mapping id.
	bad = validProfileBody()
	bad["bundle"] = map[string]any{"mappings": map[string]any{"query": "mapping-missing"}}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", bad, true); status != http.StatusBadRequest || !bytes.Contains(data, []byte("not found")) {
		t.Fatalf("missing mapping = %d: %s", status, data)
	}

	// A data.map profile whose bundle is editor 2's own item mapping is
	// valid (the bundle is the whole content — data.map has no
	// profileable config fields).
	dataMapProfile := map[string]any{
		"name": "Map profile", "nodeType": "data.map",
		"bundle": map[string]any{"mappings": map[string]any{"item": ownItem.ID}},
	}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", dataMapProfile, true); status != http.StatusCreated {
		t.Fatalf("create data.map bundle profile = %d: %s", status, data)
	}

	// A private mapping of another user fails with a named error.
	bad = dataMapProfile
	bad["name"] = "Foreign item"
	bad["bundle"] = map[string]any{"mappings": map[string]any{"item": privateItem.ID}}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", bad, true); status != http.StatusBadRequest || !bytes.Contains(data, []byte("private")) {
		t.Fatalf("private mapping in bundle = %d: %s", status, data)
	}

	// Unknown schema slot.
	bad = validProfileBody()
	bad["bundle"] = map[string]any{"schemas": map[string]any{"side": f.editor1SchemaID}}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/config-profiles", bad, true); status != http.StatusBadRequest || !bytes.Contains(data, []byte("input or output")) {
		t.Fatalf("bad slot = %d: %s", status, data)
	}
}

func TestSharedAssetEditingIsCreatorOnly(t *testing.T) {
	f := newVisibilityFixture(t)
	srv := f.srv

	for _, path := range []string{
		"/api/config-profiles/" + f.editor1ProfileID + "/share",
		"/api/mappings/" + f.editor1MappingID + "/share",
		"/api/data-schemas/" + f.editor1SchemaID + "/share",
		"/api/scripts/" + f.editor1ScriptID + "/share",
	} {
		if status, data := f.editor1.do(t, srv, http.MethodPost, path, nil, true); status != http.StatusOK {
			t.Fatalf("share %s = %d: %s", path, status, data)
		}
	}

	profileBody := validProfileBody()
	profileBody["version"] = f.editor1ProfileVer
	mappingBody := map[string]any{
		"name": "Query mapping", "kind": "query",
		"rows":    []store.MappingRow{{Target: "limit", Source: "50"}},
		"version": f.editor1MappingVer,
	}
	schemaBody := map[string]any{
		"name":    "Order schema",
		"fields":  []map[string]any{{"name": "id", "type": "string"}},
		"version": f.editor1SchemaVer,
	}
	scriptBody := map[string]any{
		"name": "Owned script", "language": "javascript", "source": "return 2;",
		"version": f.editor1ScriptVersion,
	}

	updates := []struct {
		path string
		body any
	}{
		{"/api/config-profiles/" + f.editor1ProfileID, profileBody},
		{"/api/mappings/" + f.editor1MappingID, mappingBody},
		{"/api/data-schemas/" + f.editor1SchemaID, schemaBody},
		{"/api/scripts/" + f.editor1ScriptID, scriptBody},
	}
	for _, update := range updates {
		status, data := f.editor2.do(t, srv, http.MethodPut, update.path, update.body, true)
		if status != http.StatusForbidden {
			t.Fatalf("stranger update %s = %d, want 403: %s", update.path, status, data)
		}
		if !bytes.Contains(data, []byte("only the creator or an admin")) {
			t.Fatalf("update %s does not name the governance rule: %s", update.path, data)
		}
	}

	privateProfile := validProfileBody()
	privateProfile["version"] = f.editor1ProfileVer
	privateProfile["visibility"] = store.VisibilityPrivate
	if status, data := f.editor2.do(t, srv, http.MethodPut, "/api/config-profiles/"+f.editor1ProfileID, privateProfile, true); status != http.StatusForbidden {
		t.Fatalf("stranger visibility change = %d, want 403: %s", status, data)
	}

	creatorProfile := validProfileBody()
	creatorProfile["name"] = "Creator renamed"
	creatorProfile["version"] = f.editor1ProfileVer
	if status, data := f.editor1.do(t, srv, http.MethodPut, "/api/config-profiles/"+f.editor1ProfileID, creatorProfile, true); status != http.StatusOK {
		t.Fatalf("creator update = %d: %s", status, data)
	}

	adminMapping := map[string]any{
		"name": "Admin renamed", "kind": "query",
		"rows":    []store.MappingRow{{Target: "limit", Source: "50"}},
		"version": f.editor1MappingVer,
	}
	if status, data := f.admin.do(t, srv, http.MethodPut, "/api/mappings/"+f.editor1MappingID, adminMapping, true); status != http.StatusOK {
		t.Fatalf("admin update = %d: %s", status, data)
	}
}

func TestConnectorVisibilityBoundaries(t *testing.T) {
	f := newVisibilityFixture(t)
	srv := f.srv

	body := validConnectorBody()
	body["id"] = "conn-private"
	body["name"] = "Private API"
	status, data := f.editor1.do(t, srv, http.MethodPost, "/api/connectors", body, true)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("create connector = %d: %s", status, data)
	}
	var wrap struct {
		Connector store.Connector `json:"connector"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil {
		t.Fatalf("decode connector: %v", err)
	}
	connector := wrap.Connector
	if connector.Visibility != store.VisibilityPrivate {
		t.Fatalf("connector visibility = %q, want private", connector.Visibility)
	}
	nodeType := store.ConnectorNodeTypes(&connector)[0].Type

	if status, data = f.editor1.do(t, srv, http.MethodGet, "/api/connectors", nil, false); status != http.StatusOK || !bytes.Contains(data, []byte("conn-private")) {
		t.Fatalf("creator connector list = %d: %s", status, data)
	}
	if status, data = f.editor1.do(t, srv, http.MethodGet, "/api/connectors/conn-private", nil, false); status != http.StatusOK {
		t.Fatalf("creator connector get = %d: %s", status, data)
	}

	status, data = f.editor1.do(t, srv, http.MethodGet, "/api/node-types", nil, false)
	if status != http.StatusOK || !bytes.Contains(data, []byte(nodeType)) {
		t.Fatalf("creator node-types missing %s: %d %s", nodeType, status, data)
	}

	update := validConnectorBody()
	update["id"] = "conn-private"
	update["name"] = "Private API"
	update["version"] = connector.Version
	hidden := []struct {
		method string
		path   string
		body   any
		want   int
	}{
		{http.MethodGet, "/api/connectors/conn-private/versions", nil, http.StatusNotFound},
		{http.MethodGet, "/api/connectors/conn-private/usage", nil, http.StatusNotFound},
		{http.MethodPut, "/api/connectors/conn-private", update, http.StatusNotFound},
		{http.MethodPost, "/api/connectors/conn-private/share", nil, http.StatusNotFound},
		// Delete is admin-only for every connector, so an editor gets the
		// route's role error before any asset lookup; it does not disclose
		// whether the connector exists.
		{http.MethodDelete, "/api/connectors/conn-private", nil, http.StatusForbidden},
	}
	for _, request := range hidden {
		status, data := f.editor2.do(t, srv, request.method, request.path, request.body, request.method != http.MethodGet)
		if status != request.want {
			t.Fatalf("hidden %s %s = %d, want %d: %s", request.method, request.path, status, request.want, data)
		}
	}
	status, data = f.editor2.do(t, srv, http.MethodGet, "/api/node-types", nil, false)
	if status != http.StatusOK || bytes.Contains(data, []byte(nodeType)) {
		t.Fatalf("hidden connector leaked into node-types: %d %s", status, data)
	}

	if status, data = f.editor1.do(t, srv, http.MethodPost, "/api/connectors/conn-private/share", nil, true); status != http.StatusOK {
		t.Fatalf("share connector = %d: %s", status, data)
	}
	if status, data = f.editor2.do(t, srv, http.MethodGet, "/api/connectors/conn-private/versions", nil, false); status != http.StatusOK {
		t.Fatalf("shared connector versions = %d: %s", status, data)
	}
	if status, data = f.editor2.do(t, srv, http.MethodGet, "/api/connectors/conn-private/usage", nil, false); status != http.StatusOK {
		t.Fatalf("shared connector usage = %d: %s", status, data)
	}
	status, data = f.editor2.do(t, srv, http.MethodGet, "/api/node-types", nil, false)
	if status != http.StatusOK || !bytes.Contains(data, []byte(nodeType)) {
		t.Fatalf("shared connector missing from node-types: %d %s", status, data)
	}
	if status, data = f.editor2.do(t, srv, http.MethodPut, "/api/connectors/conn-private", update, true); status != http.StatusForbidden {
		t.Fatalf("shared connector update = %d, want 403: %s", status, data)
	}
	if status, data = f.editor2.do(t, srv, http.MethodPost, "/api/connectors/conn-private/unshare", nil, true); status != http.StatusForbidden {
		t.Fatalf("shared connector unshare = %d, want 403: %s", status, data)
	}
	if status, data = f.editor2.do(t, srv, http.MethodDelete, "/api/connectors/conn-private", nil, true); status != http.StatusForbidden {
		t.Fatalf("shared connector delete = %d, want 403: %s", status, data)
	}
	if status, data = f.admin.do(t, srv, http.MethodGet, "/api/connectors/conn-private/versions", nil, false); status != http.StatusOK {
		t.Fatalf("admin connector versions = %d: %s", status, data)
	}
}
