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

// codeHTTPWorkflow: a code step that fetches through $http and transforms
// the response. The script HTTP call must land on the effect tape like any
// other host call, so replays verify it.
func codeHTTPWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "code-http", Name: "Code HTTP", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "fetch", Type: "code.run", TypeVersion: 1, Name: "Fetch", Config: map[string]any{
				"language": "javascript",
				"source": `
					const resp = $http({method: "GET", url: "https://api.example.com/orders/A-1042"});
					console.log("fetched order", resp.statusCode);
					return {order: resp.body.orderId, amount: resp.body.amount};
				`,
			}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"ok": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "fetch", Port: "main"}},
			{From: graph.Endpoint{Node: "fetch", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}
}

func TestCodeRunHTTPRecordsAndReplaysHermetically(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	recorder := engine.New(st, live)
	res, err := recorder.Start(context.Background(), codeHTTPWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	if live.callCount() != 1 {
		t.Fatalf("live host calls = %d, want 1", live.callCount())
	}
	fetchOut, ok := res.Outputs["fetch"]
	if !ok {
		t.Fatalf("no output for fetch: %v", res.Outputs)
	}
	if fetchOut["main"]["order"] != "A-1042" {
		t.Fatalf("fetch output = %#v", fetchOut["main"])
	}

	// Hermetic replay: the script's $http is served from the tape, never
	// the network (panicHost proves it), and the output matches.
	hermetic := engine.New(st, panicHost{})
	replayed, err := hermetic.Replay(context.Background(), res.RunID, "fetch", engine.ReplayStepHermetic)
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
		t.Fatalf("effects = %d, want 1 ($http must be a recorded effect)", replayed.Effects)
	}
	if len(replayed.Misses) != 0 || len(replayed.Diffs) != 0 {
		t.Fatalf("unexpected drift: misses=%v diffs=%v", replayed.Misses, replayed.Diffs)
	}
	if live.callCount() != 1 {
		t.Fatalf("replay reached the network: %d calls", live.callCount())
	}
}
