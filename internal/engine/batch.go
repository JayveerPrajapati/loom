package engine

import (
	"context"
	"fmt"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	// maxBatchItems bounds the item count one batch node may fan out.
	maxBatchItems = 10000
	// maxBatchConcurrency is the runtime clamp for batch fan-out. New saves
	// are rejected above this by graph.Validate, but stored workflows from
	// before the cap must still run safely.
	maxBatchConcurrency = 32
)

// executeBatch runs the node once per array element. Per-item failures are
// recorded as item.failed events; ContinueOnItemError decides whether the node
// fails or returns a partial result.
func (e *Engine) executeBatch(ctx context.Context, st *execState, node *graph.Node, in flow.Input, impl flow.Node, opts Options) (flow.Output, error) {
	path := strings.TrimSpace(node.Batch.ItemsPath)
	if path == "" {
		path = "items"
	}
	raw, ok := lookupPath(in.Item, path)
	if !ok {
		return nil, fmt.Errorf("batch: item has no %q array", path)
	}
	items, err := toItems(raw)
	if err != nil {
		return nil, fmt.Errorf("batch: %w", err)
	}
	if len(items) > maxBatchItems {
		return nil, fmt.Errorf("batch: too many items (limit %d)", maxBatchItems)
	}

	concurrency := 1
	if node.Batch.Concurrency > 0 {
		concurrency = node.Batch.Concurrency
	}
	// The ingest validator caps concurrency for new saves, but a workflow
	// stored before that cap can still carry a much larger value. Clamp it
	// here so one node cannot open thousands of in-flight calls.
	if concurrency > maxBatchConcurrency {
		concurrency = maxBatchConcurrency
	}

	results := make([]any, len(items))
	var failed atomic.Int64
	var firstErr error
	var errMu sync.Mutex

	processItem := func(idx int, it flow.Item) bool {
		sub := in
		sub.Item = flow.CloneItem(it)
		if in.Context != nil {
			ctxCopy := *in.Context
			ctxCopy.Index = idx
			ctxCopy.HasIndex = true
			sub.Context = &ctxCopy
		}
		itemKey := sub.IdempotencyKey()
		itemHash, err := e.saveJSON(sub.Item)
		if err != nil {
			errMu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			errMu.Unlock()
			return false
		}
		if err := e.emit(st, store.Event{
			Type: "item.started", NodeID: node.ID, InputHash: itemHash,
			Data: map[string]any{"index": idx, "idempotencyKey": itemKey},
		}); err != nil {
			errMu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			errMu.Unlock()
			return false
		}
		out, itemErr, _, _ := e.runAttempts(ctx, st, node, sub, impl, opts, itemHash, itemKey)
		if itemErr != nil {
			failed.Add(1)
			results[idx] = flow.Item{"index": idx, "error": itemErr.Error()}
			_ = e.emit(st, store.Event{
				Type: "item.failed", NodeID: node.ID,
				Error: itemErr.Error(), Data: map[string]any{"index": idx},
			})
			errMu.Lock()
			if firstErr == nil {
				firstErr = itemErr
			}
			errMu.Unlock()
			return false
		}
		result := flow.Item{}
		if out != nil {
			if main, ok := out["main"]; ok {
				result = main
			}
		}
		results[idx] = result
		_ = e.emit(st, store.Event{
			Type: "item.finished", NodeID: node.ID, Data: map[string]any{"index": idx},
		})
		return true
	}

	if concurrency <= 1 {
		for i, item := range items {
			if firstErr != nil && !node.Batch.ContinueOnItemError {
				break
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !processItem(i, item) && !node.Batch.ContinueOnItemError {
				break
			}
		}
	} else {
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i, item := range items {
			errMu.Lock()
			stop := firstErr != nil && !node.Batch.ContinueOnItemError
			errMu.Unlock()
			if stop {
				break
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(idx int, it flow.Item) {
				defer wg.Done()
				defer func() { <-sem }()
				processItem(idx, it)
			}(i, item)
		}
		wg.Wait()
	}

	if firstErr != nil && !node.Batch.ContinueOnItemError {
		return nil, firstErr
	}

	return flow.Output{"main": flow.Item{
		"items": results, "count": len(items), "failed": int(failed.Load()),
	}}, nil
}

func lookupPath(item flow.Item, path string) (any, bool) {
	var current any = map[string]any(item)
	for _, part := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		case flow.Item:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		default:
			return nil, false
		}
	}
	return current, true
}

