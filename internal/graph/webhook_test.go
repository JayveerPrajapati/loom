package graph

import "testing"

func TestValidateWebhookPathsRejectsDuplicates(t *testing.T) {
	hook := func(id string) *Workflow {
		return &Workflow{ID: id, Name: id, Version: 1, Nodes: []Node{
			mkNode("hook", "trigger.webhook", map[string]any{"path": "pwn", "method": "POST"}, nil),
		}}
	}
	a, b := hook("a"), hook("b")
	if err := ValidateWebhookPaths(a, []*Workflow{b}); err == nil {
		t.Fatal("duplicate webhook path was accepted")
	}
	if err := ValidateWebhookPaths(a, []*Workflow{a}); err != nil {
		t.Fatalf("a workflow collided with itself: %v", err)
	}
	if err := ValidateWebhookPaths(a, nil); err != nil {
		t.Fatalf("no other workflows should pass: %v", err)
	}
}
