package runbook_test

import (
	"strings"
	"testing"

	"loom/internal/graph"
	"loom/internal/runbook"
)

func TestRender(t *testing.T) {
	wf := &graph.Workflow{
		ID: "refund", Name: "Refund approval", Version: 3,
		Nodes: []graph.Node{
			{ID: "hook", Type: "trigger.webhook", TypeVersion: 1, Name: "Refund webhook", Config: map[string]any{"path": "refund"}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Ask for approval", Credentials: map[string]string{"slack": "cred_1"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "hook", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
		},
	}
	got := runbook.Render(wf)
	for _, want := range []string{
		"# Refund approval",
		"workflow `refund` (version 3)",
		"webhook `refund`",
		"`slack`",
		"| 2 | Ask for approval | `flow.approval` | 1 |",
		"`hook.main` → `approve.main`",
		"loom replay --data ./data <run-id>",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("runbook missing %q:\n%s", want, got)
		}
	}
}
