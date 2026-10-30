// Package store persists workflows, runs, events, blobs and credentials.
package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"loom/internal/graph"
)

type Store struct {
	db      *sql.DB
	blobDir string
	aead    cipher.AEAD
}

type Summary struct {
	ID               string    `json:"id"`
	Name             string    `json:"name"`
	Version          int       `json:"version"`
	PublishedVersion int       `json:"publishedVersion"`
	Draft            bool      `json:"draft"`
	UpdatedAt        time.Time `json:"updatedAt"`
	JobCount         int       `json:"jobCount"`
	ActiveJobCount   int       `json:"activeJobCount"`
}

type WorkflowVersion struct {
	WorkflowID  string    `json:"workflowId"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	NodeCount   int       `json:"nodeCount"`
	PublishedAt time.Time `json:"publishedAt"`
}

type WorkflowState struct {
	PublishedVersion int               `json:"publishedVersion"`
	Draft            bool              `json:"draft"`
	ContentHash      string            `json:"contentHash"`
	Versions         []WorkflowVersion `json:"versions"`
}

type Job struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	TemplateID      string         `json:"templateId"`
	TemplateName    string         `json:"templateName,omitempty"`
	TemplateVersion int            `json:"templateVersion"`
	Enabled         bool           `json:"enabled"`
	TriggerType     string         `json:"triggerType"`
	InputDefaults   map[string]any `json:"inputDefaults,omitempty"`
	TriggerConfig   map[string]any `json:"triggerConfig,omitempty"`
	CreatedAt       time.Time      `json:"createdAt"`
	UpdatedAt       time.Time      `json:"updatedAt"`
	LastRunAt       *time.Time     `json:"lastRunAt,omitempty"`
	NextRunAt       *time.Time     `json:"nextRunAt,omitempty"`
}

type Run struct {
	ID              string     `json:"id"`
	WorkflowID      string     `json:"workflowId"`
	WorkflowVersion string     `json:"workflowVersion"`
	TemplateVersion int        `json:"templateVersion,omitempty"`
	JobID           string     `json:"jobId,omitempty"`
	Status          string     `json:"status"`
	Trigger         string     `json:"trigger"`
	StartedAt       time.Time  `json:"startedAt"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Error           string     `json:"error,omitempty"`
	ResumeNode      string     `json:"resumeNode,omitempty"`
	ResumeToken     string     `json:"resumeToken,omitempty"`
	ResumeData      []byte     `json:"-"`
	Snapshot        []byte     `json:"-"`
	ParentRunID     string     `json:"parentRunId,omitempty"`
	ParentNodeID    string     `json:"parentNodeId,omitempty"`
}

type CredentialInfo struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	CreatedAt time.Time `json:"createdAt"`
}

type Event struct {
	Seq        int64          `json:"seq"`
	Type       string         `json:"type"`
	At         time.Time      `json:"at"`
	NodeID     string         `json:"nodeId,omitempty"`
	Attempt    int            `json:"attempt,omitempty"`
	InputHash  string         `json:"inputHash,omitempty"`
	OutputHash string         `json:"outputHash,omitempty"`
	DurationMs int64          `json:"durationMs,omitempty"`
	Error      string         `json:"error,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, "loom.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, blobDir: filepath.Join(dir, "blobs")}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.initCrypto(dir); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for name, typ := range map[string]string{
		"parent_run_id":     "TEXT",
		"parent_node_id":    "TEXT",
		"workflow_snapshot": "BLOB",
		"job_id":            "TEXT",
		"template_version":  "INTEGER",
	} {
		ok, err := s.hasColumn("runs", name)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if ok {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE runs ADD COLUMN ` + name + ` ` + typ); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}
	ok, err := s.hasColumn("workflows", "published_version")
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if !ok {
		if _, err := s.db.Exec(`ALTER TABLE workflows ADD COLUMN published_version INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate published_version: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_parent ON runs(parent_run_id)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_job ON runs(job_id)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := s.db.Exec(`UPDATE schema_version SET version = 4`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) initCrypto(dir string) error {
	secret := []byte(os.Getenv("LOOM_SECRET"))
	if len(secret) == 0 {
		if keyPath := os.Getenv("LOOM_SECRET_KEY_FILE"); keyPath != "" {
			data, err := os.ReadFile(keyPath)
			if err != nil {
				return fmt.Errorf("LOOM_SECRET_KEY_FILE: %w", err)
			}
			secret = data
		}
	}
	if len(secret) == 0 {
		path := filepath.Join(dir, "secret.key")
		data, err := os.ReadFile(path)
		switch {
		case err == nil:
			secret = data
		case errors.Is(err, os.ErrNotExist):
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				return err
			}
			if err := os.WriteFile(path, buf, 0o600); err != nil {
				return err
			}
			secret = buf
		default:
			return err
		}
	}
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	s.aead = aead
	return nil
}

// ---------- workflows ----------

func (s *Store) SaveWorkflow(wf *graph.Workflow) error {
	data, err := json.Marshal(wf)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
		INSERT INTO workflows (id, name, version, doc, updated_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name, version = excluded.version,
			doc = excluded.doc, updated_at = excluded.updated_at`,
		wf.ID, wf.Name, wf.Version, data, now())
	return err
}