func toItems(raw any) ([]flow.Item, error) {
	switch list := raw.(type) {
	case []any:
		out := make([]flow.Item, 0, len(list))
		for _, value := range list {
			item, ok := asItem(value)
			if !ok {
				return nil, fmt.Errorf("array elements must be objects")
			}
			out = append(out, item)
		}
		return out, nil
	case []flow.Item:
		return list, nil
	default:
		return nil, fmt.Errorf("value must be an array of objects")
	}
}

func asItem(value any) (flow.Item, bool) {
	switch item := value.(type) {
	case flow.Item:
		return item, true
	case map[string]any:
		return flow.Item(item), true
	default:
		return nil, false
	}
}

func itemEventData(in flow.Input, idempotencyKey string, trace map[string]map[string]any) map[string]any {
	data := nodeEventData(idempotencyKey, trace)
	if in.Context != nil && in.Context.HasIndex {
		data["itemIndex"] = in.Context.Index
	}
	return data
}

// mergeItems combines items arriving on a node's incoming edges. mode is the
// node's flow.merge.mode:
//   - "append" collects every item (edge order) into {"items": [...]}.
//   - "byIndex" merges arrays elementwise; any non-array input falls back to a
//     shallow later-wins merge so the legacy behavior is preserved.
//   - "" or an unknown mode keeps the shallow later-wins merge.
func mergeItems(items []flow.Item, mode string) flow.Item {
	switch mode {
	case "append":
		values := make([]any, 0, len(items))
		for _, item := range items {
			values = append(values, map[string]any(item))
		}
		return flow.Item{"items": values}
	case "byIndex":
		if merged, ok := mergeByIndex(items); ok {
			return merged
		}
	}
	if len(items) == 1 {
		return items[0]
	}
	out := flow.Item{}
	for _, item := range items {
		for k, v := range item {
			out[k] = v
		}
	}
	return out
}

// mergeByIndex merges items whose arrays live under "items". It reports false
// unless every item is such an array.
func mergeByIndex(items []flow.Item) (flow.Item, bool) {
	if len(items) == 0 {
		return flow.Item{}, true
	}
	arrays := make([][]any, len(items))
	maxLen := 0
	for i, item := range items {
		raw, ok := item["items"]
		if !ok {
			return nil, false
		}
		arr, ok := raw.([]any)
		if !ok {
			return nil, false
		}
		arrays[i] = arr
		if len(arr) > maxLen {
			maxLen = len(arr)
		}
	}
	merged := make([]any, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		var (
			acc     any
			present bool
		)
		for _, arr := range arrays {
			if i >= len(arr) {
				continue
			}
			if !present {
				acc, present = arr[i], true
				continue
			}
			acc = mergeValue(acc, arr[i])
		}
		merged = append(merged, acc)
	}
	return flow.Item{"items": merged}, true
}

// mergeValue shallow-merges two object elements (later wins); for anything
// else the later value replaces the earlier one.
func mergeValue(base, next any) any {
	baseMap, baseOK := asItemMap(base)
	nextMap, nextOK := asItemMap(next)
	if !baseOK || !nextOK {
		return next
	}
	out := map[string]any{}
	for k, v := range baseMap {
		out[k] = v
	}
	for k, v := range nextMap {
		out[k] = v
	}
	return out
}

func asItemMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case flow.Item:
		return map[string]any(typed), true
	default:
		return nil, false
	}
}

func finalItem(wf *graph.Workflow, outputs map[string]flow.Output) flow.Item {
	outgoing := map[string]bool{}
	for _, e := range wf.Edges {
		outgoing[e.From.Node] = true
	}
	sinks := make([]string, 0, 1)
	for _, n := range wf.Nodes {
		if !outgoing[n.ID] {
			sinks = append(sinks, n.ID)
		}
	}
	sort.Strings(sinks)

	items := make([]flow.Item, 0, len(sinks))
	for _, id := range sinks {
		out := outputs[id]
		ports := make([]string, 0, len(out))
		for port := range out {
			ports = append(ports, port)
		}
		sort.Strings(ports)
		if len(ports) == 0 {
			continue
		}
		items = append(items, out[ports[0]])
	}
	if len(items) == 0 {
		return flow.Item{}
	}
	return mergeItems(items, "")
}
