package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"loom/internal/graph"
	"time"
)

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

var ErrWorkflowNotFound = errors.New("workflow not found")

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

func (s *Store) SaveWorkflow(wf *graph.Workflow) error {
	// ConfigSources describes a publish-time resolution. A draft save
	// carries the editor's document, and edits invalidate that metadata,
	// so drafts never store it; the next publish recomputes it.
	for i := range wf.Nodes {
		wf.Nodes[i].ConfigSources = nil
	}
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
	err := s.readRow(`SELECT doc FROM workflows WHERE id = ?`, id).Scan(&doc)
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
	rows, err := s.read(`SELECT w.id, w.name, w.version, w.published_version, w.updated_at, w.doc,
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Usage pins are references, not history: deleting the workflow
	// releases the assets it pinned, otherwise delete guards trip on
	// dead rows forever. (Historical snapshots keep the resolved
	// content, so nothing is lost.)
	if _, err := tx.Exec(`DELETE FROM config_profile_usage WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM mapping_usage WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM data_schema_usage WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM script_usage WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM connector_usage WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM workflows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrWorkflowNotFound
	}
	return tx.Commit()
}

func contentHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *Store) PublishWorkflow(wf *graph.Workflow) (WorkflowVersion, error) {
	var next int
	if err := s.readRow(`SELECT COALESCE(MAX(version), 0) + 1 FROM workflow_versions WHERE workflow_id = ?`,
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
	rows, err := s.read(`SELECT workflow_id, version, doc, content_hash, published_at
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
	err := s.readRow(`SELECT doc FROM workflow_versions WHERE workflow_id = ? AND version = ?`,
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
