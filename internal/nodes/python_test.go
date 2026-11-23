package nodes

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"loom/internal/flow"
)

type fakeExecutor struct {
	*fakeHost
	captured flow.ExecRequest
	result   flow.ExecResult
	err      error
}

func newFakeExecutor() *fakeExecutor {
	return &fakeExecutor{fakeHost: &fakeHost{}}
}

func (f *fakeExecutor) Exec(_ context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	f.captured = req
	return f.result, f.err
}

func pythonConfig(source string, requirements []any) map[string]any {
	config := map[string]any{"source": source}
	if requirements != nil {
		config["requirements"] = requirements
	}
	return config
}

func TestPythonNodeExecutesViaHost(t *testing.T) {
	executor := &fakeExecutor{fakeHost: &fakeHost{}, result: flow.ExecResult{
		ExitCode: 0, Result: map[string]any{"doubled": 42.0},
	}}
	out, err := pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig(`return {"doubled": item["n"] * 2}`, nil),
		Item:   flow.Item{"n": 21},
		Host:   executor,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out["main"]["doubled"] != 42.0 {
		t.Fatalf("out = %#v", out["main"])
	}
	if fmt.Sprint(executor.captured.Stdin["n"]) != "21" {
		t.Fatalf("stdin = %#v", executor.captured.Stdin)
	}
	if executor.captured.Timeout == 0 {
		t.Fatal("default timeout must be set")
	}
}

func TestPythonNodeResultShaping(t *testing.T) {
	executor := &fakeExecutor{fakeHost: &fakeHost{}, result: flow.ExecResult{Result: []any{1.0, 2.0}}}
	out, err := pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig("return [1, 2]", nil), Host: executor,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if items, _ := out["main"]["items"].([]any); len(items) != 2 {
		t.Fatalf("out = %#v", out["main"])
	}

	executor = &fakeExecutor{fakeHost: &fakeHost{}, result: flow.ExecResult{Result: nil}}
	out, err = pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig("return None", nil), Host: executor,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(out["main"]) != 0 {
		t.Fatalf("nil result must be empty item, got %#v", out["main"])
	}
}

func TestPythonNodePinsRequirements(t *testing.T) {
	executor := &fakeExecutor{}
	_, err := pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig("return 1", []any{"httpx"}), // unpinned
		Host:   executor,
	})
	if err == nil || !strings.Contains(err.Error(), "name==version") {
		t.Fatalf("err = %v, want pinning required", err)
	}
	if executor.captured.EnvKey != "" {
		t.Fatal("unpinned requirements must not produce an env key")
	}

	_, err = pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig("return 1", []any{"httpx==0.27.0"}),
		Host:   executor,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// env key is the sha256 hex of the pinned set
	if len(executor.captured.EnvKey) != 64 {
		t.Fatalf("env key = %q", executor.captured.EnvKey)
	}
}

func TestPythonNodeWithoutExecutorHost(t *testing.T) {
	_, err := pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig("return 1", nil),
		Host:   &fakeHost{resp: flow.HTTPResponse{Status: 200}}, // no Exec
	})
	if err == nil || !strings.Contains(err.Error(), "subprocess") {
		t.Fatalf("err = %v, want a clear unavailability message", err)
	}
}

func TestPythonNodeEmptySource(t *testing.T) {
	_, err := pythonNode{}.Execute(context.Background(), flow.Input{
		Config: pythonConfig("  ", nil), Host: &fakeExecutor{},
	})
	if err == nil || !strings.Contains(err.Error(), "source is required") {
		t.Fatalf("err = %v", err)
	}
}

func TestPythonNodePassesMemoryCap(t *testing.T) {
	executor := &fakeExecutor{fakeHost: &fakeHost{}, result: flow.ExecResult{Result: map[string]any{}}}
	if _, err := (pythonNode{}).Execute(context.Background(), flow.Input{
		Config: pythonConfig(`return {"ok": True}`, nil),
		Item:   flow.Item{},
		Host:   executor,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if executor.captured.MaxMemoryMB != 512 {
		t.Fatalf("default memory cap = %d, want 512", executor.captured.MaxMemoryMB)
	}

	executor = &fakeExecutor{fakeHost: &fakeHost{}, result: flow.ExecResult{Result: map[string]any{}}}
	config := pythonConfig(`return {"ok": True}`, nil)
	config["maxMemoryMB"] = 4 // below the floor: clamped, never dropped
	if _, err := (pythonNode{}).Execute(context.Background(), flow.Input{
		Config: config, Item: flow.Item{}, Host: executor,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if executor.captured.MaxMemoryMB != 16 {
		t.Fatalf("clamped memory cap = %d, want 16", executor.captured.MaxMemoryMB)
	}

	executor = &fakeExecutor{fakeHost: &fakeHost{}, result: flow.ExecResult{Result: map[string]any{}}}
	config = pythonConfig(`return {"ok": True}`, nil)
	config["maxMemoryMB"] = 999999
	if _, err := (pythonNode{}).Execute(context.Background(), flow.Input{
		Config: config, Item: flow.Item{}, Host: executor,
	}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if executor.captured.MaxMemoryMB != 8192 {
		t.Fatalf("clamped memory cap = %d, want 8192", executor.captured.MaxMemoryMB)
	}
}
