package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return engine.New(st, host.NewSim())
}

func linearWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "linear", Name: "Linear", Version: 1,
		Nodes: []graph.Node{
			{
				ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start",
				Config: map[string]any{},
			},
			{
				ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set",
				Config: map[string]any{
					"values": map[string]any{"greeting": "hi {{ $json.name }}"},
				},
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
			{
				ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start",
				Config: map[string]any{},
			},
			{
				ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve",
				Config: map[string]any{"message": "ship it?"},
			},
			{
				ID: "yes", Type: "data.set", TypeVersion: 1, Name: "Yes",
				Config: map[string]any{"values": map[string]any{"ok": true}},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
			{From: graph.Endpoint{Node: "approve", Port: "approved"}, To: graph.Endpoint{Node: "yes", Port: "main"}},
		},
	}
}

func TestRunAndReplay(t *testing.T) {
	eng := newEngine(t)
	ctx := context.Background()

	res, err := eng.Start(ctx, linearWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{"name": "loom"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	if got := res.Outputs["set"]["main"]["greeting"]; got != "hi loom" {
		t.Fatalf("greeting = %v, want hi loom", got)
	}
	if len(res.Events) < 5 {
		t.Fatalf("got %d events, want at least 5", len(res.Events))
	}

	full, err := eng.Replay(ctx, res.RunID, "", engine.ReplayFull)
	if err != nil {
		t.Fatalf("full replay: %v", err)
	}
	if !full.Match || full.Status != "done" {
		t.Fatalf("full replay match=%v status=%q", full.Match, full.Status)
	}

	step, err := eng.Replay(ctx, res.RunID, "set", engine.ReplayStep)
	if err != nil {
		t.Fatalf("step replay: %v", err)
	}
	if !step.Match {
		t.Fatalf("step replay mismatch: recorded=%s actual=%s", step.RecordedHash, step.ActualHash)
	}

	from, err := eng.Replay(ctx, res.RunID, "set", engine.ReplayFrom)
	if err != nil {
		t.Fatalf("from replay: %v", err)
	}
	if from.Status != "done" {
		t.Fatalf("from replay status = %q", from.Status)
	}
}

type flakyHTTPHost struct {
	*host.Sim
	calls int
}

func (h *flakyHTTPHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.calls++
	if h.calls == 1 {
		return flow.HTTPResponse{}, errors.New("temporary upstream failure")
	}
	return h.Sim.HTTP(ctx, req)
}

func TestRetryFromFailedNode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &flakyHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.MaxAttempts = 1

	wf := &graph.Workflow{
		ID: "retry", Name: "Retry", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/orders/A-1",
			}, Credentials: map[string]string{"http": "cred_http"}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"retried": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
			{From: graph.Endpoint{Node: "call", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}

	retried, err := eng.Replay(context.Background(), res.RunID, "call", engine.ReplayFrom)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Status != "done" {
		t.Fatalf("retry status = %q (match=%v)", retried.Status, retried.Match)
	}
	if got := retried.Outputs["done"]["main"]["retried"]; got != true {
		t.Fatalf("downstream output = %v, want true", got)
	}
}

func TestNodeRetryPolicyOverridesEngineDefault(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &flakyHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.MaxAttempts = 1

	wf := &graph.Workflow{
		ID: "node-retry", Name: "Node retry", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/orders/A-1",
			}, Credentials: map[string]string{"http": "cred_http"},
				Retry: &graph.RetryPolicy{MaxAttempts: 2, BackoffMs: 1},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	if h.calls != 2 {
		t.Fatalf("http calls = %d, want 2 (one failure plus retry)", h.calls)
	}
}

type timingHTTPHost struct {
	*host.Sim
	mu      sync.Mutex
	started map[string]time.Time
	ended   map[string]time.Time
}

func (h *timingHTTPHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.mu.Lock()
	if h.started == nil {
		h.started = map[string]time.Time{}
		h.ended = map[string]time.Time{}
	}
	h.started[req.URL] = time.Now()
	h.mu.Unlock()

	if strings.Contains(req.URL, "/slow") {
		time.Sleep(750 * time.Millisecond)
	}

	h.mu.Lock()
	h.ended[req.URL] = time.Now()
	h.mu.Unlock()
	return flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"ok": true},
	}, nil
}

