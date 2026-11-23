package graph

import (
	"fmt"
	"testing"
	"time"
)

func TestWorkflowNodeCap(t *testing.T) {
	nodes := make([]Node, maxNodes+1)
	nodes[0] = mkNode("n0", "trigger.manual", map[string]any{}, nil)
	for i := 1; i < len(nodes); i++ {
		nodes[i] = mkNode(fmt.Sprintf("n%d", i), "data.set", map[string]any{}, nil)
	}
	report := Validate(&Workflow{ID: "big", Name: "big", Version: 1, Nodes: nodes})
	if !hasCode(report, "workflow_too_large") {
		t.Fatalf("node cap not enforced: %+v", report.Issues)
	}
}

func TestWorkflowEdgeCap(t *testing.T) {
	edges := make([]Edge, maxEdges+1)
	for i := range edges {
		edges[i] = mkEdge("a", "main", "b", "main")
	}
	report := Validate(&Workflow{ID: "big", Name: "big", Version: 1,
		Nodes: []Node{
			mkNode("a", "trigger.manual", map[string]any{}, nil),
			mkNode("b", "data.set", map[string]any{}, nil),
		}, Edges: edges})
	if !hasCode(report, "workflow_too_large") {
		t.Fatalf("edge cap not enforced: %+v", report.Issues)
	}
}

// The ancestor index must keep a legal maximum-size graph fast; the old
// per-reference parents rebuild made an 8k-node body take seconds.
func TestMaxSizeChainValidatesQuickly(t *testing.T) {
	nodes := make([]Node, maxNodes)
	nodes[0] = mkNode("n0", "trigger.manual", map[string]any{}, nil)
	edges := make([]Edge, 0, maxNodes-1)
	for i := 1; i < maxNodes; i++ {
		nodes[i] = mkNode(fmt.Sprintf("n%d", i), "data.set", map[string]any{
			"values": map[string]any{"x": `{{ $node["n0"].main }}`},
		}, nil)
		edges = append(edges, mkEdge(fmt.Sprintf("n%d", i-1), "main", fmt.Sprintf("n%d", i), "main"))
	}
	start := time.Now()
	report := Validate(&Workflow{ID: "chain", Name: "chain", Version: 1, Nodes: nodes, Edges: edges})
	if report.HasErrors() {
		t.Fatalf("legal chain rejected: %+v", report.Issues)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("validating %d nodes took %s", maxNodes, elapsed)
	}
}
