package server

import (
	"fmt"
	"sort"
	"testing"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

func eventWorkflow(id, name string) *graph.Workflow {
	return &graph.Workflow{
		ID: id, Name: id, Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.event", TypeVersion: 1, Name: "Start", Config: map[string]any{"event": name}},
		},
	}
}

func newIndexAPI(t *testing.T) (*api, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	s := &api{deps: Deps{Store: st, Engine: engine.New(st, host.NewSim())}}
	s.rebuildEventTriggers()
	return s, st
}

func assertEventIndex(t *testing.T, s *api, name string, want ...string) {
	t.Helper()
	got := s.workflowsForEvent(name)
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("index[%q] = %v, want %v", name, got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("index[%q] = %v, want %v", name, got, want)
		}
	}
}

// The index tracks create, update (trigger renamed), publish (current doc
// drives the index), delete, and a fresh build over the same store
// (restart).
func TestEventTriggerIndexLifecycle(t *testing.T) {
	s, st := newIndexAPI(t)

	if err := st.SaveWorkflow(eventWorkflow("wf-a", "alpha")); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	assertEventIndex(t, s, "alpha", "wf-a")
	assertEventIndex(t, s, "beta")

	// Update: rename the trigger. The old event loses the entry.
	renamed := eventWorkflow("wf-a", "beta")
	if err := st.SaveWorkflow(renamed); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	assertEventIndex(t, s, "alpha")
	assertEventIndex(t, s, "beta", "wf-a")

	// Publish freezes a version but leaves the current document (and its
	// triggers) alone.
	if _, err := st.PublishWorkflow(renamed); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	assertEventIndex(t, s, "beta", "wf-a")

	// A second workflow listening to the same event.
	if err := st.SaveWorkflow(eventWorkflow("wf-b", "beta")); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	assertEventIndex(t, s, "beta", "wf-a", "wf-b")

	// Delete removes the entry; the other workflow keeps receiving.
	if err := st.DeleteWorkflow("wf-a"); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	assertEventIndex(t, s, "beta", "wf-b")

	// Restart: a fresh API over the same store rebuilds the same index.
	s2 := &api{deps: s.deps}
	s2.rebuildEventTriggers()
	assertEventIndex(t, s2, "beta", "wf-b")
	assertEventIndex(t, s2, "alpha")
}

// onEvent must not start runs for workflows that no longer exist or no
// longer listen, and must not touch unrelated workflows at all: with only
// wf-beta listening, an unrelated event starts nothing.
func TestOnEventSkipsUnrelatedAndDeletedWorkflows(t *testing.T) {
	s, st := newIndexAPI(t)
	if err := st.SaveWorkflow(eventWorkflow("wf-listen", "wanted")); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveWorkflow(eventWorkflow("wf-other", "other")); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()

	// An event nobody listens to starts nothing.
	s.onEvent(flow.Event{Name: "unheard", Item: flow.Item{}})
	if runs, err := st.ListRuns("", 10); err != nil || len(runs) != 0 {
		t.Fatalf("unheard event started %d run(s) (err=%v)", len(runs), err)
	}

	// A deleted workflow leaves no stale entry behind.
	if err := st.DeleteWorkflow("wf-listen"); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	s.onEvent(flow.Event{Name: "wanted", Item: flow.Item{}})
	if runs, err := st.ListRuns("", 10); err != nil || len(runs) != 0 {
		t.Fatalf("event for a deleted workflow started %d run(s) (err=%v)", len(runs), err)
	}
}

// A stale index entry (workflow edited to stop listening after the last
// rebuild) is neutralized by the document re-check in onEvent.
func TestOnEventRechecksLoadedWorkflow(t *testing.T) {
	s, st := newIndexAPI(t)
	if err := st.SaveWorkflow(eventWorkflow("wf-stale", "stale-evt")); err != nil {
		t.Fatal(err)
	}
	s.rebuildEventTriggers()
	// The workflow is edited directly in the store, bypassing the mutation
	// path that would rebuild the index.
	edited := eventWorkflow("wf-stale", "renamed-evt")
	if err := st.SaveWorkflow(edited); err != nil {
		t.Fatal(err)
	}

	s.onEvent(flow.Event{Name: "stale-evt", Item: flow.Item{}})
	if runs, err := st.ListRuns("", 10); err != nil || len(runs) != 0 {
		t.Fatalf("stale index entry started %d run(s) (err=%v)", len(runs), err)
	}
	// The renamed trigger still works once the index catches up.
	s.rebuildEventTriggers()
	s.onEvent(flow.Event{Name: "renamed-evt", Item: flow.Item{}})
	runs, err := st.ListRuns("", 10)
	if err != nil || len(runs) != 1 {
		t.Fatalf("renamed trigger started %d run(s) (err=%v), want 1", len(runs), err)
	}
}

// TestOnEventDispatchIndependentOfUnrelatedWorkflows (P2-10): with 200
// workflows listening to other events, dispatching one event must start only
// the matching workflow's run. The in-memory index resolves the event name
// to its listeners directly, so the unrelated volume neither runs nor costs
// a scan — anything else would start 200 runs here.
func TestOnEventDispatchIndependentOfUnrelatedWorkflows(t *testing.T) {
	s, st := newIndexAPI(t)
	if err := st.SaveWorkflow(eventWorkflow("wf-wanted", "wanted")); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if err := st.SaveWorkflow(eventWorkflow(fmt.Sprintf("wf-noise-%03d", i), fmt.Sprintf("noise-%03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	s.rebuildEventTriggers()

	s.onEvent(flow.Event{Name: "wanted", Item: flow.Item{}})

	runs, err := st.ListRuns("", 500)
	if err != nil {
		t.Fatalf("list runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("event dispatch started %d run(s), want exactly the 1 matching workflow", len(runs))
	}
	if runs[0].WorkflowID != "wf-wanted" {
		t.Fatalf("started run for %s, want wf-wanted", runs[0].WorkflowID)
	}
}
