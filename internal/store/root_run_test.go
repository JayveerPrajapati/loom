package store_test

import (
	"testing"
	"time"

	"loom/internal/store"
)

// TestRootRunIDPersistsAcrossReload: the event budget must survive a restart,
// so root_run_id is written by CreateRun and read back by every SELECT.
func TestRootRunIDPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	run := store.Run{
		ID:              "run-child",
		WorkflowID:      "wf-root",
		WorkflowVersion: "v1",
		Status:          "done",
		Trigger:         "event",
		StartedAt:       time.Now().UTC(),
		RootRunID:       "run-root",
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID:              "run-root",
		WorkflowID:      "wf-root",
		WorkflowVersion: "v1",
		Status:          "done",
		Trigger:         "manual",
		StartedAt:       time.Now().UTC(),
		RootRunID:       "run-root",
	}); err != nil {
		t.Fatalf("create root run: %v", err)
	}
	if got, err := st.GetRun("run-child"); err != nil || got.RootRunID != "run-root" {
		t.Fatalf("CreateRun/GetRun root = %q, %v; want run-root", got.RootRunID, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	reloaded, err := reopened.GetRun("run-child")
	if err != nil {
		t.Fatalf("get after reload: %v", err)
	}
	if reloaded.RootRunID != "run-root" {
		t.Fatalf("root after reload = %q, want run-root", reloaded.RootRunID)
	}
	count, err := reopened.CountRunsByRoot("run-root")
	if err != nil {
		t.Fatalf("count by root: %v", err)
	}
	if count != 2 {
		t.Fatalf("CountRunsByRoot = %d, want 2", count)
	}
	if count, err := reopened.CountRunsByRoot(""); err != nil || count != 0 {
		t.Fatalf("CountRunsByRoot(empty) = %d, %v; want 0, nil", count, err)
	}
}
