package graph

import (
	"strings"
	"testing"
)

// Configuration Hub v3 plan D2/D3: the typed parameter-row model. Static
// rows carry typed literals, expression rows carry template text; row
// shape errors name the offending row so the Inspector can point at it.
func TestParseParamRows(t *testing.T) {
	raw := []any{
		map[string]any{"name": "limit", "mode": "static", "type": "number", "value": float64(20)},
		map[string]any{"name": "offset", "mode": "expression", "value": "{{ $json.page * 20 }}"},
		map[string]any{"name": "flag", "mode": "static", "type": "boolean", "value": true},
		map[string]any{"name": "meta", "mode": "static", "type": "json", "value": map[string]any{"a": float64(1)}},
		map[string]any{"name": "plain", "mode": "static", "value": "text"}, // type defaults to string
	}
	rows, err := ParseParamRows(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}
	compiled := CompileParamRows(rows)
	if compiled["limit"] != float64(20) {
		t.Fatalf("static number = %#v, want 20", compiled["limit"])
	}
	if compiled["offset"] != "{{ $json.page * 20 }}" {
		t.Fatalf("expression = %#v", compiled["offset"])
	}
	if compiled["flag"] != true {
		t.Fatalf("static boolean = %#v", compiled["flag"])
	}
	if _, ok := compiled["meta"].(map[string]any); !ok {
		t.Fatalf("static json = %#v", compiled["meta"])
	}
	if compiled["plain"] != "text" {
		t.Fatalf("default string = %#v", compiled["plain"])
	}
}

func TestParseParamRowsRejectsBadShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"not an array", map[string]any{}, "must be an array"},
		{"row not object", []any{float64(1)}, "must be an object"},
		{"missing name", []any{map[string]any{"mode": "static", "value": "x"}}, "needs a name"},
		{"duplicate name", []any{
			map[string]any{"name": "a", "mode": "static", "value": "x"},
			map[string]any{"name": "a", "mode": "static", "value": "y"},
		}, "duplicate row name"},
		{"bad mode", []any{map[string]any{"name": "a", "mode": "magic", "value": "x"}}, "mode must be static or expression"},
		{"bad static type", []any{map[string]any{"name": "a", "mode": "static", "type": "cookie", "value": "x"}}, "unknown static type"},
		{"string value for number", []any{map[string]any{"name": "a", "mode": "static", "type": "number", "value": "20"}}, "a number value is required"},
		{"number value for boolean", []any{map[string]any{"name": "a", "mode": "static", "type": "boolean", "value": float64(1)}}, "a boolean value is required"},
		{"string value for boolean", []any{map[string]any{"name": "a", "mode": "static", "type": "boolean", "value": "true"}}, "a boolean value is required"},
		{"number value for string", []any{map[string]any{"name": "a", "mode": "static", "type": "string", "value": float64(20)}}, "a string value is required"},
		{"expression not string", []any{map[string]any{"name": "a", "mode": "expression", "value": float64(20)}}, "non-empty string"},
		{"expression empty", []any{map[string]any{"name": "a", "mode": "expression", "value": "  "}}, "non-empty string"},
	}
	for _, tc := range cases {
		_, err := ParseParamRows(tc.raw)
		if err == nil {
			t.Fatalf("%s: accepted", tc.name)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: error = %q, want substring %q", tc.name, err.Error(), tc.want)
		}
	}
}

// Mapping-row compile: body keeps typed literals only for canonical JSON
// (so "20" is the number 20 but "02134" stays a string); the string
// groups place source text verbatim.
func TestCompileOverlayRows(t *testing.T) {
	rows := []OverlayRow{
		{Name: "limit", Source: "20"},
		{Name: "zip", Source: "02134"},
		{Name: "active", Source: "true"},
		{Name: "meta", Source: `{"a":1}`},
		{Name: "tpl", Source: "{{ $json.page }}"},
		{Name: "text", Source: "hello"},
	}
	body := CompileOverlayRows("body", rows)
	if body["limit"] != float64(20) {
		t.Fatalf("body limit = %#v, want number 20", body["limit"])
	}
	if body["zip"] != "02134" {
		t.Fatalf("body zip = %#v, want the string 02134 (not canonical JSON)", body["zip"])
	}
	if body["active"] != true {
		t.Fatalf("body active = %#v, want boolean true", body["active"])
	}
	if meta, ok := body["meta"].(map[string]any); !ok || meta["a"] != float64(1) {
		t.Fatalf("body meta = %#v, want the parsed object", body["meta"])
	}
	if body["tpl"] != "{{ $json.page }}" {
		t.Fatalf("body tpl = %#v, want the template string verbatim", body["tpl"])
	}
	if body["text"] != "hello" {
		t.Fatalf("body text = %#v", body["text"])
	}

	query := CompileOverlayRows("query", rows)
	if query["limit"] != "20" {
		t.Fatalf("query limit = %#v, want the string 20 (string groups are verbatim)", query["limit"])
	}
	if query["active"] != "true" {
		t.Fatalf("query active = %#v, want the string true", query["active"])
	}
}

// Draft save catches row-shape and mappingRefs-shape errors on http.request
// nodes (v3 plan Phase 2).
func TestValidateHTTPParamRows(t *testing.T) {
	newWF := func(config map[string]any) *Workflow {
		return &Workflow{
			ID: "wf", Name: "WF", Version: 1,
			Nodes: []Node{
				{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
				{ID: "http", Type: "http.request", TypeVersion: 1, Config: config},
			},
			Edges: []Edge{
				{From: Endpoint{Node: "start", Port: "main"}, To: Endpoint{Node: "http", Port: "main"}},
			},
		}
	}
	cases := []struct {
		name   string
		config map[string]any
		code   string
	}{
		{"bad rows", map[string]any{
			"method": "GET", "url": "https://x.test",
			"queryFields": []any{map[string]any{"name": "", "mode": "static", "value": "x"}},
		}, "invalid_param_rows"},
		{"unknown mapping group", map[string]any{
			"method": "GET", "url": "https://x.test",
			"mappingRefs": map[string]any{"cookie": "mapping-1"},
		}, "invalid_mapping_refs"},
		{"empty mapping id", map[string]any{
			"method": "GET", "url": "https://x.test",
			"mappingRefs": map[string]any{"query": "  "},
		}, "invalid_mapping_refs"},
	}
	for _, tc := range cases {
		report := Validate(newWF(tc.config))
		found := false
		for _, issue := range report.Issues {
			if issue.Code == tc.code {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: no %s issue in %v", tc.name, tc.code, report.Issues)
		}
	}

	// A well-formed node reports none of the new codes.
	good := Validate(newWF(map[string]any{
		"method": "GET", "url": "https://x.test",
		"queryFields": []any{map[string]any{"name": "limit", "mode": "static", "type": "number", "value": float64(20)}},
		"mappingRefs": map[string]any{"query": "mapping-1"},
	}))
	for _, issue := range good.Issues {
		if issue.Code == "invalid_param_rows" || issue.Code == "invalid_mapping_refs" {
			t.Fatalf("well-formed node has %s: %s", issue.Code, issue.Message)
		}
	}
}
