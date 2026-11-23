package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"loom/internal/flow"
	"loom/internal/store"
	"time"
)

// ErrParkedExecution is returned when an execution path reaches a run that is
// still parked behind the workflow's parallel-run cap. Parked state has no
// durable effects or progress and must be admitted before it can execute.
var ErrParkedExecution = errors.New("parked execution cannot run")

func (e *Engine) Resume(ctx context.Context, runID, decision string) (*Result, error) {
	return e.ResumeAs(ctx, runID, decision, "", "")
}

// ResumeAs resumes a waiting run and attributes the resume action to actor.
// Execution events continue under the run's original actor.
func (e *Engine) ResumeAs(ctx context.Context, runID, decision, actor, note string) (*Result, error) {
	return e.ResumeDecision(ctx, runID, decision, DecisionMeta{ActorID: actor, ActorLabel: actor, Source: "api"}, note)
}

// ResumeTimer resumes a run parked by a durable wait once its deadline passes.
func (e *Engine) ResumeTimer(ctx context.Context, runID string) (*Result, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if run.Status != "waiting" {
		return nil, fmt.Errorf("run %s is %s, not waiting", runID, run.Status)
	}
	wakeAt, ok, err := e.Store.RunWakeAt(runID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("run %s has no durable timer", runID)
	}
	if e.now().Before(wakeAt) {
		return nil, fmt.Errorf("run %s is not due until %s", runID, wakeAt.UTC().Format(time.RFC3339))
	}
	return e.ResumeAs(ctx, runID, "", "system:timer", "")
}

// ResumeDue resumes every waiting run whose durable timer has expired. It
// returns the number resumed; a run that lost the claim race is skipped.
func (e *Engine) ResumeDue(ctx context.Context) int {
	ids, err := e.Store.DueWakeups(e.now())
	if err != nil {
		return 0
	}
	resumed := 0
	for _, id := range ids {
		if _, err := e.ResumeTimer(ctx, id); err == nil {
			resumed++
		}
	}
	return resumed
}

// ResumeInterrupted continues a run that was left in flight when the previous
// process stopped. Completed nodes are restored from the event log, so only
// unfinished work executes again. The run keeps its id and audit trail; a
// run.resumed event marks the restart boundary.
func (e *Engine) ResumeInterrupted(ctx context.Context, runID string) (*Result, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	// A parented run is work its parent created; resuming it would duplicate
	// the node the parent is about to re-execute. Orphan it instead and let
	// the parent recreate a fresh child. Event runs are the exception: they
	// are parented for audit/trace but are roots that take their own
	// concurrency slot, so they must resume like any other top-level run.
	if run.ParentRunID != "" && run.Trigger != "event" {
		now := e.now()
		orphaned, err := e.Store.OrphanRun(runID, "orphaned by restart", now)
		if err != nil {
			return nil, err
		}
		if orphaned {
			_ = e.appendEvent(runID, store.Event{
				Type: "run.failed", Actor: actorOrDefault(run.Actor), Error: "orphaned by restart",
			})
		}
		return nil, fmt.Errorf("run %s orphaned by restart", runID)
	}
	if run.Status != "running" {
		return nil, fmt.Errorf("run %s is %s, not in flight", runID, run.Status)
	}
	failInterrupted := func(cause error) (*Result, error) {
		now := e.now()
		_ = e.appendEvent(runID, store.Event{Type: "run.failed", Actor: actorOrDefault(run.Actor), Error: cause.Error()})
		_ = e.Store.UpdateRunStatus(runID, "failed", cause.Error(), &now)
		return nil, cause
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return failInterrupted(fmt.Errorf("cannot resolve workflow: %w", err))
	}
	if _, err := e.Store.VerifyChain(runID); err != nil {
		return failInterrupted(fmt.Errorf("invalid event log: %w", err))
	}
	outputs, completed, err := e.loadOutputs(runID)
	if err != nil {
		return failInterrupted(fmt.Errorf("cannot load outputs: %w", err))
	}

	effects, cassetteRunID := e.persistedEffects(runID)
	st := &execState{
		runID: runID, wf: wf, outputs: outputs, completed: completed,
		skipped: map[string]bool{}, decision: map[string]string{},
		decisionInfo: map[string]flow.DecisionInfo{},
		vars:         varsFor(wf), actor: actorOrDefault(run.Actor),
		rootRunID: rootRunIDFor(run),
		effects:   effects, cassetteRunID: cassetteRunID,
	}
	st.host = wrapWorkflowEgress(e.Host, wf.Egress)
	if err := e.Store.UpdateRunStatus(runID, "running", "", nil); err != nil {
		return nil, err
	}
	if err := e.appendEvent(runID, store.Event{
		Type: "run.resumed", Actor: "system:recovery", Data: map[string]any{"reason": "restart"},
	}); err != nil {
		return nil, err
	}

	opts := Options{
		Trigger: run.Trigger, Input: flow.Item{}, MaxAttempts: e.MaxAttempts,
		JobID: run.JobID, TemplateVersion: run.TemplateVersion,
		RootRunID: rootRunIDFor(run), Actor: run.Actor,
	}
	timeout := opts.Timeout
	if timeout <= 0 && wf.Settings != nil {
		if sec, ok := wf.Settings["timeoutSeconds"].(float64); ok && sec > 0 {
			timeout = time.Duration(sec * float64(time.Second))
		} else if sec, ok := wf.Settings["timeout"].(float64); ok && sec > 0 {
			timeout = time.Duration(sec * float64(time.Second))
		}
	}

	var runCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	e.register(runID, cancel)
	defer e.unregister(runID)

	return e.execute(runCtx, st, opts)
}

