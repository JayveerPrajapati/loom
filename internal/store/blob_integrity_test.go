package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadBlobRejectsTamperedContent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	hash, err := s.SaveBlob([]byte(`{"a":1}`))
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	path := filepath.Join(s.blobDir, hash[:2], hash)
	if err := os.WriteFile(path, []byte(`{"tampered":true}`), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, err := s.LoadBlob(hash); err == nil {
		t.Fatal("LoadBlob served content that does not match its hash")
	}
}

// Prune must keep effect tapes (referenced only inside node.effects data) and
// any blob referenced by a non-terminal run, while still deleting orphans.
func TestPruneKeepsEffectTapesAndNonTerminalBlobs(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	if err := s.CreateRun(Run{
		ID: "run-tape", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: now,
	}); err != nil {
		t.Fatalf("create tape run: %v", err)
	}
	if err := s.UpdateRunStatus("run-tape", "done", "", &now); err != nil {
		t.Fatalf("finish tape run: %v", err)
	}
	tape, err := s.SaveBlob([]byte(`{"runId":"run-tape","entries":[]}`))
	if err != nil {
		t.Fatalf("save tape: %v", err)
	}
	if _, err := s.AppendEvent("run-tape", Event{
		Type: "node.effects", NodeID: "call",
		Data: map[string]any{"blob": tape, "count": 0},
	}); err != nil {
		t.Fatalf("append tape event: %v", err)
	}

	if err := s.CreateRun(Run{
		ID: "run-waiting", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "waiting", Trigger: "manual", StartedAt: now,
	}); err != nil {
		t.Fatalf("create waiting run: %v", err)
	}
	waitingBlob, err := s.SaveBlob([]byte(`{"waiting":true}`))
	if err != nil {
		t.Fatalf("save waiting blob: %v", err)
	}
	if _, err := s.AppendEvent("run-waiting", Event{
		Type: "node.finished", NodeID: "call", OutputHash: waitingBlob,
	}); err != nil {
		t.Fatalf("append waiting event: %v", err)
	}

	orphan, err := s.SaveBlob([]byte(`{"orphan":true}`))
	if err != nil {
		t.Fatalf("save orphan: %v", err)
	}

	// Backdate only the blobs; the runs stay inside the retention window.
	if _, err := s.db.Exec(`UPDATE blobs SET created_at = ?`, old.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("backdate blobs: %v", err)
	}

	result, err := s.Prune(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if result.Runs != 0 {
		t.Fatalf("pruned %d retained runs, want 0", result.Runs)
	}
	if _, err := s.LoadBlob(tape); err != nil {
		t.Fatalf("effect tape was pruned: %v", err)
	}
	if _, err := s.LoadBlob(waitingBlob); err != nil {
		t.Fatalf("non-terminal run blob was pruned: %v", err)
	}
	if _, err := s.LoadBlob(orphan); err == nil {
		t.Fatal("orphan blob survived prune")
	}
	if result.Blobs != 1 {
		t.Fatalf("pruned %d blobs, want 1 (the orphan)", result.Blobs)
	}
}

// Only node.effects data is a tape reference. A data payload on another event
// type that happens to contain a "blob" key must not pin the blob.
func TestPruneIgnoresBlobKeyOnNonEffectsEvent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	now := time.Now()
	old := now.Add(-48 * time.Hour)

	if err := s.CreateRun(Run{
		ID: "run-data", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: now,
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := s.UpdateRunStatus("run-data", "done", "", &now); err != nil {
		t.Fatalf("finish run: %v", err)
	}
	blob, err := s.SaveBlob([]byte(`{"not":"a tape"}`))
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	if _, err := s.AppendEvent("run-data", Event{
		Type: "node.finished", NodeID: "call",
		Data: map[string]any{"blob": blob, "note": "user data, not a tape"},
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	if _, err := s.db.Exec(`UPDATE blobs SET created_at = ?`, old.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("backdate blobs: %v", err)
	}

	result, err := s.Prune(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if result.Runs != 0 {
		t.Fatalf("pruned %d retained runs, want 0", result.Runs)
	}
	if _, err := s.LoadBlob(blob); err == nil {
		t.Fatal("non-effects data blob survived prune as if it were a tape reference")
	}
	if result.Blobs != 1 {
		t.Fatalf("pruned %d blobs, want 1", result.Blobs)
	}
}