func TestReadySetStartsDownstreamWithoutLevelBarrier(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &timingHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)

	httpNode := func(id, url string) graph.Node {
		return graph.Node{ID: id, Type: "http.request", TypeVersion: 1, Name: id,
			Config:      map[string]any{"method": "GET", "url": url},
			Credentials: map[string]string{"http": "cred_http"}}
	}
	wf := &graph.Workflow{
		ID: "ready-set", Name: "Ready set", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			httpNode("slow", "https://example.com/slow"),
			httpNode("fast", "https://example.com/fast"),
			httpNode("after", "https://example.com/after"),
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "slow", Port: "main"}},
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "fast", Port: "main"}},
			{From: graph.Endpoint{Node: "fast", Port: "main"}, To: graph.Endpoint{Node: "after", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	afterStart, ok := h.started["https://example.com/after"]
	if !ok {
		t.Fatal("after node never started")
	}
	slowEnd := h.ended["https://example.com/slow"]
	if slowEnd.IsZero() {
		t.Fatal("slow node never finished")
	}
	if !afterStart.Before(slowEnd) {
		t.Fatalf("after started at %v, slow ended at %v; downstream waited for the level", afterStart, slowEnd)
	}
}

type batchHTTPHost struct {
	*host.Sim
	calls int
}

func (h *batchHTTPHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.calls++
	if strings.Contains(req.URL, "/bad") {
		return flow.HTTPResponse{}, errors.New("bad item")
	}
	return flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    flow.Item{"url": req.URL},
	}, nil
}

func TestBatchRunsPerItemAndRecordsItemFailures(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &batchHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.MaxAttempts = 1

	wf := &graph.Workflow{
		ID: "batch", Name: "Batch", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call",
				Config:      map[string]any{"method": "GET", "url": "https://example.com{{ $json.path }}"},
				Credentials: map[string]string{"http": "cred_http"},
				Batch:       &graph.BatchPolicy{ItemsPath: "items", ContinueOnItemError: true}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
	input := flow.Item{"items": []any{
		map[string]any{"path": "/one"},
		map[string]any{"path": "/bad"},
		map[string]any{"path": "/three"},
	}}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: input})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	if h.calls != 3 {
		t.Fatalf("http calls = %d, want 3", h.calls)
	}
	main := res.Outputs["call"]["main"]
	if got := main["count"]; got != 3 {
		t.Fatalf("count = %#v, want 3", got)
	}
	if got := main["failed"]; got != 1 {
		t.Fatalf("failed = %#v, want 1", got)
	}
	items, ok := main["items"].([]any)
	if !ok || len(items) != 3 {
		t.Fatalf("items = %#v", main["items"])
	}

	events, err := st.ListEvents(res.RunID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var itemFailed, itemFinished int
	for _, event := range events {
		switch event.Type {
		case "item.failed":
			itemFailed++
		case "item.finished":
			itemFinished++
		}
	}
	if itemFailed != 1 || itemFinished != 2 {
		t.Fatalf("item events: failed=%d finished=%d, want 1/2", itemFailed, itemFinished)
	}
}

func TestBatchStopsOnItemFailureByDefault(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &batchHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.MaxAttempts = 1

	wf := &graph.Workflow{
		ID: "batch-stop", Name: "Batch stop", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call",
				Config:      map[string]any{"method": "GET", "url": "https://example.com{{ $json.path }}"},
				Credentials: map[string]string{"http": "cred_http"},
				Batch:       &graph.BatchPolicy{ItemsPath: "items"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
	input := flow.Item{"items": []any{
		map[string]any{"path": "/bad"},
		map[string]any{"path": "/two"},
	}}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: input})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed", res.Status)
	}
	if !strings.Contains(res.Error, "bad item") {
		t.Fatalf("error = %q", res.Error)
	}
	if h.calls != 1 {
		t.Fatalf("http calls = %d, want 1 (stop after first item)", h.calls)
	}
}

type concurrencyHTTPHost struct {
	*host.Sim
	mu     sync.Mutex
	active int
	max    int
}

func (h *concurrencyHTTPHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.mu.Lock()
	h.active++
	if h.active > h.max {
		h.max = h.active
	}
	h.mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	h.mu.Lock()
	h.active--
	h.mu.Unlock()
	return flow.HTTPResponse{Status: 200, Body: flow.Item{"url": req.URL}}, nil
}

