package nodes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"loom/internal/flow"
)

type fakeSubflow struct {
	mu    sync.Mutex
	calls []flow.Item
	fn    func(flow.Item) (flow.Item, error)
}

func (f *fakeSubflow) RunSubflow(_ context.Context, _ string, item flow.Item) (flow.Item, error) {
	f.mu.Lock()
	f.calls = append(f.calls, item)
	f.mu.Unlock()
	if f.fn != nil {
		return f.fn(item)
	}
	return item, nil
}

func (f *fakeSubflow) RunSubflowAsync(ctx context.Context, workflowID string, item flow.Item) (flow.Item, error) {
	return f.RunSubflow(ctx, workflowID, item)
}

func (f *fakeSubflow) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func credentials(name string, data map[string]any) map[string]flow.Credential {
	return map[string]flow.Credential{name: {Type: name, Data: data}}
}

func TestCodeNode(t *testing.T) {
	out, err := codeNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"source": "return { sum: 1 + 2, name: $json.name };"},
		Item:   flow.Item{"name": "loom"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["sum"] != int64(3) && out["main"]["sum"] != float64(3) {
		t.Fatalf("sum = %#v", out["main"]["sum"])
	}
	if out["main"]["name"] != "loom" {
		t.Fatalf("name = %#v", out["main"]["name"])
	}
	if _, err := (codeNode{}).Execute(context.Background(), flow.Input{
		Config: map[string]any{"source": "throw new Error('boom')"},
	}); err == nil {
		t.Fatal("throwing script was accepted")
	}
}

func TestSetNode(t *testing.T) {
	out, err := setNode{}.Execute(context.Background(), flow.Input{
		Item:   flow.Item{"keep": "yes", "name": "loom"},
		Config: map[string]any{"values": map[string]any{"greeting": "hi {{ $json.name }}"}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["keep"] != "yes" || out["main"]["greeting"] != "hi loom" {
		t.Fatalf("output = %#v", out["main"])
	}

	out, err = setNode{}.Execute(context.Background(), flow.Input{
		Item:   flow.Item{"keep": "yes"},
		Config: map[string]any{"keepOnlySet": true, "values": map[string]any{"a": "b"}},
	})
	if err != nil {
		t.Fatalf("execute keepOnly: %v", err)
	}
	if _, ok := out["main"]["keep"]; ok || out["main"]["a"] != "b" {
		t.Fatalf("keepOnly output = %#v", out["main"])
	}
}

func TestIfNode(t *testing.T) {
	out, err := ifNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"ok": true}, Config: map[string]any{"condition": "{{ $json.ok }}"},
	})
	if err != nil || out["true"] == nil || out["false"] != nil {
		t.Fatalf("true branch = %#v, %v", out, err)
	}
	out, err = ifNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"ok": false}, Config: map[string]any{"condition": "{{ $json.ok }}"},
	})
	if err != nil || out["false"] == nil || out["true"] != nil {
		t.Fatalf("false branch = %#v, %v", out, err)
	}
	if _, err := (ifNode{}).Execute(context.Background(), flow.Input{
		Config: map[string]any{"condition": "(("},
	}); err == nil {
		t.Fatal("invalid condition was accepted")
	}
}

