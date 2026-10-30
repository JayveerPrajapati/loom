package expr_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"loom/internal/expr"
	"loom/internal/flow"
)

func TestRenderTemplates(t *testing.T) {
	item := flow.Item{"name": "Ada", "n": 2, "obj": map[string]any{"ok": true}}
	cases := []map[string]string{
		{"name": "plain", "tmpl": "hello", "want": "hello"},
		{"name": "json path", "tmpl": "hi {{ $json.name }}", "want": "hi Ada"},
		{"name": "whole item", "tmpl": "{{ $json }}", "want": `{"n":2,"name":"Ada","obj":{"ok":true}}`},
		{"name": "javascript", "tmpl": "{{ $json.n + 1 }}", "want": "3"},
		{"name": "boolean", "tmpl": "{{ $json.n > 1 }}", "want": "true"},
		{"name": "object formatting", "tmpl": "{{ $json.obj }}", "want": `{"ok":true}`},
		{"name": "missing path", "tmpl": "x{{ $json.nope }}y", "want": "xy"},
		{"name": "invalid script", "tmpl": "{{ nope() }}", "want": ""},
	}
	for _, tc := range cases {
		t.Run(tc["name"], func(t *testing.T) {
			if got := expr.Render(tc["tmpl"], item); got != tc["want"] {
				t.Fatalf("Render(%q) = %q, want %q", tc["tmpl"], got, tc["want"])
			}
		})
	}
}

func TestRenderWithNodeScope(t *testing.T) {
	scope := expr.Scope{Item: flow.Item{"seed": 1}, Nodes: map[string]flow.Output{"set": {"main": {"greeting": "hello"}}}}
	if got := expr.RenderWithScope(`{{ $node["set"].json.greeting }}`, scope); got != "hello" {
		t.Fatalf("node scope = %q, want hello", got)
	}
	if got := expr.RenderWithScope(`{{ $node["set"].ports.main.greeting }}`, scope); got != "hello" {
		t.Fatalf("ports scope = %q, want hello", got)
	}
}

func TestRenderValueWalksNestedData(t *testing.T) {
	item := flow.Item{"name": "Ada"}
	in := map[string]any{"greeting": "hi {{ $json.name }}", "nested": []any{"{{ $json.name }}", map[string]any{"again": "{{ $json.name }}"}}, "number": 7}
	got := expr.RenderValue(in, item)
	want := map[string]any{"greeting": "hi Ada", "nested": []any{"Ada", map[string]any{"again": "Ada"}}, "number": 7}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("RenderValue = %#v, want %#v", got, want)
	}
}

func TestEvalBool(t *testing.T) {
	item := flow.Item{"count": 3}
	cases := []map[string]any{
		{"expression": "", "want": false},
		{"expression": "{{ $json.count > 1 }}", "want": true},
		{"expression": "{{ $json.count > 10 }}", "want": false},
		{"expression": "{{ true }}", "want": true},
	}
	for _, tc := range cases {
		expression := tc["expression"].(string)
		want := tc["want"].(bool)
		got, err := expr.EvalBool(context.Background(), expression, item)
		if err != nil {
			t.Fatalf("EvalBool(%q): %v", expression, err)
		}
		if got != want {
			t.Fatalf("EvalBool(%q) = %v, want %v", expression, got, want)
		}
	}
	if _, err := expr.EvalBool(context.Background(), "{{ nope() }}", item); err == nil {
		t.Fatal("EvalBool should fail for invalid JavaScript")
	}
}

func TestEvalValueScope(t *testing.T) {
	index := 4
	scope := expr.Scope{Item: flow.Item{"name": "Ada"}, Run: map[string]any{"id": "run-1", "trigger": "manual"}, Job: map[string]any{"id": "job-1"}, Vars: map[string]any{"region": "eu"}, Index: &index}
	cases := []map[string]string{
		{"expression": "{{ $json.name }}", "want": "Ada"},
		{"expression": "{{ $run.id }}", "want": "run-1"},
		{"expression": "{{ $job.id }}", "want": "job-1"},
		{"expression": "{{ $vars.region }}", "want": "eu"},
		{"expression": "{{ $index }}", "want": "4"},
	}
	for _, tc := range cases {
		got, err := expr.EvalValueWithScope(context.Background(), tc["expression"], scope)
		if err != nil {
			t.Fatalf("EvalValue(%q): %v", tc["expression"], err)
		}
		if fmt.Sprint(got) != tc["want"] {
			t.Fatalf("EvalValue(%q) = %v, want %s", tc["expression"], got, tc["want"])
		}
	}
}

