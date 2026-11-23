package store

import (
	"strings"
	"testing"
	"time"
)

func explainPlan(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.db.Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatalf("explain %q: %v", query, err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent int
		var notused any
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan explain row for %q: %v", query, err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("explain rows for %q: %v", query, err)
	}
	return strings.Join(details, "; ")
}

func TestQueueAndHistoryQueryPlans(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Seed a queued root run so the planner sees representative row counts;
	// index choice must not depend on data, but the fixture keeps the test
	// honest about which tables participate.
	parkWaitingRun(t, s, "plan-seed", time.Now().Add(time.Hour))

	tests := []struct {
		name  string
		query string
		want  string // substring expected in the plan
		bad   string // substring that must not appear
	}{
		{
			name: "oldest queued run",
			query: `SELECT id FROM runs WHERE workflow_id = ? AND status = 'queued'
				AND (COALESCE(parent_run_id, '') = '' OR trigger = 'event')
				ORDER BY started_at ASC LIMIT 1`,
			want: "idx_runs_workflow",
			bad:  "SCAN runs",
		},
		{
			name:  "count active runs",
			query: `SELECT COUNT(*) FROM runs WHERE workflow_id = ? AND status = 'running'`,
			want:  "idx_runs_workflow",
			bad:   "SCAN runs",
		},
		{
			name:  "admit run cap subquery",
			query: `SELECT COUNT(*) FROM runs WHERE workflow_id = ? AND status = 'running' AND (COALESCE(parent_run_id, '') = '' OR trigger = 'event')`,
			want:  "idx_runs_workflow",
			bad:   "SCAN runs",
		},
		{
			name:  "list runs by workflow",
			query: `SELECT id FROM runs WHERE workflow_id = ? ORDER BY started_at DESC LIMIT 50`,
			want:  "idx_runs_workflow",
			bad:   "SCAN runs",
		},
		{
			name:  "list runs by status",
			query: `SELECT id FROM runs WHERE status = ? ORDER BY started_at DESC LIMIT 50`,
			want:  "idx_runs_status_started",
			bad:   "SCAN runs",
		},
		{
			// The IN-list cannot use one index range for both statuses, so
			// the planner picks either status-leading index and sorts the
			// (already narrowed) rows; what matters is no full scan.
			name:  "stale runs recovery",
			query: `SELECT id FROM runs WHERE status IN ('queued', 'running') ORDER BY started_at`,
			want:  "SEARCH runs USING INDEX idx_runs_",
			bad:   "SCAN runs",
		},
		{
			name:  "filtered history page",
			query: `SELECT id FROM runs WHERE workflow_id = ? AND status = 'done' ORDER BY started_at DESC LIMIT 50 OFFSET 50`,
			want:  "idx_runs_workflow_status_started",
			bad:   "SCAN runs",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainPlan(t, s, tc.query, "wf")
			if !strings.Contains(plan, tc.want) {
				t.Fatalf("plan %q does not use %q", plan, tc.want)
			}
			if strings.Contains(plan, tc.bad) {
				t.Fatalf("plan %q degrades to %q", plan, tc.bad)
			}
		})
	}
}

// The keep-set tape scan must use the type index so pruning does not
// materialize the data column of every event row.
func TestPruneKeepSetQueryPlans(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	tests := []struct {
		name  string
		query string
		want  string
		bad   string
	}{
		{
			name:  "effect tape scan",
			query: `SELECT data FROM events WHERE type = 'node.effects' AND data IS NOT NULL`,
			want:  "idx_events_type",
			bad:   "SCAN events",
		},
		{
			name:  "effect tape scan with exclusions",
			query: `SELECT data FROM events WHERE type = 'node.effects' AND data IS NOT NULL AND run_id NOT IN (?)`,
			want:  "idx_events_type",
			bad:   "SCAN events",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainPlan(t, s, tc.query, "wf")
			if !strings.Contains(plan, tc.want) {
				t.Fatalf("plan %q does not use %q", plan, tc.want)
			}
			if strings.Contains(plan, tc.bad) {
				t.Fatalf("plan %q degrades to %q", plan, tc.bad)
			}
		})
	}
}
