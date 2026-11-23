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

// paginationWorkflow walks the sim host's cursor-paginated /paged endpoint.
func paginationWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "http-pagination", Name: "HTTP pagination", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/paged",
				"pagination": map[string]any{
					"mode": "cursor", "cursorPath": "nextCursor",
				},
			}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"ok": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
			{From: graph.Endpoint{Node: "call", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}
}

// The whole point of pagination-as-effects: a recorded two-page walk
// replays hermetically with both pages verified and no network.
func TestPaginationRecordsPerPageAndReplaysHermetically(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	recorder := engine.New(st, live)
	res, err := recorder.Start(context.Background(), paginationWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q (%s)", res.Status, res.Error)
	}
	if live.callCount() != 2 {
		t.Fatalf("live host calls = %d, want 2 pages", live.callCount())
	}
	// The recorded output must carry all three items from both pages.
	callOut, ok := res.Outputs["call"]
	if !ok {
		t.Fatalf("no output for the call node: %v", res.Outputs)
	}
	if got, _ := callOut["main"]["itemsCount"].(float64); got != 3 {
		t.Fatalf("itemsCount = %v, want 3", got)
	}

	hermetic := engine.New(st, panicHost{})
	replayed, err := hermetic.Replay(context.Background(), res.RunID, "call", engine.ReplayStepHermetic)
	if err != nil {
		t.Fatalf("hermetic replay: %v", err)
	}
	if replayed.Status != "done" {
		t.Fatalf("replay status = %q", replayed.Status)
	}
	if !replayed.Match {
		t.Fatalf("replayed output hash %s != recorded %s", replayed.ActualHash, replayed.RecordedHash)
	}
	// One tape entry per page: replays verify each request.
	if replayed.Effects != 2 {
		t.Fatalf("effects = %d, want 2", replayed.Effects)
	}
	if len(replayed.Misses) != 0 || len(replayed.Diffs) != 0 {
		t.Fatalf("unexpected drift: misses=%v diffs=%v", replayed.Misses, replayed.Diffs)
	}
	if live.callCount() != 2 {
		t.Fatalf("replay reached the network: %d calls", live.callCount())
	}
}
