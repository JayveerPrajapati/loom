package store

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func streamFixture(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.CreateRun(Run{ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := s.AppendEvent("run", Event{Type: "node.finished", NodeID: "n", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

// The streaming verifier (verifyRunChain) must accept and reject exactly
// what the sliced verifier (VerifyChain) does across every tamper class.
func TestStreamingVerifierMatchesSliced(t *testing.T) {
	cases := map[string]func(*Store){
		"healthy":         func(s *Store) {},
		"tail truncation": func(s *Store) { _, _ = s.db.Exec(`DELETE FROM events WHERE run_id = 'run' AND seq > 2`) },
		"event rewrite":   func(s *Store) { _, _ = s.db.Exec(`UPDATE events SET error = 'x' WHERE run_id = 'run' AND seq = 2`) },
		"seq gap":         func(s *Store) { _, _ = s.db.Exec(`DELETE FROM events WHERE run_id = 'run' AND seq = 2`) },
		"prev-hash break": func(s *Store) {
			_, _ = s.db.Exec(`UPDATE events SET prev_hash = 'deadbeef' WHERE run_id = 'run' AND seq = 3`)
		},
		"seal rewrite":     func(s *Store) { _, _ = s.db.Exec(`UPDATE runs SET head_seq = 9 WHERE id = 'run'`) },
		"missing key":      func(s *Store) { _, _ = s.db.Exec(`UPDATE runs SET key_id = 'gone' WHERE id = 'run'`) },
		"chain tag change": func(s *Store) { _, _ = s.db.Exec(`UPDATE runs SET chain_tag = 'nope' WHERE id = 'run'`) },
		"mid-chain delete": func(s *Store) { _, _ = s.db.Exec(`DELETE FROM events WHERE run_id = 'run' AND seq = 2`) },
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			s := streamFixture(t)
			tamper(s)
			_, slicedErr := s.VerifyChain("run")
			n, streamErr := s.verifyRunChain("run")
			if (slicedErr == nil) != (streamErr == nil) {
				t.Fatalf("verifiers disagree: sliced=%v streaming=%v", slicedErr, streamErr)
			}
			if slicedErr == nil && n != 4 {
				t.Fatalf("streaming counted %d events, want 4", n)
			}
		})
	}
}

// A pre-chain (legacy) run — unhashed events, seal stamped by the migration —
// must pass the streaming verifier just like the sliced one.
func TestStreamingVerifierAcceptsLegacyRuns(t *testing.T) {
	s := streamFixture(t)
	s.keyMu.RLock()
	key := s.legacyKey
	s.keyMu.RUnlock()
	tag := s.chainTag(key.macKey, "legacy", 0, "", "")
	seal := s.headMAC(key.macKey, "legacy", 3, "")
	if _, err := s.db.Exec(`INSERT INTO runs (id, workflow_id, workflow_version, status, trigger, started_at, chain_version, chain_tag, head_seq, head_mac)
		VALUES ('legacy', 'wf', 'v1', 'done', 'manual', '2020-01-01T00:00:00Z', 0, ?, 3, ?)`, tag, seal); err != nil {
		t.Fatal(err)
	}
	for seq := 1; seq <= 3; seq++ {
		if _, err := s.db.Exec(`INSERT INTO events (run_id, seq, type, at, data, hash, prev_hash)
			VALUES ('legacy', ?, 'node.finished', '2020-01-01T00:00:00Z', '{}', '', '')`, seq); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.verifyRunChain("legacy"); err != nil {
		t.Fatalf("streaming verifier rejected legacy run: %v", err)
	}
	if _, err := s.VerifyChain("legacy"); err != nil {
		t.Fatalf("sliced verifier rejected legacy run: %v", err)
	}
}

// Findings lists are capped while the counts keep the true totals, so a
// large broken store cannot grow the report without bound.
func TestHealthReportCapsAndCounts(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := 0; i < 25; i++ {
		id := fmt.Sprintf("run-%02d", i)
		if err := s.CreateRun(Run{ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.AppendEvent(id, Event{Type: "node.finished", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
		_, _ = s.db.Exec(`UPDATE events SET error = 'x' WHERE run_id = ? AND seq = 1`, id)
	}
	report, err := s.CheckHealth()
	if err != nil {
		t.Fatal(err)
	}
	if report.ChainFailureCount != 25 {
		t.Fatalf("chain failure count = %d, want 25", report.ChainFailureCount)
	}
	if len(report.ChainFailures) != healthFailureCap+1 {
		t.Fatalf("retained failures = %d, want cap + one truncation marker", len(report.ChainFailures))
	}
	if !strings.HasSuffix(report.ChainFailures[len(report.ChainFailures)-1], "and 5 more") {
		t.Fatalf("truncation marker missing: %v", report.ChainFailures[len(report.ChainFailures)-1])
	}
	if report.OK() {
		t.Fatal("broken store reported OK")
	}
}

// AfterRunID resumes the scan after the checkpointed run; an unknown id
// scans everything. OnProgress fires only for verified runs.
func TestHealthResumeAndProgress(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("run-%02d", i)
		if err := s.CreateRun(Run{ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < i; j++ {
			if _, err := s.AppendEvent(id, Event{Type: "node.finished", At: time.Now()}); err != nil {
				t.Fatal(err)
			}
		}
	}

	// Resume after run-02: only run-03 and run-04 are scanned.
	report, err := s.CheckHealthOpts(HealthOptions{AfterRunID: "run-02"})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunsChecked != 2 || report.EventsChecked != 7 {
		t.Fatalf("resumed scan checked %d runs / %d events, want 2 / 7", report.RunsChecked, report.EventsChecked)
	}

	// Unknown checkpoint: full scan.
	report, err = s.CheckHealthOpts(HealthOptions{AfterRunID: "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if report.RunsChecked != 4 {
		t.Fatalf("full scan checked %d runs, want 4", report.RunsChecked)
	}

	// Progress fires per verified run, in order.
	var seen []string
	report, err = s.CheckHealthOpts(HealthOptions{OnProgress: func(runID string, runs, events int) {
		seen = append(seen, runID)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 4 || seen[0] != "run-01" || seen[3] != "run-04" {
		t.Fatalf("progress order = %v", seen)
	}

	// A tampered run breaks the clean prefix: progress must stop before it
	// and the failure must still be reported, so a checkpoint written from
	// OnProgress can never skip past the damage.
	_, _ = s.db.Exec(`UPDATE events SET error = 'x' WHERE run_id = 'run-03' AND seq = 1`)
	seen = nil
	report, err = s.CheckHealthOpts(HealthOptions{OnProgress: func(runID string, runs, events int) {
		seen = append(seen, runID)
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 || seen[1] != "run-02" {
		t.Fatalf("progress after tamper = %v, want it to stop at run-02", seen)
	}
	if report.ChainFailureCount != 1 || report.RunsChecked != 3 {
		t.Fatalf("counts = %d failures / %d runs, want 1 / 3", report.ChainFailureCount, report.RunsChecked)
	}
}

func benchHealthStore(b *testing.B) *Store {
	b.Helper()
	st, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = st.Close() })
	return st
}

// BenchmarkCheckHealthLargeStore (P2-10) records the streaming health check
// over a 500-run fixture: chain verification per run plus blob and orphan
// scans. It must stay linear in run count — the streaming verifier holds
// one event row at a time.
func BenchmarkCheckHealthLargeStore(b *testing.B) {
	st := benchHealthStore(b)
	for i := 0; i < 500; i++ {
		id := fmt.Sprintf("run-bh-%03d", i)
		if err := st.CreateRun(Run{
			ID: id, WorkflowID: "wf-bh", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: time.Now(),
		}); err != nil {
			b.Fatalf("create run: %v", err)
		}
		for seq := 1; seq <= 5; seq++ {
			if _, err := st.AppendEvent(id, Event{Type: "run.started", At: time.Now()}); err != nil {
				b.Fatalf("append: %v", err)
			}
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := st.CheckHealth(); err != nil {
			b.Fatalf("health: %v", err)
		}
	}
}
