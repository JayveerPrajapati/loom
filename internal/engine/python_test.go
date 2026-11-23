package engine_test

import (
	"context"
	"testing"

	"loom/internal/cassette"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// pythonWorkflow: a code.python step doing real work in a subprocess.
func pythonWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "py-flow", Name: "Python flow", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "py", Type: "code.python", TypeVersion: 1, Name: "Python", Config: map[string]any{
				"source": `
total = item["qty"] * item["price"]
print("computed", total)
return {"total": total, "label": item["sku"] + "-total"}
`,
			}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"seen": "{{ $json.label }}"},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "py", Port: "main"}},
			{From: graph.Endpoint{Node: "py", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}
}

// execCountingHost counts subprocess executions passing through it.
type execCountingHost struct {
	flow.Host
	execs int
}

func (e *execCountingHost) Exec(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	e.execs++
	return e.Host.(flow.Executor).Exec(ctx, req)
}

// TestPythonRecordsAndReplaysHermetically is the money proof for the
// subprocess track: a real Python execution is recorded on the effect tape,
// and hermetic replay verifies it without running Python again (panicHost
// proves the replay never touches an executor).
func TestPythonRecordsAndReplaysHermetically(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := host.NewLive()
	live.EnableSubprocess = true
	counting := &execCountingHost{Host: live}

	recorder := engine.New(st, counting)
	res, err := recorder.Start(context.Background(), pythonWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{"qty": 3, "price": 14, "sku": "SKU-1"},
		Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	if counting.execs != 1 {
		t.Fatalf("subprocess executions = %d, want 1", counting.execs)
	}
	pyOut, ok := res.Outputs["py"]
	if !ok {
		t.Fatalf("no output for py: %v", res.Outputs)
	}
	if pyOut["main"]["total"] != float64(42) || pyOut["main"]["label"] != "SKU-1-total" {
		t.Fatalf("py output = %#v", pyOut["main"])
	}

	// Hermetic replay from the tape: no subprocess, no network, same output.
	hermetic := engine.New(st, panicHost{})
	replayed, err := hermetic.Replay(context.Background(), res.RunID, "py", engine.ReplayStepHermetic)
	if err != nil {
		t.Fatalf("hermetic replay: %v", err)
	}
	if replayed.Status != "done" {
		t.Fatalf("replay status = %q", replayed.Status)
	}
	if !replayed.Match {
		t.Fatalf("replayed output hash %s != recorded %s", replayed.ActualHash, replayed.RecordedHash)
	}
	if replayed.Effects != 1 {
		t.Fatalf("effects = %d, want 1 (the execution must be a recorded effect)", replayed.Effects)
	}
	if len(replayed.Misses) != 0 || len(replayed.Diffs) != 0 {
		t.Fatalf("unexpected drift: misses=%v diffs=%v", replayed.Misses, replayed.Diffs)
	}
	if counting.execs != 1 {
		t.Fatalf("replay re-ran the subprocess: %d executions", counting.execs)
	}
}
