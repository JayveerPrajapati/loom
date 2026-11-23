package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"loom/internal/host"
	"loom/internal/store"
)

// A legacy queued run with no input column must not execute the display-masked
// event log; it fails loudly instead.
func TestLegacyRedactedInputFailsRun(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := New(st, host.NewSim())

	wf := orphanTestWorkflow()
	snapshot, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	runID := "run-legacy-redacted"
	if err := st.CreateRun(store.Run{
		ID: runID, WorkflowID: wf.ID, WorkflowVersion: "1",
		Status: "queued", Trigger: "manual", StartedAt: time.Now().UTC(),
		Snapshot: snapshot,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := st.AppendEvent(runID, store.Event{
		Type: "run.started",
		Data: map[string]any{"input": map[string]any{"token": "[redacted]"}},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	eng.DrainQueued(context.Background())

	deadline := time.Now().Add(3 * time.Second)
	for {
		run, err := st.GetRun(runID)
		if err == nil && run.Status == "failed" {
			if !strings.Contains(run.Error, "redacted") {
				t.Fatalf("run error = %q, want a redaction error", run.Error)
			}
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("get run: %v", err)
			}
			t.Fatalf("run status = %q, want failed", run.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The persisted input column is still authoritative and never rejected.
func TestStoredInputIsPreferredOverEventLog(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := New(st, host.NewSim())

	runID := "run-stored-input"
	input, err := json.Marshal(map[string]any{"token": "real-value"})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID: runID, WorkflowID: "wf", WorkflowVersion: "1",
		Status: "queued", Trigger: "manual", StartedAt: time.Now().UTC(), Input: input,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	item, err := eng.inputForClaimed(runID)
	if err != nil {
		t.Fatalf("inputForClaimed: %v", err)
	}
	if got := item["token"]; got != "real-value" {
		t.Fatalf("token = %v, want real-value", got)
	}
}
