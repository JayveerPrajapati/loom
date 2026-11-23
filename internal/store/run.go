package store

import (
	"crypto/hmac"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Run struct {
	ID                string     `json:"id"`
	WorkflowID        string     `json:"workflowId"`
	WorkflowVersion   string     `json:"workflowVersion"`
	TemplateVersion   int        `json:"templateVersion,omitempty"`
	JobID             string     `json:"jobId,omitempty"`
	Status            string     `json:"status"`
	Trigger           string     `json:"trigger"`
	StartedAt         time.Time  `json:"startedAt"`
	FinishedAt        *time.Time `json:"finishedAt,omitempty"`
	Error             string     `json:"error,omitempty"`
	ResumeNode        string     `json:"resumeNode,omitempty"`
	ResumeToken       string     `json:"resumeToken,omitempty"`
	ResumeExpiresAt   *time.Time `json:"resumeTokenExpiresAt,omitempty"`
	ResumeRequestedAt *time.Time `json:"resumeRequestedAt,omitempty"`
	ResumeData        []byte     `json:"-"`
	Snapshot          []byte     `json:"-"`
	// Input is the trigger input, persisted separately from the display-masked
	// event log so a queued run executes the real values after admission.
	Input        []byte `json:"-"`
	ParentRunID  string `json:"parentRunId,omitempty"`
	ParentNodeID string `json:"parentNodeId,omitempty"`
	// RootRunID is the first run in an event chain; it budgets fan-out.
	RootRunID string `json:"rootRunId,omitempty"`
	Actor     string `json:"actor,omitempty"`
	// Effects and CassetteRunID persist the execution mode so a resumed or
	// queued run keeps the cassette it was started with (never the live host).
	Effects       string `json:"-"`
	CassetteRunID string `json:"-"`
}

func (s *Store) CreateRun(run Run) error {
	s.keyMu.RLock()
	current := s.currentKey
	s.keyMu.RUnlock()
	_, err := s.db.Exec(`
		INSERT INTO runs (id, workflow_id, workflow_version, status, trigger, started_at, error,
			resume_data, parent_run_id, parent_node_id, root_run_id, workflow_snapshot, job_id, template_version, chain_version, chain_tag, effects, cassette_run_id, input, actor, key_id, head_seq, head_mac)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 2, ?, ?, ?, ?, ?, ?, 0, ?)`,
		run.ID, run.WorkflowID, run.WorkflowVersion, run.Status, run.Trigger,
		run.StartedAt.UTC().Format(time.RFC3339Nano), run.Error, run.ResumeData,
		run.ParentRunID, run.ParentNodeID, run.RootRunID, run.Snapshot, run.JobID, run.TemplateVersion,
		s.chainTag(current.macKey, run.ID, 2, run.Effects, run.CassetteRunID), run.Effects, run.CassetteRunID, run.Input, run.Actor,
		current.id, s.headMAC(current.macKey, run.ID, 0, ""))
	return err
}

func (s *Store) GetRun(id string) (*Run, error) {
	row := s.readRow(`
		SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		       finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
		FROM runs WHERE id = ?`, id)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return run, err
}

func (s *Store) GetRunByResumeToken(token string) (*Run, error) {
	if token == "" {
		return nil, errors.New("resume token is required")
	}
	row := s.readRow(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                             finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
	                      FROM runs WHERE resume_token = ? AND status = 'waiting'
	                        AND (resume_expires_at IS NULL OR resume_expires_at = '' OR resume_expires_at >= ?)`, token, now())
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no waiting run for that token")
	}
	return run, err
}

// RunEffects returns the persisted execution mode and cassette source for a
// run. Resume, restart recovery and queue admission rebuild their exec state
// from these, so a hermetic run never falls back to the live host.
func (s *Store) RunEffects(id string) (mode, cassetteRunID string, err error) {
	var version int
	var tag, keyID string
	err = s.readRow(`SELECT COALESCE(chain_version, 0), COALESCE(chain_tag, ''),
		COALESCE(effects, ''), COALESCE(cassette_run_id, ''), COALESCE(key_id, '') FROM runs WHERE id = ?`, id).
		Scan(&version, &tag, &mode, &cassetteRunID, &keyID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", fmt.Errorf("run %q not found", id)
	}
	if err != nil {
		return "", "", err
	}
	key, kerr := s.keyFor(keyID)
	if kerr != nil {
		return "", "", kerr
	}
	if want := s.chainTag(key.macKey, id, version, mode, cassetteRunID); !hmac.Equal([]byte(tag), []byte(want)) {
		return "", "", fmt.Errorf("run %s: chain version tag mismatch", id)
	}
	return mode, cassetteRunID, nil
}

// RunInput returns the persisted trigger input for a run.
func (s *Store) RunInput(id string) ([]byte, error) {
	var data []byte
	err := s.readRow(`SELECT COALESCE(input, '') FROM runs WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("run %q not found", id)
	}
	return data, err
}

// SubflowChildRunID returns the most recent recorded child run for a subflow
// node, so a replay can serve the child's tapes instead of going live. rowid
// is insertion-ordered; started_at text sorting is not chronologically safe
// for RFC3339Nano with trimmed fractional zeros.
func (s *Store) SubflowChildRunID(parentRunID, parentNodeID string) (string, error) {
	if parentRunID == "" || parentNodeID == "" {
		return "", errors.New("subflow parent run and node are required")
	}
	var id string
	err := s.readRow(`SELECT id FROM runs WHERE parent_run_id = ? AND parent_node_id = ?
		ORDER BY rowid DESC LIMIT 1`, parentRunID, parentNodeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no recorded subflow run for %s/%s", parentRunID, parentNodeID)
	}
	return id, err
}

func (s *Store) UpdateRunStatus(id, status, errMsg string, finished *time.Time) error {
	var finishedAt any
	if finished != nil {
		finishedAt = finished.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, finishedAt, id)
	if err == nil {
		s.changes.Notify(id)
	}
	return err
}

// OrphanRun marks a run left behind by a restart as failed. The CAS only
// touches non-terminal rows, so a run that reached a terminal state while
// recovery was scanning the table is left alone.
func (s *Store) OrphanRun(id, reason string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET status = 'failed', error = ?, finished_at = ?
		WHERE id = ? AND status NOT IN ('done', 'failed', 'cancelled')`,
		reason, at.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.changes.Notify(id)
	}
	return n == 1, nil
}

// ExpireWaitingRun cancels a waiting run whose approval link has expired. The
// status predicate makes the sweep idempotent: only the first caller sees a
// row change and appends the cancellation event.
func (s *Store) ExpireWaitingRun(id string, at time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET status = 'cancelled', error = ?, finished_at = ?
		WHERE id = ? AND status = 'waiting'`,
		"approval link expired", at.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.changes.Notify(id)
	}
	return n == 1, nil
}

