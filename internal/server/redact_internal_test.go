package server

import (
	"encoding/json"
	"strings"
	"testing"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/store"
)

// Sync run responses must not return what data-context/replay deliberately hide.
func TestRedactedResultMasksNodeOutputs(t *testing.T) {
	res := &engine.Result{
		RunID:  "run-1",
		Status: "done",
		Outputs: map[string]flow.Output{
			"call": {"main": flow.Item{"headers": map[string]any{"Set-Cookie": "session=secret-value"}}},
		},
		Events: []store.Event{{
			Type: "node.finished",
			Data: map[string]any{"response": map[string]any{
				"headers": map[string]any{"set-cookie": "session=secret-value"},
			}},
		}},
	}
	raw, err := json.Marshal(redactedResult(res))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(raw), "secret-value") {
		t.Fatalf("redacted result still contains the secret: %s", raw)
	}
	if !strings.Contains(string(raw), "redacted") {
		t.Fatalf("redacted result lost the mask marker: %s", raw)
	}
	original, _ := json.Marshal(res)
	if !strings.Contains(string(original), "secret-value") {
		t.Fatalf("redaction mutated the caller's result: %s", original)
	}
}
