package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"loom/internal/cassette"
	"loom/internal/engine"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/pyenv"
	"loom/internal/server"
	"loom/internal/store"
)

func validScriptBody() map[string]any {
	return map[string]any{
		"name":     "Upper-case",
		"language": "javascript",
		"source":   "console.log('hi'); return { ...$json, text: String($json.text).toUpperCase() };",
		"tags":     []string{"text"},
	}
}

func TestScriptsLifecycle(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/scripts", "test-token", validScriptBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var created store.Script
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	if created.ID == "" || created.Version != 1 || created.Visibility != store.VisibilityPrivate {
		t.Fatalf("created = %#v", created)
	}

	// The language filter lists it; the other language does not.
	status, data = call(t, srv, http.MethodGet, "/api/scripts?language=javascript", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list = %d: %s", status, data)
	}
	var list struct {
		Scripts []store.Script `json:"scripts"`
		Count   int            `json:"count"`
	}
	if err := json.Unmarshal(data, &list); err != nil || list.Count != 1 {
		t.Fatalf("list decode: %v (%s)", err, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/scripts?language=python", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list python = %d: %s", status, data)
	}
	if err := json.Unmarshal(data, &list); err != nil || list.Count != 0 {
		t.Fatalf("python list decode: %v (%s)", err, data)
	}
	if status, _ = call(t, srv, http.MethodGet, "/api/scripts?language=ruby", "test-token", nil); status != http.StatusBadRequest {
		t.Fatalf("unknown language = %d, want 400", status)
	}

	status, data = call(t, srv, http.MethodGet, "/api/scripts/"+created.ID, "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get = %d: %s", status, data)
	}

	// Update with the right base version bumps to v2; a stale one 409s.
	body := validScriptBody()
	body["version"] = created.Version
	body["source"] = "return { ...$json };"
	status, data = call(t, srv, http.MethodPut, "/api/scripts/"+created.ID, "test-token", body)
	if status != http.StatusOK {
		t.Fatalf("update = %d: %s", status, data)
	}
	var updated store.Script
	if err := json.Unmarshal(data, &updated); err != nil || updated.Version != 2 {
		t.Fatalf("updated = %v (%s)", err, data)
	}
	stale := validScriptBody()
	stale["version"] = 1
	if status, _ = call(t, srv, http.MethodPut, "/api/scripts/"+created.ID, "test-token", stale); status != http.StatusConflict {
		t.Fatalf("stale update = %d, want 409", status)
	}
	// A language flip is rejected.
	flip := validScriptBody()
	flip["version"] = 2
	flip["language"] = "python"
	if status, _ = call(t, srv, http.MethodPut, "/api/scripts/"+created.ID, "test-token", flip); status != http.StatusBadRequest {
		t.Fatalf("language flip = %d, want 400", status)
	}

	// Version history is readable and append-only.
	status, data = call(t, srv, http.MethodGet, "/api/scripts/"+created.ID+"/versions", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("versions = %d: %s", status, data)
	}
	var versions struct {
		Versions []store.ScriptVersion `json:"versions"`
	}
	if err := json.Unmarshal(data, &versions); err != nil || len(versions.Versions) != 2 {
		t.Fatalf("versions decode: %v (%s)", err, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/scripts/"+created.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get version 1 = %d: %s", status, data)
	}
	var v1 store.Script
	if err := json.Unmarshal(data, &v1); err != nil || v1.Source != created.Source {
		t.Fatalf("version 1 = %v (%s)", err, data)
	}

	// Duplicate copies under a new id as a private asset.
	status, data = call(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/duplicate", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicate = %d: %s", status, data)
	}
	var dup store.Script
	if err := json.Unmarshal(data, &dup); err != nil || dup.ID == created.ID || dup.Version != 1 {
		t.Fatalf("duplicate = %v (%s)", err, data)
	}

	if status, _ = call(t, srv, http.MethodDelete, "/api/scripts/missing", "test-token", nil); status != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", status)
	}
	for _, id := range []string{created.ID, dup.ID} {
		if status, data = call(t, srv, http.MethodDelete, "/api/scripts/"+id, "test-token", nil); status != http.StatusNoContent {
			t.Fatalf("delete %s = %d: %s", id, status, data)
		}
	}
}

