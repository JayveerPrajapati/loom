package store

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// A batch append must produce the same chain a sequence of single appends
// would: gap-free sequence numbers, chained previous hashes, and a head seal
// that matches the appended log.
func TestAppendEventsChainsAndSeals(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-batch", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	var batch []Event
	for i := 0; i < 5; i++ {
		batch = append(batch, Event{Type: "node.finished", NodeID: "n", At: time.Now()})
	}
	seqs, err := s.AppendEvents("run-batch", batch)
	if err != nil {
		t.Fatalf("AppendEvents: %v", err)
	}
	for i, seq := range seqs {
		if seq != int64(i+1) {
			t.Fatalf("seqs = %v, want 1..5", seqs)
		}
	}

	events, err := s.VerifyChain("run-batch")
	if err != nil {
		t.Fatalf("VerifyChain after batch: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("events = %d, want 5", len(events))
	}

	// A batch after existing single appends continues the chain seamlessly.
	if _, err := s.AppendEvent("run-batch", Event{Type: "run.finished", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	more := []Event{{Type: "audit.extra", At: time.Now()}, {Type: "audit.more", At: time.Now()}}
	if _, err := s.AppendEvents("run-batch", more); err != nil {
		t.Fatal(err)
	}
	events, err = s.VerifyChain("run-batch")
	if err != nil {
		t.Fatalf("VerifyChain after mixed appends: %v", err)
	}
	if len(events) != 8 {
		t.Fatalf("events = %d, want 8", len(events))
	}

	// An empty batch is a no-op that keeps the chain valid.
	if seqs, err := s.AppendEvents("run-batch", nil); err != nil || seqs != nil {
		t.Fatalf("empty batch: %v, %v", seqs, err)
	}
	events, err = s.VerifyChain("run-batch")
	if err != nil {
		t.Fatalf("VerifyChain after empty batch: %v", err)
	}
	if len(events) != 8 {
		t.Fatalf("events = %d, want 8 after empty batch", len(events))
	}
}

// One failed event rolls back the whole batch: the log, the seal, and the
// next sequence number must look as if the batch never happened.
func TestAppendEventsRollsBackWholeBatch(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-rollback", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("run-rollback", Event{Type: "run.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// Data that cannot round-trip through JSON fails the batch mid-way.
	bad := []Event{
		{Type: "node.finished", At: time.Now()},
		{Type: "node.failed", At: time.Now(), Data: map[string]any{"x": make(chan int)}},
		{Type: "run.failed", At: time.Now()},
	}
	if _, err := s.AppendEvents("run-rollback", bad); err == nil {
		t.Fatal("expected batch containing unmarshalable data to fail")
	}

	events, err := s.ListEvents("run-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 (whole batch rolled back)", len(events))
	}
	if _, err := s.VerifyChain("run-rollback"); err != nil {
		t.Fatalf("chain broken after rollback: %v", err)
	}

	// The next append continues gap-free from the pre-batch state.
	seq, err := s.AppendEvent("run-rollback", Event{Type: "run.finished", At: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if seq != 2 {
		t.Fatalf("seq after rollback = %d, want 2", seq)
	}
	if _, err := s.VerifyChain("run-rollback"); err != nil {
		t.Fatal(err)
	}
}

// A batch append must wake change subscribers, and concurrent batched and
// single appends must interleave without sequence gaps.
func TestAppendEventsNotifyAndConcurrency(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-conc", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	wake, stop := s.SubscribeRunChanges("run-conc")
	defer stop()

	if _, err := s.AppendEvents("run-conc", []Event{{Type: "run.started", At: time.Now()}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wake:
	case <-time.After(2 * time.Second):
		t.Fatal("batched append did not notify subscriber")
	}
	stop()

	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if i%2 == 0 {
					if _, err := s.AppendEvents("run-conc", []Event{
						{Type: "node.started", NodeID: "n", At: time.Now()},
						{Type: "node.finished", NodeID: "n", At: time.Now()},
					}); err != nil {
						t.Error(err)
						return
					}
				} else if _, err := s.AppendEvent("run-conc", Event{Type: "node.skipped", At: time.Now()}); err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	if _, err := s.VerifyChain("run-conc"); err != nil {
		t.Fatalf("chain broken under concurrency: %v", err)
	}
	events, err := s.ListEvents("run-conc")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1+4*15 {
		t.Fatalf("events = %d, want %d", len(events), 1+4*15)
	}
}

// The batched primitive must preserve redaction exactly like single appends.
func TestAppendEventsRedacts(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.CreateRun(Run{
		ID: "run-redact", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvents("run-redact", []Event{{
		Type: "node.finished", At: time.Now(),
		Data: map[string]any{"headers": map[string]any{"authorization": "Bearer secret"}},
	}}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListEvents("run-redact")
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatal("missing event")
	}
	raw, _ := json.Marshal(events[0].Data)
	if strings.Contains(string(raw), "Bearer secret") {
		t.Fatalf("batched append kept a raw authorization header: %s", raw)
	}
}

// BenchmarkAppendEvents compares the cost of 100 event appends per run —
// one transaction per event versus two batches of 50 — matching the shape
// of a 50-node run's event log (two events per node).
func BenchmarkAppendEvents(b *testing.B) {
	for _, mode := range []string{"single", "batch"} {
		b.Run(mode, func(b *testing.B) {
			s, err := Open(b.TempDir())
			if err != nil {
				b.Fatal(err)
			}
			defer s.Close()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				id := "bench-" + strconv.Itoa(i)
				if err := s.CreateRun(Run{
					ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
					Status: "done", Trigger: "manual", StartedAt: time.Now(),
				}); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if mode == "single" {
					for j := 0; j < 100; j++ {
						if _, err := s.AppendEvent(id, Event{Type: "node.finished", NodeID: "n", At: time.Now()}); err != nil {
							b.Fatal(err)
						}
					}
				} else {
					for j := 0; j < 2; j++ {
						batch := make([]Event, 50)
						for k := range batch {
							batch[k] = Event{Type: "node.finished", NodeID: "n", At: time.Now()}
						}
						if _, err := s.AppendEvents(id, batch); err != nil {
							b.Fatal(err)
						}
					}
				}
			}
		})
	}
}
