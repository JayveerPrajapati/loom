package server_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

func newTestServer(t *testing.T, token string) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test",
		Store:   st,
		Engine:  engine.New(st, host.NewSim()),
		Token:   token,
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

func call(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func decode(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
	return out
}

func linearWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "linear", Name: "Linear", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set",
				Config: map[string]any{"values": map[string]any{"greeting": "hi {{ $json.name }}"}},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}},
		},
	}
}

func approvalWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "approval", Name: "Approval", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{"message": "ok?"}},
			{ID: "yes", Type: "data.set", TypeVersion: 1, Name: "Yes", Config: map[string]any{"values": map[string]any{"ok": true}}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
			{From: graph.Endpoint{Node: "approve", Port: "approved"}, To: graph.Endpoint{Node: "yes", Port: "main"}},
		},
	}
}

func TestAsyncRunAndJSONEvents(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/runs?async=1", "", map[string]any{
		"input": map[string]any{"name": "loom"},
	})
	if status != http.StatusAccepted {
		t.Fatalf("async start status = %d: %s", status, data)
	}
	runID, _ := decode(t, data)["runId"].(string)
	if runID == "" {
		t.Fatalf("missing runId: %s", data)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/events?format=json", "", nil)
		if status != http.StatusOK {
			t.Fatalf("events status = %d: %s", status, data)
		}
		events, _ := decode(t, data)["events"].([]any)
		if len(events) > 0 {
			last, _ := events[len(events)-1].(map[string]any)
			if last["type"] == "run.finished" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get run status = %d: %s", status, data)
	}
	if got := decode(t, data)["status"]; got != "done" {
		t.Fatalf("run status = %v, want done", got)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/workflow", "", nil)
	if status != http.StatusOK {
		t.Fatalf("run workflow status = %d: %s", status, data)
	}
	if got := decode(t, data)["id"]; got != "linear" {
		t.Fatalf("run workflow id = %v, want linear", got)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/events?format=json", "", nil)
	if status != http.StatusOK {
		t.Fatalf("events status = %d: %s", status, data)
	}
	events, _ := decode(t, data)["events"].([]any)
	var outputHash string
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		if event["type"] == "node.finished" && event["outputHash"] != nil {
			outputHash, _ = event["outputHash"].(string)
			break
		}
	}
	if outputHash == "" {
		t.Fatalf("no node output hash in events: %s", data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/blobs/"+outputHash, "", nil)
	if status != http.StatusOK {
		t.Fatalf("blob status = %d: %s", status, data)
	}
	if !json.Valid(data) {
		t.Fatalf("blob is not json: %s", data)
	}
	status, _ = call(t, srv, http.MethodGet, "/api/blobs/not-a-hash", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("invalid blob status = %d, want 404", status)
	}
}

func TestReplayEndpoints(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/runs", "", map[string]any{
		"input": map[string]any{"name": "loom"},
	})
	if status != http.StatusOK {
		t.Fatalf("run status = %d: %s", status, data)
	}
	runID, _ := decode(t, data)["runId"].(string)
	if runID == "" {
		t.Fatalf("missing runId: %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/replay", "", map[string]any{
		"mode": "step", "nodeId": "set",
	})
	if status != http.StatusOK {
		t.Fatalf("step replay status = %d: %s", status, data)
	}
	step := decode(t, data)
	if step["mode"] != "step" || step["match"] != true {
		t.Fatalf("step replay = %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/replay", "", map[string]any{
		"mode": "from", "nodeId": "set",
	})
	if status != http.StatusOK {
		t.Fatalf("from replay status = %d: %s", status, data)
	}
	from := decode(t, data)
	if from["mode"] != "from" || from["status"] != "done" {
		t.Fatalf("from replay = %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/runs", "", map[string]any{
		"input": map[string]any{"name": "loom"},
	})
	if status != http.StatusOK {
		t.Fatalf("second run status = %d: %s", status, data)
	}
	secondID, _ := decode(t, data)["runId"].(string)
	if secondID == "" {
		t.Fatalf("missing second runId: %s", data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/diff?against="+secondID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("diff status = %d: %s", status, data)
	}
	diff := decode(t, data)
	nodes, _ := diff["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("diff nodes = %d, want 2: %s", len(nodes), data)
	}
	for _, raw := range nodes {
		node, _ := raw.(map[string]any)
		if node["outputMatch"] != true {
			t.Fatalf("expected matching outputs: %s", data)
		}
	}
	status, _ = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/diff", "", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("diff without against status = %d, want 400", status)
	}

	status, _ = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/replay", "", map[string]any{"mode": "step"})
	if status != http.StatusBadRequest {
		t.Fatalf("missing node status = %d, want 400", status)
	}
	status, _ = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/replay", "", map[string]any{"mode": "sideways"})
	if status != http.StatusBadRequest {
		t.Fatalf("bad mode status = %d, want 400", status)
	}
	status, _ = call(t, srv, http.MethodPost, "/api/runs/missing/replay", "", map[string]any{"mode": "full"})
	if status != http.StatusNotFound {
		t.Fatalf("missing run status = %d, want 404", status)
	}
}

func TestEditorMetadataAndShell(t *testing.T) {
	srv, _ := newTestServer(t, "secret")

	status, data := call(t, srv, http.MethodGet, "/api/node-types", "secret", nil)
	if status != http.StatusOK {
		t.Fatalf("node-types status = %d: %s", status, data)
	}
	nodeTypes, _ := decode(t, data)["nodeTypes"].([]any)
	if len(nodeTypes) < 10 {
		t.Fatalf("node-types = %d, want at least 10", len(nodeTypes))
	}

	status, data = call(t, srv, http.MethodGet, "/api/examples", "secret", nil)
	if status != http.StatusOK {
		t.Fatalf("examples status = %d: %s", status, data)
	}
	examples, _ := decode(t, data)["examples"].([]any)
	if len(examples) != 5 {
		t.Fatalf("examples = %d, want 5", len(examples))
	}

	status, data = call(t, srv, http.MethodGet, "/", "", nil)
	if status != http.StatusOK {
		t.Fatalf("ui status = %d", status)
	}
	if !strings.Contains(string(data), "Loom") || !strings.Contains(string(data), "root") {
		t.Fatalf("ui shell missing: %s", data)
	}

	status, _ = call(t, srv, http.MethodGet, "/workflows/example-id", "", nil)
	if status != http.StatusOK {
		t.Fatalf("spa fallback status = %d", status)
	}
	status, _ = call(t, srv, http.MethodGet, "/api/does-not-exist", "secret", nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown api status = %d, want 404", status)
	}
	status, _ = call(t, srv, http.MethodGet, "/api/node-types", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthorized node-types status = %d", status)
	}
}

func TestWorkflowRunbook(t *testing.T) {
	srv, _ := newTestServer(t, "")
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow()); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, data)
	}
	status, data := call(t, srv, http.MethodGet, "/api/workflows/linear/runbook", "", nil)
	if status != http.StatusOK {
		t.Fatalf("runbook: %d %s", status, data)
	}
	text := string(data)
	for _, want := range []string{"# Linear", "## Nodes", "`start.main` → `set.main`", "loom replay"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runbook missing %q:\n%s", want, text)
		}
	}
	status, _ = call(t, srv, http.MethodGet, "/api/workflows/missing/runbook", "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("missing runbook status = %d", status)
	}
}

