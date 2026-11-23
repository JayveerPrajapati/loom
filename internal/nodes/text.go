package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"loom/internal/expr"
	"loom/internal/flow"
)

type jsonParseNode struct{}

func (jsonParseNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	text := expr.RenderWithScope(str(in.Config["text"]), expr.FromInput(in))
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return nil, fmt.Errorf("data.json.parse: %w", err)
	}
	switch typed := value.(type) {
	case map[string]any:
		return flow.Output{"main": flow.Item(typed)}, nil
	case []any:
		return flow.Output{"main": flow.Item{"items": typed}}, nil
	default:
		return flow.Output{"main": flow.Item{"value": typed}}, nil
	}
}

type templateNode struct{}

func (templateNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	text := expr.RenderWithScope(str(in.Config["template"]), expr.FromInput(in))
	return flow.Output{"main": flow.Item{"text": text}}, nil
}