func TestEvalObjectShapes(t *testing.T) {
	cases := []map[string]string{
		{"name": "object", "source": "return { greeting: 'hi ' + $json.name };", "want": `{"greeting":"hi Ada"}`},
		{"name": "array wrapped in items", "source": "return [1, 2];", "want": `{"items":[1,2]}`},
		{"name": "scalar wrapped in value", "source": "return 42;", "want": `{"value":42}`},
		{"name": "null becomes empty item", "source": "return null;", "want": `{}`},
	}
	for _, tc := range cases {
		t.Run(tc["name"], func(t *testing.T) {
			got, err := expr.EvalObject(context.Background(), tc["source"], flow.Item{"name": "Ada"})
			if err != nil {
				t.Fatalf("EvalObject: %v", err)
			}
			var want flow.Item
			if err := json.Unmarshal([]byte(tc["want"]), &want); err != nil {
				t.Fatalf("unmarshal expected item: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("EvalObject = %#v, want %#v", got, want)
			}
		})
	}
	if _, err := expr.EvalObject(context.Background(), "throw new Error('boom')", nil); err == nil {
		t.Fatal("EvalObject should surface JavaScript errors")
	}
}

func TestFromInput(t *testing.T) {
	index := 2
	in := flow.Input{Item: flow.Item{"name": "Ada"}, Context: &flow.Context{Nodes: map[string]flow.Output{"set": {"main": {"ok": true}}}, Run: flow.RunContext{ID: "run-1", Trigger: "manual", Status: "running"}, Job: flow.JobContext{ID: "job-1", Name: "nightly"}, Vars: map[string]any{"region": "eu"}, Index: index, HasIndex: true}}
	scope := expr.FromInput(in)
	if scope.Run["id"] != "run-1" || scope.Run["trigger"] != "manual" || scope.Run["status"] != "running" {
		t.Fatalf("run scope = %#v", scope.Run)
	}
	if scope.Job["id"] != "job-1" || scope.Job["name"] != "nightly" {
		t.Fatalf("job scope = %#v", scope.Job)
	}
	if scope.Vars["region"] != "eu" {
		t.Fatalf("vars = %#v", scope.Vars)
	}
	if scope.Index == nil || *scope.Index != index {
		t.Fatalf("index = %v", scope.Index)
	}
	if len(scope.Nodes["set"]["main"]) != 1 {
		t.Fatalf("nodes = %#v", scope.Nodes)
	}

	bare := expr.FromInput(flow.Input{Item: flow.Item{"x": 1}})
	if bare.Run != nil || bare.Job != nil || bare.Index != nil {
		t.Fatalf("nil context scope = %#v", bare)
	}
}

func TestEvalValueCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := expr.EvalValue(ctx, "{{ (function(){ while (true) {} })() }}", flow.Item{})
	if err == nil {
		t.Fatal("canceled context should interrupt script evaluation")
	}
}

func TestEvalEnvAndNow(t *testing.T) {
	t.Setenv("TEST_LOOM_VAR", "loom_val_123")
	rendered := expr.Render(`prefix_{{ $env.TEST_LOOM_VAR }}_suffix`, flow.Item{})
	if rendered != "prefix_loom_val_123_suffix" {
		t.Fatalf("rendered env = %q, want prefix_loom_val_123_suffix", rendered)
	}

	renderedNow := expr.Render(`{{ $now.length > 10 }}`, flow.Item{})
	if renderedNow != "true" {
		t.Fatalf("rendered now = %q, want true", renderedNow)
	}
}