// cancelParkedReplay finishes a replay child that parked behind the cap. It
// never enters execute: the run gets a cancelled row and an audit event so it
// cannot be mistaken for a completed replay.
func (e *Engine) cancelParkedReplay(st *execState, cancel context.CancelFunc) {
	if cancel != nil {
		cancel()
	}
	now := e.now()
	_ = e.emit(st, store.Event{Type: "run.cancelled"})
	_ = e.Store.UpdateRunStatus(st.runID, "cancelled", ErrReplayNoSlot.Error(), &now)
	e.unregister(st.runID)
}

func (e *Engine) Cancel(runID string) bool {
	cancelled := false
	// Admission can replace the parked handle with a fresh executing context
	// between our read and the cancel. Re-read the handle and the run status a
	// few times so a cancel that races admission still stops the new handle.
	for attempt := 0; attempt < 3; attempt++ {
		e.mu.Lock()
		cancel, ok := e.active[runID]
		e.mu.Unlock()
		if !ok {
			return cancelled
		}
		cancel()
		cancelled = true

		run, err := e.Store.GetRun(runID)
		if err != nil {
			return true
		}
		switch run.Status {
		case "queued":
			// A parked (queued) run has no executing goroutine listening on
			// the context; finish it directly.
			now := e.now()
			_ = e.appendEvent(runID, store.Event{Type: "run.cancelled"})
			_ = e.Store.UpdateRunStatus(runID, "cancelled", "", &now)
			e.unregister(runID)
			return true
		case "running":
			// The run may have been admitted after we cancelled the old
			// parked handle. Loop and cancel the newly registered handle.
			continue
		default:
			// Terminal (or waiting, handled by its own path): nothing else to
			// stop. Report the run was active when this call started.
			return true
		}
	}
	return cancelled
}

// CancelAll cancels all currently executing runs (used during graceful shutdown).
func (e *Engine) CancelAll() int {
	e.mu.Lock()
	e.stopped = true
	cancels := make([]context.CancelFunc, 0, len(e.active))
	for _, cancel := range e.active {
		cancels = append(cancels, cancel)
	}
	e.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

// isStopped reports whether CancelAll has frozen the engine for shutdown.
func (e *Engine) isStopped() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stopped
}

// dequeueNext admits the oldest parked run of the workflow when a slot is
// free. Called whenever a run reaches a terminal state; the atomic claim
// keeps concurrent terminals from over-admitting.
func (e *Engine) dequeueNext(workflowID string) {
	if workflowID == "" || e.isStopped() {
		return
	}
	// Fill every free slot: concurrent terminals can release several at once,
	// and admitting only one leaves the rest parked until an unrelated event.
	for i := 0; i < 1000; i++ {
		next, err := e.Store.OldestQueuedRun(workflowID)
		if err != nil || next == nil {
			return
		}
		wf, err := e.workflowForRun(next)
		if err != nil || wf == nil {
			return
		}
		maxRuns := intValue(wf.Settings["maxParallelRuns"])
		if maxRuns < 1 {
			maxRuns = 1
		}
		ok, err := e.Store.AdmitRun(next.ID, workflowID, maxRuns)
		if err != nil || !ok {
			return
		}
		e.executeClaimed(context.Background(), next)
	}
}