func TestScriptsValidation(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"unknown language", map[string]any{"name": "x", "language": "ruby", "source": "x"}, "language"},
		{"empty source", map[string]any{"name": "x", "language": "javascript", "source": "  "}, "source"},
		{"unpinned requirement", map[string]any{"name": "x", "language": "python", "source": "x", "requirements": []string{"idna"}}, "pinned"},
		{"js with requirements", map[string]any{"name": "x", "language": "javascript", "source": "x", "requirements": []string{"idna==3.10"}}, "python-script field"},
		{"js with memory", map[string]any{"name": "x", "language": "javascript", "source": "x", "maxMemoryMB": 512}, "python-script field"},
		{"js timeout too high", map[string]any{"name": "x", "language": "javascript", "source": "x", "timeoutSeconds": 900}, "timeoutSeconds"},
		{"python memory too high", map[string]any{"name": "x", "language": "python", "source": "x", "maxMemoryMB": 9000}, "maxMemoryMB"},
		{"no name", map[string]any{"language": "javascript", "source": "x"}, "name"},
	}
	for _, tc := range cases {
		if status, data := call(t, srv, http.MethodPost, "/api/scripts", "test-token", tc.body); status != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400: %s", tc.name, status, data)
		}
	}
}

func TestScriptsTestEndpoint(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/scripts", "test-token", validScriptBody())
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var created store.Script
	_ = json.Unmarshal(data, &created)

	// JavaScript runs in-process on any server: the result comes back
	// with the console log, honoring the input shaping (array → items).
	status, data = call(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/test", "test-token", map[string]any{"input": []any{"a", "b"}})
	if status != http.StatusOK {
		t.Fatalf("test = %d: %s", status, data)
	}
	var result struct {
		OK     bool                `json:"ok"`
		Result map[string]any      `json:"result"`
		Logs   []map[string]string `json:"logs"`
	}
	if err := json.Unmarshal(data, &result); err != nil || !result.OK {
		t.Fatalf("test decode: %v (%s)", err, data)
	}
	if result.Result["text"] != "UNDEFINED" {
		t.Fatalf("result = %v", result.Result)
	}
	items, _ := result.Result["items"].([]any)
	if len(items) != 2 || items[0] != "a" {
		t.Fatalf("result items = %v (array input should wrap as items)", result.Result["items"])
	}
	if len(result.Logs) != 1 || result.Logs[0]["message"] != "hi" {
		t.Fatalf("logs = %v", result.Logs)
	}

	// A runtime error reports ok:false with the message.
	broken := validScriptBody()
	broken["name"] = "Broken"
	broken["source"] = "return missing_thing;"
	status, data = call(t, srv, http.MethodPost, "/api/scripts", "test-token", broken)
	if status != http.StatusCreated {
		t.Fatalf("create broken = %d: %s", status, data)
	}
	_ = json.Unmarshal(data, &created)
	status, data = call(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/test", "test-token", map[string]any{"input": map[string]any{}})
	if status != http.StatusOK {
		t.Fatalf("test broken = %d: %s", status, data)
	}
	if err := json.Unmarshal(data, &result); err != nil || result.OK {
		t.Fatalf("broken test decode: %v (%s)", err, data)
	}

	// Python needs the subprocess executor: the simulated test server
	// answers 503 (mirrors profile testing).
	py := validScriptBody()
	py["name"] = "Py"
	py["language"] = "python"
	py["source"] = "return {'ok': True}"
	status, data = call(t, srv, http.MethodPost, "/api/scripts", "test-token", py)
	if status != http.StatusCreated {
		t.Fatalf("create py = %d: %s", status, data)
	}
	_ = json.Unmarshal(data, &created)
	if status, data = call(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/test", "test-token", map[string]any{"input": map[string]any{}}); status != http.StatusServiceUnavailable {
		t.Fatalf("python test = %d, want 503: %s", status, data)
	}
}

func TestScriptsRBACAndVisibility(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "viewer@example.com", "viewer", "viewer-password")
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	addUser(t, st, "editor2@example.com", "editor", "editor2-password")
	addUser(t, st, "admin@example.com", "admin", "admin-password")
	viewerSession := loginAs(t, srv, "viewer@example.com", "viewer-password")
	editorSession := loginAs(t, srv, "editor@example.com", "editor-password")
	editor2Session := loginAs(t, srv, "editor2@example.com", "editor2-password")
	adminSession := loginAs(t, srv, "admin@example.com", "admin-password")

	// The editor creates a private script (the default).
	status, data := editorSession.do(t, srv, http.MethodPost, "/api/scripts", validScriptBody(), true)
	if status != http.StatusCreated {
		t.Fatalf("editor create = %d: %s", status, data)
	}
	var created store.Script
	_ = json.Unmarshal(data, &created)

	// Viewers read the endpoint but cannot create.
	if status, _ := viewerSession.do(t, srv, http.MethodGet, "/api/scripts", nil, false); status != http.StatusOK {
		t.Fatalf("viewer list = %d, want 200", status)
	}
	if status, _ := viewerSession.do(t, srv, http.MethodPost, "/api/scripts", validScriptBody(), true); status != http.StatusForbidden {
		t.Fatalf("viewer create = %d, want 403", status)
	}

	// A different editor cannot see the private script: 404 on get,
	// absent from their list.
	if status, _ := editor2Session.do(t, srv, http.MethodGet, "/api/scripts/"+created.ID, nil, false); status != http.StatusNotFound {
		t.Fatalf("foreign editor get = %d, want 404", status)
	}
	status, data = editor2Session.do(t, srv, http.MethodGet, "/api/scripts", nil, false)
	if status != http.StatusOK {
		t.Fatalf("foreign editor list = %d", status)
	}
	var list struct {
		Scripts []store.Script `json:"scripts"`
		Count   int            `json:"count"`
	}
	_ = json.Unmarshal(data, &list)
	if list.Count != 0 {
		t.Fatalf("foreign editor sees %d scripts, want 0", list.Count)
	}

	// Only the creator or an admin may govern visibility.
	if status, _ := editor2Session.do(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/share", nil, true); status != http.StatusNotFound {
		t.Fatalf("foreign editor share = %d, want 404 (hidden)", status)
	}
	if status, _ := editorSession.do(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/share", nil, true); status != http.StatusOK {
		t.Fatalf("creator share = %d", status)
	}
	// Shared: the other editor sees it now.
	if status, _ := editor2Session.do(t, srv, http.MethodGet, "/api/scripts/"+created.ID, nil, false); status != http.StatusOK {
		t.Fatalf("shared get = %d, want 200", status)
	}
	if status, _ := editor2Session.do(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/unshare", nil, true); status != http.StatusForbidden {
		t.Fatalf("non-creator unshare = %d, want 403", status)
	}
	if status, _ := adminSession.do(t, srv, http.MethodPost, "/api/scripts/"+created.ID+"/unshare", nil, true); status != http.StatusOK {
		t.Fatalf("admin unshare = %d", status)
	}

	// Admins delete; editors cannot.
	if status, _ := editorSession.do(t, srv, http.MethodDelete, "/api/scripts/"+created.ID, nil, true); status != http.StatusForbidden {
		t.Fatalf("editor delete = %d, want 403", status)
	}
	if status, _ := adminSession.do(t, srv, http.MethodDelete, "/api/scripts/"+created.ID, nil, true); status != http.StatusNoContent {
		t.Fatalf("admin delete = %d, want 204", status)
	}
}

// Phase 4 publish inlining: a code node referencing a script publishes a
// self-contained snapshot (pinned source, merged requirements, script
// runtime defaults), the usage index records the pin, and the script's
// delete is guarded. A node-local source that diverges from the script
// stays authored — the escape hatch.
func TestPublishInlinesScript(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/scripts", "test-token", map[string]any{
		"name": "Shape", "language": "javascript", "source": "return { ok: true };",
		"timeoutSeconds": 120,
	})
	if status != http.StatusCreated {
		t.Fatalf("create script = %d: %s", status, data)
	}
	var script store.Script
	_ = json.Unmarshal(data, &script)

	// code.run: the test server has no python env builder, so the js
	// node exercises inlining, defaults and usage (requirements-union
	// is covered by the store resolution tests).
	wf := &graph.Workflow{
		ID: "js-script-wf", Name: "JS script", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "js", Type: "code.run", TypeVersion: 1, Name: "Run",
				Config: map[string]any{"language": "javascript", "scriptRef": script.ID}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "js", Port: "main"}},
		},
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
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
	node := snap.Nodes[1]
	if node.Config["source"] != "return { ok: true };" {
		t.Fatalf("pinned source not inlined: %#v", node.Config)
	}
	if node.Config["scriptRef"] != script.ID {
		t.Fatalf("ref not retained on the snapshot: %#v", node.Config)
	}
	if node.Config["timeoutSeconds"] != 120.0 {
		t.Fatalf("script runtime default not applied: %#v", node.Config)
	}

	usage, err := st.ScriptUsage(script.ID)
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %v, %v", usage, err)
	}
	if usage[0].WorkflowID != wf.ID || usage[0].WorkflowVersion != 1 || usage[0].ScriptVersion != 1 {
		t.Fatalf("usage row = %#v", usage[0])
	}
	if status, data = call(t, srv, http.MethodDelete, "/api/scripts/"+script.ID, "test-token", nil); status != http.StatusConflict {
		t.Fatalf("in-use delete = %d, want 409: %s", status, data)
	}

	// The escape hatch: a diverging node-local source publishes as-is.
	wf2 := &graph.Workflow{
		ID: "js-escape", Name: "JS escape", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "js", Type: "code.run", TypeVersion: 1, Name: "Run",
				Config: map[string]any{"language": "javascript", "scriptRef": script.ID, "source": "return { authored: true };"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "js", Port: "main"}},
		},
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf2); status != http.StatusCreated {
		t.Fatalf("create escape workflow = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf2.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish escape = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/workflows/"+wf2.ID+"/versions/1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("get escape version = %d: %s", status, data)
	}
	var snap2 graph.Workflow
	_ = json.Unmarshal(data, &snap2)
	if snap2.Nodes[1].Config["source"] != "return { authored: true };" {
		t.Fatalf("authored source replaced: %#v", snap2.Nodes[1].Config)
	}

	// A code node with neither source nor scriptRef fails draft save.
	wf3 := &graph.Workflow{
		ID: "js-empty", Name: "JS empty", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "js", Type: "code.run", TypeVersion: 1, Name: "Run",
				Config: map[string]any{"language": "javascript"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "js", Port: "main"}},
		},
	}
	if status, _ = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf3); status != http.StatusUnprocessableEntity {
		t.Fatalf("empty code node saved = %d, want 422", status)
	}
}

