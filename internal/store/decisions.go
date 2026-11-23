package store

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Decision is one recorded approval outcome. It is the queryable audit row
// behind the run.resumed event: who decided, when, why, and how long the
// approval waited first.
type Decision struct {
	ID          int64     `json:"id"`
	RunID       string    `json:"runId"`
	WorkflowID  string    `json:"workflowId,omitempty"`
	NodeID      string    `json:"nodeId,omitempty"`
	Decision    string    `json:"decision"`
	Note        string    `json:"note,omitempty"`
	ActorID     string    `json:"actorId,omitempty"`
	ActorLabel  string    `json:"actorLabel,omitempty"`
	Source      string    `json:"source,omitempty"`
	RequestedAt time.Time `json:"requestedAt"`
	DecidedAt   time.Time `json:"decidedAt"`
	WaitedMs    int64     `json:"waitedMs"`
	CreatedAt   time.Time `json:"createdAt"`
}

// DecisionStats summarises decisions over a window for the metrics endpoint.
type DecisionStats struct {
	Total        int     `json:"total"`
	Approved     int     `json:"approved"`
	Rejected     int     `json:"rejected"`
	WaitingNow   int     `json:"waitingNow"`
	WaitedP50Ms  int64   `json:"waitedP50Ms"`
	WaitedP95Ms  int64   `json:"waitedP95Ms"`
	OldestWaitMs int64   `json:"oldestWaitMs"`
	WindowHours  float64 `json:"windowHours"`
}

const decisionsSchema = `
CREATE TABLE IF NOT EXISTS decisions (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id TEXT NOT NULL,
	workflow_id TEXT NOT NULL DEFAULT '',
	node_id TEXT NOT NULL DEFAULT '',
	decision TEXT NOT NULL,
	note TEXT NOT NULL DEFAULT '',
	actor_id TEXT NOT NULL DEFAULT '',
	actor_label TEXT NOT NULL DEFAULT '',
	source TEXT NOT NULL DEFAULT '',
	requested_at TEXT,
	decided_at TEXT NOT NULL,
	waited_ms INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_decisions_run ON decisions(run_id);
CREATE INDEX IF NOT EXISTS idx_decisions_decided ON decisions(decided_at DESC);
`

// resumeClaim is the CAS that moves a waiting run back to running. It clears
// the wait state so a token cannot be replayed after a decision.
const resumeClaim = `UPDATE runs SET status = 'running', resume_node = '', resume_token = '',
	resume_data = NULL, resume_at = NULL, resume_expires_at = NULL
	WHERE id = ? AND status = 'waiting'`

