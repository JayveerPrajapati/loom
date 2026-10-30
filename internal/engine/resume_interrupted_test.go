package engine

import (
	"context"
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