// executeClaimed runs a parked run that just won a concurrency slot. The
// claim already flipped the row to running; the exec state is rebuilt from
// the stored snapshot and the trigger input recorded on run.started.
func (e *Engine) executeClaimed(ctx context.Context, run *store.Run) {
	wf, err := e.workflowForRun(run)
	if err != nil || wf == nil {
		now := e.now()
		_ = e.appendEvent(run.ID, store.Event{Type: "run.failed", Error: "cannot resolve workflow"})
		_ = e.Store.UpdateRunStatus(run.ID, "failed", "cannot resolve workflow", &now)
		return
	}
	input, err := e.inputForClaimed(run.ID)
	if err != nil {
		now := e.now()
		_ = e.appendEvent(run.ID, store.Event{Type: "run.failed", Error: err.Error()})
		_ = e.Store.UpdateRunStatus(run.ID, "failed", err.Error(), &now)
		return
	}
	opts := Options{
		Trigger: run.Trigger, Input: input, MaxAttempts: e.MaxAttempts,
		JobID: run.JobID, TemplateVersion: run.TemplateVersion,
		RootRunID: rootRunIDFor(run), Actor: run.Actor,
	}
	effects, cassetteRunID := e.persistedEffects(run.ID)
	st := &execState{
		runID: run.ID, wf: wf,
		outputs: map[string]flow.Output{}, completed: map[string]bool{},
		skipped: map[string]bool{}, decision: map[string]string{},
		vars: varsFor(wf), actor: actorOrDefault(run.Actor),
		rootRunID: rootRunIDFor(run),
		effects:   effects, cassetteRunID: cassetteRunID,
	}
	st.host = wrapWorkflowEgress(e.Host, wf.Egress)
	runCtx, cancel := context.WithCancel(ctx)
	e.releaseRegistered(run.ID)
	e.register(run.ID, cancel)
	go func() {
		defer cancel()
		defer e.unregister(run.ID)
		defer e.recoverBackground(run.ID)
		_, _ = e.execute(runCtx, st, opts)
	}()
}

// inputForClaimed recovers the trigger input a parked run recorded on its
// run.started event, so admitted runs keep the input they were started with.
// The event log is display-masked, so a legacy run without an input column is
// refused when the recovered item still contains redaction markers instead of
// executing masked values.
func (e *Engine) inputForClaimed(runID string) (flow.Item, error) {
	// Prefer the dedicated input column: the event log is display-masked and
	// must not be the source of truth for execution values.
	if raw, err := e.Store.RunInput(runID); err == nil && len(raw) > 0 {
		var item flow.Item
		if json.Unmarshal(raw, &item) == nil {
			return item, nil
		}
	}
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if ev.Type != "run.started" {
			continue
		}
		raw, _ := ev.Data["input"].(map[string]any)
		if len(raw) == 0 {
			return flow.Item{}, nil
		}
		item := flow.Item(raw)
		if containsRedaction(item) {
			return nil, fmt.Errorf("run %s has no stored input and the event log is redacted; refusing to execute masked values", runID)
		}
		return item, nil
	}
	return flow.Item{}, nil
}

// DrainQueued admits parked runs whose workflows have free capacity, oldest
// first. Called at startup once interrupted runs have resumed.
func (e *Engine) DrainQueued(ctx context.Context) {
	runs, err := e.Store.ListQueuedRuns()
	if err != nil {
		return
	}
	for _, run := range runs {
		if e.isStopped() {
			return
		}
		wf, err := e.workflowForRun(&run)
		if err != nil || wf == nil {
			continue
		}
		maxRuns := intValue(wf.Settings["maxParallelRuns"])
		if maxRuns < 1 {
			maxRuns = 1
		}
		ok, err := e.Store.AdmitRun(run.ID, run.WorkflowID, maxRuns)
		if err != nil || !ok {
			continue
		}
		e.executeClaimed(ctx, &run)
	}
}