// ResumeWaitingWithDecision atomically claims a waiting run and records the
// decision in one transaction. Both rows commit or neither does, so the audit
// trail can never disagree with the run status.
func (s *Store) ResumeWaitingWithDecision(id string, d Decision) (bool, error) {
	if id == "" {
		return false, errors.New("run id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(resumeClaim, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, nil
	}

	workflowID := d.WorkflowID
	if workflowID == "" {
		_ = tx.QueryRow(`SELECT workflow_id FROM runs r WHERE id = ?`, id).Scan(&workflowID)
	}
	requested := any(nil)
	if !d.RequestedAt.IsZero() {
		requested = d.RequestedAt.UTC().Format(time.RFC3339Nano)
	}
	decided := d.DecidedAt
	if decided.IsZero() {
		decided = time.Now().UTC()
	}
	if _, err := tx.Exec(`INSERT INTO decisions
		(run_id, workflow_id, node_id, decision, note, actor_id, actor_label, source, requested_at, decided_at, waited_ms, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id, workflowID, d.NodeID, d.Decision, d.Note, d.ActorID, d.ActorLabel, d.Source,
		requested, decided.UTC().Format(time.RFC3339Nano), d.WaitedMs, now()); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ClearResumeToken revokes the approval link without deciding the run. The run
// stays waiting; a fresh token is issued the next time an approval parks it.
func (s *Store) ClearResumeToken(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET resume_token = '', resume_expires_at = NULL WHERE id = ? AND status = 'waiting'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// UpdateResumeData rewrites the wait payload, used to record reminder state.
func (s *Store) UpdateResumeData(id string, data []byte) error {
	_, err := s.db.Exec(`UPDATE runs SET resume_data = ? WHERE id = ? AND status = 'waiting'`, data, id)
	return err
}

// ListDecisions returns decisions newest first, optionally filtered. limit 0
// means 50; limit is capped at 500 so one request cannot scan the table.
func (s *Store) ListDecisions(workflowID, source, decision string, limit, offset int) ([]Decision, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	where := []string{"1=1"}
	args := []any{}
	if workflowID != "" {
		where = append(where, "workflow_id = ?")
		args = append(args, workflowID)
	}
	if source != "" {
		where = append(where, "source = ?")
		args = append(args, source)
	}
	if decision != "" {
		where = append(where, "decision = ?")
		args = append(args, decision)
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.readRow(`SELECT COUNT(*) FROM decisions WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.read(`SELECT id, run_id, workflow_id, node_id, decision, note, actor_id, actor_label,
		source, requested_at, decided_at, waited_ms, created_at
		FROM decisions WHERE `+clause+` ORDER BY decided_at DESC, id DESC LIMIT ? OFFSET ?`,
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []Decision{}
	for rows.Next() {
		var d Decision
		var requested sql.NullString
		var decided, created string
		if err := rows.Scan(&d.ID, &d.RunID, &d.WorkflowID, &d.NodeID, &d.Decision, &d.Note,
			&d.ActorID, &d.ActorLabel, &d.Source, &requested, &decided, &d.WaitedMs, &created); err != nil {
			return nil, 0, err
		}
		if requested.Valid {
			d.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested.String)
		}
		d.DecidedAt, _ = time.Parse(time.RFC3339Nano, decided)
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, d)
	}
	return out, total, rows.Err()
}

// DecisionMetrics summarises decision volume and wait times over a window and
// the waiting backlog right now. Percentiles are computed in Go over the rows
// in the window so SQLite needs no extension.
func (s *Store) DecisionMetrics(window time.Duration) (DecisionStats, error) {
	if window <= 0 {
		window = 24 * time.Hour
	}
	// Bound the scan: the percentile computation is linear now, but an
	// unbounded window still turns one request into a full-table read.
	if window > 30*24*time.Hour {
		window = 30 * 24 * time.Hour
	}
	since := time.Now().UTC().Add(-window).Format(time.RFC3339Nano)
	stats := DecisionStats{WindowHours: window.Hours()}

	rows, err := s.read(`SELECT decision, waited_ms FROM decisions WHERE decided_at >= ?`, since)
	if err != nil {
		return stats, err
	}
	var waits []int64
	for rows.Next() {
		var decision string
		var waited int64
		if err := rows.Scan(&decision, &waited); err != nil {
			rows.Close()
			return stats, err
		}
		stats.Total++
		if decision == "approved" {
			stats.Approved++
		} else {
			stats.Rejected++
		}
		waits = append(waits, waited)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return stats, err
	}

	if len(waits) > 0 {
		stats.WaitedP50Ms = percentile(waits, 0.50)
		stats.WaitedP95Ms = percentile(waits, 0.95)
	}

	waiting, err := s.CountRunsByStatus("waiting")
	if err != nil {
		return stats, err
	}
	stats.WaitingNow = waiting

	return stats, nil
}

// percentile returns the nearest-rank percentile of unsorted values.
func percentile(values []int64, p float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	idx := int(float64(len(sorted)-1) * p)
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// RunsFilter narrows a run list. Empty fields mean "no filter".
type RunsFilter struct {
	WorkflowID string
	Status     string
	Query      string
	// JobID, Trigger and RootsOnly are the dashboard's run-list filters.
	// RootsOnly excludes subflow child runs so page slots are spent on the
	// runs the list actually renders.
	JobID     string
	Trigger   string
	RootsOnly bool
	Limit     int
	Offset    int
	// Cursor keyset-paginates instead of Offset: it is the opaque
	// NextCursor token a previous page returned. Prefer it over Offset —
	// its cost is constant no matter how deep the history goes, while
	// OFFSET grows linearly with page number.
	Cursor string
}

// runSearchSQL matches everything the dashboard's run search promises: run
// id, error, trigger, status, workflow name and job name. The name lookups
// are keyed subqueries, so the count stays a single runs scan.
const runSearchSQL = `(r.id LIKE ? OR r.error LIKE ? OR r.trigger LIKE ? OR r.status LIKE ? OR
	EXISTS (SELECT 1 FROM workflows w WHERE w.id = r.workflow_id AND w.name LIKE ?) OR
	EXISTS (SELECT 1 FROM jobs j WHERE j.id = r.job_id AND j.name LIKE ?))`

func runSearchArgs(query string) []any {
	like := "%" + query + "%"
	return []any{like, like, like, like, like, like}
}

// ListRunsFiltered returns matching runs newest first plus the total count, so
// the history dialog can page instead of silently stopping at 50.
func (s *Store) ListRunsFiltered(f RunsFilter) ([]Run, int, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{"1=1"}
	args := []any{}
	if f.WorkflowID != "" {
		where = append(where, "r.workflow_id = ?")
		args = append(args, f.WorkflowID)
	}
	if f.Status != "" {
		where = append(where, "r.status = ?")
		args = append(args, f.Status)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, runSearchSQL)
		args = append(args, runSearchArgs(q)...)
	}
	if f.JobID != "" {
		where = append(where, "r.job_id = ?")
		args = append(args, f.JobID)
	}
	if f.Trigger != "" {
		where = append(where, "r.trigger = ?")
		args = append(args, f.Trigger)
	}
	if f.RootsOnly {
		where = append(where, "(r.parent_run_id IS NULL OR r.parent_run_id = '')")
	}
	clause := strings.Join(where, " AND ")

	var total int
	if err := s.readRow(`SELECT COUNT(*) FROM runs r WHERE `+clause, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.read(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id,
		workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
		FROM runs r WHERE `+clause+` 		ORDER BY r.started_at DESC LIMIT ? OFFSET ?`,
		append(args, f.Limit, f.Offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	out := []Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *run)
	}
	return out, total, rows.Err()
}

// RunPage is one page of keyset-paginated run history. Total is the full
// filtered count (the history dialog shows it), and NextCursor is the opaque
// token to fetch the next page — empty on the last page.
type RunPage struct {
	Runs       []Run
	Total      int
	NextCursor string
}

// encodeRunCursor packs the keyset position — the raw stored started_at text
// plus the run-id tiebreaker — into an opaque token. The raw stored string,
// not a re-formatted time, is required: SQL string comparison must see the
// exact bytes the column holds, since RFC3339Nano formatting is not unique
// for one instant.
func encodeRunCursor(startedAt, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(startedAt + "\x1f" + id))
}

// decodeRunCursor parses a page token. Malformed cursors are an error, never
// silently treated as the first page: a corrupted position must not quietly
// rewind history.
func decodeRunCursor(cursor string) (string, string, error) {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(cursor))
	if err != nil {
		return "", "", err
	}
	parts := strings.SplitN(string(raw), "\x1f", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", errors.New("malformed run cursor")
	}
	return parts[0], parts[1], nil
}

// ListRunsPage returns matching runs newest first, keyset-paginated by
// (started_at, id): the predicate is "older than the cursor position", so
// every page costs the same regardless of how deep the user has paged. The
// filter clause matches ListRunsFiltered so both stay interchangeable.
func (s *Store) ListRunsPage(f RunsFilter) (RunPage, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	// The API caps history requests at 500 rows; match it so a paged
	// status view never returns fewer rows than the dedicated path it
	// replaced.
	if f.Limit > 500 {
		f.Limit = 500
	}
	where := []string{"1=1"}
	args := []any{}
	if f.WorkflowID != "" {
		where = append(where, "r.workflow_id = ?")
		args = append(args, f.WorkflowID)
	}
	if f.Status != "" {
		where = append(where, "r.status = ?")
		args = append(args, f.Status)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		where = append(where, runSearchSQL)
		args = append(args, runSearchArgs(q)...)
	}
	if f.JobID != "" {
		where = append(where, "r.job_id = ?")
		args = append(args, f.JobID)
	}
	if f.Trigger != "" {
		where = append(where, "r.trigger = ?")
		args = append(args, f.Trigger)
	}
	if f.RootsOnly {
		where = append(where, "(r.parent_run_id IS NULL OR r.parent_run_id = '')")
	}
	// The total is the full filtered count — the position the cursor
	// encodes is not part of the filter, or every page would report a
	// shrinking total.
	var page RunPage
	if err := s.readRow(`SELECT COUNT(*) FROM runs r WHERE `+strings.Join(where, " AND "), args...).Scan(&page.Total); err != nil {
		return RunPage{}, err
	}
	if f.Cursor != "" {
		startedAt, id, err := decodeRunCursor(f.Cursor)
		if err != nil {
			return RunPage{}, err
		}
		where = append(where, "(r.started_at < ? OR (r.started_at = ? AND r.id < ?))")
		args = append(args, startedAt, startedAt, id)
	}
	clause := strings.Join(where, " AND ")
	// Fetch one extra row to learn whether a next page exists without a
	// second count query.
	rows, err := s.read(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id,
		workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
		FROM runs r WHERE `+clause+` ORDER BY r.started_at DESC, r.id DESC LIMIT ?`,
		append(args, f.Limit+1)...)
	if err != nil {
		return RunPage{}, err
	}
	defer rows.Close()
	page.Runs = []Run{}
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return RunPage{}, err
		}
		page.Runs = append(page.Runs, *run)
	}
	if err := rows.Err(); err != nil {
		return RunPage{}, err
	}
	if len(page.Runs) > f.Limit {
		page.Runs = page.Runs[:f.Limit]
		last := page.Runs[len(page.Runs)-1]
		var lastStarted string
		if err := s.readRow(`SELECT started_at FROM runs r WHERE id = ?`, last.ID).Scan(&lastStarted); err != nil {
			return RunPage{}, err
		}
		page.NextCursor = encodeRunCursor(lastStarted, last.ID)
	}
	return page, nil
}

