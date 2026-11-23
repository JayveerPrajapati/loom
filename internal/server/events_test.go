package server_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"loom/internal/nodes"
	"loom/internal/store"
)

func manualEmitWorkflow(id, event string) map[string]any {
	return map[string]any{
		"id": id, "name": "Emit " + event, "version": 1,
		"nodes": []any{
			map[string]any{"id": "start", "type": "trigger.manual", "typeVersion": 1, "name": "Start", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}},
			map[string]any{"id": "emit", "type": "flow.emit", "typeVersion": 1, "name": "Emit", "position": map[string]any{"x": 260, "y": 0},
				"config": map[string]any{"event": event, "payload": map[string]any{"hello": "world"}}},
		},
		"edges": []any{map[string]any{"from": map[string]any{"node": "start", "port": "main"}, "to": map[string]any{"node": "emit", "port": "main"}}},
	}
}

func eventSubscriberWorkflow(id, event string) map[string]any {
	return map[string]any{
		"id": id, "name": "Subscribe " + event, "version": 1,
		"nodes": []any{
			map[string]any{"id": "start", "type": "trigger.event", "typeVersion": 1, "name": "On " + event, "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"event": event}},
			map[string]any{"id": "mark", "type": "data.set", "typeVersion": 1, "name": "Mark", "position": map[string]any{"x": 260, "y": 0},
				"config": map[string]any{"values": map[string]any{"got": "hi {{ $json.hello }}"}}},
		},
		"edges": []any{map[string]any{"from": map[string]any{"node": "start", "port": "main"}, "to": map[string]any{"node": "mark", "port": "main"}}},
	}
}

// eventLoopWorkflow listens to an event and emits the same event again:
// the shape a runaway emit→trigger cycle is made of.
func eventLoopWorkflow(id, event string) map[string]any {
	return map[string]any{
		"id": id, "name": "Loop " + event, "version": 1,
		"nodes": []any{
			map[string]any{"id": "start", "type": "trigger.event", "typeVersion": 1, "name": "On " + event, "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"event": event}},
			map[string]any{"id": "emit", "type": "flow.emit", "typeVersion": 1, "name": "Emit", "position": map[string]any{"x": 260, "y": 0},
				"config": map[string]any{"event": event, "payload": map[string]any{}}},
		},
		"edges": []any{map[string]any{"from": map[string]any{"node": "start", "port": "main"}, "to": map[string]any{"node": "emit", "port": "main"}}},
	}
}

// TestEventBusRunsSubscriberWorkflow: a flow.emit node publishes an event;
// a workflow with a matching trigger.event node starts a run that receives
// the payload as its trigger input, attributed as a child of the emitting run.
func TestEventBusRunsSubscriberWorkflow(t *testing.T) {
	srv, _ := newTestServer(t, "")

	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", manualEmitWorkflow("emit-wf", "ping")); status != http.StatusCreated {
		t.Fatalf("create emit workflow: %d %s", status, body)
	}
	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", eventSubscriberWorkflow("sub-wf", "ping")); status != http.StatusCreated {
		t.Fatalf("create subscriber workflow: %d %s", status, body)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/emit-wf/runs?async=1", "", map[string]any{"input": map[string]any{}})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("start emit run: %d %s", status, data)
	}
	var started struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(data, &started); err != nil || started.RunID == "" {
		t.Fatalf("start response: %v (%s)", err, data)
	}

	// Poll the subscriber workflow's runs for a run parented to the emitter.
	deadline := time.Now().Add(10 * time.Second)
	var subRun map[string]any
	for subRun == nil && time.Now().Before(deadline) {
		status, data = call(t, srv, http.MethodGet, "/api/runs?workflow=sub-wf&limit=20", "", nil)
		if status != http.StatusOK {
			t.Fatalf("list runs: %d", status)
		}
		var list struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatalf("runs list: %v", err)
		}
		for _, r := range list.Runs {
			if r["parentRunId"] == started.RunID && r["status"] == "done" {
				subRun = r
			}
		}
		if subRun == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if subRun == nil {
		t.Fatal("subscriber run never completed")
	}
	if subRun["trigger"] != "event" {
		t.Fatalf("subscriber trigger = %v, want event", subRun["trigger"])
	}

	// The event payload reached the subscriber as its trigger input.
	status, data = call(t, srv, http.MethodGet, "/api/runs/"+subRun["id"].(string)+"/events?format=json", "", nil)
	if status != http.StatusOK {
		t.Fatalf("subscriber events: %d", status)
	}
	var evs []map[string]any
	if err := json.Unmarshal(data, &evs); err != nil {
		var wrapped struct {
			Events []map[string]any `json:"events"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil {
			t.Fatalf("events shape: %v / %v (%s)", err, err2, data)
		}
		evs = wrapped.Events
	}
	inputHash := ""
	for _, ev := range evs {
		if ev["type"] == "node.started" && ev["nodeId"] == "start" {
			if h, ok := ev["inputHash"].(string); ok {
				inputHash = h
			}
		}
	}
	if inputHash == "" {
		t.Fatalf("trigger node.started inputHash missing from %d events", len(evs))
	}
	status, data = call(t, srv, http.MethodGet, "/api/blobs/"+inputHash, "", nil)
	if status != http.StatusOK {
		t.Fatalf("trigger input blob: %d", status)
	}
	var item map[string]any
	if err := json.Unmarshal(data, &item); err != nil {
		t.Fatalf("blob decode: %v (%s)", err, data)
	}
	if item["hello"] != "world" {
		t.Fatalf("subscriber input = %v, want hello=world from the event payload", item)
	}
}

// TestEventBusBudgetsRootDescendants: N subscribers that re-emit the same
// event would otherwise form an N^hops tree. The per-root budget must stop the
// tree at 100 runs regardless of hop depth.
func TestEventBusBudgetsRootDescendants(t *testing.T) {
	srv, st := newTestServer(t, "")

	for i := 0; i < 3; i++ {
		id := "amp-sub-" + string(rune('a'+i))
		if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", eventLoopWorkflow(id, "amp")); status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", id, status, body)
		}
	}
	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", manualEmitWorkflow("amp-kick", "amp")); status != http.StatusCreated {
		t.Fatalf("create kick workflow: %d %s", status, body)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/amp-kick/runs?async=1", "", map[string]any{"input": map[string]any{}})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("start kick run: %d %s", status, data)
	}
	var started struct {
		RunID string `json:"runId"`
	}
	if err := json.Unmarshal(data, &started); err != nil || started.RunID == "" {
		t.Fatalf("start response: %v (%s)", err, data)
	}

	deadline := time.Now().Add(20 * time.Second)
	for {
		count, err := st.CountRunsByRoot(started.RunID)
		if err != nil {
			t.Fatalf("count descendants: %v", err)
		}
		if count > 100 {
			t.Fatalf("event tree grew to %d runs, want at most 100", count)
		}
		if count == 100 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("event tree stopped at %d runs, want 100", count)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Once the budget is reached no further run may start.
	time.Sleep(500 * time.Millisecond)
	if count, err := st.CountRunsByRoot(started.RunID); err != nil || count != 100 {
		t.Fatalf("after settling, root has %d runs (%v), want 100", count, err)
	}
}

// TestEventBusCapsEventCycles: an emit→trigger loop must terminate at the
// hop cap with a visible failure instead of running forever.
func TestEventBusCapsEventCycles(t *testing.T) {
	srv, _ := newTestServer(t, "")

	// LOOP listens to "loop" and emits "loop" again; KICK starts the chain.
	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", eventLoopWorkflow("loop-wf", "loop")); status != http.StatusCreated {
		t.Fatalf("create loop workflow: %d %s", status, body)
	}
	loopEmit := manualEmitWorkflow("kick-wf", "loop")
	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", loopEmit); status != http.StatusCreated {
		t.Fatalf("create kick workflow: %d %s", status, body)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/kick-wf/runs?async=1", "", map[string]any{"input": map[string]any{}})
	if status != http.StatusCreated && status != http.StatusAccepted {
		t.Fatalf("start kick run: %d %s", status, data)
	}

	// Poll for the terminal failed run of the loop chain.
	deadline := time.Now().Add(15 * time.Second)
	var failed map[string]any
	total := 0
	for failed == nil && time.Now().Before(deadline) {
		status, data = call(t, srv, http.MethodGet, "/api/runs?workflow=loop-wf&limit=50", "", nil)
		if status != http.StatusOK {
			t.Fatalf("list loop runs: %d", status)
		}
		var list struct {
			Runs []map[string]any `json:"runs"`
		}
		if err := json.Unmarshal(data, &list); err != nil {
			t.Fatalf("loop runs list: %v", err)
		}
		total = len(list.Runs)
		for _, r := range list.Runs {
			if r["status"] == "failed" && strings.Contains(r["error"].(string), "event chain exceeds") {
				failed = r
			}
		}
		if failed == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if failed == nil {
		t.Fatalf("loop chain never failed at the hop cap (%d runs seen)", total)
	}
	// The chain must be bounded: at most one run per hop.
	if total > nodes.MaxEventHops+1 {
		t.Fatalf("loop chain produced %d runs, want at most %d (one per hop)", total, nodes.MaxEventHops+1)
	}
}

// TestCountRunsByRootCountsRootRows: the event budget trusts this query, so it
// must count every row persisted with a RootRunID (root and descendants) and
// nothing from another tree.
func TestCountRunsByRootCountsRootRows(t *testing.T) {
	_, st := newTestServer(t, "")
	started := time.Now().UTC()

	for i := 0; i < 3; i++ {
		if err := st.CreateRun(store.Run{
			ID: fmt.Sprintf("tree-a-%d", i), WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "event", StartedAt: started, RootRunID: "root-a",
		}); err != nil {
			t.Fatalf("create run %d: %v", i, err)
		}
	}
	if err := st.CreateRun(store.Run{
		ID: "tree-b-0", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "event", StartedAt: started, RootRunID: "root-b",
	}); err != nil {
		t.Fatalf("create other-tree run: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID: "no-root", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: started,
	}); err != nil {
		t.Fatalf("create rootless run: %v", err)
	}

	if count, err := st.CountRunsByRoot("root-a"); err != nil || count != 3 {
		t.Fatalf("root-a count = %d (%v), want 3", count, err)
	}
	if count, err := st.CountRunsByRoot("root-b"); err != nil || count != 1 {
		t.Fatalf("root-b count = %d (%v), want 1", count, err)
	}
	if count, err := st.CountRunsByRoot(""); err != nil || count != 0 {
		t.Fatalf("empty-root count = %d (%v), want 0", count, err)
	}
}
