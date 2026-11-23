package server

import (
	"fmt"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// The boundary is exclusive: 99 runs allow one more descendant, 100 refuse.
func TestEventBudgetAllowsBoundary(t *testing.T) {
	if !eventBudgetAllows(eventRunBudget - 1) {
		t.Fatalf("tree with %d runs should be allowed another descendant", eventRunBudget-1)
	}
	if eventBudgetAllows(eventRunBudget) {
		t.Fatalf("tree with %d runs should be refused another descendant", eventRunBudget)
	}
}

// A saturated root must not start a subscriber run: onEvent returns before it
// reaches Engine.StartAsync.
func TestSaturatedRootStartsNoEventRun(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	root := "run-root-saturated"
	for i := 0; i < eventRunBudget; i++ {
		if err := st.CreateRun(store.Run{
			ID: fmt.Sprintf("seed-%d", i), WorkflowID: "wf-other", WorkflowVersion: "v1",
			Status: "done", Trigger: "event", StartedAt: time.Now().UTC(), RootRunID: root,
		}); err != nil {
			t.Fatalf("seed run %d: %v", i, err)
		}
	}

	wf := &graph.Workflow{
		ID: "wf-event", Name: "Event", Version: 1,
		Nodes: []graph.Node{{
			ID: "start", Type: "trigger.event", TypeVersion: 1, Name: "Start",
			Config: map[string]any{"event": "saturated"},
		}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	s := &api{deps: Deps{Store: st, Engine: engine.New(st, host.NewSim())}}
	s.onEvent(flow.Event{Name: "saturated", RootRunID: root, SourceRunID: root})

	count, err := st.CountRunsByRoot(root)
	if err != nil {
		t.Fatalf("count by root: %v", err)
	}
	if count != eventRunBudget {
		t.Fatalf("saturated root has %d runs, want %d (a new run was started)", count, eventRunBudget)
	}
}
