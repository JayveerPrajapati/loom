package engine_test

import (
	"context"
	"encoding/json"
	"testing"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/schemas"
	"loom/internal/store"
)

// V2 D16: a node's output contract is validated and recorded on the
// node.finished event — recorded and surfaced, never a gate.
func TestNodeOutputContractRecorded(t *testing.T) {
	eng := newEngine(t)
	wf := &graph.Workflow{
		ID: "contract-out", Name: "Contract out", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set",
				Config: map[string]any{"values": map[string]any{"email": "a@b.c", "vip": "yes"}},
				OutputSchemaFields: []schemas.Field{
					{Name: "email", Type: schemas.TypeString, Required: true},
					{Name: "vip", Type: schemas.TypeBoolean},
				},
			},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}}},
	}
	res, err := eng.Start(context.Background(), wf, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %s (%s) — a contract violation must never fail the run", res.Status, res.Error)
	}
	for _, event := range res.Events {
		if event.Type != "node.finished" || event.NodeID != "set" {
			continue
		}
		contract, _ := event.Data["contract"].(map[string]any)
		if contract == nil {
			t.Fatal("node.finished carries no contract result")
		}
		if contract["valid"] != false {
			t.Fatalf("contract valid = %v, want false", contract["valid"])
		}
		rows, _ := contract["violations"].([]any)
		if len(rows) != 1 {
			t.Fatalf("violations = %+v, want the single vip violation", contract["violations"])
		}
		if row, _ := rows[0].(map[string]any); row["path"] != "vip" || row["message"] != "expected a boolean" {
			t.Fatalf("violation = %+v", rows[0])
		}
		return
	}
	t.Fatal("missing node.finished for set")
}

// V2: subflow contracts annotate both directions (D11 semantics — the
// verdict rides the item, nothing is rejected): the calling item is checked
// against the callee's input contract going in, and the callee's final item
// is checked against its output contract coming back.
func TestSubflowContractsAnnotateBothDirections(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	child := &graph.Workflow{
		ID: "child-contract", Name: "Child", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set",
				Config: map[string]any{"values": map[string]any{"total": "42"}},
			},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "set", Port: "main"}}},
		InputSchemaFields: []schemas.Field{
			{Name: "email", Type: schemas.TypeString, Required: true},
		},
		OutputSchemaFields: []schemas.Field{
			{Name: "total", Type: schemas.TypeNumber},
		},
	}
	if err := st.SaveWorkflow(child); err != nil {
		t.Fatalf("save child: %v", err)
	}

	parent := &graph.Workflow{
		ID: "parent-contract", Name: "Parent", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{
				ID: "sub", Type: "flow.subflow", TypeVersion: 1, Name: "Sub",
				Config: map[string]any{"workflowId": "child-contract", "wait": true},
			},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "sub", Port: "main"}}},
	}
	eng := engine.New(st, host.NewSim())

	// Missing required input field and a non-number output: both annotated.
	res, err := eng.Start(context.Background(), parent, engine.Options{Trigger: "manual", Input: flow.Item{}})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "done" {
		t.Fatalf("status = %s (%s) — contracts annotate, they never reject", res.Status, res.Error)
	}
	raw, _ := json.Marshal(res.Outputs["sub"]["main"])
	var subOut map[string]any
	if err := json.Unmarshal(raw, &subOut); err != nil {
		t.Fatalf("decode sub output: %v", err)
	}
	if subOut["schemaValid"] != false {
		t.Fatalf("output schemaValid = %v, want false", subOut["schemaValid"])
	}
	outRows, _ := subOut["schemaViolations"].([]any)
	if len(outRows) != 1 {
		t.Fatalf("output violations = %v", subOut["schemaViolations"])
	}
	if row, _ := outRows[0].(map[string]any); row["path"] != "total" {
		t.Fatalf("output violation path = %v, want total", outRows[0])
	}

	// The child run started from the annotated input: its run.started event
	// carries the input verdict.
	runs, err := st.ListChildRuns(res.RunID, 5)
	if err != nil || len(runs) != 1 {
		t.Fatalf("child runs = %v, %v", runs, err)
	}
	events, err := st.ListEvents(runs[0].ID)
	if err != nil {
		t.Fatalf("list child events: %v", err)
	}
	for _, ev := range events {
		if ev.Type != "run.started" {
			continue
		}
		data := ev.Data
		input, _ := data["input"].(map[string]any)
		if input["schemaValid"] != false {
			t.Fatalf("child input schemaValid = %v, want false", input["schemaValid"])
		}
		inRows, _ := input["schemaViolations"].([]any)
		if len(inRows) != 1 {
			t.Fatalf("input violations = %v", input["schemaViolations"])
		}
		if row, _ := inRows[0].(map[string]any); row["path"] != "email" {
			t.Fatalf("input violation path = %v, want email", inRows[0])
		}
		return
	}
	t.Fatal("child run.started not found")
}
