package engine

import (
	"context"
	"strings"
	"testing"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

func TestResumeInterruptedRun(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := New(st, host.NewSim())

	wf := &graph.Workflow{
		ID: "resume", Name: "Resume", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set", Config: map[string]any{
				"values": map[string]any{"greeting": "hi {{ $json.name }}"},
			}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"resumed": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}},
			{From: graph.Endpoint{Node: "set", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}

	state, _, cancel, err := eng.begin(context.Background(), wf, Options{Trigger: "manual"}, 0)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	runID := state.runID

	// Simulate a process that stopped after "set" finished: record the node
	// through the engine's own execution path, then abandon the run.
	node := findNode(state.wf, "set")
	if _, err := eng.executeNode(context.Background(), state, node, flow.Input{
		Item: flow.Item{"name": "loom"}, Config: node.Config,
		RunID: runID, NodeID: node.ID, Host: eng.Host, Now: eng.now(),
	}, Options{}); err != nil {
		t.Fatalf("record partial node: %v", err)
	}
	cancel()
	eng.unregister(runID)

	res, err := eng.ResumeInterrupted(context.Background(), runID)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	if got := res.Outputs["done"]["main"]["resumed"]; got != true {
		t.Fatalf("downstream output = %v, want true", got)
	}
	if got := res.Outputs["set"]["main"]["greeting"]; got != "hi loom" {
		t.Fatalf("restored output = %v, want hi loom", got)
	}

	run, err := st.GetRun(runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != "done" {
		t.Fatalf("run status = %q, want done", run.Status)
	}

	var resumedEvents, setFinished int
	for _, ev := range res.Events {
		switch {
		case ev.Type == "run.resumed":
			resumedEvents++
		case ev.Type == "node.finished" && ev.NodeID == "set":
			setFinished++
		}
	}
	if resumedEvents != 1 || setFinished != 1 {
		t.Fatalf("run.resumed = %d, set node.finished = %d; want 1 and 1", resumedEvents, setFinished)
	}

	if _, err := eng.ResumeInterrupted(context.Background(), runID); err == nil {
		t.Fatal("resuming a terminal run should fail")
	}
}

func orphanTestWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "orphan-restart", Name: "Orphan restart", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set", Config: map[string]any{
				"values": map[string]any{"ok": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}},
		},
	}
}

// A stale subflow child is orphaned by recovery; the parent will recreate a
// fresh child. A stale top-level run keeps the existing resume behavior.
func TestResumeInterruptedOrphansParentedRuns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := New(st, host.NewSim())
	wf := orphanTestWorkflow()

	childState, _, childCancel, err := eng.begin(context.Background(), wf, Options{
		Trigger: "subflow", ParentRunID: "run-parent-1",
	}, 1)
	if err != nil {
		t.Fatalf("begin child: %v", err)
	}
	childCancel()
	eng.unregister(childState.runID)

	if _, err := eng.ResumeInterrupted(context.Background(), childState.runID); err == nil {
		t.Fatal("resuming a parented run should return an error")
	}
	child, err := st.GetRun(childState.runID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if child.Status != "failed" {
		t.Fatalf("child status = %q, want failed", child.Status)
	}
	if !strings.Contains(child.Error, "orphaned by restart") {
		t.Fatalf("child error = %q, want orphaned by restart", child.Error)
	}
	childEvents, err := st.ListEvents(childState.runID)
	if err != nil {
		t.Fatalf("list child events: %v", err)
	}
	orphanEvents := 0
	for _, ev := range childEvents {
		if ev.Type == "run.failed" && strings.Contains(ev.Error, "orphaned by restart") {
			orphanEvents++
		}
	}
	if orphanEvents != 1 {
		t.Fatalf("orphaned run.failed events = %d, want 1", orphanEvents)
	}

	topState, _, topCancel, err := eng.begin(context.Background(), wf, Options{Trigger: "manual"}, 0)
	if err != nil {
		t.Fatalf("begin top-level: %v", err)
	}
	topCancel()
	eng.unregister(topState.runID)
	res, err := eng.ResumeInterrupted(context.Background(), topState.runID)
	if err != nil {
		t.Fatalf("resume top-level: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("top-level status = %q, want done (%s)", res.Status, res.Error)
	}
}

// Event runs carry ParentRunID for traceability but are roots that take their
// own concurrency slot, so a restart must resume them rather than orphan the
// work. Only real parent-owned children (subflow/batch/ci) are orphaned.
func TestResumeInterruptedResumesEventRuns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := New(st, host.NewSim())
	wf := orphanTestWorkflow()

	state, _, cancel, err := eng.begin(context.Background(), wf, Options{
		Trigger: "event", ParentRunID: "run-source-event",
	}, 0)
	if err != nil {
		t.Fatalf("begin event run: %v", err)
	}
	cancel()
	eng.unregister(state.runID)

	res, err := eng.ResumeInterrupted(context.Background(), state.runID)
	if err != nil {
		t.Fatalf("resume event run: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("event run status = %q, want done (%s)", res.Status, res.Error)
	}
	run, err := st.GetRun(state.runID)
	if err != nil {
		t.Fatalf("get event run: %v", err)
	}
	if run.Status != "done" {
		t.Fatalf("event run status = %q, want done", run.Status)
	}
	if strings.Contains(run.Error, "orphaned by restart") {
		t.Fatalf("event run was orphaned: %q", run.Error)
	}
}
