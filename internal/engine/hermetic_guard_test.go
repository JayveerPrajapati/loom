package engine_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"loom/internal/cassette"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// gateHTTPHost blocks the first call until release is closed, then behaves like
// the simulated host. It holds a concurrency slot deterministically.
type gateHTTPHost struct {
	flow.Host
	release chan struct{}
	started chan struct{}
	once    sync.Once
}

func (h *gateHTTPHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
	case <-ctx.Done():
		return flow.HTTPResponse{}, ctx.Err()
	}
	return h.Host.HTTP(ctx, req)
}

func notifyWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "notify", Name: "Notify", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{
				"message": "release payment?", "notifyUrl": "https://notify.example.com/hook",
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
		},
	}
}

func approvalThenHTTPWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "approve-http", Name: "Approve HTTP", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{"message": "ok?"}},
			{ID: "pay", Type: "http.request", TypeVersion: 1, Name: "Pay", Config: map[string]any{
				"method": "POST", "url": "https://api.example.com/pay",
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
			{From: graph.Endpoint{Node: "approve", Port: "approved"}, To: graph.Endpoint{Node: "pay", Port: "main"}},
		},
	}
}

// A hermetic replay must never fire the approval notification.
func TestHermeticReplaySuppressesApprovalNotify(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	recordHost := &effectCountingHost{Host: host.NewSim()}
	rec := engine.New(st, recordHost)
	res, err := rec.Start(context.Background(), notifyWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("recorded status = %q, want waiting", res.Status)
	}
	deadline := time.Now().Add(2 * time.Second)
	for recordHost.callCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if recordHost.callCount() == 0 {
		t.Fatal("recorded run did not notify; cannot prove suppression")
	}

	replayHost := &effectCountingHost{Host: host.NewSim()}
	hermetic := engine.New(st, replayHost)
	if _, err := hermetic.Replay(context.Background(), res.RunID, "start", engine.ReplayFromHermetic); err != nil {
		t.Fatalf("hermetic replay: %v", err)
	}
	time.Sleep(250 * time.Millisecond)
	if replayHost.callCount() != 0 {
		t.Fatalf("hermetic replay reached the live host %d time(s)", replayHost.callCount())
	}
}

// Resuming a run created in replay mode must keep replay mode: the downstream
// effect has no tape, so it fails closed instead of calling the network.
func TestResumeKeepsReplayMode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	recordHost := &effectCountingHost{Host: host.NewSim()}
	rec := engine.New(st, recordHost)
	res, err := rec.Start(context.Background(), approvalThenHTTPWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("recorded status = %q, want waiting", res.Status)
	}

	replayHost := &effectCountingHost{Host: host.NewSim()}
	hermetic := engine.New(st, replayHost)
	child, err := hermetic.Replay(context.Background(), res.RunID, "start", engine.ReplayFromHermetic)
	if err != nil {
		t.Fatalf("hermetic replay: %v", err)
	}
	if child.Status != "waiting" {
		t.Fatalf("replay child status = %q, want waiting", child.Status)
	}

	got, err := hermetic.ResumeDecision(context.Background(), child.RunID, "approved", engine.DecisionMeta{}, "")
	if err != nil {
		t.Fatalf("resume decision: %v", err)
	}
	if got.Status == "done" {
		t.Fatalf("hermetic child executed the downstream effect and finished done")
	}
	if replayHost.callCount() != 0 {
		t.Fatalf("resumed hermetic run reached the live host %d time(s)", replayHost.callCount())
	}
	if mode, _, err := st.RunEffects(child.RunID); err != nil || mode != string(cassette.ModeReplay) {
		t.Fatalf("child persisted effects = %q (err=%v), want replay", mode, err)
	}
}

// A hermetic replay of a subflow must serve the recorded child's tapes, never
// the live host.
func TestHermeticSubflowUsesRecordedChild(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	child := &graph.Workflow{
		ID: "child", Name: "Child", Version: 1,
		Nodes: []graph.Node{
			{ID: "in", Type: "trigger.subflow", TypeVersion: 1, Name: "In", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "POST", "url": "https://api.example.com/child",
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "in", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
	}
	parent := &graph.Workflow{
		ID: "parent", Name: "Parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "sub", Type: "flow.subflow", TypeVersion: 1, Name: "Sub", Config: map[string]any{
				"workflowId": "child", "wait": true,
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "sub", Port: "main"}},
		},
	}
	for _, wf := range []*graph.Workflow{child, parent} {
		if err := st.SaveWorkflow(wf); err != nil {
			t.Fatalf("save %s: %v", wf.ID, err)
		}
	}

	recordHost := &effectCountingHost{Host: host.NewSim()}
	rec := engine.New(st, recordHost)
	res, err := rec.Start(context.Background(), parent, engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record parent: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("recorded parent status = %q (%s)", res.Status, res.Error)
	}
	if recordHost.callCount() != 1 {
		t.Fatalf("recorded child calls = %d, want 1", recordHost.callCount())
	}

	replayHost := &effectCountingHost{Host: host.NewSim()}
	hermetic := engine.New(st, replayHost)
	got, err := hermetic.Replay(context.Background(), res.RunID, "start", engine.ReplayFromHermetic)
	if err != nil {
		t.Fatalf("hermetic subflow replay failed: %v", err)
	}
	if got.Status != "done" {
		t.Fatalf("hermetic subflow replay status = %q", got.Status)
	}
	if replayHost.callCount() != 0 {
		t.Fatalf("hermetic subflow replay reached the live host %d time(s)", replayHost.callCount())
	}
}

// A run parked on approval releases its slot; the next queued run must start
// without waiting for a restart or another terminal run.
func TestSuspendAdmitsQueuedRun(t *testing.T) {
	blocked := &gateHTTPHost{
		Host: host.NewSim(), release: make(chan struct{}), started: make(chan struct{}),
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := engine.New(st, blocked)
	wf := &graph.Workflow{
		ID: "suspend-queue", Name: "Suspend queue", Version: 1,
		Settings: map[string]any{"maxParallelRuns": 1},
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/hold",
			}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{"message": "go?"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
			{From: graph.Endpoint{Node: "call", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
		},
	}
	ctx := context.Background()

	first, err := eng.StartAsync(ctx, wf, engine.Options{Trigger: "manual", Input: flow.Item{"n": 1}})
	if err != nil {
		t.Fatalf("start first: %v", err)
	}
	<-blocked.started
	second, err := eng.StartAsync(ctx, wf, engine.Options{Trigger: "manual", Input: flow.Item{"n": 2}})
	if err != nil {
		t.Fatalf("start second: %v", err)
	}
	waitRunStatus(t, st, second, "queued", 3*time.Second)

	close(blocked.release)
	waitRunStatus(t, st, first, "waiting", 5*time.Second)
	waitRunStatus(t, st, second, "waiting", 5*time.Second)
}
