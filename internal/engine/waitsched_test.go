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

// fakeWakeups is a programmable Store.NextWakeup double: it returns the
// earliest programmed deadline strictly after now, mirroring the real query.
type fakeWakeups struct {
	mu    sync.Mutex
	pends []time.Time
}

func (f *fakeWakeups) add(at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pends = append(f.pends, at)
}

func (f *fakeWakeups) next(now time.Time) (*time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var best *time.Time
	for _, p := range f.pends {
		if p.After(now) && (best == nil || p.Before(*best)) {
			cp := p
			best = &cp
		}
	}
	return best, nil
}

// dueCount reports how many programmed deadlines have passed, mirroring
// Store.DueWakeups semantics the scheduler relies on.
func (f *fakeWakeups) dueCount(now time.Time) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, p := range f.pends {
		if !p.After(now) {
			n++
		}
	}
	return n
}

// resumeRecorder records when the scheduler invoked the resume function with
// at least one due deadline — the real ResumeDue is a no-op when nothing is
// due, so only deadline-driven calls are meaningful.
type resumeRecorder struct {
	mu    sync.Mutex
	fakes *fakeWakeups
	calls []time.Time
}

func (r *resumeRecorder) record(_ context.Context) int {
	n := r.fakes.dueCount(time.Now())
	if n > 0 {
		r.mu.Lock()
		r.calls = append(r.calls, time.Now())
		r.mu.Unlock()
	}
	return n
}

func (r *resumeRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func (r *resumeRecorder) call(i int) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls[i]
}

func startScheduler(t *testing.T, fakes *fakeWakeups, interval time.Duration) (*engine.WaitScheduler, *resumeRecorder, context.CancelFunc, chan struct{}) {
	t.Helper()
	rec := &resumeRecorder{fakes: fakes}
	sched := engine.NewWaitScheduler(rec.record, fakes.next, interval)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sched.Run(ctx); close(done) }()
	return sched, rec, cancel, done
}

func TestWaitSchedulerResumesAtDeadline(t *testing.T) {
	fakes := &fakeWakeups{}
	sched, rec, cancel, done := startScheduler(t, fakes, time.Hour)

	arm := time.Now().Add(200 * time.Millisecond)
	fakes.add(arm)
	begin := time.Now()
	sched.Schedule()

	waitFor(t, 2*time.Second, func() bool { return rec.count() >= 1 })
	if got := rec.call(0).Sub(begin); got < 150*time.Millisecond {
		t.Fatalf("resume fired %s after arming, want no earlier than the 200ms deadline", got)
	}
	if got := rec.call(0).Sub(begin); got > time.Second {
		t.Fatalf("resume fired %s after arming, want near the 200ms deadline", got)
	}
	cancel()
	<-done
}

func TestWaitSchedulerEarlierDeadlineReplacesLaterTimer(t *testing.T) {
	fakes := &fakeWakeups{}
	sched, rec, cancel, done := startScheduler(t, fakes, time.Hour)

	// Arm for a deadline two seconds out, then park a run with a much
	// earlier deadline. The scheduler must replace the later timer.
	later := time.Now().Add(2 * time.Second)
	fakes.add(later)
	sched.Schedule()
	time.Sleep(30 * time.Millisecond) // let the reconcile arm the later timer
	earlier := time.Now().Add(150 * time.Millisecond)
	fakes.add(earlier)

	begin := time.Now()
	sched.Schedule()

	waitFor(t, time.Second, func() bool { return rec.count() >= 1 })
	if got := rec.call(0).Sub(begin); got > 700*time.Millisecond {
		t.Fatalf("resume took %s, want the earlier deadline to replace the later timer", got)
	}
	cancel()
	<-done
}

func TestWaitSchedulerLaterDeadlineDoesNotDisplaceEarlierTimer(t *testing.T) {
	fakes := &fakeWakeups{}
	sched, rec, cancel, done := startScheduler(t, fakes, time.Hour)

	earlier := time.Now().Add(150 * time.Millisecond)
	fakes.add(earlier)
	sched.Schedule()
	time.Sleep(30 * time.Millisecond) // let the reconcile arm the earlier timer
	fakes.add(time.Now().Add(5 * time.Second))

	begin := time.Now()
	sched.Schedule()

	waitFor(t, 2*time.Second, func() bool { return rec.count() >= 1 })
	if got := rec.call(0).Sub(begin); got > 700*time.Millisecond {
		t.Fatalf("resume took %s, want the earlier timer to keep firing despite a later park", got)
	}
	cancel()
	<-done
}

func TestWaitSchedulerShutdownStopsTimer(t *testing.T) {
	fakes := &fakeWakeups{}
	sched, rec, cancel, done := startScheduler(t, fakes, time.Hour)

	// Arm a short timer, then cancel the context before it fires. The
	// pending timer must be stopped, not executed.
	fakes.add(time.Now().Add(120 * time.Millisecond))
	sched.Schedule()
	cancel()
	<-done
	time.Sleep(300 * time.Millisecond)
	if n := rec.count(); n != 0 {
		t.Fatalf("resume called %d time(s) after shutdown, want none", n)
	}
}