func TestRunConcurrencyLimit(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &concurrencyHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.Parallelism = 4

	wf := &graph.Workflow{
		ID: "concurrency", Name: "Concurrency", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
		},
		Edges: []graph.Edge{},
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		wf.Nodes = append(wf.Nodes, graph.Node{ID: id, Type: "http.request", TypeVersion: 1, Name: id,
			Config:      map[string]any{"method": "GET", "url": "https://example.com/" + id},
			Credentials: map[string]string{"http": "cred_http"}})
		wf.Edges = append(wf.Edges, graph.Edge{
			From: graph.Endpoint{Node: "start", Port: "main"},
			To:   graph.Endpoint{Node: id, Port: "main"},
		})
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual", Input: flow.Item{}, MaxConcurrency: 1,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.max != 1 {
		t.Fatalf("max concurrent HTTP calls = %d, want 1", h.max)
	}
}

func TestContinueOnErrorEmitsItemOnMain(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &flakyHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.MaxAttempts = 1

	wf := &graph.Workflow{
		ID: "continue-on-error", Name: "Continue on error", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/orders/A-1",
			}, Credentials: map[string]string{"http": "cred_http"}, OnError: "continue"},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	item := res.Outputs["call"]["main"]
	if !strings.Contains(fmt.Sprint(item["error"]), "temporary upstream failure") {
		t.Fatalf("error item = %#v, want upstream failure", item)
	}
}

func TestErrorBranchRoutesToErrorPort(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	h := &flakyHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)
	eng.MaxAttempts = 1

	wf := &graph.Workflow{
		ID: "error-branch", Name: "Error branch", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/orders/A-1",
			}, Credentials: map[string]string{"http": "cred_http"}, OnError: "branch"},
			{ID: "recover", Type: "data.set", TypeVersion: 1, Name: "Recover", Config: map[string]any{
				"values": map[string]any{"reason": "{{ $json.error }}"},
			}},
			{ID: "skipped", Type: "data.set", TypeVersion: 1, Name: "Skipped", Config: map[string]any{
				"values": map[string]any{"ran": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
			{From: graph.Endpoint{Node: "call", Port: "error"}, To: graph.Endpoint{Node: "recover", Port: "main"}},
			{From: graph.Endpoint{Node: "call", Port: "main"}, To: graph.Endpoint{Node: "skipped", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	if _, ok := res.Outputs["call"]["main"]; ok {
		t.Fatalf("main output should be empty on branch, got %#v", res.Outputs["call"])
	}
	reason := fmt.Sprint(res.Outputs["recover"]["main"]["reason"])
	if !strings.Contains(reason, "temporary upstream failure") {
		t.Fatalf("recover reason = %q", reason)
	}
	if _, ran := res.Outputs["skipped"]; ran {
		t.Fatalf("main branch should be skipped, got %#v", res.Outputs["skipped"])
	}
}

type captureHTTPHost struct {
	flow.Host
	req    flow.HTTPRequest
	status int
	body   flow.Item
}

func (h *captureHTTPHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.req = req
	return flow.HTTPResponse{
		Status: h.status, StatusText: "Created",
		Headers: map[string]string{"content-type": "application/json"},
		Body:    h.body,
	}, nil
}

func TestHTTPConfigCredentialsAndNodeReferences(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	if err := st.SaveCredential("cred_http", "http", []byte(`{"token":"abc123"}`)); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	ch := &captureHTTPHost{status: 201, body: flow.Item{"ok": true}}
	eng := engine.New(st, ch)

	wf := &graph.Workflow{
		ID: "http-config", Name: "HTTP config", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set", Config: map[string]any{
				"values": map[string]any{"greeting": "hi {{ $json.name }}"}, "keepOnlySet": true,
			}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "POST", "url": "https://api.example.com/orders/{id}",
				"pathParams":   map[string]any{"id": `{{ $node["set"].json.greeting }}`},
				"query":        map[string]any{"q": "{{ $json.greeting }}"},
				"headers":      map[string]any{"X-Test": "yes"},
				"auth":         "credential",
				"bodyType":     "raw",
				"rawBody":      `{"greeting":"{{ $node["set"].json.greeting }}"}`,
				"contentType":  "application/json",
				"successCodes": "201",
				"failOnError":  true,
			}, Credentials: map[string]string{"http": "cred_http"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}},
			{From: graph.Endpoint{Node: "set", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual", Input: flow.Item{"name": "loom"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	if ch.req.URL != "https://api.example.com/orders/hi%20loom?q=hi+loom" {
		t.Fatalf("url = %q", ch.req.URL)
	}
	if ch.req.Headers["Authorization"] != "Bearer abc123" || ch.req.Headers["X-Test"] != "yes" {
		t.Fatalf("headers = %#v", ch.req.Headers)
	}
	if ch.req.ContentType != "application/json" || string(ch.req.BodyRaw) != `{"greeting":"hi loom"}` {
		t.Fatalf("body raw = %q content type = %q", ch.req.BodyRaw, ch.req.ContentType)
	}
	out := res.Outputs["call"]["main"]
	if out["status"] != float64(201) || out["ok"] != true || out["statusText"] != "Created" {
		t.Fatalf("http output = %#v", out)
	}
}

func TestHTTPFailOnError(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := engine.New(st, &captureHTTPHost{status: 500, body: flow.Item{"error": "nope"}})
	wf := &graph.Workflow{
		ID: "http-fail", Name: "HTTP fail", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://example.com", "failOnError": true,
			}},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}}},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "success codes") {
		t.Fatalf("status = %q error = %q", res.Status, res.Error)
	}
}

