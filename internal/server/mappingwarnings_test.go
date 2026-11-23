package server

import (
	"strings"
	"testing"

	"loom/internal/graph"
	"loom/internal/schemas"
)

// Phase 6 D6-4: mapping-schema compatibility warnings are non-blocking
// publish-response observations — target names absent from the output
// contract, required output fields nothing produces, and plain-path
// sources missing from the input contract's top level.

func TestMappingSchemaWarnings(t *testing.T) {
	wf := &graph.Workflow{
		Nodes: []graph.Node{
			{
				ID:   "n1",
				Name: "Shape lead",
				Type: "data.map",
				Config: map[string]any{
					"mappingRef": "map1",
					"mappingRows": []any{
						map[string]any{"target": "email", "source": "leadEmail", "required": true},
						map[string]any{"target": "extra", "source": "upper(name)"},
					},
				},
				InputSchemaFields: []schemas.Field{
					{Name: "leadEmail", Type: "string"},
					{Name: "name", Type: "string"},
					{Name: "score", Type: "number"},
				},
				OutputSchemaFields: []schemas.Field{
					{Name: "email", Type: "string", Required: true},
					{Name: "vip", Type: "boolean", Required: true},
				},
			},
		},
	}
	got := mappingSchemaWarnings(wf)
	joined := ""
	for _, w := range got {
		joined += w.Message + "\n"
	}
	// "extra" is produced but not in the output contract.
	if want := `mapping target "extra" is not a field of the output contract`; !strings.Contains(joined, want) {
		t.Errorf("warnings missing target-not-in-contract:\n%s", joined)
	}
	// "vip" is required but nothing produces it.
	if want := `required output contract field "vip" is not produced`; !strings.Contains(joined, want) {
		t.Errorf("warnings missing required-not-produced:\n%s", joined)
	}
	// "upper(name)" is not a plain path — never checked, never warned.
	if strings.Contains(joined, "upper(name)") {
		t.Errorf("non-path source should not be checked:\n%s", joined)
	}
	// Every plain-path source here resolves against the input contract.
	if strings.Contains(joined, "does not start with a field") {
		t.Errorf("unexpected source warning:\n%s", joined)
	}
	if len(got) != 2 {
		t.Errorf("expected exactly 2 warnings, got %d: %v", len(got), got)
	}
}

func TestMappingSchemaWarningsSourceMismatch(t *testing.T) {
	wf := &graph.Workflow{
		Nodes: []graph.Node{
			{
				ID:   "n1",
				Type: "data.map",
				Config: map[string]any{
					"mappingRows": []any{
						map[string]any{"target": "a", "source": "payload.id"},
					},
				},
				InputSchemaFields: []schemas.Field{
					{Name: "item", Type: "object"},
				},
			},
		},
	}
	got := mappingSchemaWarnings(wf)
	if len(got) != 1 {
		t.Fatalf("expected 1 warning, got %d: %v", len(got), got)
	}
	if want := `mapping source "payload.id" does not start with a field of the input contract`; got[0].Message != want {
		t.Errorf("message = %q, want %q", got[0].Message, want)
	}
}

func TestMappingSchemaWarningsQuietWhenNoContracts(t *testing.T) {
	wf := &graph.Workflow{
		Nodes: []graph.Node{
			{
				ID:   "n1",
				Type: "data.map",
				Config: map[string]any{
					"mappingRows": []any{
						map[string]any{"target": "anything", "source": "whatever.x"},
					},
				},
			},
		},
	}
	if got := mappingSchemaWarnings(wf); len(got) != 0 {
		t.Errorf("no contracts pinned — expected no warnings, got %v", got)
	}
}
