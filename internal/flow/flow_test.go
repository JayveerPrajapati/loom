package flow_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"loom/internal/flow"
)

func TestLookupPath(t *testing.T) {
	item := flow.Item{"name": "Ada", "nested": map[string]any{"list": []any{map[string]any{"x": 1}}}}
	cases := []map[string]any{
		{"path": "", "want": item, "ok": true},
		{"path": "$json", "want": item, "ok": true},
		{"path": "json", "want": item, "ok": true},
		{"path": "name", "want": "Ada", "ok": true},
		{"path": "$json.name", "want": "Ada", "ok": true},
		{"path": "$.name", "want": "Ada", "ok": true},
		{"path": "json.name", "want": "Ada", "ok": true},
		{"path": "nested.list.0.x", "want": 1, "ok": true},
		{"path": "nested.list.9", "want": nil, "ok": false},
		{"path": "nested.list.x", "want": nil, "ok": false},
		{"path": "nested.list.-1", "want": nil, "ok": false},
		{"path": "name.first", "want": nil, "ok": false},
		{"path": "missing", "want": nil, "ok": false},
	}
	for _, tc := range cases {
		path := tc["path"].(string)
		got, ok := flow.LookupPath(item, path)
		if ok != tc["ok"].(bool) || !reflect.DeepEqual(got, tc["want"]) {
			t.Fatalf("LookupPath(%q) = %#v, %v; want %#v, %v", path, got, ok, tc["want"], tc["ok"])
		}
	}
}

func TestInputIdempotencyKey(t *testing.T) {
	if got := (flow.Input{}).IdempotencyKey(); got != "" {
		t.Fatalf("empty input key = %q", got)
	}
	base := flow.Input{RunID: "run-1", NodeID: "call"}
	if got := base.IdempotencyKey(); got != "run-1:call" {
		t.Fatalf("key = %q", got)
	}
	base.Context = &flow.Context{Index: 2}
	if got := base.IdempotencyKey(); got != "run-1:call" {
		t.Fatalf("key without index = %q", got)
	}
	base.Context.HasIndex = true
	if got := base.IdempotencyKey(); got != "run-1:call:item-2" {
		t.Fatalf("indexed key = %q", got)
	}
}

func TestIsSuspend(t *testing.T) {
	if _, ok := flow.IsSuspend(nil); ok {
		t.Fatal("nil is not a suspension")
	}
	if _, ok := flow.IsSuspend(errors.New("plain")); ok {
		t.Fatal("plain error is not a suspension")
	}
	suspend := &flow.Suspend{Reason: "approval", Data: map[string]any{"token": "t"}}
	got, ok := flow.IsSuspend(suspend)
	if !ok || got != suspend {
		t.Fatalf("IsSuspend = %#v, %v", got, ok)
	}
	if got.Error() != "suspended: approval" {
		t.Fatalf("suspend error = %q", got.Error())
	}
	wrapped := fmt.Errorf("node failed: %w", suspend)
	if _, ok := flow.IsSuspend(wrapped); !ok {
		t.Fatal("wrapped suspension was not detected")
	}
}

func TestCloneItem(t *testing.T) {
	if got := flow.CloneItem(nil); got == nil || len(got) != 0 {
		t.Fatalf("clone nil = %#v", got)
	}
	original := flow.Item{"name": "Ada", "nested": map[string]any{"n": 1}}
	clone := flow.CloneItem(original)
	original["name"] = "Grace"
	original["nested"].(map[string]any)["n"] = 2
	if clone["name"] != "Ada" {
		t.Fatalf("clone name = %v", clone["name"])
	}
	if clone["nested"].(map[string]any)["n"] != float64(1) {
		t.Fatalf("clone nested = %#v", clone["nested"])
	}
}

func TestNewIDAndToken(t *testing.T) {
	first := flow.NewID("run")
	second := flow.NewID("run")
	if !strings.HasPrefix(first, "run-") || len(first) <= len("run-") {
		t.Fatalf("id = %q", first)
	}
	if first == second {
		t.Fatalf("ids collided: %q", first)
	}
	token := flow.NewToken()
	if len(token) != 32 || token == flow.NewToken() {
		t.Fatalf("token = %q", token)
	}
}
