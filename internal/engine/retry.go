// Package engine executes typed workflow DAGs and records every step.
package engine

import (
	"time"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
)

// handleNodeError converts a terminal node failure into a normal output when
// the node opts into continue or branch handling. The node.finished event keeps
// the error text, so the run can finish while the failure stays visible and is
// restorable after a crash.
func (e *Engine) handleNodeError(st *execState, node *graph.Node, nodeErr error, attempts int) (flow.Output, error) {
	item := flow.Item{
		"error":    nodeErr.Error(),
		"nodeId":   node.ID,
		"attempts": attempts,
	}
	out := flow.Output{}
	if node.OnError == "branch" {
		out["error"] = item
	} else {
		out["main"] = item
	}
	outputHash, err := e.saveJSON(out)
	if err != nil {
		return nil, err
	}
	if err := e.emit(st, store.Event{
		Type: "node.finished", NodeID: node.ID, Attempt: attempts,
		OutputHash: outputHash, Error: nodeErr.Error(),
		Data: map[string]any{"handled": node.OnError},
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// retryDelay returns the wait before the next attempt. Without a policy it
// keeps the historical quadratic backoff; with one it uses capped exponential
// backoff so a single flaky node can retry cheaply without stalling the run.
func retryDelay(policy *graph.RetryPolicy, attempt int) time.Duration {
	if policy == nil {
		return time.Duration(attempt*attempt) * 100 * time.Millisecond
	}
	base := time.Duration(policy.BackoffMs) * time.Millisecond
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	maxDelay := time.Duration(policy.MaxDelayMs) * time.Millisecond
	delay := base
	for i := 1; i < attempt; i++ {
		if maxDelay > 0 && delay >= maxDelay {
			return maxDelay
		}
		delay *= 2
	}
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func nodeEventData(idempotencyKey string, trace map[string]map[string]any) map[string]any {
	data := map[string]any{"idempotencyKey": idempotencyKey}
	for kind, value := range trace {
		data[kind] = value
	}
	return data
}
