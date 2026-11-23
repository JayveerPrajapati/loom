package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"loom/internal/flow"
	"loom/internal/graph"
)

func batchLimitInput(count int) flow.Item {
	items := make([]any, count)
	for i := range items {
		items[i] = flow.Item{"i": i}
	}
	return flow.Item{"items": items}
}

// The batch item cap must be enforced before any work starts. The engine runs
// 10 000 items but rejects 10 001 outright.
func TestExecuteBatchItemLimit(t *testing.T) {
	eng := &Engine{}
	node := &graph.Node{ID: "batch", Type: "data.set", Batch: &graph.BatchPolicy{ItemsPath: "items"}}

	_, err := eng.executeBatch(context.Background(), &execState{}, node,
		flow.Input{Item: batchLimitInput(maxBatchItems + 1)}, nil, Options{})
	if err == nil || !strings.Contains(err.Error(), "batch: too many items (limit 10000)") {
		t.Fatalf("10001 items: err = %v, want too many items", err)
	}

	// At the limit the guard passes. A pre-cancelled context makes the loop
	// abort on its first check, proving acceptance without executing 10 000
	// nodes or writing their events.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = eng.executeBatch(ctx, &execState{}, node,
		flow.Input{Item: batchLimitInput(maxBatchItems)}, nil, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("10000 items: err = %v, want context cancellation after the guard", err)
	}
	if err != nil && strings.Contains(err.Error(), "too many items") {
		t.Fatalf("10000 items was rejected by the item cap: %v", err)
	}
}
