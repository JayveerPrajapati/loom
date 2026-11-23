// Package engine executes typed workflow DAGs and records every step.
package engine

import (
	"context"
	"encoding/json"
	"time"

	"loom/internal/graph"
	"loom/internal/store"
)

func (e *Engine) fail(st *execState, nodeID string, runErr error) (*Result, error) {
	now := e.now()
	if err := e.emit(st, store.Event{Type: "run.failed", NodeID: nodeID, Error: runErr.Error()}); err != nil {
		return nil, err
	}
	if err := e.Store.UpdateRunStatus(st.runID, "failed", runErr.Error(), &now); err != nil {
		return nil, err
	}
	e.dequeueNext(st.wf.ID)
	e.notifyFinished(st.runID)
	return e.result(st, "failed", "", "", runErr.Error()), nil
}

func (e *Engine) cancelled(st *execState) (*Result, error) {
	now := e.now()
	if err := e.emit(st, store.Event{Type: "run.cancelled"}); err != nil {
		return nil, err
	}
	if err := e.Store.UpdateRunStatus(st.runID, "cancelled", "", &now); err != nil {
		return nil, err
	}
	e.dequeueNext(st.wf.ID)
	e.notifyFinished(st.runID)
	return e.result(st, "cancelled", "", "", ""), nil
}

func (e *Engine) done(st *execState) (*Result, error) {
	now := e.now()
	if err := e.emit(st, store.Event{Type: "run.finished"}); err != nil {
		return nil, err
	}
	if err := e.Store.UpdateRunStatus(st.runID, "done", "", &now); err != nil {
		return nil, err
	}
	e.dequeueNext(st.wf.ID)
	e.notifyFinished(st.runID)
	return e.result(st, "done", "", "", ""), nil
}

// notifyFinished hands a terminal run to the optional post-run hook without
// blocking the caller; the hook may start another run (shadow verification).
func (e *Engine) notifyFinished(runID string) {
	if e.OnRunFinished == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		e.OnRunFinished(ctx, runID)
	}()
}

// AppendRunEvent adds a post-run event to an existing run's audit chain. It is
// used for verdicts that are not part of execution (shadow verification).
func (e *Engine) AppendRunEvent(runID, eventType string, data map[string]any) error {
	_, err := e.Store.AppendEvent(runID, store.Event{
		Type: eventType, At: e.now(), Actor: "system:shadow", Data: data,
	})
	return err
}

func (e *Engine) result(st *execState, status, resumeNode, token, errMsg string) *Result {
	events, _ := e.Store.ListEvents(st.runID)
	return &Result{
		RunID: st.runID, Status: status, Outputs: st.outputs,
		ResumeNode: resumeNode, ResumeToken: token, Error: errMsg, Events: events,
	}
}

func (e *Engine) workflowForRun(run *store.Run) (*graph.Workflow, error) {
	if len(run.Snapshot) > 0 {
		var wf graph.Workflow
		if err := json.Unmarshal(run.Snapshot, &wf); err != nil {
			return nil, err
		}
		return &wf, nil
	}
	return e.Store.GetWorkflow(run.WorkflowID)
}
