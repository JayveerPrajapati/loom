package engine_test

import (
	"context"
	"strings"
	"testing"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// A stored workflow may hold limits that ingest now rejects. The engine must
// still load it (ValidateStored skips the caps) and enforce them at runtime.
func TestStoredSwitchRejectsMatchingOverflowCase(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := engine.New(st, host.NewSim())

	wf := &graph.Workflow{
		ID: "switch-overflow", Name: "Switch overflow", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "switch", Type: "flow.switch", TypeVersion: 1, Name: "Switch", Config: map[string]any{
				"cases": []any{"{{ false }}", "{{ false }}", "{{ false }}", "{{ true }}"},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "switch", Port: "main"}},
		},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed (%s)", res.Status, res.Error)
	}
	if !strings.Contains(res.Error, "flow.switch") {
		t.Fatalf("error = %q, want flow.switch", res.Error)
	}
}

func TestStoredWaitRejectsOutOfRangeSeconds(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := engine.New(st, host.NewSim())

	wf := &graph.Workflow{
		ID: "wait-overflow", Name: "Wait overflow", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "wait", Type: "flow.wait", TypeVersion: 1, Name: "Wait", Config: map[string]any{
				"seconds": float64(2592001),
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "wait", Port: "main"}},
		},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed (%s)", res.Status, res.Error)
	}
	if !strings.Contains(res.Error, "flow.wait") {
		t.Fatalf("error = %q, want flow.wait", res.Error)
	}
}

// A stored batch may request more concurrency than the ingest cap. The engine
// must clamp the fan-out to 32 instead of refusing to run it.
func TestStoredBatchConcurrencyClamps(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := &concurrencyHTTPHost{Sim: host.NewSim()}
	eng := engine.New(st, h)

	const items = 64
	input := make([]any, items)
	for i := range input {
		input[i] = map[string]any{"path": "/item"}
	}
	wf := &graph.Workflow{
		ID: "batch-clamp", Name: "Batch clamp", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call",
				Config:      map[string]any{"method": "GET", "url": "https://example.com{{ $json.path }}"},
				Credentials: map[string]string{"http": "cred_http"},
				Batch:       &graph.BatchPolicy{ItemsPath: "items", Concurrency: 33}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual", Input: flow.Item{"items": input},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	h.mu.Lock()
	max := h.max
	h.mu.Unlock()
	if max > 32 {
		t.Fatalf("max concurrent batch calls = %d, want <= 32 after clamp", max)
	}
}
