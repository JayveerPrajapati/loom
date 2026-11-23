package nodes

import (
	"context"
	"fmt"
	"loom/internal/expr"
	"loom/internal/flow"
	"time"
)

type waitNode struct{}

// maxWaitSeconds matches the ingest cap on flow.wait. Stored workflows may
// predate it, so the runtime rejects an over-limit value instead of parking
// a run for an unbounded time.
const maxWaitSeconds = 2592000

func (waitNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	seconds := num(in.Config["seconds"])
	if seconds < 0 {
		seconds = 0
	}
	if seconds > maxWaitSeconds {
		return nil, fmt.Errorf("flow.wait: seconds %v exceeds the maximum of %d", seconds, maxWaitSeconds)
	}
	if in.DurableWait && seconds > 0 {
		wakeAt := in.WakeAt
		if wakeAt.IsZero() {
			wakeAt = in.Now.Add(time.Duration(seconds * float64(time.Second)))
		}
		if in.Now.Before(wakeAt) {
			// Park the run instead of holding a goroutine. The engine persists
			// this deadline and resumes once it is due.
			return nil, &flow.Suspend{Reason: "wait", Data: map[string]any{
				"wakeAt": wakeAt.UTC().Format(time.RFC3339Nano),
			}}
		}
		return flow.Output{"main": in.Item}, nil
	}
	if err := in.Host.Sleep(ctx, time.Duration(seconds*float64(time.Second))); err != nil {
		return nil, err
	}
	return flow.Output{"main": in.Item}, nil
}

type approvalNode struct{}

func (approvalNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	channel := expr.RenderWithScope(str(in.Config["channel"]), scope)
	message := expr.RenderWithScope(str(in.Config["message"]), scope)

	if in.Decision == "" {
		return nil, &flow.Suspend{
			Reason: "approval",
			Data:   map[string]any{"channel": channel, "message": message},
		}
	}
	if in.Decision != "approved" && in.Decision != "rejected" {
		return nil, fmt.Errorf("flow.approval: decision must be approved or rejected, got %q", in.Decision)
	}

	decidedAt := in.DecisionAt
	if decidedAt.IsZero() {
		decidedAt = in.Now
	}
	out := flow.CloneItem(in.Item)
	out["approved"] = in.Decision == "approved"
	// Downstream steps can read the decision and its reason:
	//   {{ $json.approval.decision }} / {{ $json.approval.note }}
	out["approval"] = map[string]any{
		"decision":  in.Decision,
		"note":      in.DecisionNote,
		"actor":     in.DecisionActor,
		"decidedAt": decidedAt.UTC().Format(time.RFC3339),
		"nodeId":    in.NodeID,
	}
	return flow.Output{in.Decision: out}, nil
}

type subflowNode struct{}

func (subflowNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	workflowID := expr.RenderWithScope(str(in.Config["workflowId"]), expr.FromInput(in))
	if workflowID == "" {
		return nil, fmt.Errorf("flow.subflow: workflowId is required")
	}
	wait := true
	if configured, ok := in.Config["wait"].(bool); ok {
		wait = configured
	}
	var (
		item flow.Item
		err  error
	)
	if wait {
		item, err = in.Subflow.RunSubflow(ctx, workflowID, in.Item)
	} else {
		item, err = in.Subflow.RunSubflowAsync(ctx, workflowID, in.Item)
	}
	if err != nil {
		return nil, err
	}
	return flow.Output{"main": item}, nil
}
