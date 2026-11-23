package store

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func sealTestRun(t *testing.T, s *Store, id string, n int) {
	t.Helper()
	if err := s.CreateRun(Run{
		ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if _, err := s.AppendEvent(id, Event{Type: "node.start", At: time.Now()}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
}

// Deleting the tail of a run's event chain must fail verification: the head
// seal records (count, terminal hash) under the signing key, and a verifying
// prefix is no longer enough.
func TestTailTruncationDetected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealTestRun(t, s, "run-tail", 4)

	if _, err := s.db.Exec(`DELETE FROM events WHERE run_id = 'run-tail' AND seq > 2`); err != nil {
		t.Fatal(err)
	}
	rep, err := s.CheckHealth()
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK() {
		t.Fatal("tail truncation passed CheckHealth")
	}
	if len(rep.ChainFailures) == 0 || !strings.Contains(rep.ChainFailures[0], "head seal mismatch") {
		t.Fatalf("expected head seal mismatch, got %v", rep.ChainFailures)
	}
}

// A database-only attacker can also rewrite head_seq to match the truncated
// count — but cannot forge the MAC, so it still fails.
func TestHeadSealTamperDetected(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealTestRun(t, s, "run-tamper", 4)

	if _, err := s.db.Exec(`DELETE FROM events WHERE run_id = 'run-tamper' AND seq > 2`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET head_seq = 2 WHERE id = 'run-tamper'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET head_mac = '' WHERE id = 'run-tamper'`); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEventChain("run-tamper", mustEvents(t, s, "run-tamper")); err == nil {
		t.Fatal("tampered head seal verified")
	}
}

// Sealing is idempotent: re-opening a sealed directory does not touch any
// stored hash, and a pre-seal directory (head columns absent or blank) is
// stamped once and then verifies.
func TestSealMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	sealTestRun(t, s, "run-seal", 3)
	var headBefore string
	if err := s.db.QueryRow(`SELECT head_mac FROM runs WHERE id = 'run-seal'`).Scan(&headBefore); err != nil {
		t.Fatal(err)
	}
	s.Close()

	if _, err := os.Stat(filepath.Join(dir, "head.sealed")); err != nil {
		t.Fatalf("head.sealed marker missing: %v", err)
	}
	// Re-open: no re-stamp, hashes unchanged, still healthy.
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	var headAfter string
	if err := s2.db.QueryRow(`SELECT head_mac FROM runs WHERE id = 'run-seal'`).Scan(&headAfter); err != nil {
		t.Fatal(err)
	}
	if headBefore != headAfter {
		t.Fatal("re-opening a sealed store changed the head seal")
	}
	if rep, err := s2.CheckHealth(); err != nil || !rep.OK() {
		t.Fatalf("sealed store unhealthy after reopen: %v %v", rep, err)
	}

	// Simulate a pre-seal directory: columns blank and the marker gone.
	// Re-opening must stamp the seal exactly once, then detect truncation.
	s2.Close()
	if err := os.Remove(filepath.Join(dir, "head.sealed")); err != nil {
		t.Fatal(err)
	}
	s3, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if _, err := s3.db.Exec(`DELETE FROM events WHERE run_id = 'run-seal' AND seq > 2`); err != nil {
		t.Fatal(err)
	}
	// The open above already re-sealed the full chain; truncating after it
	// must now be caught.
	if err := s3.VerifyEventChain("run-seal", mustEvents(t, s3, "run-seal")); err == nil {
		t.Fatal("truncation after re-seal passed")
	}
}

// Appends continue to maintain the seal, so the normal flow stays healthy.
func TestSealMaintainedByAppend(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sealTestRun(t, s, "run-live", 5)
	if err := s.VerifyEventChain("run-live", mustEvents(t, s, "run-live")); err != nil {
		t.Fatalf("healthy run failed verification: %v", err)
	}
}

// The migration seals in bounded, independently committed batches: one
// batch never processes more than headSealBatchSize runs, a partial run of
// batches leaves durable progress (no marker, sealed rows stay sealed), and
// the next sealChainHeads call finishes only what remains.
func TestSealMigrationBatchesAndResumes(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	const extra = 7
	total := headSealBatchSize + extra
	for i := 0; i < total; i++ {
		sealTestRun(t, s, "run-batch-"+strconv.Itoa(i), 1)
	}
	// Simulate a pre-migration database: every head seal blank and the
	// completion marker gone.
	if _, err := s.db.Exec(`UPDATE runs SET head_seq = 0, head_mac = ''`); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(dir, "head.sealed")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	// One batch must stop at exactly the batch size and not write the
	// completion marker.
	n, err := s.sealChainHeadBatch()
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}
	if n != headSealBatchSize {
		t.Fatalf("first batch sealed %d runs, want %d", n, headSealBatchSize)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("marker written after a partial batch")
	}
	var unsealed int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE head_mac = ''`).Scan(&unsealed); err != nil {
		t.Fatal(err)
	}
	if unsealed != extra {
		t.Fatalf("%d runs still unsealed after one batch, want %d", unsealed, extra)
	}

	// A crash here would restart from the same state; finishing the
	// migration must only touch the remaining runs.
	if err := s.sealChainHeads(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM runs WHERE head_mac = ''`).Scan(&unsealed); err != nil {
		t.Fatal(err)
	}
	if unsealed != 0 {
		t.Fatalf("%d runs still unsealed after migration, want 0", unsealed)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("marker missing after migration completed: %v", err)
	}
	rep, err := s.CheckHealth()
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK() {
		t.Fatalf("batch-migrated store unhealthy: %v", rep.ChainFailures)
	}
}

func mustEvents(t *testing.T, s *Store, runID string) []Event {
	t.Helper()
	events, err := s.ListEvents(runID)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

// BenchmarkSealChainHeads measures a full pre-migration stamp of 2500 runs
// (5 batches) so the batched path's duration stays observable as the scheme
// and store shape evolve.
func BenchmarkSealChainHeads(b *testing.B) {
	dir := b.TempDir()
	s, err := Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer s.Close()
	const runs = 2500
	for i := 0; i < runs; i++ {
		id := "bench-" + strconv.Itoa(i)
		if err := s.CreateRun(Run{
			ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: time.Now(),
		}); err != nil {
			b.Fatal(err)
		}
		if _, err := s.AppendEvent(id, Event{Type: "node.start", At: time.Now()}); err != nil {
			b.Fatal(err)
		}
	}
	marker := filepath.Join(dir, "head.sealed")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		if _, err := s.db.Exec(`UPDATE runs SET head_seq = 0, head_mac = ''`); err != nil {
			b.Fatal(err)
		}
		if err := os.Remove(marker); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := s.sealChainHeads(); err != nil {
			b.Fatal(err)
		}
	}
}
