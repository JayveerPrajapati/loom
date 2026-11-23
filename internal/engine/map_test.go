package engine_test

import (
	"context"
	"testing"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
)

// The data.map node renders mapping rows through the real engine: overrides
// win per target, non-required empty rows drop, required empty rows fail the
// node with a named error (v2 D13).
func mapWorkflow(config map[string]any) *graph.Workflow {
	return &graph.Workflow{
		ID: "map-run", Name: "Map run", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "map", Type: "data.map", TypeVersion: 1, Name: "Map data", Config: config},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "map", Port: "main"}}},
	}
}

func TestDataMapNodeExecutes(t *testing.T) {
	eng := newEngine(t)
	wf := mapWorkflow(map[string]any{
		"mappingRef": "m1",
		"mappingRows": []any{
			map[string]any{"target": "email", "source": "{{ $json.email }}", "required": true},
			map[string]any{"target": "nickname", "source": "{{ $json.nick }}"},
			map[string]any{"target": "city", "source": "{{ $json.city }}"},
		},
		"overrides": map[string]any{"city": "{{ $json.city }} (overridden)"},
	})
	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual",
		Input:   flow.Item{"email": "a@b.c", "city": "SF"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %s (%s)", res.Status, res.Error)
	}
	out := map[string]any(res.Outputs["map"]["main"])
	if out["email"] != "a@b.c" {
		t.Fatalf("email = %v", out["email"])
	}
	if _, present := out["nickname"]; present {
		t.Fatalf("non-required empty row should drop: %v", out)
	}
	if out["city"] != "SF (overridden)" {
		t.Fatalf("override did not win: %v", out["city"])
	}
}

func TestDataMapRequiredRowFailsNode(t *testing.T) {
	eng := newEngine(t)
	wf := mapWorkflow(map[string]any{
		"mappingRef": "m1",
		"mappingRows": []any{
			map[string]any{"target": "email", "source": "{{ $json.missing }}", "required": true},
		},
	})
	res, err := eng.Start(context.Background(), wf, engine.Options{
		Trigger: "manual",
		Input:   flow.Item{"email": "a@b.c"},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %s — required-empty must fail the node", res.Status)
	}
	if res.Error == "" || !contains(res.Error, "email") {
		t.Fatalf("error does not name the row: %q", res.Error)
	}
}

func TestDataMapWithoutRowsFails(t *testing.T) {
	eng := newEngine(t)
	wf := mapWorkflow(map[string]any{"mappingRef": "m1"})
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "failed" {
		t.Fatalf("status = %s — unresolved mapping must fail loudly, not pass through", res.Status)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
