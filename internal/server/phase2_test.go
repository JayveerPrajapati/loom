package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

// phase2BlockingHost holds an HTTP call whose URL contains "block" so a run
// keeps a concurrency slot occupied during the replay attempt.
type phase2BlockingHost struct {
	flow.Host
	mu      sync.Mutex
	blocked bool
	release chan struct{}
}

func (h *phase2BlockingHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	if strings.Contains(req.URL, "block") {
		h.mu.Lock()
		h.blocked = true
		h.mu.Unlock()
		select {
		case <-h.release:
		case <-ctx.Done():
			return flow.HTTPResponse{}, ctx.Err()
		}
	}
	return h.Host.HTTP(ctx, req)
}

func (h *phase2BlockingHost) isBlocked() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.blocked
}

func waitRunHTTPStatus(t *testing.T, srv *httptest.Server, runID, want string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		status, data := call(t, srv, http.MethodGet, "/api/runs/"+runID, "", nil)
		if status != http.StatusOK {
			t.Fatalf("get run %s: %d %s", runID, status, data)
		}
		var run struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(data, &run); err != nil {
			t.Fatalf("decode run: %v (%s)", err, data)
		}
		if run.Status == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s stuck at %s, want %s", runID, run.Status, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TestReplayNoSlotMapsToConflict: when a replay cannot get a concurrency slot,
// the API must return 409 rather than 422 so clients know to retry later.
func TestReplayNoSlotMapsToConflict(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	blocking := &phase2BlockingHost{Host: host.NewSim(), release: make(chan struct{})}
	eng := engine.New(st, blocking)
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version:        "test",
		Store:          st,
		Engine:         eng,
		AllowAnonymous: true,
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(blocking.release) })

	wf := map[string]any{
		"id": "replay-cap", "name": "Replay cap", "version": 1,
		"settings": map[string]any{"maxParallelRuns": 1},
		"nodes": []any{
			map[string]any{"id": "start", "type": "trigger.manual", "typeVersion": 1, "name": "Start",
				"position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}},
			map[string]any{"id": "call", "type": "http.request", "typeVersion": 1, "name": "Call",
				"position": map[string]any{"x": 260, "y": 0},
				"config":   map[string]any{"method": "GET", "url": "{{ $json.url }}"}},
		},
		"edges": []any{map[string]any{
			"from": map[string]any{"node": "start", "port": "main"},
			"to":   map[string]any{"node": "call", "port": "main"},
		}},
	}
	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", wf); status != http.StatusCreated {
		t.Fatalf("create workflow: %d %s", status, body)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/replay-cap/runs?async=1", "", map[string]any{
		"input": map[string]any{"url": "https://quick.example"},
	})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("start source run: %d %s", status, data)
	}
	var source struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(data, &source); err != nil || source.RunID == "" {
		t.Fatalf("source response: %v (%s)", err, data)
	}
	waitRunHTTPStatus(t, srv, source.RunID, "done")

	status, data = call(t, srv, http.MethodPost, "/api/workflows/replay-cap/runs?async=1", "", map[string]any{
		"input": map[string]any{"url": "https://block.example"},
	})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("start holder run: %d %s", status, data)
	}
	var holder struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(data, &holder); err != nil || holder.RunID == "" {
		t.Fatalf("holder response: %v (%s)", err, data)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !blocking.isBlocked() {
		if time.Now().After(deadline) {
			t.Fatal("holder run never reached the blocking HTTP call")
		}
		time.Sleep(20 * time.Millisecond)
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+source.RunID+"/replay", "", map[string]any{
		"mode": "from", "nodeId": "call",
	})
	if status != http.StatusConflict {
		t.Fatalf("replay without a slot = %d %s, want 409", status, data)
	}
	if !strings.Contains(string(data), "replay needs a free run slot") {
		t.Fatalf("409 body = %s, want the ErrReplayNoSlot message", data)
	}
}
