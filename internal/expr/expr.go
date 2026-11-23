// Package expr renders templates and evaluates the small JavaScript expressions
// used by conditions and code steps.
package expr

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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
	// Host is wired automatically from the node input. $http is only
	// exposed to code-step bodies (AllowHostCalls); template rendering
	// shares this runtime and must stay side-effect free.
	Host flow.Host
	// AllowHostCalls gates $http to code-step bodies only.
	AllowHostCalls bool
	// Log receives console output from code steps; nil discards it.
	Log func(level, message string)
}

// FromInput builds a scope from a node's execution input.
func FromInput(in flow.Input) Scope {
	scope := Scope{Item: in.Item, Host: in.Host}
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

// RenderStrictWithScope renders exactly like RenderWithScope but reports
// the first failing expression instead of rendering it as the empty
// string. It powers the expression tester: the value it produces for a
// valid template is identical to what nodes render at run time.
func RenderStrictWithScope(tmpl string, scope Scope) (string, error) {
	var renderErr error
	out := templateRe.ReplaceAllStringFunc(tmpl, func(match string) string {
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
			if renderErr == nil {
				renderErr = fmt.Errorf("%s: %w", expression, err)
			}
			return ""
		}
		return format(v)
	})
	return out, renderErr
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
	v, err := runScript(ctx, "(function(){ return ("+expression+"); })()", scope, jsTimeout)
	if err != nil {
		return false, err
	}
	return v.ToBoolean(), nil
}

func EvalValue(ctx context.Context, expression string, item flow.Item) (any, error) {
	return EvalValueWithScope(ctx, expression, Scope{Item: item})
}

func EvalValueWithScope(ctx context.Context, expression string, scope Scope) (any, error) {
	v, err := runScript(ctx, "(function(){ return ("+strip(expression)+"); })()", scope, jsTimeout)
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
	return EvalObjectWithTimeout(ctx, source, scope, jsTimeout)
}

// EvalObjectWithTimeout runs a code-step body with an explicit execution
// timeout. The timeout is clamped to a sane positive duration by goja's
// interrupt mechanism, which still fires on context cancellation.
// Code steps are the only scripts allowed to reach the host.
func EvalObjectWithTimeout(ctx context.Context, source string, scope Scope, timeout time.Duration) (flow.Item, error) {
	scope.AllowHostCalls = true
	v, err := runScript(ctx, "(function(){\n"+source+"\n})()", scope, timeout)
	if err != nil {
		return nil, err
	}
	return toItem(v.Export())
}

