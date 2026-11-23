package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

func validProfileBody() map[string]any {
	return map[string]any{
		"name":     "House JSON API",
		"nodeType": "http.request",
		"config": map[string]any{
			"method":  "POST",
			"url":     "https://api.example.com/resource",
			"timeout": 30.0,
		},
	}
}

func pythonWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "wf-python-" + flow.NewID("t"), Name: "Python profile publish", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Position: graph.Position{}, Config: map[string]any{}},
			{ID: "py", Type: "code.python", TypeVersion: 1, Name: "Run", Position: graph.Position{X: 260},
				Config: map[string]any{"source": "print(40 + 2)"}},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "py", Port: "main"}}},
	}
}

func httpWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "http-wf", Name: "HTTP", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				Config:      map[string]any{"method": "GET", "url": "https://api.example.com/one"},
				Credentials: map[string]string{"http": "none"},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "http", Port: "main"}},
		},
	}
}

func TestConfigProfilesLifecycle(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	_ = st

	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", validProfileBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var created store.ConfigProfile
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Version != 1 || created.Config["method"] != "POST" {
		t.Fatalf("created = %#v", created)
	}

	// List and get agree.
	status, data = call(t, srv, http.MethodGet, "/api/config-profiles", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d: %s", status, data)
	}
	var list struct {
		Profiles []store.ConfigProfile `json:"profiles"`
		Count    int                   `json:"count"`
	}
	if err := json.Unmarshal(data, &list); err != nil || list.Count != 1 {
		t.Fatalf("list decode: %v (%s)", err, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/config-profiles/"+created.ID, "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get = %d: %s", status, data)
	}

	// Update with the right base version succeeds and bumps to v2.
	body := validProfileBody()
	body["version"] = created.Version
	body["config"] = map[string]any{"method": "GET", "timeout": 60.0}
	status, data = call(t, srv, http.MethodPut, "/api/config-profiles/"+created.ID, "test-token", body)
	if status != http.StatusOK {
		t.Fatalf("update = %d: %s", status, data)
	}
	var updated store.ConfigProfile
	if err := json.Unmarshal(data, &updated); err != nil {
		t.Fatalf("decode updated: %v", err)
	}
	if updated.Version != 2 || updated.Config["method"] != "GET" {
		t.Fatalf("updated = %#v", updated)
	}

	// A second update from stale version 1 is a 409.
	stale := validProfileBody()
	stale["version"] = 1
	status, data = call(t, srv, http.MethodPut, "/api/config-profiles/"+created.ID, "test-token", stale)
	if status != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409: %s", status, data)
	}

	// Version history is readable and append-only.
	status, data = call(t, srv, http.MethodGet, "/api/config-profiles/"+created.ID+"/versions", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("versions = %d: %s", status, data)
	}
	var versions struct {
		Versions []store.ConfigProfileVersion `json:"versions"`
	}
	if err := json.Unmarshal(data, &versions); err != nil || len(versions.Versions) != 2 {
		t.Fatalf("versions decode: %v (%s)", err, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/config-profiles/"+created.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 = %d: %s", status, data)
	}
	var v1 store.ConfigProfile
	if err := json.Unmarshal(data, &v1); err != nil || v1.Config["method"] != "POST" {
		t.Fatalf("version 1 = %v (%s)", err, data)
	}

	// Duplicate copies the current config under a new id.
	status, data = call(t, srv, http.MethodPost, "/api/config-profiles/"+created.ID+"/duplicate", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicate = %d: %s", status, data)
	}
	var dup store.ConfigProfile
	if err := json.Unmarshal(data, &dup); err != nil || dup.ID == created.ID || dup.Version != 1 {
		t.Fatalf("duplicate = %v (%s)", err, data)
	}

	// Delete the unused original and the duplicate, then a missing one.
	for _, id := range []string{created.ID, dup.ID} {
		if status, data = call(t, srv, http.MethodDelete, "/api/config-profiles/"+id, "test-token", nil); status != http.StatusNoContent {
			t.Fatalf("delete %s = %d: %s", id, status, data)
		}
	}
	if status, _ = call(t, srv, http.MethodDelete, "/api/config-profiles/missing", "test-token", nil); status != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", status)
	}
}

func TestConfigProfilesValidateAgainstCatalog(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	cases := []struct {
		name string
		body map[string]any
	}{
		{"non-profileable field", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{"failOnError": true}
			return b
		}()},
		{"mapping refs stay node-only", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{"mappingRefs": map[string]any{"query": "m-1"}}
			return b
		}()},
		{"malformed param rows", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{"queryFields": []any{
				map[string]any{"name": "limit", "mode": "sometimes", "type": "number", "value": 25.0},
			}}
			return b
		}()},
		{"param row value type mismatch", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{"queryFields": []any{
				map[string]any{"name": "limit", "mode": "static", "type": "number", "value": "twenty"},
			}}
			return b
		}()},
		{"unknown field", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{"nope": "x"}
			return b
		}()},
		{"bad enum", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{"method": "TELEPORT"}
			return b
		}()},
		{"unknown node type", func() map[string]any {
			b := validProfileBody()
			b["nodeType"] = "http.ftp"
			return b
		}()},
		{"empty name", func() map[string]any {
			b := validProfileBody()
			b["name"] = " "
			return b
		}()},
		{"secret field via credential ref slot", func() map[string]any {
			b := validProfileBody()
			b["credentialRefs"] = map[string]string{"postgres": "some-cred"}
			return b
		}()},
		{"empty config with no retry", func() map[string]any {
			b := validProfileBody()
			b["config"] = map[string]any{}
			b["retry"] = nil
			return b
		}()},
	}
	for _, tc := range cases {
		if status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", tc.body); status != http.StatusBadRequest {
			t.Fatalf("%s: create = %d, want 400: %s", tc.name, status, data)
		}
	}

	// A missing version on update is rejected up front.
	if status, data := call(t, srv, http.MethodPut, "/api/config-profiles/whatever", "test-token", validProfileBody()); status != http.StatusBadRequest {
		t.Fatalf("update without version = %d, want 400: %s", status, data)
	}

	// A retry-only profile is still meaningful (v4 D5-B): no config
	// fields, but the retry policy applies at publish.
	retryOnly := map[string]any{
		"name":     "Retry only",
		"nodeType": "http.request",
		"config":   map[string]any{},
		"retry":    map[string]any{"maxAttempts": 3},
	}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", retryOnly)
	if status != http.StatusCreated {
		t.Fatalf("retry-only profile create = %d, want 201: %s", status, data)
	}
	var retryProfile store.ConfigProfile
	_ = json.Unmarshal(data, &retryProfile)
	if retryProfile.Retry == nil || retryProfile.Retry.MaxAttempts != 3 {
		t.Fatalf("retry-only profile = %#v", retryProfile)
	}
	if status, _ = call(t, srv, http.MethodDelete, "/api/config-profiles/"+retryProfile.ID, "test-token", nil); status != http.StatusNoContent {
		t.Fatalf("cleanup retry-only profile = %d", status)
	}
}

