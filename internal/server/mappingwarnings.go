package server

import (
	"fmt"
	"strings"

	"loom/internal/graph"
	"loom/internal/schemas"
)

// Mapping-schema compatibility warnings (Phase 6 D6-4): non-blocking
// checks run at publish between a data.map node's resolved mapping rows
// and its input/output data contracts. They never fail the publish —
// the response carries them and the editor surfaces them.

// MappingWarning is one non-blocking compatibility observation.
type MappingWarning struct {
	Node    string `json:"node"`
	Message string `json:"message"`
}

// mappingRows reads the publish-resolved mappingRows in either shape
// publish writes them ([]map[string]any) or a draft hand-edits them
// through raw JSON ([]any of map[string]any).
func mappingRows(raw any) []map[string]any {
	switch rows := raw.(type) {
	case []map[string]any:
		return rows
	case []any:
		typed := make([]map[string]any, 0, len(rows))
		for _, item := range rows {
			if row, ok := item.(map[string]any); ok {
				typed = append(typed, row)
			}
		}
		return typed
	default:
		return nil
	}
}

// mappingSchemaWarnings compares every data.map node's resolved mapping
// rows with the node's contracts. wf must already have gone through
// mapping resolution (mappingRows inlined) and schema resolution
// (InputSchemaFields/OutputSchemaFields filled).
func mappingSchemaWarnings(wf *graph.Workflow) []MappingWarning {
	var warnings []MappingWarning
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.Type != "data.map" {
			continue
		}
		rows := mappingRows(n.Config["mappingRows"])
		if len(rows) == 0 {
			continue
		}
		label := n.Name
		if label == "" {
			label = n.ID
		}
		var targets, sources []string
		for _, row := range rows {
			if target, _ := row["target"].(string); target != "" {
				targets = append(targets, target)
			}
			if source, _ := row["source"].(string); source != "" {
				sources = append(sources, source)
			}
		}
		if out := topLevelFields(n.OutputSchemaFields); len(out) > 0 {
			produced := map[string]bool{}
			for _, target := range targets {
				produced[target] = true
			}
			for _, target := range targets {
				if !out[target] {
					warnings = append(warnings, MappingWarning{
						Node:    label,
						Message: fmt.Sprintf("mapping target %q is not a field of the output contract — it will be produced but not validated", target),
					})
				}
			}
			for _, field := range n.OutputSchemaFields {
				if field.Required && !produced[field.Name] {
					warnings = append(warnings, MappingWarning{
						Node:    label,
						Message: fmt.Sprintf("required output contract field %q is not produced by any mapping row", field.Name),
					})
				}
			}
		}
		// Source refs: only plain path-shaped sources (a, a.b) can be
		// checked against the input contract's top-level fields.
		if in := topLevelFields(n.InputSchemaFields); len(in) > 0 {
			for _, source := range sources {
				if !isPlainPath(source) {
					continue
				}
				root := strings.SplitN(source, ".", 2)[0]
				if !in[root] {
					warnings = append(warnings, MappingWarning{
						Node:    label,
						Message: fmt.Sprintf("mapping source %q does not start with a field of the input contract", source),
					})
				}
			}
		}
	}
	return warnings
}

// topLevelFields flattens a contract's top-level field names.
func topLevelFields(fields []schemas.Field) map[string]bool {
	names := map[string]bool{}
	for _, field := range fields {
		names[field.Name] = true
	}
	return names
}

// isPlainPath reports whether a source expression is a bare dotted path
// (no operators, quotes, brackets or function calls) — the only shape
// the input-contract check can infer anything about.
func isPlainPath(source string) bool {
	if source == "" {
		return false
	}
	for _, r := range source {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}