func TestWorkflowLifecycle(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/workflows", "", nil)
	if status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	if got := decode(t, data)["count"]; got != float64(1) {
		t.Fatalf("count = %v, want 1", got)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/runs", "",
		map[string]any{"input": map[string]any{"name": "loom"}})
	if status != http.StatusOK {
		t.Fatalf("run status = %d: %s", status, data)
	}
	run := decode(t, data)
	if run["status"] != "done" {
		t.Fatalf("run status = %v", run["status"])
	}
	runID := run["runId"].(string)

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get run status = %d", status)
	}
	if got := decode(t, data)["status"]; got != "done" {
		t.Fatalf("stored status = %v", got)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/events", "", nil)
	if status != http.StatusOK {
		t.Fatalf("events status = %d", status)
	}
	if !strings.Contains(string(data), "run.finished") {
		t.Fatalf("events missing run.finished: %s", data)
	}

	status, _ = call(t, srv, http.MethodDelete, "/api/workflows/linear", "", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
}

func TestApprovalOverHTTP(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", approvalWorkflow())
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/approval/runs", "", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("run status = %d: %s", status, data)
	}
	run := decode(t, data)
	if run["status"] != "waiting" {
		t.Fatalf("run status = %v, want waiting", run["status"])
	}

	runID := run["runId"].(string)
	status, data = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/resume", "", map[string]any{"decision": "approved"})
	if status != http.StatusOK {
		t.Fatalf("resume status = %d: %s", status, data)
	}
	if got := decode(t, data)["status"]; got != "done" {
		t.Fatalf("resumed status = %v", got)
	}
}