// Language is immutable: an update that omits it (the web form keeps
// the select disabled) keeps the current language instead of 400ing.
func TestScriptUpdateWithoutLanguage(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/scripts", "test-token", map[string]any{
		"name": "Nolang", "language": "python", "source": "return 1",
	})
	if status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	var script store.Script
	_ = json.Unmarshal(data, &script)

	status, data = call(t, srv, http.MethodPut, "/api/scripts/"+script.ID, "test-token", map[string]any{
		"name": "Nolang", "source": "return 2", "version": script.Version,
	})
	if status != http.StatusOK {
		t.Fatalf("update without language = %d: %s", status, data)
	}
	var updated store.Script
	_ = json.Unmarshal(data, &updated)
	if updated.Language != "python" || updated.Source != "return 2" || updated.Version != 2 {
		t.Fatalf("updated = %#v", updated)
	}
}

// Phase 5: publish builds one environment per distinct node requirement
// set (not one union env), so every node's exec-time env key is built;
// the publish response reports the built envs; the listing endpoint
// answers empty on sim.
func TestPublishBuildsPerSetPythonEnvs(t *testing.T) {
	var built [][]string
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Two python nodes with different requirement sets.
	wf := pythonWorkflow()
	wf.Nodes = append(wf.Nodes, graph.Node{
		ID: "py2", Type: "code.python", TypeVersion: 1, Name: "Run 2", Position: graph.Position{X: 520},
		Config: map[string]any{"source": "print(1)", "requirements": []any{"certifi==2025.1.1"}},
	})
	wf.Nodes[1].Config["requirements"] = []any{"idna==3.10"}
	wf.Edges = append(wf.Edges, graph.Edge{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "py2", Port: "main"}})

	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()), Token: "test-token",
		BuildEnvs: func(requirements []string) error {
			built = append(built, requirements)
			return nil
		},
		ListPythonEnvs: func() ([]pyenv.EnvInfo, error) {
			return []pyenv.EnvInfo{{
				Key: pyenv.Key([]string{"certifi==2025.1.1"}), Requirements: []string{"certifi==2025.1.1"}, SizeMB: 12.5,
			}}, nil
		},
	}))
	t.Cleanup(srv.Close)

	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}
	if len(built) != 2 {
		t.Fatalf("BuildEnvs calls = %v, want one per distinct set", built)
	}
	seen := map[string]bool{}
	for _, set := range built {
		seen[strings.Join(set, ",")] = true
	}
	if !seen["idna==3.10"] || !seen["certifi==2025.1.1"] {
		t.Fatalf("built sets = %v", built)
	}

	// The publish response reports the built environment (hash + size).
	var published struct {
		PythonEnvs []pyenv.EnvInfo `json:"pythonEnvs"`
	}
	_ = json.Unmarshal(data, &published)
	if len(published.PythonEnvs) != 1 || published.PythonEnvs[0].SizeMB != 12.5 {
		t.Fatalf("publish response envs = %#v", published.PythonEnvs)
	}

	// The listing endpoint answers enabled with the envs.
	status, data = call(t, srv, http.MethodGet, "/api/python-envs", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list envs = %d", status)
	}
	var listing struct {
		Envs    []pyenv.EnvInfo `json:"envs"`
		Enabled bool            `json:"enabled"`
	}
	_ = json.Unmarshal(data, &listing)
	if !listing.Enabled || len(listing.Envs) != 1 {
		t.Fatalf("envs listing = %#v", listing)
	}
}

