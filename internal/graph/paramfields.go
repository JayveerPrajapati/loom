package graph

import (
	"encoding/json"
	"fmt"
	"strings"
)

// HTTP parameter-group rows (Configuration Hub v3 plan D2/D3): the editor
// writes typed value rows into bodyFields/queryFields/headerFields/
// pathFields; publish compiles them into the plain body/query/headers/
// pathParams config the engine already executes, so the engine and replay
// stay unaware of the row model. A present fields array wins over the raw
// object at publish; JSON editing mode clears the array. No envelope
// objects survive into the published doc: static rows compile to typed
// literals, expression rows to the template text itself.

const (
	ParamModeStatic     = "static"
	ParamModeExpression = "expression"
)

// ParamRow is one typed value row. Static rows carry a typed literal in
// Value (Type names it: string|number|boolean|json); expression rows
// carry the template text placed verbatim — the engine renders {{ }}
// at run time.
type ParamRow struct {
	Name  string `json:"name"`
	Mode  string `json:"mode"`
	Type  string `json:"type,omitempty"`
	Value any    `json:"value"`
}

// paramStaticTypes are the literal types a static row may carry.
var paramStaticTypes = map[string]bool{
	"string": true, "number": true, "boolean": true, "json": true,
}

// ParseParamRows decodes a raw *Fields config value into validated rows.
// Row errors name the offending row so the Inspector can point at it.
func ParseParamRows(raw any) ([]ParamRow, error) {
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array of rows")
	}
	rows := make([]ParamRow, 0, len(items))
	seen := map[string]bool{}
	for i, item := range items {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("row %d must be an object", i+1)
		}
		var row ParamRow
		name, _ := m["name"].(string)
		row.Name = strings.TrimSpace(name)
		mode, _ := m["mode"].(string)
		row.Mode = strings.TrimSpace(mode)
		typ, _ := m["type"].(string)
		row.Type = strings.TrimSpace(typ)
		row.Value = m["value"]
		if row.Name == "" {
			return nil, fmt.Errorf("row %d needs a name", i+1)
		}
		if seen[row.Name] {
			return nil, fmt.Errorf("duplicate row name %q", row.Name)
		}
		seen[row.Name] = true
		switch row.Mode {
		case ParamModeStatic:
			if row.Type == "" {
				row.Type = "string"
			}
			if !paramStaticTypes[row.Type] {
				return nil, fmt.Errorf("row %q: unknown static type %q (want string, number, boolean or json)", row.Name, row.Type)
			}
			if err := checkStaticValue(row.Name, row.Type, row.Value); err != nil {
				return nil, err
			}
		case ParamModeExpression:
			if s, ok := row.Value.(string); !ok || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("row %q: expression values must be a non-empty string", row.Name)
			}
		default:
			return nil, fmt.Errorf("row %q: mode must be static or expression, got %q", row.Name, row.Mode)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func checkStaticValue(name, typ string, value any) error {
	switch typ {
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("row %q: a string value is required", name)
		}
	case "number":
		if _, ok := value.(float64); !ok {
			return fmt.Errorf("row %q: a number value is required", name)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("row %q: a boolean value is required", name)
		}
	case "json":
		// Any JSON shape is valid.
	}
	return nil
}

// CompileParamRows folds validated rows into the plain parameter object:
// static rows keep their typed literal, expression rows are placed
// verbatim (templates render at run time).
func CompileParamRows(rows []ParamRow) map[string]any {
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		out[row.Name] = row.Value
	}
	return out
}

// OverlayRow is one mapping row projected onto a parameter group: Name is
// the parameter, Source is its value text.
type OverlayRow struct {
	Name   string
	Source string
}

// canonicalJSON reports whether s is JSON in canonical form: it parses and
// re-marshals to the identical text. Only then does a body mapping row
// compile to the typed value — "20" becomes the number 20, while "02134"
// and "1e3" stay strings, and anything with a template stays the literal
// text.
func canonicalJSON(s string) (any, bool) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return nil, false
	}
	var value any
	if err := json.Unmarshal([]byte(trimmed), &value); err != nil {
		return nil, false
	}
	repacked, err := json.Marshal(value)
	if err != nil || string(repacked) != trimmed {
		return nil, false
	}
	return value, true
}

// CompileOverlayRows compiles mapping rows for one parameter group.
// Query, headers and pathParams place the source text verbatim — the
// engine stringifies exactly as it always has. Body keeps typed literals
// when the source is canonical JSON so numbers and booleans survive,
// and treats everything else as the literal or template string.
func CompileOverlayRows(group string, rows []OverlayRow) map[string]any {
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		if group == "body" {
			if value, ok := canonicalJSON(row.Source); ok {
				out[row.Name] = value
				continue
			}
		}
		out[row.Name] = row.Source
	}
	return out
}