func TestSubflowChildRuns(t *testing.T) {
	srv, _ := newTestServer(t, "")
	child := linearWorkflow()
	child.ID = "child"
	child.Name = "Child"
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", child); status != http.StatusCreated {
		t.Fatalf("create child: %d %s", status, data)
	}
	parent := &graph.Workflow{
		ID: "parent", Name: "Parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "sub", Type: "flow.subflow", TypeVersion: 1, Name: "Child", Config: map[string]any{"workflowId": "child", "wait": true}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "sub", Port: "main"}},
		},
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", parent); status != http.StatusCreated {
		t.Fatalf("create parent: %d %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows/parent/runs", "", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("run parent: %d %s", status, data)
	}
	parentRun := decode(t, data)
	parentID, _ := parentRun["runId"].(string)
	if parentID == "" {
		t.Fatalf("parent run id missing: %s", data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs?parent="+parentID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("list children: %d %s", status, data)
	}
	result := decode(t, data)
	runs, _ := result["runs"].([]any)
	if len(runs) != 1 {
		t.Fatalf("child runs = %s", data)
	}
	childRun := runs[0].(map[string]any)
	if childRun["parentRunId"] != parentID || childRun["parentNodeId"] != "sub" {
		t.Fatalf("child parent = %#v", childRun)
	}
}

func TestApprovalCallback(t *testing.T) {
	srv, _ := newTestServer(t, "")
	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", approvalWorkflow()); status != http.StatusCreated {
		t.Fatalf("create: %d %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows/approval/runs", "", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("run: %d %s", status, data)
	}
	result := decode(t, data)
	token, _ := result["resumeToken"].(string)
	if token == "" {
		t.Fatalf("missing resume token: %s", data)
	}
	path := "/hook/approval/" + token

	status, data = call(t, srv, http.MethodGet, path, "", nil)
	if status != http.StatusOK || !strings.Contains(string(data), "Approve this run?") {
		t.Fatalf("approval page: %d %s", status, data)
	}

	req, err := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader("decision=approved"))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post approval: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "approved") {
		t.Fatalf("approval response: %d %s", resp.StatusCode, body)
	}

	runID, _ := result["runId"].(string)
	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("get run: %d %s", status, data)
	}
	if got := decode(t, data)["status"]; got != "done" {
		t.Fatalf("run status = %v, want done", got)
	}
}

func TestSlackApprovalCallback(t *testing.T) {
	srv, _ := newTestServer(t, "")
	call(t, srv, http.MethodPost, "/api/workflows", "", approvalWorkflow())
	status, data := call(t, srv, http.MethodPost, "/api/workflows/approval/runs", "", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("run: %d %s", status, data)
	}
	token, _ := decode(t, data)["resumeToken"].(string)
	payload := `{"actions":[{"action_id":"approve","value":"approved"}]}`
	form := "payload=" + url.QueryEscape(payload)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/hook/approval/"+token, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post slack callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("slack callback status = %d", resp.StatusCode)
	}
}

