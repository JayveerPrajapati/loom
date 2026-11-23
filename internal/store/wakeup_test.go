package store

import (
	"strings"
	"testing"
	"time"
)

func parkWaitingRun(t *testing.T, s *Store, id string, wakeAt time.Time) {
	t.Helper()
	if err := s.CreateRun(Run{
		ID: id, WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run %s: %v", id, err)
	}
	if err := s.SetRunWaitingUntil(id, "wait", "token-"+id, nil, wakeAt, time.Time{}); err != nil {
		t.Fatalf("park run %s: %v", id, err)
	}
}

func TestNextWakeupReturnsEarliestPending(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC()
	parkWaitingRun(t, s, "run-due", now.Add(-time.Minute))
	parkWaitingRun(t, s, "run-soon", now.Add(2*time.Second))
	parkWaitingRun(t, s, "run-later", now.Add(10*time.Second))

	next, err := s.NextWakeup(now)
	if err != nil {
		t.Fatalf("next wakeup: %v", err)
	}
	if next == nil {
		t.Fatal("next wakeup = nil, want the run-soon deadline")
	}
	if diff := next.Sub(now.Add(2 * time.Second)); diff < -50*time.Millisecond || diff > 50*time.Millisecond {
		t.Fatalf("next wakeup = %s (%s off), want the run-soon deadline", next, diff)
	}

	// Once run-soon is also due, the earliest pending is run-later.
	next, err = s.NextWakeup(now.Add(3 * time.Second))
	if err != nil {
		t.Fatalf("next wakeup: %v", err)
	}
	if next == nil {
		t.Fatal("next wakeup = nil, want the run-later deadline")
	}
	if diff := next.Sub(now.Add(10 * time.Second)); diff < -50*time.Millisecond || diff > 50*time.Millisecond {
		t.Fatalf("next wakeup = %s (%s off), want the run-later deadline", next, diff)
	}

	// Past every deadline, nothing is pending.
	next, err = s.NextWakeup(now.Add(11 * time.Second))
	if err != nil {
		t.Fatalf("next wakeup: %v", err)
	}
	if next != nil {
		t.Fatalf("next wakeup = %s, want nil once every deadline has passed", next)
	}

	// Already-due waits stay visible to DueWakeups, which the scheduler
	// resumes before arming the next timer.
	due, err := s.DueWakeups(now.Add(3 * time.Second))
	if err != nil {
		t.Fatalf("due wakeups: %v", err)
	}
	if len(due) != 2 || due[0] != "run-due" || due[1] != "run-soon" {
		t.Fatalf("due wakeups = %v, want [run-due run-soon]", due)
	}
}

func TestNextWakeupIgnoresCancelledRuns(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	now := time.Now().UTC()
	parkWaitingRun(t, s, "run-cancelled", now.Add(time.Second))
	parkWaitingRun(t, s, "run-live", now.Add(5*time.Second))
	if _, err := s.db.Exec(`UPDATE runs SET status = 'cancelled' WHERE id = ?`, "run-cancelled"); err != nil {
		t.Fatalf("cancel run: %v", err)
	}

	next, err := s.NextWakeup(now)
	if err != nil {
		t.Fatalf("next wakeup: %v", err)
	}
	if next == nil {
		t.Fatal("next wakeup = nil, want the live run's deadline")
	}
	if diff := next.Sub(now.Add(5 * time.Second)); diff < -50*time.Millisecond || diff > 50*time.Millisecond {
		t.Fatalf("next wakeup = %s (%s off), want the live run's deadline", next, diff)
	}
}

func TestWaitingResumeIndexRecreatedAfterDrop(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	// Simulate a database created before the index existed.
	if _, err := s.db.Exec(`DROP INDEX idx_runs_waiting_resume`); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	var name string
	if err := s2.db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND name = ?`, "idx_runs_waiting_resume").Scan(&name); err != nil {
		t.Fatalf("index missing after reopen: %v", err)
	}
}

func TestWaitingResumeQueryPlansUseIndex(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cutoff := "2026-01-01T00:00:00Z"
	queries := []string{
		`SELECT id FROM runs WHERE status = 'waiting' AND resume_at IS NOT NULL AND resume_at <= '` + cutoff + `' ORDER BY resume_at`,
		`SELECT MIN(resume_at) FROM runs WHERE status = 'waiting' AND resume_at IS NOT NULL AND resume_at > '` + cutoff + `'`,
	}
	for _, q := range queries {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + q)
		if err != nil {
			t.Fatalf("explain %q: %v", q, err)
		}
		var details []string
		for rows.Next() {
			var id, parent int
			var notused any
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatalf("scan explain row: %v", err)
			}
			details = append(details, detail)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatalf("explain rows: %v", err)
		}
		joined := strings.Join(details, "; ")
		if !strings.Contains(joined, "idx_runs_waiting_resume") {
			t.Fatalf("query %s does not use idx_runs_waiting_resume (plan: %s)", q, joined)
		}
		if strings.Contains(joined, "SCAN runs") {
			t.Fatalf("query %s degrades to a full runs scan (plan: %s)", q, joined)
		}
	}
}