func (s *Store) GetWorkflow(id string) (*graph.Workflow, error) {
	var doc []byte
	err := s.db.QueryRow(`SELECT doc FROM workflows WHERE id = ?`, id).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workflow %q not found", id)
	}
	if err != nil {
		return nil, err
	}
	var wf graph.Workflow
	if err := json.Unmarshal(doc, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (s *Store) ListWorkflows() ([]Summary, error) {
	rows, err := s.db.Query(`SELECT w.id, w.name, w.version, w.published_version, w.updated_at, w.doc,
		v.content_hash,
		(SELECT COUNT(*) FROM jobs j WHERE j.template_id = w.id),
		(SELECT COUNT(*) FROM jobs j WHERE j.template_id = w.id AND j.enabled = 1)
		FROM workflows w
		LEFT JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.published_version
		ORDER BY w.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Summary
	for rows.Next() {
		var item Summary
		var updated string
		var doc []byte
		var publishedHash sql.NullString
		if err := rows.Scan(&item.ID, &item.Name, &item.Version, &item.PublishedVersion,
			&updated, &doc, &publishedHash, &item.JobCount, &item.ActiveJobCount); err != nil {
			return nil, err
		}
		item.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
		item.Draft = item.PublishedVersion == 0 || !publishedHash.Valid ||
			publishedHash.String != contentHash(doc)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DeleteWorkflow(id string) error {
	_, err := s.db.Exec(`DELETE FROM workflows WHERE id = ?`, id)
	return err
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) PublishWorkflow(wf *graph.Workflow) (WorkflowVersion, error) {
	var next int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM workflow_versions WHERE workflow_id = ?`,
		wf.ID).Scan(&next); err != nil {
		return WorkflowVersion{}, err
	}
	snapshot := *wf
	snapshot.Version = next
	data, err := json.Marshal(&snapshot)
	if err != nil {
		return WorkflowVersion{}, err
	}
	publishedAt := time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return WorkflowVersion{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO workflow_versions (workflow_id, version, doc, content_hash, published_at)
		VALUES (?, ?, ?, ?, ?)`, wf.ID, next, data, contentHash(data), publishedAt.Format(time.RFC3339Nano)); err != nil {
		return WorkflowVersion{}, err
	}
	if _, err := tx.Exec(`UPDATE workflows SET version = ?, published_version = ?, doc = ?, updated_at = ?
		WHERE id = ?`, next, next, data, publishedAt.Format(time.RFC3339Nano), wf.ID); err != nil {
		return WorkflowVersion{}, err
	}
	if err := tx.Commit(); err != nil {
		return WorkflowVersion{}, err
	}
	wf.Version = next
	return WorkflowVersion{
		WorkflowID: wf.ID, Version: next, ContentHash: contentHash(data),
		NodeCount: len(snapshot.Nodes), PublishedAt: publishedAt,
	}, nil
}

func (s *Store) ListWorkflowVersions(workflowID string) ([]WorkflowVersion, error) {
	rows, err := s.db.Query(`SELECT workflow_id, version, doc, content_hash, published_at
		FROM workflow_versions WHERE workflow_id = ? ORDER BY version DESC`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkflowVersion
	for rows.Next() {
		var item WorkflowVersion
		var doc []byte
		var published string
		if err := rows.Scan(&item.WorkflowID, &item.Version, &doc, &item.ContentHash, &published); err != nil {
			return nil, err
		}
		item.NodeCount = len(decodeNodeIDs(doc))
		item.PublishedAt, _ = time.Parse(time.RFC3339Nano, published)
		out = append(out, item)
	}
	return out, rows.Err()
}

func decodeNodeIDs(doc []byte) []string {
	var wf graph.Workflow
	if err := json.Unmarshal(doc, &wf); err != nil {
		return nil
	}
	ids := make([]string, 0, len(wf.Nodes))
	for _, node := range wf.Nodes {
		ids = append(ids, node.ID)
	}
	return ids
}

func (s *Store) GetWorkflowVersion(workflowID string, version int) (*graph.Workflow, error) {
	var doc []byte
	err := s.db.QueryRow(`SELECT doc FROM workflow_versions WHERE workflow_id = ? AND version = ?`,
		workflowID, version).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("workflow %q version %d not found", workflowID, version)
	}
	if err != nil {
		return nil, err
	}
	var wf graph.Workflow
	if err := json.Unmarshal(doc, &wf); err != nil {
		return nil, err
	}
	return &wf, nil
}

func (s *Store) WorkflowState(workflowID string) (WorkflowState, error) {
	wf, err := s.GetWorkflow(workflowID)
	if err != nil {
		return WorkflowState{}, err
	}
	versions, err := s.ListWorkflowVersions(workflowID)
	if err != nil {
		return WorkflowState{}, err
	}
	var published int
	var publishedHash string
	if len(versions) > 0 {
		published = versions[0].Version
		publishedHash = versions[0].ContentHash
	}
	data, err := json.Marshal(wf)
	if err != nil {
		return WorkflowState{}, err
	}
	return WorkflowState{
		PublishedVersion: published,
		Draft:            published == 0 || publishedHash != contentHash(data),
		ContentHash:      contentHash(data),
		Versions:         versions,
	}, nil
}

// ---------- jobs ----------

func (s *Store) CreateJob(job *Job) error {
	input, err := marshalMap(job.InputDefaults)
	if err != nil {
		return err
	}
	trigger, err := marshalMap(job.TriggerConfig)
	if err != nil {
		return err
	}
	now := now()
	_, err = s.db.Exec(`INSERT INTO jobs
		(id, name, template_id, template_version, enabled, trigger_type, input_defaults, trigger_config, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.Name, job.TemplateID, job.TemplateVersion, boolInt(job.Enabled),
		job.TriggerType, input, trigger, now, now)
	if err != nil {
		return err
	}
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, now)
	job.UpdatedAt = job.CreatedAt
	return nil
}

func (s *Store) GetJob(id string) (*Job, error) {
	row := s.db.QueryRow(jobQuery+` WHERE j.id = ?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("job %q not found", id)
	}
	return job, err
}

func (s *Store) CountJobsByTemplate(templateID string) (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE template_id = ?`, templateID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListJobs() ([]Job, error) {
	rows, err := s.db.Query(jobQuery + ` ORDER BY j.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *job)
	}
	return out, rows.Err()
}

func (s *Store) SetJobEnabled(id string, enabled bool) error {
	res, err := s.db.Exec(`UPDATE jobs SET enabled = ?, updated_at = ? WHERE id = ?`, boolInt(enabled), now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %q not found", id)
	}
	return nil
}

func (s *Store) UpdateJobVersion(id string, version int) error {
	res, err := s.db.Exec(`UPDATE jobs SET template_version = ?, updated_at = ? WHERE id = ?`, version, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %q not found", id)
	}
	return nil
}

func (s *Store) UpdateJob(job *Job) error {
	input, err := marshalMap(job.InputDefaults)
	if err != nil {
		return err
	}
	trigger, err := marshalMap(job.TriggerConfig)
	if err != nil {
		return err
	}
	now := now()
	res, err := s.db.Exec(`UPDATE jobs SET name = ?, trigger_type = ?, input_defaults = ?,
		trigger_config = ?, updated_at = ? WHERE id = ?`,
		job.Name, job.TriggerType, input, trigger, now, job.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %q not found", job.ID)
	}
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, now)
	return nil
}

func (s *Store) DeleteJob(id string) error {
	res, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("job %q not found", id)
	}
	return nil
}

const jobQuery = `SELECT j.id, j.name, j.template_id, COALESCE(w.name, ''), j.template_version,
	j.enabled, j.trigger_type, j.input_defaults, j.trigger_config, j.created_at, j.updated_at,
	(SELECT MAX(r.started_at) FROM runs r WHERE r.job_id = j.id)
	FROM jobs j LEFT JOIN workflows w ON w.id = j.template_id`

func scanJob(row scanner) (*Job, error) {
	var job Job
	var enabled int
	var input, trigger []byte
	var created, updated string
	var lastRun sql.NullString
	if err := row.Scan(&job.ID, &job.Name, &job.TemplateID, &job.TemplateName, &job.TemplateVersion,
		&enabled, &job.TriggerType, &input, &trigger, &created, &updated, &lastRun); err != nil {
		return nil, err
	}
	job.Enabled = enabled != 0
	job.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	job.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	if lastRun.Valid {
		t, _ := time.Parse(time.RFC3339Nano, lastRun.String)
		job.LastRunAt = &t
	}
	if len(input) > 0 {
		_ = json.Unmarshal(input, &job.InputDefaults)
	}
	if len(trigger) > 0 {
		_ = json.Unmarshal(trigger, &job.TriggerConfig)
	}
	return &job, nil
}

func marshalMap(value map[string]any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ---------- runs ----------

func (s *Store) CreateRun(run Run) error {
	_, err := s.db.Exec(`
		INSERT INTO runs (id, workflow_id, workflow_version, status, trigger, started_at, error,
			resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowID, run.WorkflowVersion, run.Status, run.Trigger,
		run.StartedAt.UTC().Format(time.RFC3339Nano), run.Error, run.ResumeData,
		run.ParentRunID, run.ParentNodeID, run.Snapshot, run.JobID, run.TemplateVersion)
	return err
}

func (s *Store) GetRun(id string) (*Run, error) {
	row := s.db.QueryRow(`
		SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		       finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version
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
	row := s.db.QueryRow(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                             finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version
	                      FROM runs WHERE resume_token = ? AND status = 'waiting'`, token)
	run, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no waiting run for that token")
	}
	return run, err
}

func (s *Store) UpdateRunStatus(id, status, errMsg string, finished *time.Time) error {
	var finishedAt any
	if finished != nil {
		finishedAt = finished.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.Exec(`UPDATE runs SET status = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, errMsg, finishedAt, id)
	return err
}

func (s *Store) SetRunWaiting(id, nodeID, token string, data []byte) error {
	_, err := s.db.Exec(`UPDATE runs SET status = 'waiting', resume_node = ?, resume_token = ?, resume_data = ? WHERE id = ?`,
		nodeID, token, data, id)
	return err
}

func (s *Store) ClearRunWaiting(id string) error {
	_, err := s.db.Exec(`UPDATE runs SET status = 'running', resume_node = '', resume_token = '', resume_data = NULL WHERE id = ?`, id)
	return err
}

func (s *Store) ListRuns(workflowID string, limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 50
	}
	query := `SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                 finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version
	          FROM runs`
	args := []any{}
	if workflowID != "" {
		query += ` WHERE workflow_id = ?`
		args = append(args, workflowID)
	}
	query += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(query, args...)
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
	rows, err := s.db.Query(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                                finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version
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
	rows, err := s.db.Query(`SELECT id, workflow_id, workflow_version, status, trigger, started_at,
	                                finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version
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

type PruneResult struct {
	Runs   int64 `json:"runs"`
	Events int64 `json:"events"`
	Blobs  int64 `json:"blobs"`
}

func (s *Store) Prune(before time.Time) (PruneResult, error) {
	var result PruneResult
	cutoff := before.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id FROM runs
	                       WHERE status IN ('done', 'failed', 'cancelled')
	                         AND COALESCE(finished_at, started_at) < ?`, cutoff)
	if err != nil {
		return result, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	for _, id := range ids {
		res, err := tx.Exec(`DELETE FROM events WHERE run_id = ?`, id)
		if err != nil {
			return result, err
		}
		n, _ := res.RowsAffected()
		result.Events += n
		if _, err := tx.Exec(`DELETE FROM runs WHERE id = ?`, id); err != nil {
			return result, err
		}
		result.Runs++
	}

	rows, err = tx.Query(`SELECT hash FROM blobs
	                      WHERE created_at < ?
	                        AND hash NOT IN (
	                          SELECT input_hash FROM events WHERE input_hash <> ''
	                          UNION
	                          SELECT output_hash FROM events WHERE output_hash <> ''
	                        )`, cutoff)
	if err != nil {
		return result, err
	}
	var hashes []string
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			rows.Close()
			return result, err
		}
		hashes = append(hashes, hash)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	for _, hash := range hashes {
		if _, err := tx.Exec(`DELETE FROM blobs WHERE hash = ?`, hash); err != nil {
			return result, err
		}
		result.Blobs++
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	for _, hash := range hashes {
		_ = os.Remove(filepath.Join(s.blobDir, hash[:2], hash))
	}
	return result, nil
}

// StaleRuns lists runs that were in flight when the previous process stopped.
// Statuses are deliberately left untouched: the engine resumes them from their
// event log instead of failing work that already completed.
func (s *Store) StaleRuns() ([]Run, error) {
	rows, err := s.db.Query(`
		SELECT id, workflow_id, workflow_version, status, trigger, started_at,
		       finished_at, error, resume_node, resume_token, resume_data, parent_run_id, parent_node_id, workflow_snapshot, job_id, template_version
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

// AcquireLease takes or renews a named lease. It returns false when another
// owner holds an unexpired lease. Expiry uses wall-clock nanoseconds so the
// comparison is numeric and immune to timestamp formatting.
func (s *Store) AcquireLease(name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	expires := now.Add(ttl).UnixNano()
	res, err := s.db.Exec(`
		INSERT INTO leases(name, owner, expires_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET owner = excluded.owner, expires_at = excluded.expires_at
		WHERE leases.owner = excluded.owner OR leases.expires_at < ?`,
		name, owner, expires, now.UnixNano())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) ReleaseLease(name, owner string) error {
	_, err := s.db.Exec(`DELETE FROM leases WHERE name = ? AND owner = ?`, name, owner)
	return err
}

// ---------- events ----------

func (s *Store) AppendEvent(runID string, ev Event) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id = ?`, runID).Scan(&seq); err != nil {
		return 0, err
	}
	data, _ := json.Marshal(RedactValue(ev.Data))
	if _, err := tx.Exec(`
		INSERT INTO events (run_id, seq, type, at, node_id, attempt, input_hash, output_hash, duration_ms, error, data)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID, seq, ev.Type, ev.At.UTC().Format(time.RFC3339Nano), ev.NodeID, ev.Attempt,
		ev.InputHash, ev.OutputHash, ev.DurationMs, ev.Error, data); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return seq, nil
}

func (s *Store) ListEvents(runID string) ([]Event, error) {
	return s.ListEventsAfter(runID, 0)
}

// ListEventsAfter returns only events with a sequence greater than after, so
// streaming clients can poll incrementally instead of re-reading the log.
func (s *Store) ListEventsAfter(runID string, after int64) ([]Event, error) {
	rows, err := s.db.Query(`
		SELECT seq, type, at, node_id, attempt, input_hash, output_hash, duration_ms, error, data
		FROM events WHERE run_id = ? AND seq > ? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ev Event
		var at string
		var data []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &at, &ev.NodeID, &ev.Attempt,
			&ev.InputHash, &ev.OutputHash, &ev.DurationMs, &ev.Error, &data); err != nil {
			return nil, err
		}
		ev.At, _ = time.Parse(time.RFC3339Nano, at)
		if len(data) > 0 {
			_ = json.Unmarshal(data, &ev.Data)
		}
		out = append(out, ev)
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
	err := s.db.QueryRow(`
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

func (s *Store) SaveBlob(data []byte) (string, error) {
	data = RedactJSON(data)
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(s.blobDir, hash[:2])
	path := filepath.Join(dir, hash)

	if _, err := os.Stat(path); err == nil {
		return hash, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "blob-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return "", err
	}
	_, _ = s.db.Exec(`INSERT INTO blobs (hash, size, created_at) VALUES (?, ?, ?)
		ON CONFLICT(hash) DO NOTHING`, hash, len(data), now())
	return hash, nil
}

func (s *Store) LoadBlob(hash string) ([]byte, error) {
	if len(hash) != 64 {
		return nil, fmt.Errorf("invalid blob hash %q", hash)
	}
	for _, r := range hash {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return nil, fmt.Errorf("invalid blob hash %q", hash)
		}
	}
	return os.ReadFile(filepath.Join(s.blobDir, hash[:2], hash))
}

// ---------- credentials ----------

func (s *Store) SaveCredential(id, typ string, plaintext []byte) error {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := s.aead.Seal(nonce, nonce, plaintext, nil)
	_, err := s.db.Exec(`
		INSERT INTO credentials (id, type, data, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET type = excluded.type, data = excluded.data`,
		id, typ, sealed, now())
	return err
}

func (s *Store) ListCredentials() ([]CredentialInfo, error) {
	rows, err := s.db.Query(`SELECT id, type, created_at FROM credentials ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialInfo
	for rows.Next() {
		var item CredentialInfo
		var created string
		if err := rows.Scan(&item.ID, &item.Type, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) DeleteCredential(id string) error {
	res, err := s.db.Exec(`DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("credential %q not found", id)
	}
	return nil
}

func (s *Store) GetCredential(id string) (string, []byte, error) {
	var typ string
	var sealed []byte
	err := s.db.QueryRow(`SELECT type, data FROM credentials WHERE id = ?`, id).Scan(&typ, &sealed)
	if err != nil {
		return "", nil, err
	}
	nonceSize := s.aead.NonceSize()
	if len(sealed) < nonceSize {
		return "", nil, errors.New("credential is corrupt")
	}
	plaintext, err := s.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return "", nil, err
	}
	return typ, plaintext, nil
}

// ---------- helpers ----------

type scanner interface {
	Scan(dest ...any) error
}

func scanRun(row scanner) (*Run, error) {
	var run Run
	var started, finished sql.NullString
	var errMsg, node, token, parentRun, parentNode, jobID sql.NullString
	var templateVersion sql.NullInt64
	var data, snapshot []byte

	if err := row.Scan(&run.ID, &run.WorkflowID, &run.WorkflowVersion, &run.Status, &run.Trigger,
		&started, &finished, &errMsg, &node, &token, &data, &parentRun, &parentNode,
		&snapshot, &jobID, &templateVersion); err != nil {
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
	run.Snapshot = snapshot
	run.ParentRunID = parentRun.String
	run.ParentNodeID = parentNode.String
	run.JobID = jobID.String
	if templateVersion.Valid {
		run.TemplateVersion = int(templateVersion.Int64)
	}
	return &run, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version (version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version);

CREATE TABLE IF NOT EXISTS leases (
	name TEXT PRIMARY KEY,
	owner TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS workflows (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	version INTEGER NOT NULL,
	published_version INTEGER NOT NULL DEFAULT 0,
	doc BLOB NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_versions (
	workflow_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	published_at TEXT NOT NULL,
	PRIMARY KEY (workflow_id, version)
);
CREATE INDEX IF NOT EXISTS idx_workflow_versions_workflow ON workflow_versions(workflow_id, version DESC);

CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	template_id TEXT NOT NULL,
	template_version INTEGER NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	trigger_type TEXT NOT NULL DEFAULT 'manual',
	input_defaults BLOB,
	trigger_config BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_template ON jobs(template_id);

CREATE TABLE IF NOT EXISTS runs (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	workflow_version TEXT NOT NULL,
	status TEXT NOT NULL,
	trigger TEXT NOT NULL,
	started_at TEXT NOT NULL,
	finished_at TEXT,
	error TEXT,
	resume_node TEXT,
	resume_token TEXT,
	resume_data BLOB,
	parent_run_id TEXT,
	parent_node_id TEXT,
	workflow_snapshot BLOB,
	job_id TEXT,
	template_version INTEGER
);
CREATE INDEX IF NOT EXISTS idx_runs_workflow ON runs(workflow_id);

CREATE TABLE IF NOT EXISTS events (
	run_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	type TEXT NOT NULL,
	at TEXT NOT NULL,
	node_id TEXT,
	attempt INTEGER,
	input_hash TEXT,
	output_hash TEXT,
	duration_ms INTEGER,
	error TEXT,
	data BLOB,
	PRIMARY KEY (run_id, seq)
);

CREATE TABLE IF NOT EXISTS blobs (
	hash TEXT PRIMARY KEY,
	size INTEGER NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS credentials (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	data BLOB NOT NULL,
	created_at TEXT NOT NULL
);
`
