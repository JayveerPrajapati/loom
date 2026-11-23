package store

import (
	"testing"
	"time"
)

// The backfill must run exactly once. Otherwise an attacker clears chain_tag,
// restarts, and the migration re-blesses a downgraded chain.
func TestChainDowngradeSurvivesRestartFails(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := s.CreateRun(Run{
		ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if _, err := s.AppendEvent("run", Event{Type: "run.started"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if _, err := s.AppendEvent("run", Event{Type: "run.finished"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	_ = s.Close()

	// Tamper, then restart. The marker created on the first open must stop the
	// backfill from stamping a fresh tag over version 0.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if _, err := s2.db.Exec(`UPDATE runs SET chain_version = 0, chain_tag = '' WHERE id = 'run'`); err != nil {
		t.Fatalf("downgrade: %v", err)
	}
	if _, err := s2.db.Exec(`UPDATE events SET hash = '', prev_hash = '' WHERE run_id = 'run'`); err != nil {
		t.Fatalf("strip: %v", err)
	}
	_ = s2.Close()

	s3, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after tamper: %v", err)
	}
	defer s3.Close()
	events, err := s3.ListEvents("run")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := s3.VerifyEventChain("run", events); err == nil {
		t.Fatal("downgraded chain passed verification after a restart")
	}
}

// A DB-only attacker flipping the execution mode must not turn a hermetic run
// live: effects are covered by the run's HMAC tag.
func TestRunEffectsTamperRejected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "waiting", Trigger: "replay", StartedAt: time.Now(),
		Effects: "replay", CassetteRunID: "origin",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if mode, source, err := s.RunEffects("run"); err != nil || mode != "replay" || source != "origin" {
		t.Fatalf("run effects = %q/%q err=%v", mode, source, err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET effects = '', cassette_run_id = '' WHERE id = 'run'`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if _, _, err := s.RunEffects("run"); err == nil {
		t.Fatal("tampered effects passed the chain tag check")
	}
}