// DecisionsForRun returns the decision history of one run, oldest first.
func (s *Store) DecisionsForRun(runID string) ([]Decision, error) {
	if runID == "" {
		return nil, errors.New("run id is required")
	}
	rows, err := s.read(`SELECT id, run_id, workflow_id, node_id, decision, note, actor_id, actor_label,
		source, requested_at, decided_at, waited_ms, created_at
		FROM decisions WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Decision{}
	for rows.Next() {
		var d Decision
		var requested sql.NullString
		var decided, created string
		if err := rows.Scan(&d.ID, &d.RunID, &d.WorkflowID, &d.NodeID, &d.Decision, &d.Note,
			&d.ActorID, &d.ActorLabel, &d.Source, &requested, &decided, &d.WaitedMs, &created); err != nil {
			return nil, err
		}
		if requested.Valid {
			d.RequestedAt, _ = time.Parse(time.RFC3339Nano, requested.String)
		}
		d.DecidedAt, _ = time.Parse(time.RFC3339Nano, decided)
		d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, d)
	}
	return out, rows.Err()
}

// ensure the schema is created exactly once per store.
func (s *Store) initDecisions() error {
	if _, err := s.db.Exec(decisionsSchema); err != nil {
		return fmt.Errorf("migrate decisions: %w", err)
	}
	return nil
}