func runScript(ctx context.Context, script string, scope Scope, timeout time.Duration) (goja.Value, error) {
	if len(script) > maxScriptLen {
		return nil, fmt.Errorf("javascript: script exceeds limit of %d bytes", maxScriptLen)
	}

	vm := goja.New()

	// Guard against large array and string memory bombs. vm.Interrupt fires at
	// interpreter boundaries, not inside a native allocation, so the allocator
	// builtins themselves must bound their output.
	_, _ = vm.RunString(`
		(function() {
			const MAX_STRING = 1048576;
			const MAX_ARRAY = 100000;

			const _OrigArray = Array;
			function SafeArray(...args) {
				if (args.length === 1 && typeof args[0] === 'number') {
					if (args[0] > MAX_ARRAY) {
						throw new RangeError("Array length exceeds maximum allowed limit (" + MAX_ARRAY + ")");
					}
				}
				return new _OrigArray(...args);
			}
			SafeArray.prototype = _OrigArray.prototype;
			// The shared prototype's constructor must point back at the capped
			// constructor, otherwise [].constructor escapes to _OrigArray.
			SafeArray.prototype.constructor = SafeArray;
			SafeArray.isArray = _OrigArray.isArray;
			// Array.from on a large array-like allocates from its length in one
			// native call, before the interpreter can be interrupted, so cap the
			// input's length property before delegating.
			SafeArray.from = function(items, mapFn, thisArg) {
				if (items !== null && items !== undefined) {
					const length = Number(items.length);
					if (length > MAX_ARRAY) {
						throw new RangeError("Array.from length exceeds maximum allowed limit (" + MAX_ARRAY + ")");
					}
				}
				return _OrigArray.from(items, mapFn, thisArg);
			};
			// Array.of allocates one element per argument; cap the count so an
			// apply/spread of many arguments cannot build an oversized array.
			SafeArray.of = function(...items) {
				if (items.length > MAX_ARRAY) {
					throw new RangeError("Array.of length exceeds maximum allowed limit (" + MAX_ARRAY + ")");
				}
				return new _OrigArray(...items);
			};
			Array = SafeArray;

			function _boundedCount(value, unitLength, what) {
				let count = Number(value);
				if (count !== count) {
					return 0;
				}
				count = count < 0 ? Math.ceil(count) : Math.floor(count);
				if (count > MAX_STRING) {
					throw new Error(what + " exceeds maximum allowed string length (" + MAX_STRING + ")");
				}
				if (count > 0 && unitLength > 0 && count > MAX_STRING / unitLength) {
					throw new Error(what + " exceeds maximum allowed string length (" + MAX_STRING + ")");
				}
				return count;
			}

			const _repeat = String.prototype.repeat;
			String.prototype.repeat = function(count) {
				const bounded = _boundedCount(count, String(this).length, "String.prototype.repeat");
				return _repeat.call(this, bounded);
			};

			const _padStart = String.prototype.padStart;
			String.prototype.padStart = function(targetLength, padString) {
				_boundedCount(targetLength, 0, "String.prototype.padStart");
				return _padStart.call(this, targetLength, padString);
			};

			const _padEnd = String.prototype.padEnd;
			String.prototype.padEnd = function(targetLength, padString) {
				_boundedCount(targetLength, 0, "String.prototype.padEnd");
				return _padEnd.call(this, targetLength, padString);
			};
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

	// console capture: code steps stream their output into the run's trace.
	consoleFn := func(level string) func(call goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, arg := range call.Arguments {
				if arg == nil || goja.IsUndefined(arg) || goja.IsNull(arg) {
					parts = append(parts, "undefined")
					continue
				}
				parts = append(parts, arg.String())
			}
			if scope.Log != nil {
				scope.Log(level, strings.Join(parts, " "))
			}
			return goja.Undefined()
		}
	}
	if err := vm.Set("console", map[string]any{
		"log": consoleFn("log"), "info": consoleFn("info"),
		"warn": consoleFn("warn"), "error": consoleFn("error"),
	}); err != nil {
		return nil, err
	}

	// $http routes script HTTP through the host — the SSRF guard and the
	// effect recording replay verifies stay in the loop. Code steps only:
	// template rendering must stay side-effect free.
	if scope.Host != nil && scope.AllowHostCalls {
		host := scope.Host
		if err := vm.Set("$http", func(opts map[string]any) (goja.Value, error) {
			if opts == nil {
				return nil, fmt.Errorf("$http: expected an options object {method, url, ...}")
			}
			req := flow.HTTPRequest{
				Method: strings.ToUpper(strValue(opts["method"])),
				URL:    strValue(opts["url"]),
			}
			if req.Method == "" {
				req.Method = "GET"
			}
			if req.URL == "" {
				return nil, fmt.Errorf("$http: url is required")
			}
			if raw, ok := opts["headers"].(map[string]any); ok {
				req.Headers = make(map[string]string, len(raw))
				for key, value := range raw {
					req.Headers[key] = fmt.Sprint(value)
				}
			}
			if json, ok := opts["json"]; ok && json != nil {
				req.Body = json
			} else if body, ok := opts["body"]; ok && body != nil {
				req.BodyRaw = []byte(fmt.Sprint(body))
			}
			req.ContentType = strValue(opts["contentType"])
			if seconds, ok := opts["timeout"].(float64); ok && seconds > 0 {
				req.Timeout = time.Duration(seconds * float64(time.Second))
			}
			resp, err := host.HTTP(ctx, req)
			if err != nil {
				return nil, fmt.Errorf("$http: %w", err)
			}
			return vm.ToValue(map[string]any{
				"statusCode": resp.Status,
				"statusText": resp.StatusText,
				"headers":    resp.Headers,
				"body":       resp.Body,
			}), nil
		}); err != nil {
			return nil, err
		}
	}

	// Batteries: hashing, base64. No fs, os, process, require or import.
	if err := vm.Set("$crypto", map[string]any{
		"sha256": func(s string) string {
			sum := sha256.Sum256([]byte(s))
			return hex.EncodeToString(sum[:])
		},
		"hmacSha256": func(s, key string) string {
			mac := hmac.New(sha256.New, []byte(key))
			mac.Write([]byte(s))
			return hex.EncodeToString(mac.Sum(nil))
		},
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("$base64", map[string]any{
		"encode": func(s string) string {
			return base64.StdEncoding.EncodeToString([]byte(s))
		},
		"decode": func(s string) (string, error) {
			data, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				return "", fmt.Errorf("$base64.decode: %w", err)
			}
			return string(data), nil
		},
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("btoa", func(s string) string {
		return base64.StdEncoding.EncodeToString([]byte(s))
	}); err != nil {
		return nil, err
	}
	if err := vm.Set("atob", func(s string) (string, error) {
		data, err := base64.StdEncoding.DecodeString(s)
		if err != nil {
			return "", fmt.Errorf("atob: %w", err)
		}
		return string(data), nil
	}); err != nil {
		return nil, err
	}
	if scope.Index != nil {
		if err := vm.Set("$index", *scope.Index); err != nil {
			return nil, err
		}
	}

	timer := time.AfterFunc(timeout, func() { vm.Interrupt("timeout") })
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

// strValue renders a JS option value as text; absent values are empty.
func strValue(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
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

// envAllowVar lists the environment names a workflow may read through $env.
// It is empty by default: workflow authors are untrusted, so the process
// environment is not exposed unless the operator opts specific names in.
const envAllowVar = "LOOM_EXPR_ENV_ALLOW"

func envScope() map[string]string {
	env := make(map[string]string)
	for _, entry := range strings.Split(os.Getenv(envAllowVar), ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if prefix, ok := strings.CutSuffix(entry, "*"); ok {
			if prefix == "" || envDenied(prefix) {
				continue
			}
			for _, kv := range os.Environ() {
				pair := strings.SplitN(kv, "=", 2)
				if len(pair) != 2 || !strings.HasPrefix(pair[0], prefix) || envDenied(pair[0]) {
					continue
				}
				env[pair[0]] = pair[1]
			}
			continue
		}
		if envDenied(entry) {
			continue
		}
		if value, ok := os.LookupEnv(entry); ok {
			env[entry] = value
		}
	}
	return env
}

// envDenied is a hard denylist applied even when a name is allow-listed, so
// the kernel's own secrets can never reach workflow code.
func envDenied(name string) bool {
	switch name {
	case "LOOM_TOKEN", "LOOM_SECRET_KEY_FILE", "LOOM_BOOTSTRAP_ADMIN_PASSWORD",
		"LOOM_E2E_CSRF", "PGPASSWORD", "AWS_SECRET_ACCESS_KEY", "GOOGLE_APPLICATION_CREDENTIALS":
		return true
	}
	for _, prefix := range []string{
		"LOOM_SECRET", "LOOM_SECRETS_", "AWS_", "GCP_", "GOOGLE_", "AZURE_",
		"OPENAI_", "ANTHROPIC_", "GITHUB_", "GITLAB_", "SLACK_", "STRIPE_",
		"VAULT_", "TWILIO_",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	switch name {
	case "DATABASE_URL", "POSTGRES_PASSWORD", "MYSQL_PASSWORD", "REDIS_URL", "MONGO_URI":
		return true
	}
	return false
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
