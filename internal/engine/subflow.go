package engine

import (
	"context"
	"errors"
	"fmt"
	"loom/internal/cassette"
	"loom/internal/flow"
	"loom/internal/schemas"
)

const maxSubflowDepth = 5

type subflowRunner struct {
	engine       *Engine
	depth        int
	parentRunID  string
	parentNodeID string
	actor        string
	rootRunID    string
	// effects is inherited from the parent run. A replay must not let the
	// child run fall back to the live host.
	effects cassette.Mode
	// cassetteRunID is the original parent run when replaying; the recorded
	// child run under it supplies the child's tapes.
	cassetteRunID string
}

func (r *subflowRunner) RunSubflow(ctx context.Context, workflowID string, item flow.Item) (flow.Item, error) {
	if r.depth >= maxSubflowDepth {
		return nil, fmt.Errorf("subflow depth limit (%d) exceeded", maxSubflowDepth)
	}
	if r.engine.ResolveWorkflow == nil {
		return nil, errors.New("subflow resolver is not configured")
	}
	wf, err := r.engine.ResolveWorkflow(workflowID)
	if err != nil {
		return nil, err
	}
	// Workflow input contract (v2 plan D11 semantics): the calling item is
	// validated against the callee's resolved contract and the result
	// rides the item into the child run — annotate, never reject.
	item = annotateContract(item, wf.InputSchemaFields)
	cassetteSource := ""
	if r.effects == cassette.ModeReplay {
		originalParent := r.cassetteRunID
		if originalParent == "" {
			originalParent = r.parentRunID
		}
		if childID, err := r.engine.Store.SubflowChildRunID(originalParent, r.parentNodeID); err == nil {
			cassetteSource = childID
		}
	}
	res, err := r.engine.start(ctx, wf, Options{
		Trigger: "subflow", Input: item, MaxAttempts: r.engine.MaxAttempts,
		ParentRunID: r.parentRunID, ParentNodeID: r.parentNodeID,
		RootRunID: r.rootRunID, Actor: r.actor,
		Effects: r.effects, CassetteRunID: cassetteSource,
	}, r.depth+1)
	if err != nil {
		return nil, err
	}
	if res.Status != "done" {
		return nil, fmt.Errorf("subflow %s ended %s: %s", workflowID, res.Status, res.Error)
	}
	// Workflow output contract: the callee's final item is validated and
	// the result rides the item back to the caller.
	return annotateContract(finalItem(wf, res.Outputs), wf.OutputSchemaFields), nil
}

// annotateContract returns item with the contract result attached (a copy
// when a contract is present; the input untouched otherwise). A nil/empty
// contract annotates nothing.
func annotateContract(item flow.Item, fields []schemas.Field) flow.Item {
	if len(fields) == 0 {
		return item
	}
	if item == nil {
		item = flow.Item{}
	}
	out := flow.Item{}
	for k, v := range item {
		out[k] = v
	}
	violations := schemas.Check(out, fields)
	out["schemaValid"] = len(violations) == 0
	if len(violations) > 0 {
		rows := make([]map[string]any, 0, len(violations))
		for _, v := range violations {
			rows = append(rows, map[string]any{"path": v.Path, "message": v.Message})
		}
		out["schemaViolations"] = rows
	}
	return out
}

// RunSubflowAsync starts the child as an independent run and returns the input
// item immediately. Replay stays synchronous so the child's effects are
// recorded and replayed in the same order as the parent's tape.
func (r *subflowRunner) RunSubflowAsync(ctx context.Context, workflowID string, item flow.Item) (flow.Item, error) {
	if r.effects == cassette.ModeReplay {
		return r.RunSubflow(ctx, workflowID, item)
	}
	if r.depth >= maxSubflowDepth {
		return nil, fmt.Errorf("subflow depth limit (%d) exceeded", maxSubflowDepth)
	}
	if r.engine.ResolveWorkflow == nil {
		return nil, errors.New("subflow resolver is not configured")
	}
	wf, err := r.engine.ResolveWorkflow(workflowID)
	if err != nil {
		return nil, err
	}
	// Input contract annotation applies on the async path too; the caller
	// sees the verdict on the returned item, and the child run starts from
	// the annotated copy.
	item = annotateContract(item, wf.InputSchemaFields)
	// Detach from the parent's execution context: a fire-and-forget child must
	// not be cancelled the moment the parent run finishes.
	if _, err := r.engine.startAsync(context.WithoutCancel(ctx), wf, Options{
		Trigger: "subflow", Input: flow.CloneItem(item), MaxAttempts: r.engine.MaxAttempts,
		ParentRunID: r.parentRunID, ParentNodeID: r.parentNodeID,
		RootRunID: r.rootRunID, Snapshot: wf, Actor: r.actor,
		Effects: r.effects,
	}, r.depth+1); err != nil {
		return nil, err
	}
	return item, nil
}
