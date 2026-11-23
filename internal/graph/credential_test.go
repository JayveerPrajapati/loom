package graph

import "testing"

func TestRejectsTraversalCredentialID(t *testing.T) {
	start := mkNode("start", "trigger.manual", map[string]any{}, nil)
	call := mkNode("call", "http.request",
		map[string]any{"method": "GET", "url": "https://example.com"},
		map[string]string{"http": "../../outside/stolen"})
	wf := &Workflow{ID: "wf", Name: "wf", Version: 1, Nodes: []Node{start, call},
		Edges: []Edge{mkEdge("start", "main", "call", "main")}}
	report := Validate(wf)
	if !hasCode(report, "invalid_credential") {
		t.Fatalf("traversal credential id was accepted: %+v", report.Issues)
	}
}