func TestSwitchNode(t *testing.T) {
	cases := []any{"{{ $json.kind == 'a' }}", "{{ $json.kind == 'b' }}", "{{ $json.kind == 'c' }}"}
	out, err := switchNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"kind": "b"}, Config: map[string]any{"cases": cases},
	})
	if err != nil || out["case1"] == nil {
		t.Fatalf("case1 = %#v, %v", out, err)
	}
	out, err = switchNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"kind": "z"}, Config: map[string]any{"cases": cases},
	})
	if err != nil || out["default"] == nil {
		t.Fatalf("default = %#v, %v", out, err)
	}
	if _, err := (switchNode{}).Execute(context.Background(), flow.Input{
		Item: flow.Item{"kind": "c"},
		Config: map[string]any{"cases": []any{
			"{{ false }}", "{{ false }}", "{{ false }}", "{{ true }}",
		}},
	}); err == nil || !strings.Contains(err.Error(), "flow.switch") {
		t.Fatalf("overflow case = %v, want flow.switch error", err)
	}
	// A fourth case that does not match is harmless: only a matching
	// out-of-range case is an error.
	out, err = switchNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"kind": "c"},
		Config: map[string]any{"cases": []any{
			"{{ false }}", "{{ false }}", "{{ false }}", "{{ false }}",
		}},
	})
	if err != nil || out["default"] == nil {
		t.Fatalf("unmatched overflow case = %#v, %v", out, err)
	}
	if _, err := (switchNode{}).Execute(context.Background(), flow.Input{
		Config: map[string]any{"cases": []any{"(("}},
	}); err == nil {
		t.Fatal("invalid case expression was accepted")
	}
}

func TestMergeNode(t *testing.T) {
	out, err := mergeNode{}.Execute(context.Background(), flow.Input{Item: flow.Item{"a": 1}})
	if err != nil || out["main"]["a"] != 1 {
		t.Fatalf("merge = %#v, %v", out, err)
	}
}

// A merge node with no explicit mode must resolve to the documented shallow
// default. The catalog briefly defaulted to append, which made the documented
// mode unreachable and changed the effective behavior of no-mode nodes.
func TestDefaultsUnknownAndMutableCopy(t *testing.T) {
	if got := Defaults("missing.node"); got != nil {
		t.Fatalf("Defaults(missing.node) = %#v, want nil", got)
	}

	def := Defaults("flow.merge")
	if got := def["mode"]; got != "shallow" {
		t.Fatalf("flow.merge default mode = %#v, want shallow", got)
	}
	def["mode"] = "append"
	if got := Defaults("flow.merge")["mode"]; got != "shallow" {
		t.Fatalf("shared catalog default was mutated: got %#v, want shallow", got)
	}
}

func TestMergeModeDefaultsToShallow(t *testing.T) {
	def, ok := catalog["flow.merge"]
	if !ok {
		t.Fatal("flow.merge is not in the catalog")
	}
	if got := Defaults("flow.merge")["mode"]; got != "shallow" {
		t.Fatalf("flow.merge default mode = %#v, want shallow", got)
	}
	for _, want := range []string{"shallow", "append", "byIndex"} {
		found := false
		for _, mode := range def.Config["mode"].Enum {
			if mode == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("flow.merge mode enum = %v, missing %q", def.Config["mode"].Enum, want)
		}
	}
}