func (s *Store) SetRunWaiting(id, nodeID, token string, data []byte) error {
	return s.SetRunWaitingUntil(id, nodeID, token, data, time.Time{}, time.Time{})
}

// SetRunWaitingUntil parks a run and, when resumeAt is non-zero, records the
// earliest time a wakeup may resume it. An approval passes the zero time.
func (s *Store) SetRunWaitingUntil(id, nodeID, token string, data []byte, resumeAt, expiresAt time.Time) error {
	var wake, expires any
	if !resumeAt.IsZero() {
		wake = resumeAt.UTC().Format(time.RFC3339Nano)
	}
	if !expiresAt.IsZero() {
		expires = expiresAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`UPDATE runs SET status = 'waiting', resume_node = ?, resume_token = ?, resume_data = ?, resume_at = ?, resume_expires_at = ? WHERE id = ?`,
		nodeID, token, data, wake, expires, id)
	if err == nil {
		s.changes.Notify(id)
	}
	return err
}

// DueWakeups returns waiting runs whose durable timer has expired. Timestamps
// are stored as RFC3339Nano UTC strings, so lexical order matches chronology.
func (s *Store) DueWakeups(now time.Time) ([]string, error) {
	rows, err := s.read(`
		SELECT id FROM runs
		WHERE status = 'waiting' AND resume_at IS NOT NULL AND resume_at <= ?
		ORDER BY resume_at`, now.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// NextWakeup reports the earliest pending wakeup strictly after now. It
// returns nil when no waiting run has a scheduled timer. Like DueWakeups it
// relies on RFC3339Nano UTC strings sorting chronologically, so MIN() picks
// the earliest deadline and the idx_runs_waiting_resume index serves the
// lookup without scanning the runs table.
func (s *Store) NextWakeup(now time.Time) (*time.Time, error) {
	var raw sql.NullString
	err := s.readRow(`
		SELECT MIN(resume_at) FROM runs
		WHERE status = 'waiting' AND resume_at IS NOT NULL AND resume_at > ?`,
		now.UTC().Format(time.RFC3339Nano)).Scan(&raw)
	if err != nil {
		return nil, err
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	at, err := time.Parse(time.RFC3339Nano, raw.String)
	if err != nil {
		return nil, err
	}
	return &at, nil
}

// RunWakeAt reports the persisted wakeup time for a waiting run.
func (s *Store) RunWakeAt(id string) (time.Time, bool, error) {
	var raw sql.NullString
	err := s.readRow(`SELECT resume_at FROM runs WHERE id = ?`, id).Scan(&raw)
	if err != nil {
		return time.Time{}, false, err
	}
	if !raw.Valid || raw.String == "" {
		return time.Time{}, false, nil
	}
	at, err := time.Parse(time.RFC3339Nano, raw.String)
	if err != nil {
		return time.Time{}, false, err
	}
	return at, true, nil
}

// ResumeWaiting atomically claims a waiting run. It returns false when another
// request already resumed the run, so concurrent approval submissions cannot
// both append run.resumed or re-execute downstream nodes.
func (s *Store) ResumeWaiting(id string) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET status = 'running', resume_node = '', resume_token = '', resume_data = NULL, resume_at = NULL WHERE id = ? AND status = 'waiting'`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		s.changes.Notify(id)
	}
	return n == 1, nil
}

func (s *Store) ListRuns(workflowID string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                 finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
	          FROM runs`
	args := []any{}
	if workflowID != "" {
		query += ` WHERE workflow_id = ?`
		args = append(args, workflowID)
	}
	query += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.read(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// ListRunsByStatus returns the most recent runs in one status, including
// child runs: waiting approvals can sit inside subflow runs.
// CountRunsByStatus counts every run in a status regardless of any list
// limit. The waiting-approval badge depends on the true total, not on how many
// rows the caller asked to return.
// CountRunsByRoot counts every run in an event tree, including the root. The
// event bus uses it to stop amplification before it starts another descendant.
func (s *Store) CountRunsByRoot(root string) (int, error) {
	if root == "" {
		return 0, nil
	}
	var count int
	if err := s.readRow(`SELECT COUNT(*) FROM runs WHERE root_run_id = ?`, root).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) CountRunsByStatus(status string) (int, error) {
	if status == "" {
		return 0, errors.New("status is required")
	}
	var count int
	if err := s.readRow(`SELECT COUNT(*) FROM runs WHERE status = ?`, status).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListRunsByStatus(status string, limit int) ([]Run, error) {
	if status == "" {
		return nil, errors.New("status is required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.read(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                                finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
	                         FROM runs WHERE status = ? ORDER BY started_at DESC LIMIT ?`, status, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// CountActiveRuns returns how many runs of the workflow are executing.
func (s *Store) CountActiveRuns(workflowID string) (int, error) {
	var n int
	err := s.readRow(`SELECT COUNT(*) FROM runs WHERE workflow_id = ? AND status = 'running'`, workflowID).Scan(&n)
	return n, err
}

// OldestQueuedRun returns the oldest parked root run of the workflow that is
// waiting for a concurrency slot, or nil when the queue is empty. Roots are
// parentless runs plus event runs; other parented work inherits its parent's
// slot and is never parked.
func (s *Store) OldestQueuedRun(workflowID string) (*Run, error) {
	row := s.readRow(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                            finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
	                     FROM runs WHERE workflow_id = ? AND status = 'queued'
	                       AND (COALESCE(parent_run_id, '') = '' OR trigger = 'event')
	                     ORDER BY started_at ASC LIMIT 1`, workflowID)
	run, err := scanRun(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return run, nil
}

// ClaimQueuedRun atomically flips a parked run to running. It returns false
// when the run was cancelled or claimed in the meantime.
func (s *Store) ClaimQueuedRun(id string, started time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET status = 'running', started_at = ?, finished_at = NULL WHERE id = ? AND status = 'queued'`,
		started.UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.changes.Notify(id)
	}
	return n == 1, nil
}

// AdmitRun atomically flips a queued run to running while the workflow has
// fewer than maxParallel executing root runs. Roots are parentless runs plus
// event runs; parented batch/subflow/ci children inherit a parent's slot and
// never count against (or wait for) the cap. Returns false when the cap is met
// or another writer claimed the run first.
func (s *Store) AdmitRun(id, workflowID string, maxParallel int) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET status = 'running', finished_at = NULL
		WHERE id = ? AND status = 'queued'
		AND (SELECT COUNT(*) FROM runs WHERE workflow_id = ? AND status = 'running'
		     AND (COALESCE(parent_run_id, '') = '' OR trigger = 'event')) < ?`,
		id, workflowID, maxParallel)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	if n == 1 {
		s.changes.Notify(id)
	}
	return n == 1, nil
}

// ListQueuedRuns returns every parked root run, oldest first.
func (s *Store) ListQueuedRuns() ([]Run, error) {
	rows, err := s.read(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		       finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
		FROM runs WHERE status = 'queued'
		  AND (COALESCE(parent_run_id, '') = '' OR trigger = 'event')
		ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *Store) ListRunsByJob(jobID string, limit int) ([]Run, error) {
	if jobID == "" {
		return nil, errors.New("job id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.read(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                                finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
	                         FROM runs WHERE job_id = ? AND COALESCE(parent_run_id, '') = '' ORDER BY started_at DESC LIMIT ?`, jobID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func (s *Store) ListChildRuns(parentRunID string, limit int) ([]Run, error) {
	if parentRunID == "" {
		return nil, errors.New("parent run id is required")
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.read(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                                finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
	                         FROM runs WHERE parent_run_id = ? ORDER BY started_at ASC LIMIT ?`, parentRunID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// StaleRuns lists runs that were in flight when the previous process stopped.
// Statuses are deliberately left untouched: the engine resumes them from their
// event log instead of failing work that already completed.
func (s *Store) StaleRuns() ([]Run, error) {
	rows, err := s.read(`
		SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		       finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version, COALESCE(actor, ''), COALESCE(resume_expires_at, ''), COALESCE(root_run_id, '')
		FROM runs WHERE status IN ('queued', 'running') ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

// RunStats is a workspace-wide rollup used by the dashboard.
type RunStats struct {
	Total   int `json:"total"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Waiting int `json:"waiting"`
	Active  int `json:"active"`
	Last24h int `json:"last24h"`
}

func (s *Store) RunStats() (RunStats, error) {
	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339Nano)
	var stats RunStats
	err := s.readRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(CASE WHEN status = 'done' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'failed' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status = 'waiting' THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN status IN ('queued', 'running') THEN 1 ELSE 0 END), 0),
		       COALESCE(SUM(CASE WHEN started_at >= ? THEN 1 ELSE 0 END), 0)
		FROM runs`, since).Scan(
		&stats.Total, &stats.Done, &stats.Failed, &stats.Waiting, &stats.Active, &stats.Last24h,
	)
	return stats, err
}

// ---------- blobs ----------

func scanRun(row scanner) (*Run, error) {
	var run Run
	var started, finished sql.NullString
	var errMsg, node, token, parentRun, parentNode, jobID sql.NullString
	var templateVersion sql.NullInt64
	var actor, expires, rootRun sql.NullString
	var data, snapshot []byte

	if err := row.Scan(&run.ID, &run.WorkflowID, &run.WorkflowVersion, &run.Status, &run.Trigger,
		&started, &finished, &errMsg, &node, &token, &data, &parentRun, &parentNode,
		&snapshot, &jobID, &templateVersion, &actor, &expires, &rootRun); err != nil {
		return nil, err
	}
	run.StartedAt, _ = time.Parse(time.RFC3339Nano, started.String)
	if finished.Valid {
		t, _ := time.Parse(time.RFC3339Nano, finished.String)
		run.FinishedAt = &t
	}
	run.Error = errMsg.String
	run.ResumeNode = node.String
	run.ResumeToken = token.String
	run.ResumeData = data
	if len(data) > 0 {
		var payload map[string]any
		if json.Unmarshal(data, &payload) == nil {
			if raw, ok := payload["requestedAt"].(string); ok {
				if t, err := time.Parse(time.RFC3339, raw); err == nil {
					run.ResumeRequestedAt = &t
				}
			}
		}
	}
	run.Snapshot = snapshot
	run.ParentRunID = parentRun.String
	run.ParentNodeID = parentNode.String
	run.JobID = jobID.String
	run.Actor = actor.String
	run.RootRunID = rootRun.String
	if expires.Valid && expires.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, expires.String)
		run.ResumeExpiresAt = &t
	}
	if templateVersion.Valid {
		run.TemplateVersion = int(templateVersion.Int64)
	}
	return &run, nil
}