// Deleted-profile reference integrity (D2/D6): a workflow whose node
// references a missing (or wrong-type) profile cannot be saved.
func TestWorkflowSaveRejectsMissingProfileRef(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	wf := linearWorkflow()
	wf.Nodes[1].ProfileRef = "profile-missing"
	status, data := call(t, srv, http.MethodPut, "/api/workflows/"+wf.ID, "test-token", wf)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("save with missing profileRef = %d, want 422: %s", status, data)
	}
	if !bytes.Contains(data, []byte("profile-missing")) {
		t.Fatalf("error does not name the profile: %s", data)
	}

	// A wrong-type profile is rejected by name too.
	status, data = call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", validProfileBody())
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var created store.ConfigProfile
	_ = json.Unmarshal(data, &created)

	wf2 := linearWorkflow()
	wf2.Nodes[1].ProfileRef = created.ID // http profile on a code.run node
	status, data = call(t, srv, http.MethodPut, "/api/workflows/"+wf2.ID, "test-token", wf2)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("save with wrong-type profileRef = %d, want 422: %s", status, data)
	}

	// Node types with no profileable fields cannot host profiles at all.
	bad := validProfileBody()
	bad["nodeType"] = "trigger.manual"
	if status, data = call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", bad); status != http.StatusBadRequest {
		t.Fatalf("unprofileable node type = %d, want 400: %s", status, data)
	}
}

// D4/D5: publish resolves profile config inline into the version snapshot
// (node's own config wins per key) and records the usage pin.
func TestPublishResolvesProfileInline(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	profileBody := validProfileBody()
	profileBody["config"] = map[string]any{
		"method":       "POST",
		"url":          "https://api.example.com/resource",
		"timeout":      30.0,
		"successCodes": "2xx",
	}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", profileBody)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)

	// Seed a workflow whose http node pins the profile and overrides only
	// the timeout.
	wf := httpWorkflow()
	wf.Nodes[1].ProfileRef = profile.ID
	wf.Nodes[1].Config = map[string]any{"timeout": 60.0}
	status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf)
	if status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	// The published version snapshot carries the resolved config.
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 = %d: %s", status, data)
	}
	var snap graph.Workflow
	if err := json.Unmarshal(data, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	node := snap.Nodes[1]
	if node.Config["method"] != "POST" {
		t.Fatalf("profile method missing from snapshot: %#v", node.Config)
	}
	if node.Config["timeout"] != 60.0 {
		t.Fatalf("node override did not win: %#v", node.Config)
	}
	if node.Config["url"] != "https://api.example.com/resource" {
		t.Fatalf("profile url missing from snapshot: %#v", node.Config)
	}

	// The usage index recorded the pin.
	usage, err := st.ConfigProfileUsage(profile.ID)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %v, %v", usage, err)
	}
	if usage[0].WorkflowID != wf.ID || usage[0].WorkflowVersion != 1 || usage[0].ProfileVersion != 1 {
		t.Fatalf("usage row = %#v", usage[0])
	}

	// Now the profile is in use: deletion is blocked.
	if status, data = call(t, srv, http.MethodDelete, "/api/config-profiles/"+profile.ID, "test-token", nil); status != http.StatusConflict {
		t.Fatalf("in-use delete = %d, want 409: %s", status, data)
	}
}

