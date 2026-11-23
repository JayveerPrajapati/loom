package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"loom/internal/cassette"
	"loom/internal/egress"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// panicHost proves hermetic replay never reaches the outside world.
type panicHost struct{}

func (panicHost) HTTP(context.Context, flow.HTTPRequest) (flow.HTTPResponse, error) {
	panic("hermetic replay called HTTP")
}

func (panicHost) Query(context.Context, string, string) ([]flow.Item, error) {
	panic("hermetic replay called Query")
}

func (panicHost) Slack(context.Context, string, string, string) error {
	panic("hermetic replay called Slack")
}

func (panicHost) Sleep(context.Context, time.Duration) error { return nil }
func (panicHost) Now() time.Time                             { return time.Unix(0, 0).UTC() }

type effectCountingHost struct {
	flow.Host
	calls atomic.Int64
}

func (h *effectCountingHost) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.calls.Add(1)
	return h.Host.HTTP(ctx, req)
}

func (h *effectCountingHost) callCount() int64 {
	return h.calls.Load()
}

func effectWorkflow() *graph.Workflow {
	return &graph.Workflow{
		ID: "effects", Name: "Effects", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://api.example.com/orders/A-1",
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

func TestRecordModeWritesEffectTapes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	eng := engine.New(st, live)
	res, err := eng.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %q, want done (%s)", res.Status, res.Error)
	}
	if live.callCount() != 1 {
		t.Fatalf("live host calls = %d, want 1", live.callCount())
	}

	events, err := st.ListEvents(res.RunID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	tapes := map[string]int{}
	for _, ev := range events {
		if ev.Type == "node.effects" {
			count, _ := ev.Data["count"].(float64)
			tapes[ev.NodeID] = int(count)
			if hash, _ := ev.Data["blob"].(string); hash == "" {
				t.Fatalf("node %s tape has no blob", ev.NodeID)
			}
		}
	}
	if tapes["call"] != 1 {
		t.Fatalf("call tape count = %d, want 1 (tapes=%v)", tapes["call"], tapes)
	}
	if count, ok := tapes["done"]; !ok || count != 0 {
		t.Fatalf("done tape = %d (present=%v), want an explicit empty tape", count, ok)
	}
}

func TestHermeticStepReplayUsesTapeNotNetwork(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	recorder := engine.New(st, live)
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	hermetic := engine.New(st, panicHost{})
	replayed, err := hermetic.Replay(context.Background(), res.RunID, "call", engine.ReplayStepHermetic)
	if err != nil {
		t.Fatalf("hermetic replay: %v", err)
	}
	if replayed.Status != "done" {
		t.Fatalf("status = %q, want done", replayed.Status)
	}
	if !replayed.Match {
		t.Fatalf("replayed output hash %s != recorded %s", replayed.ActualHash, replayed.RecordedHash)
	}
	if replayed.Effects != 1 {
		t.Fatalf("effects = %d, want 1", replayed.Effects)
	}
	if len(replayed.Misses) != 0 || len(replayed.Diffs) != 0 {
		t.Fatalf("unexpected drift: misses=%v diffs=%v", replayed.Misses, replayed.Diffs)
	}
	if live.callCount() != 1 {
		t.Fatalf("replay reached the network: %d calls", live.callCount())
	}
}

func TestHermeticFromReplayRunsDescendantsOffline(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	recorder := engine.New(st, live)
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	hermetic := engine.New(st, panicHost{})
	child, err := hermetic.Replay(context.Background(), res.RunID, "call", engine.ReplayFromHermetic)
	if err != nil {
		t.Fatalf("from-hermetic replay: %v", err)
	}
	if child.Status != "done" {
		t.Fatalf("child status = %q (%s)", child.Status, child.RunID)
	}
	if got := child.Outputs["done"]["main"]["ok"]; got != true {
		t.Fatalf("descendant output = %v, want true", got)
	}
	if live.callCount() != 1 {
		t.Fatalf("hermetic child reached the network: %d calls", live.callCount())
	}
}

func TestFullReplayFailsWhenEffectTapeMissing(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	recorder := engine.New(st, live)
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	events, err := st.ListEvents(res.RunID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	tapeHash := ""
	for _, ev := range events {
		if ev.Type == "node.effects" && ev.NodeID == "call" {
			tapeHash, _ = ev.Data["blob"].(string)
		}
	}
	if tapeHash == "" {
		t.Fatal("recorded run has no effect tape")
	}
	if err := os.Remove(filepath.Join(dir, "blobs", tapeHash[:2], tapeHash)); err != nil {
		t.Fatalf("remove tape: %v", err)
	}

	hermetic := engine.New(st, panicHost{})
	full, err := hermetic.Replay(context.Background(), res.RunID, "", engine.ReplayFull)
	if err == nil {
		t.Fatalf("full replay matched with a missing tape: %+v", full)
	}
	if !strings.Contains(err.Error(), "effect tape missing") {
		t.Fatalf("error = %v, want an effect tape missing error", err)
	}
}

func TestHermeticReplayFailsClosedWithoutTape(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Recorded without Effects, so no tapes exist.
	recorder := engine.New(st, host.NewSim())
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	hermetic := engine.New(st, panicHost{})
	_, err = hermetic.Replay(context.Background(), res.RunID, "call", engine.ReplayStepHermetic)
	if err == nil {
		t.Fatal("want a fail-closed error for a run without tapes")
	}
}

func TestOnRunFinishedFiresAfterTerminalState(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eng := engine.New(st, host.NewSim())
	done := make(chan string, 1)
	eng.OnRunFinished = func(_ context.Context, runID string) { done <- runID }

	res, err := eng.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case id := <-done:
		if id != res.RunID {
			t.Fatalf("hook saw %s, want %s", id, res.RunID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnRunFinished did not fire")
	}
}

func TestVerifyAgainstRunPassesIdenticalGraph(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	recorder := engine.New(st, host.NewSim())
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	verifier := engine.New(st, panicHost{})
	verdict, err := verifier.VerifyAgainstRun(context.Background(), res.RunID, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !verdict.Passed {
		t.Fatalf("verdict failed: %s nodes=%+v", verdict.Error, verdict.Nodes)
	}
	for _, node := range verdict.Nodes {
		if node.Status != "match" {
			t.Fatalf("node %s status = %q, want match", node.NodeID, node.Status)
		}
	}
}

func TestVerifyAgainstRunDetectsChangedOutput(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	recorder := engine.New(st, host.NewSim())
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	candidate := effectWorkflow()
	for i := range candidate.Nodes {
		if candidate.Nodes[i].ID == "done" {
			candidate.Nodes[i].Config = map[string]any{"values": map[string]any{"ok": false}}
		}
	}

	verifier := engine.New(st, panicHost{})
	verdict, err := verifier.VerifyAgainstRun(context.Background(), res.RunID, candidate)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verdict.Passed {
		t.Fatal("want a failing verdict for a changed output")
	}
	var done *engine.CINode
	for i := range verdict.Nodes {
		if verdict.Nodes[i].NodeID == "done" {
			done = &verdict.Nodes[i]
		}
	}
	if done == nil || done.Status != "drift" {
		t.Fatalf("done verdict = %+v, want drift", done)
	}
}

func TestVerifyAgainstRunFailsClosedWithoutTapes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	recorder := engine.New(st, host.NewSim())
	res, err := recorder.Start(context.Background(), effectWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}

	verifier := engine.New(st, panicHost{})
	verdict, err := verifier.VerifyAgainstRun(context.Background(), res.RunID, nil)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verdict.Passed {
		t.Fatal("want a failing verdict when the run has no recorded effects")
	}
}

// egressDenyWorkflow is effectWorkflow plus a workflow egress allowlist that
// does not cover the http.request node's destination.
func egressDenyWorkflow() *graph.Workflow {
	wf := effectWorkflow()
	wf.Egress = &egress.WorkflowPolicy{Allowlist: []string{"https://allowed.example.com"}}
	return wf
}

// A full recorded run must enforce the workflow egress allowlist even though
// the effect recorder re-wraps the host: the recorder wraps the egress-wrapped
// host, not the raw engine host (regression: effectScope once used e.Host,
// silently bypassing the policy for every live run).
func TestWorkflowEgressEnforcedInRecordMode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	eng := engine.New(st, live)
	res, err := eng.Start(context.Background(), egressDenyWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed (error=%s)", res.Status, res.Error)
	}
	if !strings.Contains(res.Error, "workflow's egress allowlist") {
		t.Fatalf("run error = %q, want a workflow egress denial", res.Error)
	}
	if live.callCount() != 0 {
		t.Fatalf("denied request reached the live host: %d calls", live.callCount())
	}

	// The allowed destination still passes through the recorder.
	allowed := effectWorkflow()
	allowed.Egress = &egress.WorkflowPolicy{Allowlist: []string{"https://api.example.com"}}
	res2, err := eng.Start(context.Background(), allowed, engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("start allowed: %v", err)
	}
	if res2.Status != "done" {
		t.Fatalf("allowed status = %q, want done (error=%s)", res2.Status, res2.Error)
	}
	if live.callCount() != 1 {
		t.Fatalf("live host calls = %d, want 1", live.callCount())
	}
}

// The default (no effects) branch must enforce the policy too.
func TestWorkflowEgressEnforcedInDefaultMode(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	eng := engine.New(st, live)
	res, err := eng.Start(context.Background(), egressDenyWorkflow(), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %q, want failed (error=%s)", res.Status, res.Error)
	}
	if !strings.Contains(res.Error, "workflow's egress allowlist") {
		t.Fatalf("run error = %q, want a workflow egress denial", res.Error)
	}
	if live.callCount() != 0 {
		t.Fatalf("denied request reached the live host: %d calls", live.callCount())
	}
}

// The approval notification webhook honors the workflow egress allowlist:
// delivery to an unlisted destination is denied (recorded as webhookError)
// instead of silently leaving the engine host.
func TestApprovalNotifyEnforcesWorkflowEgress(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	eng := engine.New(st, live)
	wf := &graph.Workflow{
		ID: "notify-egress", Name: "Notify Egress", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{
				"message": "release payment?", "notifyUrl": "https://notify.example.com/hook",
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
		},
		Egress: &egress.WorkflowPolicy{Allowlist: []string{"https://elsewhere.example.com"}},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", res.Status)
	}

	var notified *store.Event
	for i := 0; i < 40 && notified == nil; i++ {
		events, err := st.ListEvents(res.RunID)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		for _, ev := range events {
			if ev.Type == "approval.notified" {
				notified = &ev
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if notified == nil {
		t.Fatal("approval.notified event never appeared")
	}
	if webhookErr, _ := notified.Data["webhookError"].(string); !strings.Contains(webhookErr, "egress allowlist") {
		t.Fatalf("webhookError = %q, want a workflow egress denial", webhookErr)
	}
	if live.callCount() != 0 {
		t.Fatalf("denied notification reached the live host: %d calls", live.callCount())
	}
}

// A live replay re-run (ReplayStepExecute) enforces the run's workflow
// egress policy: replaying with an item override that renders the node's
// templated URL at an unlisted destination must be denied (D-P2: the replay
// executes the run's own resolved doc — its snapshot — so the policy rides
// the same doc).
func TestReplayStepExecuteEnforcesWorkflowEgress(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	live := &effectCountingHost{Host: host.NewSim()}
	eng := engine.New(st, live)
	wf := &graph.Workflow{
		ID: "template-url", Name: "Template URL", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "call", Type: "http.request", TypeVersion: 1, Name: "Call", Config: map[string]any{
				"method": "GET", "url": "https://{{host}}/orders",
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "call", Port: "main"}},
		},
		Egress: &egress.WorkflowPolicy{Allowlist: []string{"https://api.example.com"}},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual", Input: flow.Item{"host": "api.example.com"}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if res.Status != "done" || live.callCount() != 1 {
		t.Fatalf("record: status=%s calls=%d (error=%s)", res.Status, live.callCount(), res.Error)
	}

	denied := flow.Item{"host": "evil.example.net"}
	if _, err := eng.Replay(context.Background(), res.RunID, "call", engine.ReplayStepExecute, &denied); err == nil || !strings.Contains(err.Error(), "egress allowlist") {
		t.Fatalf("replay error = %v, want a workflow egress denial", err)
	}
	if live.callCount() != 1 {
		t.Fatalf("denied replay reached the live host: %d calls", live.callCount())
	}

	allowed := flow.Item{"host": "api.example.com"}
	if _, err := eng.Replay(context.Background(), res.RunID, "call", engine.ReplayStepExecute, &allowed); err != nil {
		t.Fatalf("allowed replay: %v", err)
	}
	if live.callCount() != 2 {
		t.Fatalf("live host calls = %d, want 2", live.callCount())
	}
}
