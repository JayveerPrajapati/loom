package engine

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"loom/internal/cassette"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

type rateTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *rateTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *rateTestClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if target := c.now.Add(d); target.After(c.now) {
		c.now = target
	}
}

type rateTestHost struct {
	flow.Host
	clock *rateTestClock
}

func (h *rateTestHost) Sleep(_ context.Context, d time.Duration) error {
	h.clock.advance(d)
	return nil
}

func TestMergeItemsAppend(t *testing.T) {
	first := flow.Item{"a": 1}
	second := flow.Item{"b": 2}

	got := mergeItems([]flow.Item{first, second}, "append")

	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("append output = %#v, want an items array", got)
	}
	if len(items) != 2 {
		t.Fatalf("append output has %d items, want 2", len(items))
	}
	head, ok := items[0].(map[string]any)
	if !ok || head["a"] != 1 {
		t.Fatalf("first appended item = %#v, want a=1", items[0])
	}
	tail, ok := items[1].(map[string]any)
	if !ok || tail["b"] != 2 {
		t.Fatalf("second appended item = %#v, want b=2", items[1])
	}
}

func TestMergeItemsByIndex(t *testing.T) {
	first := flow.Item{"items": []any{
		map[string]any{"x": 1, "shared": "first"},
		map[string]any{"x": 2},
	}}
	second := flow.Item{"items": []any{
		map[string]any{"y": 9, "shared": "second"},
	}}

	got := mergeItems([]flow.Item{first, second}, "byIndex")

	items, ok := got["items"].([]any)
	if !ok {
		t.Fatalf("byIndex output = %#v, want an items array", got)
	}
	if len(items) != 2 {
		t.Fatalf("byIndex output has %d items, want 2", len(items))
	}
	head, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("merged item 0 = %#v, want an object", items[0])
	}
	if head["x"] != 1 || head["y"] != 9 || head["shared"] != "second" {
		t.Fatalf("merged item 0 = %#v, want x=1 y=9 shared=second", head)
	}
	tail, ok := items[1].(map[string]any)
	if !ok || tail["x"] != 2 {
		t.Fatalf("merged item 1 = %#v, want x=2", items[1])
	}
}

func TestMergeItemsByIndexFallsBackToShallow(t *testing.T) {
	first := flow.Item{"onlyA": 1, "v": "A"}
	second := flow.Item{"onlyB": 1, "v": "B"}

	got := mergeItems([]flow.Item{first, second}, "byIndex")

	if got["onlyA"] != 1 || got["onlyB"] != 1 || got["v"] != "B" {
		t.Fatalf("fallback merge = %#v, want shallow later-wins", got)
	}
}

func TestMergeItemsUnknownModeKeepsShallow(t *testing.T) {
	first := flow.Item{"onlyA": 1, "v": "A"}
	second := flow.Item{"onlyB": 1, "v": "B"}

	got := mergeItems([]flow.Item{first, second}, "bogus")

	if got["onlyA"] != 1 || got["onlyB"] != 1 || got["v"] != "B" {
		t.Fatalf("unknown-mode merge = %#v, want shallow later-wins", got)
	}
}

// TestRateGateConcurrentWindows: 20 concurrent callers must never reserve more
// than 5 slots in any rolling minute. onRateRecord captures the exact
// reservation timestamp, so the check is not skewed by goroutine scheduling.
func TestRateGateConcurrentWindows(t *testing.T) {
	clock := &rateTestClock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	eng := &Engine{
		Host:        &rateTestHost{clock: clock},
		Now:         clock.Now,
		active:      map[string]context.CancelFunc{},
		rateWindows: map[string][]time.Time{},
	}
	var mu sync.Mutex
	admitted := make([]time.Time, 0, 20)
	eng.onRateRecord = func(_ string, at time.Time) {
		mu.Lock()
		admitted = append(admitted, at)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := eng.acquireRateSlot(context.Background(), "wf/node", 5); err != nil {
				t.Errorf("acquire: %v", err)
			}
		}()
	}
	wg.Wait()

	if len(admitted) != 20 {
		t.Fatalf("admitted %d calls, want 20", len(admitted))
	}
	sort.Slice(admitted, func(i, j int) bool { return admitted[i].Before(admitted[j]) })
	for i := range admitted {
		windowEnd := admitted[i].Add(time.Minute)
		count := 0
		for _, at := range admitted {
			if !at.Before(admitted[i]) && at.Before(windowEnd) {
				count++
			}
		}
		if count > 5 {
			t.Fatalf("%d admissions between %s and %s (limit 5)", count, admitted[i], windowEnd)
		}
	}
}

// TestSubflowAsyncStaysSynchronousUnderReplay: a replay must execute the child
// in the caller so effects are recorded/replayed in order. The returned item
// proves the child actually ran (an async call would return the input verbatim).
func TestSubflowAsyncStaysSynchronousUnderReplay(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	child := &graph.Workflow{
		ID: "child-sync", Name: "Child", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set", Config: map[string]any{
				"values": map[string]any{"childRan": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}},
		},
	}
	if err := st.SaveWorkflow(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	parent := &graph.Workflow{
		ID: "parent-sync", Name: "Parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "sub", Type: "flow.subflow", TypeVersion: 1, Name: "Sub", Config: map[string]any{
				"workflowId": child.ID, "wait": true,
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "sub", Port: "main"}},
		},
	}

	eng := New(st, host.NewSim())
	recorded, err := eng.Start(context.Background(), parent, Options{
		Trigger: "manual", Input: flow.Item{"seed": 1}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record parent: %v", err)
	}
	if recorded.Status != "done" {
		t.Fatalf("recorded parent status = %s, want done", recorded.Status)
	}

	runner := &subflowRunner{
		engine: eng, parentRunID: recorded.RunID, parentNodeID: "sub",
		cassetteRunID: recorded.RunID, effects: cassette.ModeReplay,
	}
	item, err := runner.RunSubflowAsync(context.Background(), child.ID, flow.Item{"seed": 1})
	if err != nil {
		t.Fatalf("async replay: %v", err)
	}
	if item["childRan"] != true {
		t.Fatalf("replay returned %#v, want the synchronously executed child output", item)
	}
}