func TestLoopNode(t *testing.T) {
	if _, err := (loopNode{}).Execute(context.Background(), flow.Input{}); err == nil {
		t.Fatal("loop without workflowId was accepted")
	}
	if _, err := (loopNode{}).Execute(context.Background(), flow.Input{
		Config:  map[string]any{"workflowId": "child", "itemsPath": "payload.items"},
		Item:    flow.Item{"payload": flow.Item{}},
		Subflow: &fakeSubflow{},
	}); err == nil {
		t.Fatal("missing itemsPath was accepted")
	}
	if _, err := (loopNode{}).Execute(context.Background(), flow.Input{
		Config:  map[string]any{"workflowId": "child"},
		Item:    flow.Item{"items": "nope"},
		Subflow: &fakeSubflow{},
	}); err == nil {
		t.Fatal("non-array items were accepted")
	}

	sub := &fakeSubflow{fn: func(item flow.Item) (flow.Item, error) {
		return flow.Item{"got": item["value"]}, nil
	}}
	out, err := loopNode{}.Execute(context.Background(), flow.Input{
		Config:  map[string]any{"workflowId": "child", "itemsPath": "payload.items", "maxIterations": 1},
		Item:    flow.Item{"payload": flow.Item{"items": []any{"a", "b"}}},
		Subflow: sub,
	})
	if err != nil {
		t.Fatalf("sequential loop: %v", err)
	}
	items, _ := out["done"]["items"].([]any)
	if len(items) != 1 || sub.count() != 1 {
		t.Fatalf("maxIterations: items=%d calls=%d", len(items), sub.count())
	}

	sub = &fakeSubflow{fn: func(item flow.Item) (flow.Item, error) {
		return flow.Item{"got": item["value"]}, nil
	}}
	out, err = loopNode{}.Execute(context.Background(), flow.Input{
		Config:  map[string]any{"workflowId": "child", "concurrency": 2},
		Item:    flow.Item{"items": []any{flow.Item{"a": 1}, flow.Item{"a": 2}}},
		Subflow: sub,
	})
	if err != nil {
		t.Fatalf("concurrent loop: %v", err)
	}
	if items, _ := out["done"]["items"].([]any); len(items) != 2 {
		t.Fatalf("concurrent items = %d", len(items))
	}

	failing := &fakeSubflow{fn: func(item flow.Item) (flow.Item, error) {
		return nil, errors.New("child failed")
	}}
	if _, err := (loopNode{}).Execute(context.Background(), flow.Input{
		Config:  map[string]any{"workflowId": "child", "concurrency": 2},
		Item:    flow.Item{"items": []any{1, 2}},
		Subflow: failing,
	}); err == nil || !strings.Contains(err.Error(), "flow.loop") {
		t.Fatalf("child failure = %v", err)
	}

}

func TestWaitNode(t *testing.T) {
	h := &fakeHost{}
	out, err := waitNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"a": 1}, Config: map[string]any{"seconds": 0.5}, Host: h,
	})
	if err != nil || out["main"]["a"] != 1 {
		t.Fatalf("wait = %#v, %v", out, err)
	}
	if len(h.sleeps) != 1 || h.sleeps[0] != 500*time.Millisecond {
		t.Fatalf("sleeps = %#v", h.sleeps)
	}
	negative := &fakeHost{}
	_, _ = waitNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"seconds": -3}, Host: negative,
	})
	if len(negative.sleeps) != 1 || negative.sleeps[0] != 0 {
		t.Fatalf("negative sleep = %#v", negative.sleeps)
	}
	over := &fakeHost{}
	if _, err := (waitNode{}).Execute(context.Background(), flow.Input{
		Config: map[string]any{"seconds": float64(maxWaitSeconds + 1)}, Host: over,
	}); err == nil || !strings.Contains(err.Error(), "flow.wait") {
		t.Fatalf("over-limit wait = %v, want flow.wait error", err)
	}
	if len(over.sleeps) != 0 {
		t.Fatalf("over-limit wait slept: %#v", over.sleeps)
	}

	now := time.Now().UTC()
	out, err = waitNode{}.Execute(context.Background(), flow.Input{
		Item:        flow.Item{"a": 1},
		Config:      map[string]any{"seconds": 1},
		DurableWait: true,
		WakeAt:      now.Add(-time.Second),
		Now:         now,
	})
	if err != nil || out["main"]["a"] != 1 {
		t.Fatalf("due durable wait = %#v, %v", out, err)
	}
}

func TestApprovalNode(t *testing.T) {
	_, err := approvalNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"message": "ship?"},
	})
	var suspend *flow.Suspend
	if !errors.As(err, &suspend) || suspend.Reason != "approval" || suspend.Data["message"] != "ship?" {
		t.Fatalf("suspend = %#v", err)
	}

	out, err := approvalNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{"title": "t"}, Decision: "approved",
	})
	if err != nil || out["approved"]["approved"] != true {
		t.Fatalf("approved = %#v, %v", out, err)
	}
	out, err = approvalNode{}.Execute(context.Background(), flow.Input{
		Item: flow.Item{}, Decision: "rejected",
	})
	if err != nil || out["rejected"]["approved"] != false {
		t.Fatalf("rejected = %#v, %v", out, err)
	}
	if _, err := (approvalNode{}).Execute(context.Background(), flow.Input{Decision: "maybe"}); err == nil {
		t.Fatal("invalid decision was accepted")
	}
}