func TestDataContextAndPreview(t *testing.T) {
	eng := newEngine(t)
	res, err := eng.Start(context.Background(), linearWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{"name": "loom"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, err := eng.DataContext(res.RunID, "set")
	if err != nil {
		t.Fatalf("data context: %v", err)
	}
	found := false
	for _, id := range ctx.Upstream {
		if id == "start" {
			found = true
		}
	}
	if !found {
		t.Fatalf("upstream = %v, want start", ctx.Upstream)
	}
	value, err := eng.Preview(res.RunID, "set", `{{ $node["start"].json.name }}`)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if value != "loom" {
		t.Fatalf("preview = %q, want loom", value)
	}
}

func TestHTTPTraceRecordsRequestAndResponse(t *testing.T) {
	eng := newEngine(t)
	wf := &graph.Workflow{
		ID: "trace", Name: "Trace", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/orders/{{ $json.id }}",
				"headers": map[string]any{"Authorization": "Bearer secret", "Accept": "application/json"},
			}, Credentials: map[string]string{"http": "cred_http"}},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}}},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{"id": "A-1"}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	for _, event := range res.Events {
		if event.Type != "node.finished" || event.NodeID != "call" {
			continue
		}
		request, _ := event.Data["request"].(map[string]any)
		if request["url"] != "https://api.example.com/orders/A-1" {
			t.Fatalf("request url = %v", request["url"])
		}
		headers, _ := request["headers"].(map[string]any)
		if headers["Authorization"] != "[redacted]" {
			t.Fatalf("authorization header = %v", headers["Authorization"])
		}
		response, _ := event.Data["response"].(map[string]any)
		if fmt.Sprint(response["status"]) != "200" {
			t.Fatalf("response status = %v", response["status"])
		}
		return
	}
	t.Fatal("missing node.finished event for call")
}

func TestApprovalSuspendAndResume(t *testing.T) {
	eng := newEngine(t)
	ctx := context.Background()

	res, err := eng.Start(ctx, approvalWorkflow(), engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", res.Status)
	}
	if res.ResumeNode != "approve" {
		t.Fatalf("resume node = %q, want approve", res.ResumeNode)
	}
	if res.ResumeToken == "" {
		t.Fatal("resume token is empty")
	}
	for _, event := range res.Events {
		if event.Type == "run.waiting" {
			if _, leaked := event.Data["token"]; leaked {
				t.Fatal("run.waiting event must not contain the resume token")
			}
		}
	}

	resumed, err := eng.Resume(ctx, res.RunID, "approved")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != "done" {
		t.Fatalf("resumed status = %q (%s)", resumed.Status, resumed.Error)
	}
	if got := resumed.Outputs["yes"]["main"]["ok"]; got != true {
		t.Fatalf("ok = %v, want true", got)
	}

	step, err := eng.Replay(ctx, res.RunID, "approve", engine.ReplayStep)
	if err != nil {
		t.Fatalf("step replay of approval: %v", err)
	}
	if !step.Match {
		t.Fatalf("approval step replay mismatch: recorded=%s actual=%s", step.RecordedHash, step.ActualHash)
	}
}

func TestApprovalSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	eng := engine.New(st, host.NewSim())

	res, err := eng.Start(context.Background(), approvalWorkflow(), engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", res.Status)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	st2, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st2.Close()
	eng2 := engine.New(st2, host.NewSim())

	resumed, err := eng2.Resume(context.Background(), res.RunID, "approved")
	if err != nil {
		t.Fatalf("resume after restart: %v", err)
	}
	if resumed.Status != "done" {
		t.Fatalf("resumed status = %q (%s)", resumed.Status, resumed.Error)
	}
}

type flakyHost struct {
	flow.Host
	mu       sync.Mutex
	failures int
	keys     []string
}

func (h *flakyHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.keys = append(h.keys, req.IdempotencyKey)
	if h.failures > 0 {
		h.failures--
		return flow.HTTPResponse{}, errors.New("temporary upstream error")
	}
	return flow.HTTPResponse{Status: 200, Body: flow.Item{"ok": true}}, nil
}

type blockingHost struct {
	flow.Host
	started chan struct{}
	once    sync.Once
}

func (h *blockingHost) Sleep(ctx context.Context, _ time.Duration) error {
	h.once.Do(func() { close(h.started) })
	<-ctx.Done()
	return ctx.Err()
}

func httpWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "http", Name: "HTTP", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "HTTP",
				Config:      map[string]any{"method": "GET", "url": "https://example.com/x"},
				Credentials: map[string]string{"http": "cred"},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "http", Port: "main"}},
		},
	}
}

func waitWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "wait", Name: "Wait", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "wait", Type: "flow.wait", TypeVersion: 1, Name: "Wait", Config: map[string]any{"seconds": 30.0}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "wait", Port: "main"}},
		},
	}
}