func TestAuth(t *testing.T) {
	srv, _ := newTestServer(t, "secret")

	status, _ := call(t, srv, http.MethodGet, "/healthz", "", nil)
	if status != http.StatusOK {
		t.Fatalf("healthz status = %d", status)
	}
	status, _ = call(t, srv, http.MethodGet, "/readyz", "", nil)
	if status != http.StatusOK {
		t.Fatalf("readyz status = %d", status)
	}

	status, _ = call(t, srv, http.MethodGet, "/api/workflows", "", nil)
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", status)
	}

	status, _ = call(t, srv, http.MethodGet, "/api/workflows", "secret", nil)
	if status != http.StatusOK {
		t.Fatalf("authenticated status = %d", status)
	}
}

func TestWebhook(t *testing.T) {
	srv, _ := newTestServer(t, "")

	wf := linearWorkflow()
	wf.ID = "hook"
	wf.Nodes[0] = graph.Node{
		ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
		Config: map[string]any{"path": "orders", "method": "POST"},
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/hook/orders", "", map[string]any{"name": "loom"})
	if status != http.StatusOK {
		t.Fatalf("hook status = %d: %s", status, data)
	}
	if got := decode(t, data)["status"]; got != "done" {
		t.Fatalf("hook run status = %v", got)
	}

	status, _ = call(t, srv, http.MethodGet, "/hook/orders", "", nil)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("wrong method status = %d, want 405", status)
	}
}

func TestWebhookRejectsOversizedBody(t *testing.T) {
	srv, _ := newTestServer(t, "")

	wf := linearWorkflow()
	wf.ID = "hook-oversize"
	wf.Nodes[0] = graph.Node{
		ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
		Config: map[string]any{"path": "oversize", "method": "POST"},
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/hook/oversize", "", strings.Repeat("x", (1<<20)+1))
	if status != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized hook status = %d: %s", status, data)
	}
}

