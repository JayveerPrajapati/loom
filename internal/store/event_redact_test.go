package store

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

// Event data is stored redacted. The redactor's type switch only matched plain
// map[string]any, so a nested named map (flow.Item) kept its raw headers.
func TestAppendEventRedactsNestedNamedMaps(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	body := flow.Item{"headers": map[string]any{"set-cookie": "session=secret-value"}}
	if _, err := s.AppendEvent("run", Event{
		Type: "node.finished",
		Data: map[string]any{"response": map[string]any{"body": body}},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	raw, _ := json.Marshal(events[0].Data)
	if strings.Contains(string(raw), "session=secret-value") {
		t.Fatalf("stored event kept a raw nested header: %s", raw)
	}
	if !strings.Contains(string(raw), "redacted") {
		t.Fatalf("stored event was not masked at all: %s", raw)
	}
}
