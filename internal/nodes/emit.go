package nodes

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"loom/internal/expr"
	"loom/internal/flow"
)

// MaxEventHops caps how many times an event may chain from workflow to
// workflow before the chain is treated as a cycle and the emitting node
// fails visibly instead of looping forever.
const MaxEventHops = 8

// emitNode is the flow.emit action: it publishes a named event so any
// workflow with a matching trigger.event node starts a run. On hosts
// without the EventPublisher capability (simulation) the publish is a
// no-op and the node still outputs the payload.
type emitNode struct{}

func (emitNode) Execute(_ context.Context, in flow.Input) (flow.Output, error) {
	name, _ := in.Config["event"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("event name is required")
	}
	payload := in.Item
	if raw, ok := in.Config["payload"]; ok {
		payload = emitPayload(expr.RenderValueWithScope(raw, expr.FromInput(in)))
	}
	hops := 0
	if in.Context == nil {
		return nil, errors.New("flow.emit needs a run context")
	}
	hops = in.Context.Run.Hops
	next := hops + 1
	if next > MaxEventHops {
		return nil, fmt.Errorf("event chain exceeds %d hops (possible cycle)", MaxEventHops)
	}
	rootRunID := in.Context.Run.RootRunID
	if rootRunID == "" {
		rootRunID = in.Context.Run.ID
	}
	if publisher, ok := in.Host.(flow.EventPublisher); ok {
		publisher.Publish(flow.Event{
			Name: name, Item: flow.Item(payload), Hops: next,
			SourceRunID: in.RunID, RootRunID: rootRunID,
		})
	}
	return flow.Output{"main": flow.Item(payload)}, nil
}

// emitPayload normalizes a rendered payload into an item. Objects stay
// objects, arrays become {"items": [...]} and scalars become {"value": ...}.
func emitPayload(value any) flow.Item {
	switch typed := value.(type) {
	case flow.Item:
		return typed
	case map[string]any:
		return flow.Item(typed)
	case []any:
		return flow.Item{"items": typed}
	case nil:
		return flow.Item{}
	default:
		return flow.Item{"value": typed}
	}
}
