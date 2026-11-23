package engine_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// captureGateHost blocks the first HTTP call, records every request URL, then
// behaves like the simulated host.
type captureGateHost struct {
	flow.Host
	release chan struct{}
	started chan struct{}
	once    sync.Once
	mu      sync.Mutex
	urls    []string
}

func (h *captureGateHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.mu.Lock()
	h.urls = append(h.urls, req.URL)
	h.mu.Unlock()
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
	case <-ctx.Done():
		return flow.HTTPResponse{}, ctx.Err()
	}
	return h.Host.HTTP(ctx, req)
}

func (h *captureGateHost) URLs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.urls...)
}

// A queued run must execute the trigger input it was started with, not the
// display-masked copy stored in the event log.
func TestQueuedRunUsesPersistedInput(t *testing.T) {
	gate := &captureGateHost{Host: host.NewSim(), release: make(chan struct{}), started: make(chan struct{})}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := engine.New(st, gate)

	wf := &graph.Workflow{
		ID: "queued-input", Name: "Queued input", Version: 1,
		Settings: map[string]any{"maxParallelRuns": 1},
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": `https://api.example.com/{{ $json.token }}`,
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
	ctx := context.Background()

	first, err := eng.StartAsync(ctx, wf, engine.Options{Trigger: "manual", Input: flow.Item{"token": "first-token"}})
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	<-gate.started
	second, err := eng.StartAsync(ctx, wf, engine.Options{Trigger: "manual", Input: flow.Item{"token": "REAL-SECRET-TOKEN"}})
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	waitRunStatus(t, st, second, "queued", 3*time.Second)
	close(gate.release)
	waitRunStatus(t, st, first, "done", 5*time.Second)
	waitRunStatus(t, st, second, "done", 5*time.Second)

	var sawReal bool
	for _, url := range gate.URLs() {
		if url == "https://api.example.com/REAL-SECRET-TOKEN" {
			sawReal = true
		}
		if url == "https://api.example.com/[redacted]" {
			t.Fatalf("queued run executed the masked value: %v", gate.URLs())
		}
	}
	if !sawReal {
		t.Fatalf("queued run never used the persisted input: %v", gate.URLs())
	}
}