func TestWaitSchedulerReconcilesImmediatelyAtStartup(t *testing.T) {
	fakes := &fakeWakeups{}
	// A wait that came due while the process was down: restart recovery
	// must resume it in Run's first reconcile, with no deadline or poke.
	fakes.add(time.Now().Add(-time.Second))
	_, rec, cancel, done := startScheduler(t, fakes, time.Hour)

	waitFor(t, time.Second, func() bool { return rec.count() >= 1 })
	cancel()
	<-done
}

func TestWaitSchedulerFallbackSweepRuns(t *testing.T) {
	fakes := &fakeWakeups{}
	// A due wait with no poke (simulating a missed park notification): the
	// fallback sweep must reconcile it and keep sweeping.
	fakes.add(time.Now().Add(-time.Second))
	_, rec, cancel, done := startScheduler(t, fakes, 120*time.Millisecond)

	time.Sleep(500 * time.Millisecond)
	if n := rec.count(); n < 2 {
		t.Fatalf("fallback sweep reconciled %d time(s) in 500ms with a 120ms interval, want at least 2", n)
	}
	cancel()
	<-done
}

func TestWaitSchedulerConcurrentScheduleIsSafe(t *testing.T) {
	fakes := &fakeWakeups{}
	sched, rec, cancel, done := startScheduler(t, fakes, time.Hour)

	fakes.add(time.Now().Add(80 * time.Millisecond))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				sched.Schedule()
			}
		}()
	}
	wg.Wait()
	waitFor(t, time.Second, func() bool { return rec.count() >= 1 })
	cancel()
	<-done
}

func waitFor(t *testing.T, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitWorkflowSeconds(seconds float64) *graph.Workflow {
	return &graph.Workflow{
		ID: "fast-wait", Name: "Fast wait", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "wait", Type: "flow.wait", TypeVersion: 1, Name: "Wait", Config: map[string]any{"seconds": seconds}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"woke": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "wait", Port: "main"}},
			{From: graph.Endpoint{Node: "wait", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}
}

func runUntilTerminal(t *testing.T, st *store.Store, runID string, limit time.Duration) (time.Duration, string, string) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		run, err := st.GetRun(runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		switch run.Status {
		case "done", "failed":
			return time.Until(deadline), run.Status, run.Error
		}
		time.Sleep(10 * time.Millisecond)
	}
	run, err := st.GetRun(runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	return limit, run.Status, run.Error
}

func TestWaitSchedulerResumesShortWaitNearDeadline(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eng := engine.New(st, host.NewSim())
	eng.DurableWaits = true
	sched := engine.NewWaitScheduler(eng.ResumeDue, st.NextWakeup, 15*time.Second)
	eng.OnRunParked = func(time.Time) { sched.Schedule() }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	begin := time.Now()
	res, err := eng.Start(ctx, waitWorkflowSeconds(0.1), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", res.Status)
	}
	_, status, runErr := runUntilTerminal(t, st, res.RunID, 600*time.Millisecond)
	if status != "done" {
		t.Fatalf("run ended %s (%s), want done within the deadline tolerance", status, runErr)
	}
	if elapsed := time.Since(begin); elapsed < 80*time.Millisecond {
		t.Fatalf("run finished after %s, want the 100ms wait honored", elapsed)
	}
}

func TestWaitSchedulerFallbackSweepResumesMissedPoke(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eng := engine.New(st, host.NewSim())
	eng.DurableWaits = true
	// The park hook is deliberately not wired: the exact-deadline timer
	// never learns about the run, so only the fallback sweep can resume it.
	sched := engine.NewWaitScheduler(eng.ResumeDue, st.NextWakeup, 150*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	res, err := eng.Start(ctx, waitWorkflowSeconds(0.1), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	_, status, runErr := runUntilTerminal(t, st, res.RunID, 900*time.Millisecond)
	if status != "done" {
		t.Fatalf("run ended %s (%s), want the fallback sweep to resume a missed poke", status, runErr)
	}
}

func TestWaitSchedulerRecoversWaitsDueAtRestart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Park a run with no scheduler running at all, then let its deadline
	// pass — the waiting state lives entirely in the database.
	eng := engine.New(st, host.NewSim())
	eng.DurableWaits = true
	res, err := eng.Start(context.Background(), waitWorkflowSeconds(0.1), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	sched := engine.NewWaitScheduler(eng.ResumeDue, st.NextWakeup, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sched.Run(ctx)

	_, status, runErr := runUntilTerminal(t, st, res.RunID, time.Second)
	if status != "done" {
		t.Fatalf("run ended %s (%s), want the startup reconcile to resume a due wait", status, runErr)
	}
}
