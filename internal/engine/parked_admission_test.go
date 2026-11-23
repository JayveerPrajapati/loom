package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"loom/internal/cassette"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/host"
	"loom/internal/store"
)

// gateSleepHost blocks flow.wait until the test releases it, so a run can hold
// a concurrency slot deterministically.
type gateSleepHost struct {
	flow.Host
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (h *gateSleepHost) Sleep(ctx context.Context, _ time.Duration) error {
	h.once.Do(func() { close(h.started) })
	select {
	case <-h.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func waitStarted(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("holder run did not start")
	}
}

// A from-replay that parks behind the cap must fail closed: no host call, a
// cancelled child row, and the exported conflict error.
func TestParkedFromReplayFailsClosed(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	recordHost := &effectCountingHost{Host: host.NewSim()}
	recorder := engine.New(st, recordHost)
	wf := effectWorkflow()
	wf.Settings = map[string]any{"maxParallelRuns": 1.0}
	recorded, err := recorder.Start(context.Background(), wf, engine.Options{
		Trigger: "manual", Input: flow.Item{}, Effects: cassette.ModeRecord,
	})
	if err != nil {
		t.Fatalf("record run: %v", err)
	}
	if recorded.Status != "done" {
		t.Fatalf("record status = %q (%s)", recorded.Status, recorded.Error)
	}

	gate := &gateSleepHost{
		Host:    host.NewSim(),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	holderEngine := engine.New(st, gate)
	holderWF := waitingWorkflow(wf.ID)
	holderWF.Settings = map[string]any{"maxParallelRuns": 1.0}
	holderCtx, cancelHolder := context.WithCancel(context.Background())
	defer cancelHolder()
	holderID, err := holderEngine.StartAsync(holderCtx, holderWF, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start holder: %v", err)
	}
	waitStarted(t, gate.started)

	replayHost := &effectCountingHost{Host: host.NewSim()}
	hermetic := engine.New(st, replayHost)
	_, err = hermetic.Replay(context.Background(), recorded.RunID, "call", engine.ReplayFromHermetic)
	if !errors.Is(err, engine.ErrReplayNoSlot) {
		t.Fatalf("replay error = %v, want ErrReplayNoSlot", err)
	}
	if replayHost.callCount() != 0 {
		t.Fatalf("parked replay reached the host %d times", replayHost.callCount())
	}

	children, err := st.ListChildRuns(recorded.RunID, 10)
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	var replayChild *store.Run
	for i := range children {
		if children[i].Trigger == "replay" {
			replayChild = &children[i]
		}
	}
	if replayChild == nil {
		t.Fatal("replay child row was not created")
	}
	if replayChild.Status != "cancelled" {
		t.Fatalf("replay child status = %q, want cancelled", replayChild.Status)
	}
	if replayChild.Error != engine.ErrReplayNoSlot.Error() {
		t.Fatalf("replay child error = %q, want %q", replayChild.Error, engine.ErrReplayNoSlot.Error())
	}

	close(gate.release)
	waitRunStatus(t, st, holderID, "done", 5*time.Second)
}

// Event runs are roots: they queue behind the cap and are admitted when the
// holder finishes. Batch/subflow children inherit the parent's slot and run
// immediately even while the cap is occupied.
func TestEventRunsQueueAndChildrenInheritSlot(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	gate := &gateSleepHost{
		Host:    host.NewSim(),
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	eng := engine.New(st, gate)

	holderWF := waitingWorkflow("admission")
	holderWF.Settings = map[string]any{"maxParallelRuns": 1.0}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	holderID, err := eng.StartAsync(ctx, holderWF, engine.Options{Trigger: "manual"})
	if err != nil {
		t.Fatalf("start holder: %v", err)
	}
	waitStarted(t, gate.started)

	eventWF := linearWorkflow()
	eventWF.ID = "admission"
	eventWF.Settings = map[string]any{"maxParallelRuns": 1.0}

	first, err := eng.StartAsync(ctx, eventWF, engine.Options{Trigger: "event", ParentRunID: holderID})
	if err != nil {
		t.Fatalf("start event 1: %v", err)
	}
	second, err := eng.StartAsync(ctx, eventWF, engine.Options{Trigger: "event", ParentRunID: holderID})
	if err != nil {
		t.Fatalf("start event 2: %v", err)
	}
	waitRunStatus(t, st, first, "queued", 3*time.Second)
	waitRunStatus(t, st, second, "queued", 3*time.Second)

	batchID, err := eng.StartAsync(ctx, eventWF, engine.Options{Trigger: "batch", ParentRunID: holderID})
	if err != nil {
		t.Fatalf("start batch child: %v", err)
	}
	waitRunStatus(t, st, batchID, "done", 5*time.Second)

	subflowID, err := eng.StartAsync(ctx, eventWF, engine.Options{Trigger: "subflow", ParentRunID: holderID})
	if err != nil {
		t.Fatalf("start subflow child: %v", err)
	}
	waitRunStatus(t, st, subflowID, "done", 5*time.Second)

	// The holder still owns the root slot while both children completed.
	if run, err := st.GetRun(holderID); err != nil || run.Status != "running" {
		t.Fatalf("holder = %v (%v), want still running", run, err)
	}

	close(gate.release)
	waitRunStatus(t, st, holderID, "done", 5*time.Second)
	waitRunStatus(t, st, first, "done", 5*time.Second)
	waitRunStatus(t, st, second, "done", 5*time.Second)
}
