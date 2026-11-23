package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

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
	row := s.readRow(jobQuery+` WHERE j.id = ?`, id)
	job, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("job %q not found", id)
	}
	return job, err
}

func (s *Store) CountJobsByTemplate(templateID string) (int, error) {
	var count int
	if err := s.readRow(`SELECT COUNT(*) FROM jobs WHERE template_id = ?`, templateID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) ListJobs() ([]Job, error) {
	rows, err := s.read(jobQuery + ` ORDER BY j.created_at DESC`)
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
