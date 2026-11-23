package graph

import "testing"

func TestBatchConcurrencyValidation(t *testing.T) {
	build := func(concurrency int, withBatch bool) *Workflow {
		call := mkNode("call", "http.request",
			map[string]any{"method": "GET", "url": "https://example.com"},
			map[string]string{"http": "cred_http"})
		if withBatch {
			call.Batch = &BatchPolicy{Concurrency: concurrency}
		}
		return &Workflow{
			ID: "batch", Name: "Batch", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				call,
			},
			Edges: []Edge{mkEdge("start", "main", "call", "main")},
		}
	}

	cases := []struct {
		name        string
		concurrency int
		withBatch   bool
		wantIssue   bool
	}{
		{"33 rejected", 33, true, true},
		{"32 accepted", 32, true, false},
		{"0 (default) accepted", 0, true, false},
		{"negative rejected", -1, true, true},
		{"absent accepted", 0, false, false},
	}
	for _, tc := range cases {
		report := Validate(build(tc.concurrency, tc.withBatch))
		got := hasCode(report, "invalid_batch_concurrency")
		if got != tc.wantIssue {
			t.Errorf("%s: invalid_batch_concurrency = %v, want %v (%v)", tc.name, got, tc.wantIssue, report.Issues)
		}
	}
}

func TestFlowWaitSecondsBounds(t *testing.T) {
	build := func(seconds float64) *Workflow {
		return &Workflow{
			ID: "wait", Name: "Wait", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				mkNode("wait", "flow.wait", map[string]any{"seconds": seconds}, nil),
			},
			Edges: []Edge{mkEdge("start", "main", "wait", "main")},
		}
	}

	cases := []struct {
		name      string
		seconds   float64
		wantIssue bool
	}{
		{"0 rejected", 0, true},
		{"2592001 rejected", 2592001, true},
		{"1 accepted", 1, false},
		{"86400 accepted", 86400, false},
		{"2592000 accepted", 2592000, false},
	}
	for _, tc := range cases {
		report := Validate(build(tc.seconds))
		got := hasCode(report, "invalid_config")
		if got != tc.wantIssue {
			t.Errorf("%s: invalid_config = %v, want %v (%v)", tc.name, got, tc.wantIssue, report.Issues)
		}
	}
}

func TestFlowSwitchCaseLimit(t *testing.T) {
	build := func(count int) *Workflow {
		cases := make([]any, count)
		for i := range cases {
			cases[i] = map[string]any{"when": "true"}
		}
		return &Workflow{
			ID: "switch", Name: "Switch", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				mkNode("switch", "flow.switch", map[string]any{"cases": cases}, nil),
			},
			Edges: []Edge{mkEdge("start", "main", "switch", "main")},
		}
	}

	cases := []struct {
		name      string
		count     int
		wantIssue bool
	}{
		{"4 rejected", 4, true},
		{"3 accepted", 3, false},
		{"0 accepted", 0, false},
	}
	for _, tc := range cases {
		report := Validate(build(tc.count))
		got := hasCode(report, "too_many_switch_cases")
		if got != tc.wantIssue {
			t.Errorf("%s: too_many_switch_cases = %v, want %v (%v)", tc.name, got, tc.wantIssue, report.Issues)
		}
	}
}

// ValidateStored must not enforce the runtime-safety caps added at ingest:
// workflows saved before the caps existed still have to load and run.
func TestValidateStoredSkipsRuntimeSafetyLimits(t *testing.T) {
	batch := mkNode("call", "http.request",
		map[string]any{"method": "GET", "url": "https://example.com"},
		map[string]string{"http": "cred_http"})
	batch.Batch = &BatchPolicy{Concurrency: 33}
	batchWF := &Workflow{
		ID: "batch", Name: "Batch", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			batch,
		},
		Edges: []Edge{mkEdge("start", "main", "call", "main")},
	}
	if report := ValidateStored(batchWF); hasCode(report, "invalid_batch_concurrency") {
		t.Fatalf("ValidateStored rejected stored batch concurrency: %v", report.Issues)
	}

	switchCases := make([]any, 4)
	for i := range switchCases {
		switchCases[i] = "{{ true }}"
	}
	switchWF := &Workflow{
		ID: "switch", Name: "Switch", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("switch", "flow.switch", map[string]any{"cases": switchCases}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "switch", "main")},
	}
	if report := ValidateStored(switchWF); hasCode(report, "too_many_switch_cases") {
		t.Fatalf("ValidateStored rejected stored switch cases: %v", report.Issues)
	}

	waitWF := &Workflow{
		ID: "wait", Name: "Wait", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("wait", "flow.wait", map[string]any{"seconds": float64(2592001)}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "wait", "main")},
	}
	if report := ValidateStored(waitWF); hasCode(report, "invalid_config") {
		t.Fatalf("ValidateStored rejected stored wait seconds: %v", report.Issues)
	}
}
