package server

import (
	"context"
	"log"
)

// shadowRun replays a workflow's configured shadow version against a finished
// live run and records the verdict on that run. It never calls the network:
// verification is hermetic, and the shadow run itself is a normal audited run.
func (s *api) shadowRun(ctx context.Context, runID string) {
	run, err := s.deps.Store.GetRun(runID)
	if err != nil {
		return
	}
	// Only production triggers shadow. Verification runs (ci/replay) must not
	// recursively shadow themselves.
	switch run.Trigger {
	case "webhook", "manual", "event", "scheduler", "cron":
	default:
		return
	}
	wf, err := s.deps.Store.GetWorkflow(run.WorkflowID)
	if err != nil {
		return
	}
	version := settingInt(wf.Settings["shadowVersion"])
	if version <= 0 {
		return
	}
	candidate, err := s.deps.Store.GetWorkflowVersion(run.WorkflowID, version)
	if err != nil {
		_ = s.deps.Engine.AppendRunEvent(runID, "shadow.verdict", map[string]any{
			"version": version, "passed": false,
			"error": "version not found: " + err.Error(),
		})
		return
	}
	verdict, err := s.deps.Engine.VerifyAgainstRun(ctx, runID, candidate)
	if err != nil {
		_ = s.deps.Engine.AppendRunEvent(runID, "shadow.verdict", map[string]any{
			"version": version, "passed": false, "error": err.Error(),
		})
		return
	}
	_ = s.deps.Engine.AppendRunEvent(runID, "shadow.verdict", map[string]any{
		"version": version, "passed": verdict.Passed, "verdictRunId": verdict.RunID,
		"status": verdict.Status, "nodes": len(verdict.Nodes),
	})
	if !verdict.Passed {
		log.Printf("shadow version %d of %s did not reproduce run %s: %s",
			version, run.WorkflowID, runID, verdict.Error)
	}
}

func settingInt(raw any) int {
	switch value := raw.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}
