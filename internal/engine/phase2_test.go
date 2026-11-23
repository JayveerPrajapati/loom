package engine_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

type phase2Clock struct {
	mu  sync.Mutex
	now time.Time
}

func newPhase2Clock() *phase2Clock {
	return &phase2Clock{now: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *phase2Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *phase2Clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if target := c.now.Add(d); target.After(c.now) {
		c.now = target
	}
}

// phase2ClockHost advances the engine's fake clock in Sleep instead of
// blocking, so rate-limit windows can be exercised without wall-clock waits.
type phase2ClockHost struct {
	flow.Host
	clock *phase2Clock

	mu     sync.Mutex
	starts []time.Time
	slept  []time.Duration
}

func (h *phase2ClockHost) Sleep(ctx context.Context, d time.Duration) error {
	h.mu.Lock()
	if d > 0 {
		h.slept = append(h.slept, d)
	}
	h.mu.Unlock()
	h.clock.advance(d)
	return nil
}

func (h *phase2ClockHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.mu.Lock()
	h.starts = append(h.starts, h.clock.Now())
	h.mu.Unlock()
	return h.Host.HTTP(ctx, req)
}

// phase2GateHost delays long sleeps until released so a child run stays in
// flight while the parent returns.
type phase2GateHost struct {
	flow.Host
	entered chan struct{}
	release chan struct{}
}

func (h *phase2GateHost) Sleep(ctx context.Context, d time.Duration) error {
	if d >= time.Second {
		select {
		case h.entered <- struct{}{}:
		default:
		}
		select {
		case <-h.release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return h.Host.Sleep(ctx, d)
}

func TestSubflowWaitFalseReturnsImmediately(t *testing.T) {
	gate := &phase2GateHost{
		Host:    host.NewSim(),
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	child := &graph.Workflow{
		ID: "slow-child", Name: "Slow child", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "hold", Type: "flow.wait", TypeVersion: 1, Name: "Hold", Config: map[string]any{"seconds": 5.0}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "hold", Port: "main"}},
		},
	}
	if err := st.SaveWorkflow(child); err != nil {
		t.Fatalf("save child: %v", err)
	}
	parent := &graph.Workflow{
		ID: "async-parent", Name: "Async parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "sub", Type: "flow.subflow", TypeVersion: 1, Name: "Sub", Config: map[string]any{
				"workflowId": "slow-child", "wait": false,
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "sub", Port: "main"}},
		},
	}

	eng := engine.New(st, gate)
	started := time.Now()
	res, err := eng.Start(context.Background(), parent, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start parent: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("wait:false parent blocked for %v", elapsed)
	}
	if res.Status != "done" {
		t.Fatalf("parent status = %s, want done", res.Status)
	}

	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("slow child never started")
	}
	children, err := st.ListChildRuns(res.RunID, 10)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children) != 1 {
		t.Fatalf("child runs = %d, want 1", len(children))
	}
	if children[0].Status != "running" {
		t.Fatalf("child status while parent done = %s, want running", children[0].Status)
	}

	close(gate.release)
	waitRunStatus(t, st, children[0].ID, "done", 5*time.Second)
}

func approvalPhase2Workflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "ci-approval", Name: "CI approval", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{"message": "ok?"}},
			{ID: "yes", Type: "data.set", TypeVersion: 1, Name: "Yes", Config: map[string]any{
				"values": map[string]any{"ok": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
			{From: graph.Endpoint{Node: "approve", Port: "approved"}, To: graph.Endpoint{Node: "yes", Port: "main"}},
		},
	}
}

// TestVerifyAgainstRunAutoApprovesApprovals: a CI/shadow replay must not park
// on an approval node; it auto-approves and reaches a verdict.
func TestVerifyAgainstRunAutoApprovesApprovals(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := engine.New(st, host.NewSim())

	source, err := eng.Start(context.Background(), approvalPhase2Workflow(), engine.Options{
		Trigger: "manual", AutoApprove: true,
	})
	if err != nil {
		t.Fatalf("source run: %v", err)
	}
	if source.Status != "done" {
		t.Fatalf("source status = %s, want done", source.Status)
	}

	verdict, err := eng.VerifyAgainstRun(context.Background(), source.RunID, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verdict.RunID == "" {
		t.Fatal("verdict has no CI run id")
	}
	ciRun, err := st.GetRun(verdict.RunID)
	if err != nil {
		t.Fatalf("get ci run: %v", err)
	}
	if ciRun.Status == "waiting" || ciRun.Status == "queued" {
		t.Fatalf("ci run status = %s, want a terminal verdict", ciRun.Status)
	}
	if ciRun.Trigger != "ci" {
		t.Fatalf("ci run trigger = %s, want ci", ciRun.Trigger)
	}
	waiting, err := st.CountRunsByStatus("waiting")
	if err != nil {
		t.Fatalf("count waiting: %v", err)
	}
	if waiting != 0 {
		t.Fatalf("waiting runs = %d, want 0", waiting)
	}
}
