package store

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func keyRaceRun(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.CreateRun(Run{
		ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// Rotating the keyring while appends are in flight must stay safe: the
// keyring map is mutated while other goroutines resolve keys, and a run that
// started under key #1 keeps signing under key #1 across the rotation. The
// old key stays in the ring so its chain keeps verifying.
func TestRotationConcurrentWithAppends(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	keyRaceRun(t, s, "run-old")

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var appendErr error
	var mu sync.Mutex
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := s.AppendEvent("run-old", Event{Type: "node.finished", NodeID: "n", At: time.Now()}); err != nil {
				mu.Lock()
				appendErr = err
				mu.Unlock()
				return
			}
		}
	}()

	for i := 0; i < 3; i++ {
		if _, err := s.RotateKey(); err != nil {
			t.Fatalf("rotate %d: %v", i, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()

	if appendErr != nil {
		t.Fatalf("append during rotation: %v", appendErr)
	}

	// The run started under key #1; every event must verify under that key,
	// unaffected by the three rotations that happened mid-stream.
	var runKey string
	if err := s.readRow(`SELECT COALESCE(key_id, '') FROM runs WHERE id = 'run-old'`).Scan(&runKey); err != nil {
		t.Fatal(err)
	}
	if runKey == "" {
		t.Fatal("run has no key id")
	}
	keys := s.ListKeys()
	if len(keys) != 4 {
		t.Fatalf("keyring has %d keys, want 4 (bootstrap + 3 rotations)", len(keys))
	}
	currentKey := ""
	for _, k := range keys {
		if k.Current {
			currentKey = k.ID
		}
	}
	if runKey == currentKey {
		t.Fatal("run started before rotation was re-signed under the new current key")
	}
	if _, err := s.VerifyChain("run-old"); err != nil {
		t.Fatalf("chain broken by rotation: %v", err)
	}

	// A run created after the rotation signs under the new current key and
	// its chain verifies too; concurrent appends under both keys interleave.
	keyRaceRun(t, s, "run-new")
	var wg2 sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg2.Add(1)
		go func(g int) {
			defer wg2.Done()
			id := "run-old"
			if g%2 == 1 {
				id = "run-new"
			}
			for i := 0; i < 25; i++ {
				if _, err := s.AppendEvent(id, Event{Type: "node.finished", NodeID: "n", At: time.Now()}); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg2.Wait()

	if _, err := s.VerifyChain("run-old"); err != nil {
		t.Fatalf("run-old chain after mixed appends: %v", err)
	}
	if _, err := s.VerifyChain("run-new"); err != nil {
		t.Fatalf("run-new chain after mixed appends: %v", err)
	}

	keys = s.ListKeys()
	current := 0
	for _, k := range keys {
		if k.Current {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("keyring has %d current keys, want 1", current)
	}
}

// A key id that is not in the keyring fails closed even mid-batch: the
// append refuses rather than re-blessing events under a different key.
func TestAppendEventsUnknownKeyFailsClosed(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	keyRaceRun(t, s, "run-unknown")

	if _, err := s.db.Exec(`UPDATE runs SET key_id = 'missing' WHERE id = 'run-unknown'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("run-unknown", Event{Type: "node.finished", At: time.Now()}); err == nil || !strings.Contains(err.Error(), "not found in keyring") {
		t.Fatalf("unknown key append err = %v, want keyring failure", err)
	}
	events, err := s.ListEvents("run-unknown")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %d, want 0 (fail closed must not append)", len(events))
	}
}
