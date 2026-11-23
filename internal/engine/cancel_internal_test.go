package engine

import (
	"context"
	"testing"
	"time"

	"loom/internal/host"
	"loom/internal/store"
)

// A cancel that races queue admission must re-read the registered handle and
// cancel the newly executing context instead of the stale parked one.
func TestCancelReReadsNewHandleAfterAdmission(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	eng := New(st, host.NewSim())

	runID := "run-cancel-race"
	now := time.Now().UTC()
	if err := st.CreateRun(store.Run{
		ID: runID, WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: now,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	calls := 0
	_, baseCancel := context.WithCancel(context.Background())
	wrapped := func() {
		calls++
		if calls == 2 {
			// Simulate the admitted goroutine observing the second cancel and
			// reaching a terminal state.
			at := time.Now().UTC()
			_ = st.UpdateRunStatus(runID, "cancelled", "", &at)
		}
		baseCancel()
	}
	eng.mu.Lock()
	eng.active[runID] = wrapped
	eng.mu.Unlock()

	if !eng.Cancel(runID) {
		t.Fatal("cancel returned false for an active run")
	}
	if calls < 2 {
		t.Fatalf("cancel handle called %d times, want at least 2", calls)
	}
	run, err := st.GetRun(runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "cancelled" {
		t.Fatalf("run status = %q, want cancelled", run.Status)
	}
}