// On a sim server (no ListPythonEnvs), the endpoint answers an empty
// disabled list rather than an error.
func TestPythonEnvsEmptyOnSim(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")
	status, data := call(t, srv, http.MethodGet, "/api/python-envs", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("list envs = %d: %s", status, data)
	}
	var listing struct {
		Envs    []pyenv.EnvInfo `json:"envs"`
		Enabled bool            `json:"enabled"`
	}
	_ = json.Unmarshal(data, &listing)
	if listing.Enabled || len(listing.Envs) != 0 {
		t.Fatalf("sim envs listing = %#v", listing)
	}
}

// Phase 5, D5-4: the recorded test reads the effect tape of a finished
// run through the script's usage pins — nothing executes, so it works
// on a simulated server. The recorded source must match the pinned
// version exactly.
func TestRecordedScriptTest(t *testing.T) {
	srv, st, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/scripts", "test-token", map[string]any{
		"name": "recorded-script", "language": "python", "source": "print(40 + 2)",
	})
	if status != http.StatusCreated {
		t.Fatalf("create script = %d: %s", status, data)
	}
	var script store.Script
	_ = json.Unmarshal(data, &script)

	// Publish a workflow that pins the script (usage pin recorded).
	wf := pythonWorkflow()
	wf.Nodes[1].ProfileRef = ""
	wf.Nodes[1].Config = map[string]any{"scriptRef": script.ID}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows", "test-token", wf); status != http.StatusCreated {
		t.Fatalf("create workflow = %d: %s", status, data)
	}
	if status, data = call(t, srv, http.MethodPost, "/api/workflows/"+wf.ID+"/publish", "test-token", nil); status != http.StatusCreated {
		t.Fatalf("publish = %d: %s", status, data)
	}

	// Fabricate the evidence a finished run leaves behind: a snapshot
	// with the resolved node, a taped KindExec entry in the blob store,
	// and its node.effects event.
	snapshot, err := st.GetWorkflow(wf.ID)
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	tape := cassette.Tape{RunID: "run-rec", NodeID: "py", Attempt: 0, ItemIndex: 0, Entries: []cassette.Entry{{
		Kind: cassette.KindExec, Index: 0,
		Request:  []byte(`{"source":"print(40 + 2)","stdin":null,"env":""}`),
		Response: []byte(`{"ExitCode":0,"Stdout":"42\n","Stderr":"","Result":42}`),
	}}}
	raw, err := json.Marshal(tape)
	if err != nil {
		t.Fatalf("marshal tape: %v", err)
	}
	hash, err := st.SaveBlob(raw)
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID: "run-rec", WorkflowID: wf.ID, WorkflowVersion: "1", Status: "finished",
		StartedAt: time.Now().UTC(), Snapshot: mustJSON(t, snapshot), Actor: "e2e",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := st.AppendEvent("run-rec", store.Event{
		Type: "node.effects", NodeID: "py",
		Data: map[string]any{"blob": hash, "count": 1, "itemIndex": 0},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	status, data = call(t, srv, http.MethodGet, "/api/scripts/"+script.ID+"/recorded-test", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("recorded test = %d: %s", status, data)
	}
	var recorded struct {
		Found    bool   `json:"found"`
		RunID    string `json:"runId"`
		NodeName string `json:"nodeName"`
		Stdout   string `json:"stdout"`
		Result   any    `json:"result"`
		Message  string `json:"message"`
	}
	_ = json.Unmarshal(data, &recorded)
	if !recorded.Found || recorded.RunID != "run-rec" || recorded.NodeName != "Run" || recorded.Stdout != "42\n" {
		t.Fatalf("recorded result = %#v", recorded)
	}
	if result, _ := recorded.Result.(float64); result != 42 {
		t.Fatalf("recorded result value = %#v", recorded.Result)
	}

	// A script with no recorded evidence answers found:false with the
	// explanatory message, never an error.
	freshStatus, freshData := call(t, srv, http.MethodPost, "/api/scripts", "test-token", map[string]any{
		"name": "never-run", "language": "python", "source": "print(1)",
	})
	if freshStatus != http.StatusCreated {
		t.Fatalf("create fresh = %d: %s", freshStatus, freshData)
	}
	var fresh store.Script
	_ = json.Unmarshal([]byte(freshData), &fresh)
	status, data = call(t, srv, http.MethodGet, "/api/scripts/"+fresh.ID+"/recorded-test", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("fresh recorded test = %d: %s", status, data)
	}
	_ = json.Unmarshal(data, &recorded)
	if recorded.Found || recorded.Message == "" {
		t.Fatalf("fresh recorded = %#v", recorded)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
