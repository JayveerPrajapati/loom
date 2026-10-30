package engine_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
)

const (
	perfNodes  = 100
	perfBudget = 5 * time.Second
)

func perfWorkflow() *graph.Workflow {
	wf := &graph.Workflow{ID: "perf", Name: "Perf", Version: 1}
	wf.Nodes = append(wf.Nodes, graph.Node{
		ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start",
		Config: map[string]any{},
	})
	for i := 0; i < perfNodes; i++ {
		id := fmt.Sprintf("n%d", i)
		wf.Nodes = append(wf.Nodes, graph.Node{
			ID: id, Type: "data.set", TypeVersion: 1, Name: id,
			Config: map[string]any{
				"values": map[string]any{"step": i},
			},
		})
		from := "start"
		if i > 0 {
			from = fmt.Sprintf("n%d", i-1)
		}
		wf.Edges = append(wf.Edges, graph.Edge{
			From: graph.Endpoint{Node: from, Port: "main"},
			To:   graph.Endpoint{Node: id, Port: "main"},
		})
	}
	return wf
}

func TestPerformanceBudget(t *testing.T) {
	eng := newEngine(t)
	started := time.Now()
	res, err := eng.Start(context.Background(), perfWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{"seed": true},
	})
	runElapsed := time.Since(started)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	if runElapsed > perfBudget {
		t.Fatalf("%d-node run took %s, budget %s", perfNodes, runElapsed, perfBudget)
	}

	started = time.Now()
	replay, err := eng.Replay(context.Background(), res.RunID, "", engine.ReplayFull)
	replayElapsed := time.Since(started)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Match {
		t.Fatal("full replay did not match")
	}
	if replayElapsed > perfBudget {
		t.Fatalf("%d-node replay took %s, budget %s", perfNodes, replayElapsed, perfBudget)
	}
	t.Logf("%d nodes: run=%s replay=%s", perfNodes, runElapsed, replayElapsed)
}
