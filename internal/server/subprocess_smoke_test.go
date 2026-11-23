package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"loom/internal/cassette"
	"loom/internal/engine"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

// codePythonWorkflow is a minimal no-dependency code.python workflow.
const codePythonWorkflow = `{
  "id": "py-server-test", "name": "Py server test", "version": 1,
  "settings": {},
  "nodes": [
    { "id": "start", "type": "trigger.manual", "typeVersion": 1, "name": "Start",
      "position": { "x": 0, "y": 0 }, "config": { "fields": [] } },
    { "id": "py", "type": "code.python", "typeVersion": 1, "name": "Compute",
      "position": { "x": 220, "y": 0 }, "config": { "source": "return 2 + 5" } }
  ],
  "edges": [
    { "from": { "node": "start", "port": "main" }, "to": { "node": "py", "port": "main" } }
  ]
}`

// newSubprocessServer builds a Handler whose engine runs behind the real
// event-bus wiring (wireEventBus wraps the host) and, when record is set,
// records external effects exactly like serve's default configuration.
func newSubprocessServer(t *testing.T, record bool) (*httptest.Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	liveHost := host.NewLive()
	liveHost.EnableSubprocess = true
	liveHost.EnvsRoot = filepath.Join(dir, "envs")
	eng := engine.New(st, host.NewBlobHost(liveHost, st))
	if record {
		eng.DefaultEffects = cassette.ModeRecord
	}
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: eng,
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

// runCodePythonWorkflow drives the full API path: login, create, publish,
// run, and returns the run status and python node output.
func runCodePythonWorkflow(t *testing.T, srv *httptest.Server, st *store.Store) (string, map[string]any) {
	t.Helper()
	addUser(t, st, "root@example.com", "admin", "root-password-1")
	admin := loginAs(t, srv, "root@example.com", "root-password-1")

	status, body := admin.do(t, srv, http.MethodPost, "/api/workflows", json.RawMessage(codePythonWorkflow), true)
	if status != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", status, body)
	}
	status, body = admin.do(t, srv, http.MethodPost, "/api/workflows/py-server-test/publish", nil, true)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("publish workflow: %d %s", status, body)
	}
	status, body = admin.do(t, srv, http.MethodPost, "/api/workflows/py-server-test/runs", map[string]any{}, true)
	if status != http.StatusOK {
		t.Fatalf("run workflow: %d %s", status, body)
	}
	var res struct {
		Status  string `json:"status"`
		Error   string `json:"error"`
		Outputs map[string]struct {
			Main map[string]any `json:"main"`
		} `json:"outputs"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode run: %v: %s", err, body)
	}
	return res.Status, res.Outputs["py"].Main
}

// TestServerCodePythonRunLive covers the unwrapped path: the host the
// engine exposes through the API still executes subprocesses after the
// event-bus wrapper (publishHost) is applied.
func TestServerCodePythonRunLive(t *testing.T) {
	srv, st := newSubprocessServer(t, false)
	status, out := runCodePythonWorkflow(t, srv, st)
	if status != "done" {
		t.Fatalf("run status = %s (want done)", status)
	}
	if got, _ := out["value"].(float64); got != 7 {
		t.Fatalf("python output = %v, want value 7", out)
	}
}

// TestServerCodePythonRunRecorded covers serve's default configuration
// (--record-effects): the cassette recorder wraps the event-bus host, and
// the subprocess capability must survive both layers. This is the exact
// path that regressed when publishHost dropped flow.Executor.
func TestServerCodePythonRunRecorded(t *testing.T) {
	srv, st := newSubprocessServer(t, true)
	status, out := runCodePythonWorkflow(t, srv, st)
	if status != "done" {
		t.Fatalf("run status = %s (want done)", status)
	}
	if got, _ := out["value"].(float64); got != 7 {
		t.Fatalf("python output = %v, want value 7", out)
	}
}