func TestSubflowNode(t *testing.T) {
	if _, err := (subflowNode{}).Execute(context.Background(), flow.Input{}); err == nil {
		t.Fatal("subflow without workflowId was accepted")
	}
	sub := &fakeSubflow{fn: func(flow.Item) (flow.Item, error) { return flow.Item{"child": true}, nil }}
	out, err := subflowNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"workflowId": "child"}, Subflow: sub,
	})
	if err != nil || out["main"]["child"] != true {
		t.Fatalf("subflow = %#v, %v", out, err)
	}
	failing := &fakeSubflow{fn: func(flow.Item) (flow.Item, error) { return nil, errors.New("child failed") }}
	if _, err := (subflowNode{}).Execute(context.Background(), flow.Input{
		Config: map[string]any{"workflowId": "child"}, Subflow: failing,
	}); err == nil {
		t.Fatal("child failure was accepted")
	}
}

func TestPostgresRecognizesDeclaredWriteOperations(t *testing.T) {
	for _, tc := range []struct {
		query     string
		operation string
	}{
		{query: "insert into t values (1)", operation: "INSERT"},
		{query: "update t set a = 1", operation: " update "},
	} {
		if err := validatePostgresQuery(tc.query, tc.operation); err != nil {
			t.Fatalf("operation %q: %v", tc.operation, err)
		}
	}
}

func TestPostgresNode(t *testing.T) {
	h := &fakeHost{rows: []flow.Item{{"id": "1"}, {"id": "2"}}}
	var kinds []string
	out, err := postgresNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"operation": "query", "query": "select 1"},
		Host:   h, Credentials: map[string]string{"postgres": "cred"},
		Trace: func(kind string, _ map[string]any) { kinds = append(kinds, kind) },
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	rows, _ := out["main"]["rows"].([]any)
	if len(rows) != 2 || len(kinds) != 2 {
		t.Fatalf("rows=%d traces=%v", len(rows), kinds)
	}
	if _, err := (postgresNode{}).Execute(context.Background(), flow.Input{
		Host: &fakeHost{queryErr: errors.New("db down")},
	}); err == nil {
		t.Fatal("query error was accepted")
	}
}

func TestJSONParseScalar(t *testing.T) {
	out, err := jsonParseNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{"text": "42"},
	})
	if err != nil || out["main"]["value"] != float64(42) {
		t.Fatalf("scalar = %#v, %v", out, err)
	}
	if _, err := (jsonParseNode{}).Execute(context.Background(), flow.Input{
		Config: map[string]any{"text": "{"},
	}); err == nil {
		t.Fatal("invalid JSON was accepted")
	}
}

func TestCredentialField(t *testing.T) {
	if got := credentialField(flow.Input{}, "missing", "token"); got != "" {
		t.Fatalf("missing credential = %q", got)
	}
	in := flow.Input{CredentialData: credentials("x", map[string]any{"token": " t "})}
	if got := credentialField(in, "x", "token"); got != "t" {
		t.Fatalf("credential field = %q", got)
	}
}

func TestManualTriggerClonesItem(t *testing.T) {
	item := flow.Item{"a": "keep"}
	out, err := triggerNode{typ: "trigger.manual"}.Execute(context.Background(), flow.Input{Item: item})
	if err != nil || out["main"]["a"] != "keep" {
		t.Fatalf("manual = %#v, %v", out, err)
	}
	out["main"]["a"] = "changed"
	if item["a"] != "keep" {
		t.Fatal("trigger did not clone the input item")
	}
}