func TestConcurrentWebhooks(t *testing.T) {
	srv, st := newTestServer(t, "")

	wf := linearWorkflow()
	wf.ID = "bulk"
	wf.Nodes[0] = graph.Node{
		ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
		Config: map[string]any{"path": "bulk", "method": "POST"},
	}
	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	const n = 100
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			raw, err := json.Marshal(map[string]any{"name": fmt.Sprintf("n%d", i)})
			if err != nil {
				errs <- err
				return
			}
			resp, err := http.Post(srv.URL+"/hook/bulk", "application/json", bytes.NewReader(raw))
			if err != nil {
				errs <- err
				return
			}
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("webhook %d status %d: %s", i, resp.StatusCode, body)
				return
			}
			var out map[string]any
			if err := json.Unmarshal(body, &out); err != nil {
				errs <- fmt.Errorf("webhook %d decode: %w", i, err)
				return
			}
			if out["status"] != "done" {
				errs <- fmt.Errorf("webhook %d run status = %v", i, out["status"])
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	runs, err := st.ListRuns("bulk", n+10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != n {
		t.Fatalf("stored %d runs, want %d", len(runs), n)
	}
	for _, run := range runs {
		events, err := st.ListEvents(run.ID)
		if err != nil {
			t.Fatalf("events for %s: %v", run.ID, err)
		}
		if len(events) != 6 {
			t.Fatalf("run %s has %d events, want 6", run.ID, len(events))
		}
		for i, ev := range events {
			if ev.Seq != int64(i+1) {
				t.Fatalf("run %s event %d seq = %d", run.ID, i, ev.Seq)
			}
		}
	}
}

func TestPublishVersionsAndPinnedJobRun(t *testing.T) {
	srv, st := newTestServer(t, "")

	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow()); status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("publish v1 status = %d: %s", status, data)
	}
	if got := int(decode(t, data)["version"].(map[string]any)["version"].(float64)); got != 1 {
		t.Fatalf("published version = %d, want 1", got)
	}

	status, data = call(t, srv, http.MethodGet, "/api/workflows/linear/state", "", nil)
	if status != http.StatusOK {
		t.Fatalf("state status = %d: %s", status, data)
	}
	state := decode(t, data)
	if int(state["publishedVersion"].(float64)) != 1 || state["draft"].(bool) {
		t.Fatalf("unexpected fresh state: %s", data)
	}

	second := linearWorkflow()
	second.Name = "Linear v2"
	second.Nodes[1].Config = map[string]any{"values": map[string]any{"greeting": "v2 {{ $json.name }}"}}
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/linear", "", second); status != http.StatusOK {
		t.Fatalf("save draft status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/workflows/linear/state", "", nil)
	if status != http.StatusOK {
		t.Fatalf("state status = %d: %s", status, data)
	}
	state = decode(t, data)
	if !state["draft"].(bool) {
		t.Fatalf("expected draft after edit: %s", data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish v2 status = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/jobs", "", map[string]any{
		"name": "Pinned v1", "templateId": "linear", "templateVersion": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("create job status = %d: %s", status, data)
	}
	job := decode(t, data)
	jobID := job["id"].(string)
	if int(job["templateVersion"].(float64)) != 1 {
		t.Fatalf("job pinned to wrong version: %s", data)
	}

	third := linearWorkflow()
	third.Name = "Linear draft"
	third.Nodes[1].Config = map[string]any{"values": map[string]any{"greeting": "draft {{ $json.name }}"}}
	if status, data := call(t, srv, http.MethodPut, "/api/workflows/linear", "", third); status != http.StatusOK {
		t.Fatalf("save draft status = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/run", "", map[string]any{
		"input": map[string]any{"name": "loom"},
	})
	if status != http.StatusOK {
		t.Fatalf("run job status = %d: %s", status, data)
	}
	runID := decode(t, data)["runId"].(string)
	run, err := st.GetRun(runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.JobID != jobID || run.TemplateVersion != 1 {
		t.Fatalf("run pin = job %q version %d, want job %q version 1", run.JobID, run.TemplateVersion, jobID)
	}
	if len(run.Snapshot) == 0 {
		t.Fatal("run has no snapshot")
	}
	var snapshot graph.Workflow
	if err := json.Unmarshal(run.Snapshot, &snapshot); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if snapshot.Name != "Linear" {
		t.Fatalf("run snapshot = %q, want pinned v1 name", snapshot.Name)
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+runID+"/replay", "", map[string]any{
		"mode": "step", "nodeId": "set",
	})
	if status != http.StatusOK {
		t.Fatalf("replay status = %d: %s", status, data)
	}
	if match, _ := decode(t, data)["match"].(bool); !match {
		t.Fatalf("step replay did not use pinned snapshot: %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/enabled", "", map[string]any{"enabled": false})
	if status != http.StatusOK {
		t.Fatalf("pause job status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/run", "", map[string]any{})
	if status != http.StatusConflict {
		t.Fatalf("paused job run status = %d, want 409: %s", status, data)
	}
}

func TestMigrateCronWorkflowsIsIdempotent(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	wf := &graph.Workflow{
		ID: "legacy", Name: "Legacy digest",
		Nodes: []graph.Node{{
			ID: "schedule", Type: "trigger.cron", TypeVersion: 1, Name: "Schedule",
			Config: map[string]any{"schedule": "0 9 * * *"},
		}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	n, err := server.MigrateCronWorkflows(st)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("migrated = %d, want 1", n)
	}
	jobs, err := st.ListJobs()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].TriggerType != "cron" || jobs[0].TemplateVersion != 1 {
		t.Fatalf("migrated jobs = %+v", jobs)
	}
	if n, err := server.MigrateCronWorkflows(st); err != nil || n != 0 {
		t.Fatalf("second migration = %d, %v", n, err)
	}
	jobs, _ = st.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("migration duplicated jobs: %+v", jobs)
	}
}

func TestCredentialsDataContextAndPreview(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodPost, "/api/credentials", "", map[string]any{
		"id": "cred_http", "type": "http", "data": map[string]any{"token": "secret"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create credential status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/credentials", "", nil)
	if status != http.StatusOK {
		t.Fatalf("list credentials status = %d: %s", status, data)
	}
	credentials, _ := decode(t, data)["credentials"].([]any)
	if len(credentials) != 1 {
		t.Fatalf("credentials = %v", credentials)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow())
	if status != http.StatusCreated {
		t.Fatalf("create workflow status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/runs?async=1", "", map[string]any{
		"input": map[string]any{"name": "loom"},
	})
	if status != http.StatusAccepted {
		t.Fatalf("start run status = %d: %s", status, data)
	}
	runID, _ := decode(t, data)["runId"].(string)
	deadline := time.Now().Add(3 * time.Second)
	for {
		status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
		if status == http.StatusOK && decode(t, data)["status"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %s", data)
		}
		time.Sleep(20 * time.Millisecond)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+runID+"/data-context?nodeId=set", "", nil)
	if status != http.StatusOK {
		t.Fatalf("data context status = %d: %s", status, data)
	}
	ctx := decode(t, data)
	upstream, _ := ctx["upstream"].([]any)
	found := false
	for _, raw := range upstream {
		if raw == "start" {
			found = true
		}
	}
	if !found {
		t.Fatalf("upstream = %v, want start", upstream)
	}

	status, data = call(t, srv, http.MethodPost, "/api/workflows/linear/preview", "", map[string]any{
		"runId": runID, "nodeId": "set", "expression": `{{ $node["start"].json.name }}`,
	})
	if status != http.StatusOK {
		t.Fatalf("preview status = %d: %s", status, data)
	}
	if value := decode(t, data)["value"]; value != "loom" {
		t.Fatalf("preview value = %v, want loom", value)
	}

	status, _ = call(t, srv, http.MethodDelete, "/api/credentials/cred_http", "", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete credential status = %d", status)
	}
	status, data = call(t, srv, http.MethodGet, "/api/credentials", "", nil)
	if status != http.StatusOK || decode(t, data)["count"] != float64(0) {
		t.Fatalf("credentials after delete = %d: %s", status, data)
	}
}

func TestJobUpdateDuplicateAndRunFilter(t *testing.T) {
	srv, _ := newTestServer(t, "")

	if status, data := call(t, srv, http.MethodPost, "/api/workflows", "", linearWorkflow()); status != http.StatusCreated {
		t.Fatalf("create workflow status = %d: %s", status, data)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/workflows/linear/publish", "", nil); status != http.StatusCreated {
		t.Fatalf("publish status = %d: %s", status, data)
	}
	status, data := call(t, srv, http.MethodPost, "/api/jobs", "", map[string]any{
		"name": "Original", "templateId": "linear", "templateVersion": 1,
	})
	if status != http.StatusCreated {
		t.Fatalf("create job status = %d: %s", status, data)
	}
	jobID := decode(t, data)["id"].(string)

	status, data = call(t, srv, http.MethodGet, "/api/workflows", "", nil)
	if status != http.StatusOK {
		t.Fatalf("list workflows status = %d", status)
	}
	workflows := decode(t, data)["workflows"].([]any)
	if len(workflows) != 1 {
		t.Fatalf("workflows = %s", data)
	}
	summary := workflows[0].(map[string]any)
	if summary["jobCount"] != float64(1) || summary["activeJobCount"] != float64(1) {
		t.Fatalf("workflow job counts = %s", data)
	}

	status, data = call(t, srv, http.MethodPut, "/api/jobs/"+jobID, "", map[string]any{
		"name": "Renamed", "triggerType": "manual",
		"inputDefaults": map[string]any{"name": "loom"},
	})
	if status != http.StatusOK {
		t.Fatalf("update job status = %d: %s", status, data)
	}
	updated := decode(t, data)
	if updated["name"] != "Renamed" || updated["inputDefaults"].(map[string]any)["name"] != "loom" {
		t.Fatalf("job update not applied: %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/duplicate", "", nil)
	if status != http.StatusCreated {
		t.Fatalf("duplicate status = %d: %s", status, data)
	}
	duplicate := decode(t, data)
	duplicateID := duplicate["id"].(string)
	if duplicate["name"] != "Renamed copy" || duplicate["enabled"].(bool) {
		t.Fatalf("duplicate should be paused copy: %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/jobs/"+jobID+"/run", "", map[string]any{})
	if status != http.StatusOK {
		t.Fatalf("run job status = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodGet, "/api/runs?job="+jobID, "", nil)
	if status != http.StatusOK {
		t.Fatalf("filtered runs status = %d: %s", status, data)
	}
	runs := decode(t, data)["runs"].([]any)
	if len(runs) != 1 || runs[0].(map[string]any)["jobId"] != jobID {
		t.Fatalf("filtered runs = %s", data)
	}

	status, data = call(t, srv, http.MethodPut, "/api/jobs/"+jobID, "", map[string]any{
		"triggerType":   "cron",
		"triggerConfig": map[string]any{"schedule": "0 9 * * *", "timezone": "UTC"},
	})
	if status != http.StatusOK {
		t.Fatalf("cron update status = %d: %s", status, data)
	}
	scheduled := decode(t, data)
	if scheduled["triggerType"] != "cron" || scheduled["nextRunAt"] == nil {
		t.Fatalf("cron job missing next run: %s", data)
	}
	status, data = call(t, srv, http.MethodPut, "/api/jobs/"+jobID, "", map[string]any{
		"triggerType":   "cron",
		"triggerConfig": map[string]any{"schedule": "not a cron"},
	})
	if status != http.StatusBadRequest {
		t.Fatalf("invalid cron update status = %d, want 400: %s", status, data)
	}
	status, _ = call(t, srv, http.MethodDelete, "/api/jobs/"+duplicateID, "", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete duplicate status = %d", status)
	}
	status, _ = call(t, srv, http.MethodGet, "/api/jobs/"+duplicateID, "", nil)
	if status != http.StatusNotFound {
		t.Fatalf("deleted job status = %d, want 404", status)
	}
	status, data = call(t, srv, http.MethodDelete, "/api/workflows/linear", "", nil)
	if status != http.StatusConflict {
		t.Fatalf("delete template with jobs status = %d, want 409: %s", status, data)
	}
	status, _ = call(t, srv, http.MethodDelete, "/api/jobs/"+jobID, "", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete job status = %d", status)
	}
	status, _ = call(t, srv, http.MethodDelete, "/api/workflows/linear", "", nil)
	if status != http.StatusNoContent {
		t.Fatalf("delete template status = %d", status)
	}
}

func TestPreviewSchedule(t *testing.T) {
	srv, _ := newTestServer(t, "")

	status, data := call(t, srv, http.MethodPost, "/api/schedules/preview", "", map[string]any{
		"triggerType":   "cron",
		"triggerConfig": map[string]any{"schedule": "0 9 * * *", "timezone": "UTC"},
		"count":         2,
	})
	if status != http.StatusOK {
		t.Fatalf("preview status = %d: %s", status, data)
	}
	preview := decode(t, data)
	if preview["valid"] != true {
		t.Fatalf("preview should be valid: %s", data)
	}
	next, ok := preview["next"].([]any)
	if !ok || len(next) != 2 {
		t.Fatalf("preview next = %s", data)
	}
	first, err := time.Parse(time.RFC3339, fmt.Sprint(next[0]))
	if err != nil {
		t.Fatalf("parse first preview time: %v", err)
	}
	second, err := time.Parse(time.RFC3339, fmt.Sprint(next[1]))
	if err != nil {
		t.Fatalf("parse second preview time: %v", err)
	}
	if !second.After(first) {
		t.Fatalf("preview times not increasing: %s", data)
	}

	status, data = call(t, srv, http.MethodPost, "/api/schedules/preview", "", map[string]any{
		"triggerType":   "cron",
		"triggerConfig": map[string]any{"schedule": "not a cron"},
	})
	if status != http.StatusOK {
		t.Fatalf("invalid preview status = %d: %s", status, data)
	}
	invalid := decode(t, data)
	if invalid["valid"] != false || invalid["error"] == "" {
		t.Fatalf("invalid preview = %s", data)
	}
}
