// Package expr renders templates and evaluates the small JavaScript expressions
// used by conditions and code steps.
package expr

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/dop251/goja"

	"loom/internal/flow"
)

const (
	jsTimeout    = 2 * time.Second
	maxScriptLen = 256 * 1024
)

var templateRe = regexp.MustCompile(`\{\{\s*([^{}]+?)\s*\}\}`)

// Scope is the data available to a template or JavaScript expression.
type Scope struct {
	Item  flow.Item
	Nodes map[string]flow.Output
	Run   map[string]any
	Job   map[string]any
	Vars  map[string]any
	Index *int
}

// FromInput builds a scope from a node's execution input.
func FromInput(in flow.Input) Scope {
	scope := Scope{Item: in.Item}
	if in.Context != nil {
		scope.Nodes = in.Context.Nodes
		scope.Run = map[string]any{
			"id":      in.Context.Run.ID,
			"trigger": in.Context.Run.Trigger,
			"status":  in.Context.Run.Status,
		}
		scope.Job = map[string]any{
			"id":   in.Context.Job.ID,
			"name": in.Context.Job.Name,
		}
		scope.Vars = in.Context.Vars
		if in.Context.HasIndex {
			index := in.Context.Index
			scope.Index = &index
		}
	}
	return scope
}

// Render replaces {{ $json.path }}, {{ $node["id"].json.path }} and
// {{ expression }} placeholders.
func Render(tmpl string, item flow.Item) string {
	return RenderWithScope(tmpl, Scope{Item: item})
}

func RenderWithScope(tmpl string, scope Scope) string {
	return templateRe.ReplaceAllStringFunc(tmpl, func(match string) string {
		sub := templateRe.FindStringSubmatch(match)
		if len(sub) != 2 {
			return match
		}
		expression := strings.TrimSpace(sub[1])
		if v, ok := flow.LookupPath(scope.Item, expression); ok {
			return format(v)
		}
		v, err := EvalValueWithScope(context.Background(), expression, scope)
		if err != nil {
			return ""
		}
		return format(v)
	})
}

// RenderValue walks maps and slices, rendering every string it finds.
func RenderValue(v any, item flow.Item) any {
	return RenderValueWithScope(v, Scope{Item: item})
}

