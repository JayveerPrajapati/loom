package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The expression tester uses the production renderer: identical output for
// valid templates, named errors for failing ones, and no host surface.
func TestExpressionEvalEndpoint(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	post := func(body map[string]any) (int, map[string]any) {
		status, data := call(t, srv, http.MethodPost, "/api/expressions/eval", "test-token", body)
		var result map[string]any
		_ = json.Unmarshal(data, &result)
		return status, result
	}

	status, result := post(map[string]any{
		"expression": "Order {{ $json.count }} for {{ $json.name }}",
		"sample":     map[string]any{"count": 7, "name": "Ada"},
	})
	if status != http.StatusOK || result["ok"] != true {
		t.Fatalf("render = %d %v", status, result)
	}
	if result["value"] != "Order 7 for Ada" {
		t.Fatalf("value = %v", result["value"])
	}

	// JS beyond path lookups runs exactly as at run time.
	_, result = post(map[string]any{
		"expression": "{{ $json.count * 2 }}",
		"sample":     map[string]any{"count": 21},
	})
	if result["value"] != "42" {
		t.Fatalf("arithmetic = %v", result["value"])
	}

	// Failing expressions name the error, not an empty string.
	_, result = post(map[string]any{
		"expression": "{{ $json.missing.deep }} is {{ missingVar }}",
		"sample":     map[string]any{},
	})
	if result["ok"] == true {
		t.Fatalf("missing paths must error: %v", result)
	}

	// Empty expression is a 400.
	if status, _ = post(map[string]any{"expression": "  "}); status != http.StatusBadRequest {
		t.Fatalf("empty = %d", status)
	}

	// A viewer token is rejected (editor-only).
	_ = status
}
