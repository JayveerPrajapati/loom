package store

import (
	"testing"
	"time"
)

func chainFixture(t *testing.T) (*Store, []Event) {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateRun(Run{
		ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	for i := 0; i < 3; i++ {
		if _, err := s.AppendEvent("run", Event{
			Type: "node.finished",
			Data: map[string]any{"i": i},
		}); err != nil {
			t.Fatalf("append event %d: %v", i, err)
		}
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	return s, events
}

func TestEventChainVerifies(t *testing.T) {
	s, events := chainFixture(t)
	if err := s.VerifyEventChain("run", events); err != nil {
		t.Fatalf("valid chain rejected: %v", err)
	}
	if events[0].Hash == "" {
		t.Fatal("first event has no hash")
	}
	for i := 1; i < len(events); i++ {
		if events[i].PrevHash != events[i-1].Hash {
			t.Fatalf("event %d prev hash does not link to event %d", i+1, i)
		}
	}
}

func TestEventChainDetectsDataTamper(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE events SET data = ? WHERE run_id = 'run' AND seq = 2`, []byte(`{"i":99}`)); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("run", events); err == nil {
		t.Fatal("tampered event data passed verification")
	}
}

func TestEventChainDetectsHashTamper(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE events SET hash = 'deadbeef' WHERE run_id = 'run' AND seq = 2`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("run", events); err == nil {
		t.Fatal("tampered event hash passed verification")
	}
}

func TestEventChainDetectsBrokenLink(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE events SET prev_hash = '' WHERE run_id = 'run' AND seq = 2`); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("run", events); err == nil {
		t.Fatal("broken prev-hash link passed verification")
	}
}

func TestEventChainRejectsStrippedChain(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE events SET prev_hash = NULL, hash = NULL WHERE run_id = 'run'`); err != nil {
		t.Fatalf("strip chain: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("run", events); err == nil {
		t.Fatal("stripped chain was accepted for a chained run")
	}
}

func TestEventChainAcceptsLegacyLog(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	for seq := 1; seq <= 2; seq++ {
		if _, err := s.db.Exec(`
			INSERT INTO events (run_id, seq, type, at, data)
			VALUES ('legacy', ?, 'node.finished', ?, '{}')`,
			seq, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			t.Fatalf("insert legacy event: %v", err)
		}
	}
	events, err := s.ListEvents("legacy")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("legacy", events); err != nil {
		t.Fatalf("legacy log rejected: %v", err)
	}
}