// TestPublishMergesProfileParamRowsPerName (v4.1 E4): profile parameter
// rows fill around the node's own — per row name for row arrays, and a
// node-level plain target object wins the whole group.
func TestPublishMergesProfileParamRowsPerName(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	profileBody := validProfileBody()
	profileBody["config"] = map[string]any{
		"method": "POST",
		"url":    "https://api.example.com/resource",
		"queryFields": []any{
			map[string]any{"name": "limit", "mode": "static", "type": "number", "value": 50.0},
			map[string]any{"name": "active", "mode": "static", "type": "boolean", "value": true},
		},
	}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", profileBody)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatalf("decode profile: %v", err)
	}

	wf := &graph.Workflow{
		ID: "wf-param-merge-" + flow.NewID("t"), Name: "Param merge", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "rows", Type: "http.request", TypeVersion: 1, Name: "Rows win per name", ProfileRef: profile.ID,
				Config: map[string]any{
					"method": "POST", "url": "https://api.example.com/resource",
					"queryFields": []any{
						map[string]any{"name": "limit", "mode": "static", "type": "number", "value": 25.0},
					},
				},
			},
			{
				ID: "raw", Type: "http.request", TypeVersion: 1, Name: "Raw object wins whole group", ProfileRef: profile.ID,
				Config: map[string]any{
					"method": "POST", "url": "https://api.example.com/resource",
					"query": map[string]any{"q": "x"},
				},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "rows", Port: "main"}},
			{From: graph.Endpoint{Node: "rows", Port: "main"}, To: graph.Endpoint{Node: "raw", Port: "main"}},
		},
	}
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

	var rowsNode, rawNode graph.Node
	for _, node := range snap.Nodes {
		switch node.ID {
		case "rows":
			rowsNode = node
		case "raw":
			rawNode = node
		}
	}
	// Row arrays merge per name: the node's limit wins, the profile's
	// active fills in, and the compiled query carries the merge. (The
	// fields array itself stays in the doc — v3 keeps editor rows; the
	// engine reads only the compiled target.)
	rowsQuery, _ := rowsNode.Config["query"].(map[string]any)
	if rowsQuery["limit"] != 25.0 || rowsQuery["active"] != true || len(rowsQuery) != 2 {
		t.Fatalf("merged query = %#v, want {limit: 25, active: true}", rowsQuery)
	}
	// A node-level plain query object is an explicit whole-group
	// override: the profile's rows never compile over it.
	rawQuery, _ := rawNode.Config["query"].(map[string]any)
	if rawQuery["q"] != "x" || len(rawQuery) != 1 {
		t.Fatalf("raw query = %#v, want {q: x} only", rawQuery)
	}
	if _, leaked := rawNode.Config["queryFields"]; leaked {
		t.Fatalf("profile rows leaked into an explicitly-set group: %#v", rawNode.Config)
	}
}

func TestConfigProfilesDeleteGuardAndUsage(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", validProfileBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var created store.ConfigProfile
	_ = json.Unmarshal(data, &created)

	// Pin it the way publish will: usage index row.
	if err := st.RecordConfigProfileUsage(created.ID, 1, "wf-1", 1); err != nil {
		t.Fatalf("record usage: %v", err)
	}

	if status, data = call(t, srv, http.MethodDelete, "/api/config-profiles/"+created.ID, "test-token", nil); status != http.StatusConflict {
		t.Fatalf("in-use delete = %d, want 409: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/config-profiles/"+created.ID+"/usage", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("usage = %d: %s", status, data)
	}
	var usage struct {
		Usage []store.ConfigProfileUsage `json:"usage"`
		Count int                        `json:"count"`
	}
	if err := json.Unmarshal(data, &usage); err != nil || usage.Count != 1 || usage.Usage[0].WorkflowID != "wf-1" {
		t.Fatalf("usage = %v, %v (%s)", usage, err, data)
	}
}

func TestConfigProfilesRBAC(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "viewer@example.com", "viewer", "viewer-password")
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	addUser(t, st, "admin@example.com", "admin", "admin-password")
	viewerSession := loginAs(t, srv, "viewer@example.com", "viewer-password")
	editorSession := loginAs(t, srv, "editor@example.com", "editor-password")
	adminSession := loginAs(t, srv, "admin@example.com", "admin-password")

	// Editors create (D7).
	status, data := editorSession.do(t, srv, http.MethodPost, "/api/config-profiles", validProfileBody(), true)
	if status != http.StatusCreated {
		t.Fatalf("editor create = %d: %s", status, data)
	}
	var created store.ConfigProfile
	_ = json.Unmarshal(data, &created)

	// Viewers read but cannot create.
	if status, _ := viewerSession.do(t, srv, http.MethodGet, "/api/config-profiles", nil, false); status != http.StatusOK {
		t.Fatalf("viewer list = %d, want 200", status)
	}
	if status, _ := viewerSession.do(t, srv, http.MethodPost, "/api/config-profiles", validProfileBody(), true); status != http.StatusForbidden {
		t.Fatalf("viewer create = %d, want 403", status)
	}

	// Editors update; viewers cannot.
	body := validProfileBody()
	body["version"] = 1
	if status, _ := editorSession.do(t, srv, http.MethodPut, "/api/config-profiles/"+created.ID, body, true); status != http.StatusOK {
		t.Fatalf("editor update = %d, want 200", status)
	}
	if status, _ := viewerSession.do(t, srv, http.MethodPut, "/api/config-profiles/"+created.ID, body, true); status != http.StatusForbidden {
		t.Fatalf("viewer update = %d, want 403", status)
	}

	// Admins delete; editors cannot (D7).
	if status, _ := adminSession.do(t, srv, http.MethodDelete, "/api/config-profiles/"+created.ID, nil, true); status != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", status)
	}
	if status, _ := editorSession.do(t, srv, http.MethodDelete, "/api/config-profiles/"+created.ID, nil, true); status != http.StatusForbidden {
		t.Fatalf("editor delete = %d, want 403", status)
	}

	// Profile mutations land in the audit trail.
	events, err := st.ListAuthEvents(50)
	if err != nil {
		t.Fatalf("list auth events: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Type] = true
	}
	for _, want := range []string{"profile.created", "profile.updated", "profile.deleted"} {
		if !seen[want] {
			t.Fatalf("audit event %q missing; have %v", want, seen)
		}
	}
}

// fakeProfileTester records calls so the endpoint's wiring (not the
// network) is what's under test; internal/host covers the real dials.
type fakeProfileTester struct {
	httpCalls    int
	postgresCall int
	lastURL      string
	lastTimeout  time.Duration
	httpStatus   int
	httpErr      error
	pgErr        error
}