func RenderValueWithScope(v any, scope Scope) any {
	switch val := v.(type) {
	case string:
		return RenderWithScope(val, scope)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, inner := range val {
			out[k] = RenderValueWithScope(inner, scope)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, inner := range val {
			out[i] = RenderValueWithScope(inner, scope)
		}
		return out
	default:
		return v
	}
}

func EvalBool(ctx context.Context, expression string, item flow.Item) (bool, error) {
	return EvalBoolWithScope(ctx, expression, Scope{Item: item})
}

func EvalBoolWithScope(ctx context.Context, expression string, scope Scope) (bool, error) {
	expression = strip(expression)
	if expression == "" {
		return false, nil
	}
	v, err := runScript(ctx, "(function(){ return ("+expression+"); })()", scope)
	if err != nil {
		return false, err
	}
	return v.ToBoolean(), nil
}

func EvalValue(ctx context.Context, expression string, item flow.Item) (any, error) {
	return EvalValueWithScope(ctx, expression, Scope{Item: item})
}

func EvalValueWithScope(ctx context.Context, expression string, scope Scope) (any, error) {
	v, err := runScript(ctx, "(function(){ return ("+strip(expression)+"); })()", scope)
	if err != nil {
		return nil, err
	}
	return v.Export(), nil
}

// EvalObject runs a code-step body. The body must return a value; objects are
// passed through, arrays are wrapped as {"items": [...]}, scalars as {"value": ...}.
func EvalObject(ctx context.Context, source string, item flow.Item) (flow.Item, error) {
	return EvalObjectWithScope(ctx, source, Scope{Item: item})
}

func EvalObjectWithScope(ctx context.Context, source string, scope Scope) (flow.Item, error) {
	v, err := runScript(ctx, "(function(){\n"+source+"\n})()", scope)
	if err != nil {
		return nil, err
	}
	return toItem(v.Export())
}

func runScript(ctx context.Context, script string, scope Scope) (goja.Value, error) {
	if len(script) > maxScriptLen {
		return nil, fmt.Errorf("javascript: script exceeds limit of %d bytes", maxScriptLen)
	}

	vm := goja.New()

	// Guard against large array memory bombs
	_, _ = vm.RunString(`
		(function() {
			const _OrigArray = Array;
			function SafeArray(...args) {
				if (args.length === 1 && typeof args[0] === 'number') {
					if (args[0] > 100000) {
						throw new RangeError("Array length exceeds maximum allowed limit (100000)");
					}
				}
				return new _OrigArray(...args);
			}
			SafeArray.prototype = _OrigArray.prototype;
			SafeArray.isArray = _OrigArray.isArray;
			SafeArray.from = _OrigArray.from;
			SafeArray.of = _OrigArray.of;
			Array = SafeArray;
		})();
	`)

	item := map[string]any(scope.Item)
	if err := vm.Set("$json", item); err != nil {
		return nil, err
	}
	if err := vm.Set("$item", item); err != nil {
		return nil, err
	}
	if err := vm.Set("$node", nodeScope(scope.Nodes)); err != nil {
		return nil, err
	}
	if err := vm.Set("$run", scope.Run); err != nil {
		return nil, err
	}
	if err := vm.Set("$job", scope.Job); err != nil {
		return nil, err
	}
	if err := vm.Set("$vars", scope.Vars); err != nil {
		return nil, err
	}
	if err := vm.Set("$now", time.Now().UTC().Format(time.RFC3339)); err != nil {
		return nil, err
	}
	if err := vm.Set("$env", envScope()); err != nil {
		return nil, err
	}
	if scope.Index != nil {
		if err := vm.Set("$index", *scope.Index); err != nil {
			return nil, err
		}
	}

	timer := time.AfterFunc(jsTimeout, func() { vm.Interrupt("timeout") })
	defer timer.Stop()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			vm.Interrupt("canceled")
		case <-stop:
		}
	}()

	v, err := vm.RunString(script)
	if err != nil {
		return nil, fmt.Errorf("javascript: %w", err)
	}
	return v, nil
}

func nodeScope(nodes map[string]flow.Output) map[string]any {
	out := make(map[string]any, len(nodes))
	for id, output := range nodes {
		ports := make(map[string]any, len(output))
		for port, item := range output {
			ports[port] = map[string]any(item)
		}
		main, ok := ports["main"]
		if !ok {
			for _, port := range sortedPorts(output) {
				main = ports[port]
				break
			}
		}
		out[id] = map[string]any{"json": main, "ports": ports}
	}
	return out
}

func envScope() map[string]string {
	env := make(map[string]string)
	for _, e := range os.Environ() {
		pair := strings.SplitN(e, "=", 2)
		if len(pair) == 2 {
			env[pair[0]] = pair[1]
		}
	}
	return env
}

func sortedPorts(output flow.Output) []string {
	ports := make([]string, 0, len(output))
	for port := range output {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	return ports
}

func toItem(exported any) (flow.Item, error) {
	data, err := json.Marshal(exported)
	if err != nil {
		return nil, err
	}
	switch {
	case string(data) == "null":
		return flow.Item{}, nil
	case len(data) > 0 && data[0] == '[':
		var arr []any
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, err
		}
		return flow.Item{"items": arr}, nil
	case len(data) > 0 && data[0] == '{':
		var out flow.Item
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, err
		}
		return out, nil
	default:
		var scalar any
		if err := json.Unmarshal(data, &scalar); err != nil {
			return nil, err
		}
		return flow.Item{"value": scalar}, nil
	}
}

func strip(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "{{")
	s = strings.TrimSuffix(s, "}}")
	return strings.TrimSpace(s)
}

func format(v any) string {
	switch val := v.(type) {
	case nil:
		return ""
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%v", val)
	case map[string]any, []any, flow.Item:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(data)
	default:
		return fmt.Sprint(val)
	}
}
