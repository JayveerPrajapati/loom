package nodes

import (
	"context"
	"fmt"
	"loom/internal/expr"
	"loom/internal/flow"
	"sync"
	"time"
)

type codeNode struct{}

func (codeNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	seconds := num(in.Config["timeoutSeconds"])
	if seconds == 0 {
		seconds = 2
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 300 {
		seconds = 300
	}
	timeout := time.Duration(seconds * float64(time.Second))
	scope := expr.FromInput(in)
	var logs []any
	scope.Log = func(level, message string) {
		logs = append(logs, map[string]any{"level": level, "message": message})
	}
	item, err := expr.EvalObjectWithTimeout(ctx, str(in.Config["source"]), scope, timeout)
	if err != nil {
		return nil, err
	}
	if len(logs) > 0 {
		trace(in, "console", map[string]any{"logs": logs})
	}
	return flow.Output{"main": item}, nil
}

type setNode struct{}

func (setNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	out := flow.CloneItem(in.Item)
	if keepOnly, _ := in.Config["keepOnlySet"].(bool); keepOnly {
		out = flow.Item{}
	}
	if values, ok := in.Config["values"].(map[string]any); ok {
		for k, v := range values {
			out[k] = expr.RenderValueWithScope(v, scope)
		}
	}
	return flow.Output{"main": out}, nil
}

// mapNode applies a reusable mapping (v2 D13): each row maps a target
// field from a source expression; node overrides win per target. Unlike
// contracts (annotate), a required row that renders empty fails the node —
// the author asserts this transform is complete.
type mapNode struct{}

func (mapNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	overrides, _ := in.Config["overrides"].(map[string]any)
	rows, _ := in.Config["mappingRows"].([]any)
	if len(rows) == 0 {
		return nil, fmt.Errorf("data.map: no mapping rows resolved (publish resolves mappingRef)")
	}
	out := flow.Item{}
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		target, _ := row["target"].(string)
		if target == "" {
			continue
		}
		source, _ := row["source"].(string)
		if override, ok := overrides[target].(string); ok && override != "" {
			source = override
		}
		rendered := expr.RenderValueWithScope(source, scope)
		text, isText := rendered.(string)
		if isText && text == "" {
			if required, _ := row["required"].(bool); required {
				return nil, fmt.Errorf("data.map: required row %q rendered empty", target)
			}
			continue
		}
		out[target] = rendered
	}
	return flow.Output{"main": out}, nil
}

type ifNode struct{}

func (ifNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	ok, err := expr.EvalBoolWithScope(ctx, str(in.Config["condition"]), expr.FromInput(in))
	if err != nil {
		return nil, err
	}
	port := "false"
	if ok {
		port = "true"
	}
	return flow.Output{port: in.Item}, nil
}

type switchNode struct{}

// maxSwitchCases matches the case0..case2 output ports declared by the
// catalog. Ingest rejects more, but a stored workflow may predate that cap.
const maxSwitchCases = 3

func (switchNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	cases, _ := in.Config["cases"].([]any)
	for i, raw := range cases {
		expression, ok := raw.(string)
		if !ok {
			continue
		}
		match, err := expr.EvalBoolWithScope(ctx, expression, scope)
		if err != nil {
			return nil, err
		}
		if match {
			if i >= maxSwitchCases {
				return nil, fmt.Errorf("flow.switch: case %d matches but only %d cases are supported", i, maxSwitchCases)
			}
			return flow.Output{fmt.Sprintf("case%d", i): in.Item}, nil
		}
	}
	return flow.Output{"default": in.Item}, nil
}

type mergeNode struct{}

func (mergeNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	return flow.Output{"main": in.Item}, nil
}

type loopNode struct{}

func (loopNode) Execute(ctx context.Context, in flow.Input) (flow.Output, error) {
	scope := expr.FromInput(in)
	workflowID := expr.RenderWithScope(str(in.Config["workflowId"]), scope)
	if workflowID == "" {
		return nil, fmt.Errorf("flow.loop: workflowId is required")
	}

	var raw any
	if path := expr.RenderWithScope(str(in.Config["itemsPath"]), scope); path != "" {
		value, ok := flow.LookupPath(in.Item, path)
		if !ok {
			return nil, fmt.Errorf("flow.loop: itemsPath %q not found", path)
		}
		raw = value
	} else if value, ok := in.Item["items"]; ok {
		raw = value
	} else {
		raw = in.Item
	}

	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("flow.loop: expected an array, got %T", raw)
	}

	max := int(num(in.Config["maxIterations"]))
	if max <= 0 || max > len(arr) {
		max = len(arr)
	}

	concurrency := int(num(in.Config["concurrency"]))
	if concurrency <= 0 {
		concurrency = 1
	}

	results := make([]any, max)
	if concurrency == 1 {
		for i := 0; i < max; i++ {
			item := flow.Item{"value": arr[i]}
			switch v := arr[i].(type) {
			case map[string]any:
				item = flow.Item(v)
			case flow.Item:
				item = v
			}
			child, err := in.Subflow.RunSubflow(ctx, workflowID, item)
			if err != nil {
				return nil, fmt.Errorf("flow.loop iteration %d: %w", i, err)
			}
			results[i] = map[string]any(child)
		}
	} else {
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex

		for i := 0; i < max; i++ {
			errMu.Lock()
			stop := firstErr != nil
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
			go func(idx int) {
				defer wg.Done()
				defer func() { <-sem }()

				item := flow.Item{"value": arr[idx]}
				switch v := arr[idx].(type) {
				case map[string]any:
					item = flow.Item(v)
				case flow.Item:
					item = v
				}
				child, err := in.Subflow.RunSubflow(ctx, workflowID, item)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = fmt.Errorf("flow.loop iteration %d: %w", idx, err)
					}
					errMu.Unlock()
					return
				}
				results[idx] = map[string]any(child)
			}(i)
		}
		wg.Wait()
		if firstErr != nil {
			return nil, firstErr
		}
	}
	return flow.Output{"done": flow.Item{"items": results}}, nil
}
