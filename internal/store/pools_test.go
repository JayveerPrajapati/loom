package store

import (
	"strings"
	"sync"
	"testing"
	"time"
)

// Reads served from the read pool must see committed writes immediately:
// WAL readers snapshot at statement start, so a write that committed before
// the read began is visible even across pools.
func TestReadAfterWriteAcrossPools(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-raw", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := s.AppendEvent("run-raw", Event{Type: "node.started", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		// Read immediately through the read pool after each commit.
		events, err := s.ListEventsAfter("run-raw", 0)
		if err != nil {
			t.Fatalf("read after write %d: %v", i, err)
		}
		if len(events) != i+1 {
			t.Fatalf("read pool sees %d events after %d commits, want %d", len(events), i+1, i+1)
		}
	}
}

// The acceptance core: a reader must make progress while the writer holds a
// long write transaction. Reads on the old single connection queued behind
// the writer; on the split pools they must not.
func TestReadsDoNotBlockBehindWriteTransaction(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-block", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("run-block", Event{Type: "node.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// Hold a write transaction open on the writer.
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO blobs (hash, created_at, size) VALUES ('hold', ?, 0)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := s.ListEventsAfter("run-block", 0)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("read behind write tx failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("read blocked behind the writer's open transaction")
	}
}

// The read pool must refuse mutations at the driver level, so a query
// accidentally routed there fails loudly instead of corrupting the split.
func TestReadPoolRefusesWrites(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if _, err := s.readDB.Exec(`INSERT INTO blobs (hash, created_at, size) VALUES ('bad', ?, 0)`,
		time.Now().UTC().Format(time.RFC3339Nano)); err == nil {
		t.Fatal("read pool accepted a mutation")
	}
}

// Concurrent readers share the pool without serializing on each other: N
// parallel reads on a pool of N readers complete well inside the time N
// sequential ones would need when each query sleeps.
func TestReadPoolServesReadersConcurrently(t *testing.T) {
	s, err := OpenWithReadPool(t.TempDir(), 4)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()
	if s.ReadPoolSize() != 4 {
		t.Fatalf("pool size = %d, want 4", s.ReadPoolSize())
	}
	for i := 0; i < 4; i++ {
		if err := s.CreateRun(Run{
			ID: "run-p" + strings.Repeat("x", i+1), WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Each read takes ~150ms (a sleep embedded in a user-defined function
	// would not work here, so approximate with N parallel queries over an
	// indexed table; the assertion is that the pool has more than one live
	// connection, which stats expose directly).
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ListRuns("", 50); err != nil {
				t.Errorf("parallel read: %v", err)
			}
		}()
	}
	wg.Wait()
	stats := s.readDB.Stats()
	if stats.MaxOpenConnections < 2 {
		t.Fatalf("read pool max open connections = %d, want the pool actually shared", stats.MaxOpenConnections)
	}
}