func TestRetryUsesStableIdempotencyKey(t *testing.T) {
	eng := newEngine(t)
	fh := &flakyHost{Host: host.NewSim(), failures: 1}
	eng.Host = fh

	res, err := eng.Start(context.Background(), httpWorkflow(), engine.Options{
		Trigger: "manual", MaxAttempts: 3,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}

	if len(fh.keys) != 2 {
		t.Fatalf("host saw %d calls, want 2", len(fh.keys))
	}
	if fh.keys[0] == "" || fh.keys[0] != fh.keys[1] {
		t.Fatalf("idempotency keys = %#v, want one stable non-empty key", fh.keys)
	}
	wantKey := res.RunID + ":http"
	if fh.keys[0] != wantKey {
		t.Fatalf("idempotency key = %q, want %q", fh.keys[0], wantKey)
	}

	var failed, retried int
	var finishedAttempt int
	for _, ev := range res.Events {
		switch ev.Type {
		case "node.failed":
			failed++
		case "node.retried":
			retried++
		case "node.finished":
			if ev.NodeID == "http" {
				finishedAttempt = ev.Attempt
			}
		}
	}
	if failed != 1 || retried != 1 {
		t.Fatalf("failed=%d retried=%d, want 1 and 1", failed, retried)
	}
	if finishedAttempt != 2 {
		t.Fatalf("finished attempt = %d, want 2", finishedAttempt)
	}
}

func TestCancelRun(t *testing.T) {
	eng := newEngine(t)
	bh := &blockingHost{Host: host.NewSim(), started: make(chan struct{})}
	eng.Host = bh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type outcome struct {
		res *engine.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := eng.Start(ctx, waitWorkflow(), engine.Options{Trigger: "manual"})
		done <- outcome{res, err}
	}()

	select {
	case <-bh.started:
	case <-time.After(2 * time.Second):
		t.Fatal("wait node did not start")
	}

	var runID string
	for i := 0; i < 100; i++ {
		runs, err := eng.Store.ListRuns("", 1)
		if err != nil {
			t.Fatalf("list runs: %v", err)
		}
		if len(runs) > 0 {
			runID = runs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if runID == "" {
		t.Fatal("no run recorded")
	}
	if !eng.Cancel(runID) {
		t.Fatal("cancel returned false")
	}

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("start returned error: %v", out.err)
		}
		if out.res.Status != "cancelled" {
			t.Fatalf("status = %q, want cancelled", out.res.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not cancel")
	}
}

func TestConcurrentRuns(t *testing.T) {
	eng := newEngine(t)
	const n = 100

	var wg sync.WaitGroup
	ids := make(chan string, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			name := fmt.Sprintf("n%d", i)
			res, err := eng.Start(context.Background(), linearWorkflow(), engine.Options{
				Trigger: "webhook", Input: flow.Item{"name": name},
			})
			if err != nil {
				errs <- err
				return
			}
			if res.Status != "done" {
				errs <- fmt.Errorf("run %d status %q: %s", i, res.Status, res.Error)
				return
			}
			if got := res.Outputs["set"]["main"]["greeting"]; got != "hi "+name {
				errs <- fmt.Errorf("run %d greeting = %v", i, got)
				return
			}
			ids <- res.RunID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)

	for err := range errs {
		t.Error(err)
	}
	if len(ids) != n {
		t.Fatalf("completed %d runs, want %d", len(ids), n)
	}

	runs, err := eng.Store.ListRuns("", n+10)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != n {
		t.Fatalf("stored %d runs, want %d", len(runs), n)
	}
	seen := map[string]bool{}
	for _, run := range runs {
		if seen[run.ID] {
			t.Fatalf("duplicate run id %s", run.ID)
		}
		seen[run.ID] = true
		events, err := eng.Store.ListEvents(run.ID)
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

func TestTerminalStatusAlwaysHasEvent(t *testing.T) {
	eng := newEngine(t)
	for i := 0; i < 100; i++ {
		runID, err := eng.StartAsync(context.Background(), linearWorkflow(), engine.Options{
			Trigger: "manual", Input: flow.Item{"name": fmt.Sprintf("n%d", i)},
		})
		if err != nil {
			t.Fatalf("start run %d: %v", i, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			run, err := eng.Store.GetRun(runID)
			if err != nil {
				t.Fatalf("get run %s: %v", runID, err)
			}
			if run.Status == "done" || run.Status == "failed" || run.Status == "cancelled" {
				events, err := eng.Store.ListEvents(runID)
				if err != nil {
					t.Fatalf("events for %s: %v", runID, err)
				}
				terminal := map[string]string{"done": "run.finished", "failed": "run.failed", "cancelled": "run.cancelled"}[run.Status]
				found := false
				for _, event := range events {
					if event.Type == terminal {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("run %s status %q without %s event", runID, run.Status, terminal)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s did not finish", runID)
			}
		}
	}
}

func TestInvalidWorkflowReturnsValidationError(t *testing.T) {
	eng := newEngine(t)
	wf := linearWorkflow()
	wf.Nodes[1].Type = "not.a.node"

	_, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	if !strings.Contains(err.Error(), "invalid workflow") {
		t.Fatalf("error = %v, want invalid workflow", err)
	}
}

func TestSubflow(t *testing.T) {
	eng := newEngine(t)

	child := &graph.Workflow{
		ID: "child", Name: "Child", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set",
				Config: map[string]any{
					"values":      map[string]any{"child": true},
					"keepOnlySet": true,
				},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}},
		},
	}
	if err := eng.Store.SaveWorkflow(child); err != nil {
		t.Fatalf("save child: %v", err)
	}

	parent := &graph.Workflow{
		ID: "parent", Name: "Parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "sub", Type: "flow.subflow", TypeVersion: 1, Name: "Child",
				Config: map[string]any{"workflowId": "child", "wait": true},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "sub", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), parent, engine.Options{
		Trigger: "manual", Input: flow.Item{"seed": "x"},
	})
	if err != nil {
		t.Fatalf("start parent: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	if got := res.Outputs["sub"]["main"]["child"]; got != true {
		t.Fatalf("subflow output = %#v", res.Outputs["sub"])
	}

	runs, err := eng.Store.ListRuns("child", 1)
	if err != nil {
		t.Fatalf("list child runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Status != "done" {
		t.Fatalf("child runs = %#v", runs)
	}
}

func TestMergeInputs(t *testing.T) {
	eng := newEngine(t)
	wf := &graph.Workflow{
		ID: "merge", Name: "Merge", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "a", Type: "data.set", TypeVersion: 1, Name: "A",
				Config: map[string]any{"values": map[string]any{"a": 1}, "keepOnlySet": true},
			},
			{
				ID: "b", Type: "data.set", TypeVersion: 1, Name: "B",
				Config: map[string]any{"values": map[string]any{"b": 2}, "keepOnlySet": true},
			},
			{ID: "merge", Type: "flow.merge", TypeVersion: 1, Name: "Merge", Config: map[string]any{"mode": "append"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "a", Port: "main"}},
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "b", Port: "main"}},
			{From: graph.Endpoint{Node: "a", Port: "main"}, To: graph.Endpoint{Node: "merge", Port: "a"}},
			{From: graph.Endpoint{Node: "b", Port: "main"}, To: graph.Endpoint{Node: "merge", Port: "b"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	got := res.Outputs["merge"]["main"]
	if got["a"] != 1 || got["b"] != 2 {
		t.Fatalf("merged item = %#v", got)
	}
}

func TestFailedRunRecordsTerminalEvent(t *testing.T) {
	eng := newEngine(t)
	wf := &graph.Workflow{
		ID: "failure", Name: "Failure", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "boom", Type: "code.run", TypeVersion: 1, Name: "Boom",
				Config: map[string]any{"language": "javascript", "source": "throw new Error('boom')"},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "boom", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "boom") {
		t.Fatalf("status=%q error=%q", res.Status, res.Error)
	}
	last := res.Events[len(res.Events)-1]
	if last.Type != "run.failed" || !strings.Contains(last.Error, "boom") {
		t.Fatalf("last event = %#v", last)
	}
	run, err := eng.Store.GetRun(res.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "failed" || !strings.Contains(run.Error, "boom") {
		t.Fatalf("stored run = %#v", run)
	}
}

func TestResumeRejectsNonWaitingRun(t *testing.T) {
	eng := newEngine(t)
	res, err := eng.Start(context.Background(), linearWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{"name": "loom"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q", res.Status)
	}
	if _, err := eng.Resume(context.Background(), res.RunID, "approved"); err == nil {
		t.Fatal("resume of a finished run succeeded")
	}
}

func TestApprovalRejected(t *testing.T) {
	eng := newEngine(t)
	res, err := eng.Start(context.Background(), approvalWorkflow(), engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("status = %q", res.Status)
	}
	resumed, err := eng.Resume(context.Background(), res.RunID, "rejected")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != "done" {
		t.Fatalf("status = %q (%s)", resumed.Status, resumed.Error)
	}
	if got := resumed.Outputs["approve"]["rejected"]["approved"]; got != false {
		t.Fatalf("approved = %v, want false", got)
	}
}

func TestResumeRequiresExplicitDecision(t *testing.T) {
	eng := newEngine(t)
	res, err := eng.Start(context.Background(), approvalWorkflow(), engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := eng.Resume(context.Background(), res.RunID, ""); err == nil {
		t.Fatal("resume with an empty decision should fail")
	}
	run, err := eng.Store.GetRun(res.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "waiting" {
		t.Fatalf("status after rejected resume = %q, want waiting", run.Status)
	}
	resumed, err := eng.Resume(context.Background(), res.RunID, "approved")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if resumed.Status != "done" {
		t.Fatalf("status = %q (%s)", resumed.Status, resumed.Error)
	}
	if got := resumed.Outputs["yes"]["main"]["ok"]; got != true {
		t.Fatalf("ok = %v, want true", got)
	}
}

func TestSubflowRequiresResolver(t *testing.T) {
	eng := newEngine(t)
	eng.ResolveWorkflow = nil
	wf := &graph.Workflow{
		ID: "parent", Name: "Parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "child", Type: "flow.subflow", TypeVersion: 1, Name: "Child",
				Config: map[string]any{"workflowId": "missing"},
			},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "child", Port: "main"}},
		},
	}

	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" || !strings.Contains(res.Error, "resolver") {
		t.Fatalf("status=%q error=%q", res.Status, res.Error)
	}
}

func TestCancelAll(t *testing.T) {
	eng := newEngine(t)
	bh := &blockingHost{Host: host.NewSim(), started: make(chan struct{})}
	eng.Host = bh

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	type outcome struct {
		res *engine.Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := eng.Start(ctx, waitWorkflow(), engine.Options{Trigger: "manual"})
		done <- outcome{res, err}
	}()

	select {
	case <-bh.started:
	case <-time.After(2 * time.Second):
		t.Fatal("wait node did not start")
	}

	eng.CancelAll()

	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("start returned error: %v", out.err)
		}
		if out.res.Status != "cancelled" {
			t.Fatalf("status = %q, want cancelled", out.res.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not cancel")
	}
}

func TestResumeInterruptedFailures(t *testing.T) {
	eng := newEngine(t)
	// 1. Non-existent run
	if _, err := eng.ResumeInterrupted(context.Background(), "non-existent"); err == nil {
		t.Fatal("expected error for non-existent run")
	}

	// 2. Not in flight
	rec := store.Run{
		ID: "done-run", WorkflowID: "wf-1", WorkflowVersion: "1",
		Status: "done", Trigger: "manual",
	}
	if err := eng.Store.CreateRun(rec); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := eng.ResumeInterrupted(context.Background(), "done-run"); err == nil {
		t.Fatal("expected error for done run")
	}

	// 3. Unresolvable workflow (fails and marks run as failed)
	runningRec := store.Run{
		ID: "unresolvable-run", WorkflowID: "missing-wf", WorkflowVersion: "1",
		Status: "running", Trigger: "manual",
	}
	if err := eng.Store.CreateRun(runningRec); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := eng.ResumeInterrupted(context.Background(), "unresolvable-run"); err == nil {
		t.Fatal("expected error for unresolvable workflow")
	}
	updated, err := eng.Store.GetRun("unresolvable-run")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if updated.Status != "failed" {
		t.Fatalf("updated status = %q, want failed", updated.Status)
	}
}

func TestEngineHelpers(t *testing.T) {
	// IntValue
	if v := engine.IntValue(42); v != 42 {
		t.Fatalf("int: got %d, want 42", v)
	}
	if v := engine.IntValue(int64(42)); v != 42 {
		t.Fatalf("int64: got %d, want 42", v)
	}
	if v := engine.IntValue(float64(42.7)); v != 42 {
		t.Fatalf("float64: got %d, want 42", v)
	}
	if v := engine.IntValue(json.Number("42")); v != 42 {
		t.Fatalf("json.Number: got %d, want 42", v)
	}
	if v := engine.IntValue("invalid"); v != 0 {
		t.Fatalf("string: got %d, want 0", v)
	}

	// LookupPath
	item := flow.Item{
		"a": map[string]any{"b": flow.Item{"c": "val"}},
	}
	if val, ok := engine.LookupPath(item, "a.b.c"); !ok || val != "val" {
		t.Fatalf("LookupPath valid: got %v, %v", val, ok)
	}
	if _, ok := engine.LookupPath(item, "a.b.missing"); ok {
		t.Fatal("LookupPath missing key: expected false")
	}
	if _, ok := engine.LookupPath(item, "a.b.c.toolong"); ok {
		t.Fatal("LookupPath too long: expected false")
	}

	// AsItem
	if _, ok := engine.AsItem(flow.Item{"k": "v"}); !ok {
		t.Fatal("AsItem flow.Item: expected true")
	}
	if _, ok := engine.AsItem(map[string]any{"k": "v"}); !ok {
		t.Fatal("AsItem map: expected true")
	}
	if _, ok := engine.AsItem("string"); ok {
		t.Fatal("AsItem string: expected false")
	}

	// ToItems
	itemsList := []flow.Item{{"k": "v"}}
	if res, err := engine.ToItems(itemsList); err != nil || len(res) != 1 {
		t.Fatalf("ToItems []flow.Item: %v, %v", res, err)
	}
	anyList := []any{map[string]any{"k": "v"}}
	if res, err := engine.ToItems(anyList); err != nil || len(res) != 1 {
		t.Fatalf("ToItems []any: %v, %v", res, err)
	}
	if _, err := engine.ToItems([]any{"invalid"}); err == nil {
		t.Fatal("ToItems invalid element: expected error")
	}
	if _, err := engine.ToItems("not an array"); err == nil {
		t.Fatal("ToItems non-array: expected error")
	}
}