func (f *fakeProfileTester) TestHTTPProfile(_ context.Context, rawURL string, timeout time.Duration) (int, int64, error) {
	f.httpCalls++
	f.lastURL = rawURL
	f.lastTimeout = timeout
	if f.httpErr != nil {
		return 0, 0, f.httpErr
	}
	return f.httpStatus, 12, nil
}

func (f *fakeProfileTester) TestPostgresProfile(_ context.Context, _ string, _ time.Duration) (int64, error) {
	f.postgresCall++
	if f.pgErr != nil {
		return 0, f.pgErr
	}
	return 34, nil
}

func TestConfigProfileTestEndpoint(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Simulated server (no TestProfile): actionable 503.
	simSrv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()), Token: "t",
	}))
	t.Cleanup(simSrv.Close)
	body := validProfileBody()
	status, data := call(t, simSrv, http.MethodPost, "/api/config-profiles", "t", body)
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)
	if status, data := call(t, simSrv, http.MethodPost, "/api/config-profiles/"+profile.ID+"/test", "t", nil); status != http.StatusServiceUnavailable {
		t.Fatalf("sim test = %d: %s, want 503", status, data)
	}

	// Live server (fake tester injected).
	tester := &fakeProfileTester{httpStatus: 200}
	liveSrv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()), Token: "t", TestProfile: tester,
	}))
	t.Cleanup(liveSrv.Close)

	// Happy path: the profile's url and timeout reach the tester.
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+profile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("test = %d: %s", status, data)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if parsed["ok"] != true {
		t.Fatalf("ok = %v, want true", parsed["ok"])
	}
	if parsed["status"] != float64(200) {
		t.Fatalf("status = %v, want 200", parsed["status"])
	}
	if tester.httpCalls != 1 || tester.lastURL != "https://api.example.com/resource" {
		t.Fatalf("tester calls = %d url = %q", tester.httpCalls, tester.lastURL)
	}
	if tester.lastTimeout != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s (from profile config)", tester.lastTimeout)
	}

	// A failing dial is a 200 with ok:false — information, not a verdict.
	tester.httpErr = errors.New("refusing to call private address \"127.0.0.1\"")
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+profile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("failed test = %d: %s, want 200", status, data)
	}
	var failed map[string]any
	_ = json.Unmarshal(data, &failed)
	if failed["ok"] != false || failed["error"] == nil {
		t.Fatalf("failed body = %v", failed)
	}

	// A profile without a url is a 422 before any dial.
	noURL := validProfileBody()
	noURL["config"] = map[string]any{"timeout": 30.0}
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles", "t", noURL)
	if status != http.StatusCreated {
		t.Fatalf("create no-url = %d: %s", status, data)
	}
	var noURLProfile store.ConfigProfile
	_ = json.Unmarshal(data, &noURLProfile)
	if status, _ := call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+noURLProfile.ID+"/test", "t", nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("no-url test = %d, want 422", status)
	}

	// Unsupported node type is a 422.
	other := validProfileBody()
	other["nodeType"] = "data.echo"
	if status, data := call(t, liveSrv, http.MethodPost, "/api/config-profiles", "t", other); status != http.StatusBadRequest {
		t.Fatalf("create echo = %d: %s", status, data)
	}

	// Postgres without a credential reference is a 422 with guidance.
	pg := validProfileBody()
	pg["nodeType"] = "db.postgres"
	pg["config"] = map[string]any{"operation": "query"}
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles", "t", pg)
	if status != http.StatusCreated {
		t.Fatalf("create pg = %d: %s", status, data)
	}
	var pgProfile store.ConfigProfile
	_ = json.Unmarshal(data, &pgProfile)
	if status, _ := call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+pgProfile.ID+"/test", "t", nil); status != http.StatusUnprocessableEntity {
		t.Fatalf("pg no-cred test = %d, want 422", status)
	}

	// Postgres with a credential reference goes through the tester.
	if err := st.SaveCredential("warehouse", "postgres", []byte(`{"url":"postgres://u:p@db.example.com:5432/w"}`)); err != nil {
		t.Fatalf("save warehouse credential: %v", err)
	}
	pg["version"] = pgProfile.Version
	pg["credentialRefs"] = map[string]string{"postgres": "warehouse"}
	if status, data := call(t, liveSrv, http.MethodPut, "/api/config-profiles/"+pgProfile.ID, "t", pg); status != http.StatusOK {
		t.Fatalf("update pg = %d: %s", status, data)
	}
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+pgProfile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("pg test = %d: %s", status, data)
	}
	var pgResult map[string]any
	_ = json.Unmarshal(data, &pgResult)
	if pgResult["ok"] != true || tester.postgresCall != 1 {
		t.Fatalf("pg result = %v calls = %d", pgResult, tester.postgresCall)
	}

	// UX Phase 2 D2-7: the test endpoint gains a bundle section — refs
	// resolved to their current versions, parameter rows compiled, named
	// issues — alongside connectivity.
	seedMapping := func(name string) string {
		status, data := call(t, liveSrv, http.MethodPost, "/api/mappings", "t", map[string]any{
			"name": name, "kind": "query", "rows": []map[string]any{{"target": "limit", "source": "20"}},
		})
		if status != http.StatusCreated {
			t.Fatalf("seed mapping = %d: %s", status, data)
		}
		var mapping struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &mapping)
		return mapping.ID
	}
	seedSchema := func(name string) string {
		status, data := call(t, liveSrv, http.MethodPost, "/api/data-schemas", "t", map[string]any{
			"name": name, "fields": []map[string]any{{"name": "limit", "type": "number"}},
		})
		if status != http.StatusCreated {
			t.Fatalf("seed schema = %d: %s", status, data)
		}
		var schema struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(data, &schema)
		return schema.ID
	}

	// A bundled profile reports resolved refs alongside the dial.
	tester.httpErr = nil
	mappingID := seedMapping("Test Paging")
	schemaID := seedSchema("Test Limits")
	bundled := validProfileBody()
	bundled["name"] = "Bundled House API"
	bundled["bundle"] = map[string]any{
		"mappings": map[string]string{"query": mappingID},
		"schemas":  map[string]string{"input": schemaID},
	}
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles", "t", bundled)
	if status != http.StatusCreated {
		t.Fatalf("create bundled = %d: %s", status, data)
	}
	var bundledProfile store.ConfigProfile
	_ = json.Unmarshal(data, &bundledProfile)
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+bundledProfile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("bundled test = %d: %s", status, data)
	}
	var bundledResult struct {
		Ok       bool `json:"ok"`
		BundleOk bool `json:"bundleOk"`
		Bundle   struct {
			Mappings []struct {
				Kind string `json:"kind"`
				Name string `json:"name"`
				Rows int    `json:"rows"`
			} `json:"mappings"`
			Schemas []struct {
				Slot   string `json:"slot"`
				Name   string `json:"name"`
				Fields int    `json:"fields"`
			} `json:"schemas"`
			Issues []struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"issues"`
		} `json:"bundle"`
	}
	if err := json.Unmarshal(data, &bundledResult); err != nil {
		t.Fatalf("parse bundled body: %v", err)
	}
	if !bundledResult.Ok || !bundledResult.BundleOk {
		t.Fatalf("bundled result = %+v", bundledResult)
	}
	if len(bundledResult.Bundle.Mappings) != 1 ||
		bundledResult.Bundle.Mappings[0].Kind != "query" ||
		bundledResult.Bundle.Mappings[0].Name != "Test Paging" ||
		bundledResult.Bundle.Mappings[0].Rows != 1 {
		t.Fatalf("bundle mappings = %+v", bundledResult.Bundle.Mappings)
	}
	if len(bundledResult.Bundle.Schemas) != 1 ||
		bundledResult.Bundle.Schemas[0].Slot != "input" ||
		bundledResult.Bundle.Schemas[0].Name != "Test Limits" ||
		bundledResult.Bundle.Schemas[0].Fields != 1 {
		t.Fatalf("bundle schemas = %+v", bundledResult.Bundle.Schemas)
	}

	// A dangling mapping ref is a named issue, not a failed request.
	// Server-side create/update validation rejects dangling refs, so this
	// writes the profile straight to the store — the imported/legacy
	// shape the endpoint defends against.
	danglingProfile, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID:       "dangling-profile-" + flow.NewID("t"),
		Name:     "Dangling House API",
		NodeType: "http.request",
		Config: map[string]any{
			"method":  "POST",
			"url":     "https://api.example.com/resource",
			"timeout": 30.0,
		},
		Bundle: &store.ProfileBundle{Mappings: map[string]string{"query": "no-such-mapping"}},
	}, "test")
	if err != nil {
		t.Fatalf("seed dangling profile: %v", err)
	}
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+danglingProfile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("dangling test = %d: %s", status, data)
	}
	var danglingResult struct {
		BundleOk bool `json:"bundleOk"`
		Bundle   struct {
			Issues []struct {
				Name    string `json:"name"`
				Message string `json:"message"`
			} `json:"issues"`
		} `json:"bundle"`
	}
	_ = json.Unmarshal(data, &danglingResult)
	if danglingResult.BundleOk || len(danglingResult.Bundle.Issues) != 1 ||
		danglingResult.Bundle.Issues[0].Name != "query mapping" ||
		!strings.Contains(danglingResult.Bundle.Issues[0].Message, "no longer exists") {
		t.Fatalf("dangling result = %+v", danglingResult)
	}

	// Parameter rows compile into the response; a no-url profile with
	// rows tests 200 with connectivity skipped instead of 422.
	rowy := validProfileBody()
	rowy["name"] = "Rowy House API"
	rowy["config"] = map[string]any{
		"timeout": 30.0,
		"queryFields": []map[string]any{
			{"name": "limit", "mode": "static", "type": "number", "value": 20.0},
		},
	}
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles", "t", rowy)
	if status != http.StatusCreated {
		t.Fatalf("create rowy = %d: %s", status, data)
	}
	var rowyProfile store.ConfigProfile
	_ = json.Unmarshal(data, &rowyProfile)
	status, data = call(t, liveSrv, http.MethodPost, "/api/config-profiles/"+rowyProfile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("rowy test = %d: %s", status, data)
	}
	var rowyResult struct {
		Skipped  string `json:"skipped"`
		BundleOk bool   `json:"bundleOk"`
		Bundle   struct {
			ParamRows []struct {
				Group string `json:"group"`
				Rows  int    `json:"rows"`
			} `json:"paramRows"`
		} `json:"bundle"`
	}
	_ = json.Unmarshal(data, &rowyResult)
	if rowyResult.Skipped == "" || !strings.Contains(rowyResult.Skipped, "no url") ||
		len(rowyResult.Bundle.ParamRows) != 1 ||
		rowyResult.Bundle.ParamRows[0].Group != "queryFields" ||
		rowyResult.Bundle.ParamRows[0].Rows != 1 {
		t.Fatalf("rowy result = %+v", rowyResult)
	}

	// A simulated server answers bundle work without a tester: the
	// connectivity half is skipped with the actionable message.
	simBundled := validProfileBody()
	simBundled["name"] = "Sim Bundled House API"
	simBundled["bundle"] = map[string]any{"mappings": map[string]string{"query": mappingID}}
	status, data = call(t, simSrv, http.MethodPost, "/api/config-profiles", "t", simBundled)
	if status != http.StatusCreated {
		t.Fatalf("create sim-bundled = %d: %s", status, data)
	}
	var simBundledProfile store.ConfigProfile
	_ = json.Unmarshal(data, &simBundledProfile)
	status, data = call(t, simSrv, http.MethodPost, "/api/config-profiles/"+simBundledProfile.ID+"/test", "t", nil)
	if status != http.StatusOK {
		t.Fatalf("sim-bundled test = %d: %s", status, data)
	}
	var simBundledResult struct {
		Skipped  string `json:"skipped"`
		BundleOk bool   `json:"bundleOk"`
		Bundle   struct {
			Mappings []struct {
				Kind string `json:"kind"`
				Rows int    `json:"rows"`
			} `json:"mappings"`
		} `json:"bundle"`
	}
	_ = json.Unmarshal(data, &simBundledResult)
	if !strings.Contains(simBundledResult.Skipped, "simulated host") ||
		!simBundledResult.BundleOk ||
		len(simBundledResult.Bundle.Mappings) != 1 ||
		simBundledResult.Bundle.Mappings[0].Kind != "query" {
		t.Fatalf("sim-bundled result = %+v", simBundledResult)
	}

	// Viewers cannot run tests.
	rbacSrv, rbacSt, _ := newAuthServer(t)
	addUser(t, rbacSt, "viewer@example.com", "viewer", "viewer-password")
	viewerSession := loginAs(t, rbacSrv, "viewer@example.com", "viewer-password")
	if status, _ := viewerSession.do(t, rbacSrv, http.MethodPost, "/api/config-profiles/some-id/test", nil, false); status != http.StatusForbidden {
		t.Fatalf("viewer test = %d, want 403", status)
	}
}

func TestPublishBuildsPythonEnvFromProfileRequirements(t *testing.T) {
	var built [][]string
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()), Token: "test-token",
		BuildEnvs: func(requirements []string) error {
			built = append(built, requirements)
			return nil
		},
	}))
	t.Cleanup(srv.Close)

	profileBody := validProfileBody()
	profileBody["nodeType"] = "code.python"
	profileBody["config"] = map[string]any{
		"requirements":   []any{"idna==3.10", "charset-normalizer==3.4.0"},
		"timeoutSeconds": float64(60),
	}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", profileBody)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)

	wf := pythonWorkflow()
	wf.Nodes[1].ProfileRef = profile.ID
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	if len(built) != 1 || len(built[0]) != 2 {
		t.Fatalf("BuildEnvs calls = %v, want one call with the profile requirements", built)
	}

	// The snapshot carries the resolved runtime config.
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version = %d: %s", status, data)
	}
	var snap graph.Workflow
	_ = json.Unmarshal(data, &snap)
	node := snap.Nodes[1]
	if node.Config["timeoutSeconds"] != float64(60) {
		t.Fatalf("snapshot timeoutSeconds = %v, want 60", node.Config["timeoutSeconds"])
	}
	reqs, ok := node.Config["requirements"].([]any)
	if !ok || len(reqs) != 2 {
		t.Fatalf("snapshot requirements = %v, want two entries", node.Config["requirements"])
	}
}

func TestPublishMergesProfileCredentialRefs(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")
	if err := st.SaveCredential("shared-cred", "http", []byte(`{"token":"secret"}`)); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	profileBody := validProfileBody()
	profileBody["credentialRefs"] = map[string]string{"http": "shared-cred"}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", profileBody)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)

	// Node with no credential of its own: the profile's reference fills
	// the slot in the published snapshot.
	wf := httpWorkflow()
	wf.Nodes[1].ProfileRef = profile.ID
	wf.Nodes[1].Credentials = nil
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version = %d: %s", status, data)
	}
	var snap graph.Workflow
	_ = json.Unmarshal(data, &snap)
	if got := snap.Nodes[1].Credentials["http"]; got != "shared-cred" {
		t.Fatalf("snapshot credential = %q, want shared-cred (profile fills the empty slot)", got)
	}

	// A node's own credential wins over the profile's (node overrides).
	wf2 := httpWorkflow()
	wf2.ID = "cred-override-" + flow.NewID("t")
	wf2.Nodes[1].ProfileRef = profile.ID
	wf2.Nodes[1].Credentials = map[string]string{"http": "node-own-cred"}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf2); status != http.StatusCreated {
		t.Fatalf("create workflow 2 = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf2.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish 2 = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf2.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version 2 = %d: %s", status, data)
	}
	var snap2 graph.Workflow
	_ = json.Unmarshal(data, &snap2)
	if got := snap2.Nodes[1].Credentials["http"]; got != "node-own-cred" {
		t.Fatalf("snapshot credential = %q, want node-own-cred (node wins)", got)
	}
}

func TestPublishMergesProfileRetryPolicy(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	// A profile with a retry policy for its nodes.
	body := validProfileBody()
	body["retry"] = map[string]any{"maxAttempts": 5, "backoffMs": 250, "maxDelayMs": 4000}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", body)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)
	if profile.Retry == nil || profile.Retry.MaxAttempts != 5 {
		t.Fatalf("stored retry = %#v", profile.Retry)
	}

	// Node with no policy of its own: the profile's retry lands in the
	// published snapshot.
	wf := httpWorkflow()
	wf.Nodes[1].ProfileRef = profile.ID
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version = %d: %s", status, data)
	}
	var snap graph.Workflow
	_ = json.Unmarshal(data, &snap)
	if snap.Nodes[1].Retry == nil || snap.Nodes[1].Retry.MaxAttempts != 5 || snap.Nodes[1].Retry.BackoffMs != 250 {
		t.Fatalf("snapshot retry = %#v, want the profile policy", snap.Nodes[1].Retry)
	}

	// A node's own policy wins over the profile's.
	wf2 := httpWorkflow()
	wf2.ID = "retry-override-" + flow.NewID("t")
	wf2.Nodes[1].ProfileRef = profile.ID
	wf2.Nodes[1].Retry = &graph.RetryPolicy{MaxAttempts: 2}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf2); status != http.StatusCreated {
		t.Fatalf("create workflow 2 = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf2.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish 2 = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf2.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("version 2 = %d: %s", status, data)
	}
	var snap2 graph.Workflow
	_ = json.Unmarshal(data, &snap2)
	if snap2.Nodes[1].Retry == nil || snap2.Nodes[1].Retry.MaxAttempts != 2 {
		t.Fatalf("snapshot retry = %#v, want the node policy (node wins)", snap2.Nodes[1].Retry)
	}
	_ = st
}

func TestProfileRetryValidationBounds(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")
	for name, retry := range map[string]map[string]any{
		"attempts too low":  {"maxAttempts": 0},
		"attempts too high": {"maxAttempts": 11},
		"negative backoff":  {"maxAttempts": 3, "backoffMs": -1},
		"backoff too high":  {"maxAttempts": 3, "backoffMs": 700000},
		"max delay too low": {"maxAttempts": 3, "maxDelayMs": -5},
	} {
		body := validProfileBody()
		body["retry"] = retry
		if status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", body); status != http.StatusBadRequest {
			t.Fatalf("%s: create = %d: %s, want 400", name, status, data)
		}
	}
}

// TestWorkflowUpgradePreviewAndApply (UX Phase 2 D2-6): a published
// workflow's profile pins go stale when the profile moves. The
// upgrade-preview endpoint infers per-key ownership — a stored value
// that equals the pinned version's value follows the profile forward, a
// node override stays — and {"apply": true} republishes with the
// upgrade.
func TestWorkflowUpgradePreviewAndApply(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	profileBody := validProfileBody()
	profileBody["config"] = map[string]any{
		"method":  "POST",
		"url":     "https://api.example.com/resource",
		"timeout": 30.0,
	}
	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", profileBody)
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	var profile store.ConfigProfile
	_ = json.Unmarshal(data, &profile)

	// The node pins the profile and overrides only the timeout — the
	// override must survive the upgrade.
	wf := httpWorkflow()
	wf.Nodes[1].ProfileRef = profile.ID
	wf.Nodes[1].Config = map[string]any{"timeout": 60.0}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	// The profile moves: method and url change, headers are added,
	// timeout stays 30 (the node's stored 60 is its own override).
	profileBody["version"] = 1
	profileBody["config"] = map[string]any{
		"method":  "GET",
		"url":     "https://api.example.com/v2",
		"timeout": 30.0,
		"headers": map[string]any{"accept": "json"},
	}
	if status, data = call(t, srv, http.MethodPut, "/api/config-profiles/"+profile.ID, "test-token", profileBody); status != http.StatusOK {
		t.Fatalf("update profile = %d: %s", status, data)
	}

	// Preview (no apply): a plan of exactly the profile-owned changes.
	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/upgrade-preview", "test-token", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("preview = %d: %s", status, data)
	}
	var preview struct {
		Applied bool                         `json:"applied"`
		Plan    []store.ProfileUpgradeChange `json:"plan"`
	}
	if err := json.Unmarshal(data, &preview); err != nil {
		t.Fatalf("decode preview: %v", err)
	}
	if preview.Applied {
		t.Fatal("preview must not apply")
	}
	planByKey := map[string]store.ProfileUpgradeChange{}
	for _, change := range preview.Plan {
		planByKey[change.Key] = change
	}
	if len(preview.Plan) != 3 {
		t.Fatalf("plan = %#v", preview.Plan)
	}
	if c := planByKey["config.method"]; c.Before != `"POST"` || c.After != `"GET"` {
		t.Fatalf("method change = %#v", c)
	}
	if c := planByKey["config.url"]; c.Before == "" || c.After != `"https://api.example.com/v2"` {
		t.Fatalf("url change = %#v", c)
	}
	if c := planByKey["config.headers"]; c.Before != "" || c.After == "" {
		t.Fatalf("headers fill = %#v", c)
	}
	if _, ok := planByKey["config.timeout"]; ok {
		t.Fatalf("node override must not be in the plan: %#v", planByKey["config.timeout"])
	}

	// Apply: the upgrade lands in a new published version.
	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/upgrade-preview", "test-token", map[string]any{"apply": true})
	if status != http.StatusCreated {
		t.Fatalf("apply = %d: %s", status, data)
	}
	var applied struct {
		Applied  bool                  `json:"applied"`
		Workflow graph.Workflow        `json:"workflow"`
		Version  store.WorkflowVersion `json:"version"`
	}
	if err := json.Unmarshal(data, &applied); err != nil {
		t.Fatalf("decode apply: %v", err)
	}
	if !applied.Applied || applied.Version.Version != 2 {
		t.Fatalf("apply result = %+v", applied.Version)
	}
	node := applied.Workflow.Nodes[1]
	if node.Config["method"] != "GET" {
		t.Fatalf("method not upgraded: %#v", node.Config)
	}
	if node.Config["url"] != "https://api.example.com/v2" {
		t.Fatalf("url not upgraded: %#v", node.Config)
	}
	if node.Config["timeout"] != 60.0 {
		t.Fatalf("node override lost: %#v", node.Config)
	}
	if node.Config["headers"] == nil {
		t.Fatalf("headers fill missing: %#v", node.Config)
	}

	// The usage index now pins the current profile version.
	usage, err := st.ConfigProfileUsage(profile.ID)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	latestUsage := usage[0]
	for _, row := range usage {
		if row.WorkflowVersion > latestUsage.WorkflowVersion {
			latestUsage = row
		}
	}
	if latestUsage.WorkflowVersion != 2 || latestUsage.ProfileVersion != 2 {
		t.Fatalf("usage row = %#v", latestUsage)
	}

	// A second preview is empty: the workflow is current.
	status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/upgrade-preview", "test-token", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("second preview = %d: %s", status, data)
	}
	_ = json.Unmarshal(data, &preview)
	if len(preview.Plan) != 0 {
		t.Fatalf("second preview must be empty: %#v", preview.Plan)
	}
}

// Phase 5: the test fixture rides the profile doc — validated as JSON,
// size-capped, versioned like any other content — and code profiles
// execute against it (code.run in-process even on a simulated host;
// code.python skips on sim with the actionable message).
func TestProfileTestInputFixture(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	// A fixture arriving through the JSON API is valid JSON by
	// construction (readJSON would reject the whole body otherwise);
	// the size cap is the meaningful save-time gate.
	status, body := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "big-fixture", "nodeType": "code.run",
		"config": map[string]any{"language": "javascript"}, "testInput": `"` + strings.Repeat("x", 9000) + `"`,
	})
	if status != http.StatusBadRequest || !strings.Contains(string(body), "8 KiB") {
		t.Fatalf("oversize fixture = %d: %s", status, body)
	}

	// A code.run profile composes a bundle script plus runtime defaults
	// and a fixture; its test executes the script in-process (even on
	// a simulated host) with the fixture as the item.
	scriptStatus, scriptBody := call(t, srv, http.MethodPost, "/api/scripts", "test-token", map[string]any{
		"name": "shape-script", "language": "javascript", "source": "return { total: $item.count * 2 }",
	})
	if scriptStatus != http.StatusCreated {
		t.Fatalf("create script = %d: %s", scriptStatus, scriptBody)
	}
	var script store.Script
	_ = json.Unmarshal([]byte(scriptBody), &script)
	status, body = call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "shape-profile", "nodeType": "code.run",
		"config":    map[string]any{"language": "javascript", "timeoutSeconds": 5},
		"bundle":    map[string]any{"script": script.ID},
		"testInput": map[string]any{"count": 21},
	})
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, body)
	}
	var created store.ConfigProfile
	_ = json.Unmarshal([]byte(body), &created)
	stored, err := st.GetConfigProfile(created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if strings.TrimSpace(string(stored.TestInput)) != `{"count":21}` {
		t.Fatalf("fixture round-trip = %s", stored.TestInput)
	}

	status, body = call(t, srv, http.MethodPost, "/api/config-profiles/"+created.ID+"/test", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("test = %d: %s", status, body)
	}
	var result map[string]any
	_ = json.Unmarshal([]byte(body), &result)
	if result["ok"] != true {
		t.Fatalf("code.run test result = %#v", result)
	}
	total, _ := result["result"].(map[string]any)["total"].(float64)
	if total != 42 {
		t.Fatalf("fixture not used: %#v", result["result"])
	}

	// An update without a fixture clears it (JSON null/absent = unset).
	status, body = call(t, srv, http.MethodPut, "/api/config-profiles/"+created.ID, "test-token", map[string]any{
		"name": "shape-profile", "nodeType": "code.run",
		"config": map[string]any{"language": "javascript", "timeoutSeconds": 5},
		"bundle": map[string]any{"script": script.ID}, "version": created.Version,
	})
	if status != http.StatusOK {
		t.Fatalf("update = %d: %s", status, body)
	}
	var updated store.ConfigProfile
	_ = json.Unmarshal([]byte(body), &updated)
	if len(updated.TestInput) != 0 {
		t.Fatalf("fixture survived update: %s", updated.TestInput)
	}

	// A code.python profile's test skips on the simulated host with the
	// actionable message (the live path is exercised by the python
	// subprocess tests).
	pyScriptStatus, pyScriptBody := call(t, srv, http.MethodPost, "/api/scripts", "test-token", map[string]any{
		"name": "py-fixture-script", "language": "python", "source": "return 1",
	})
	if pyScriptStatus != http.StatusCreated {
		t.Fatalf("create py script = %d: %s", pyScriptStatus, pyScriptBody)
	}
	var pyScript store.Script
	_ = json.Unmarshal([]byte(pyScriptBody), &pyScript)
	status, body = call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "py-profile", "nodeType": "code.python",
		"config": map[string]any{"requirements": []string{"idna==3.10"}},
		"bundle": map[string]any{"script": pyScript.ID},
	})
	if status != http.StatusCreated {
		t.Fatalf("create py = %d: %s", status, body)
	}
	var pyProfile store.ConfigProfile
	_ = json.Unmarshal([]byte(body), &pyProfile)
	status, body = call(t, srv, http.MethodPost, "/api/config-profiles/"+pyProfile.ID+"/test", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("py test = %d: %s", status, body)
	}
	var pyResult map[string]any
	_ = json.Unmarshal([]byte(body), &pyResult)
	if skipped, _ := pyResult["skipped"].(string); !strings.Contains(skipped, "simulated host") {
		t.Fatalf("py test on sim = %#v", pyResult)
	}
}
