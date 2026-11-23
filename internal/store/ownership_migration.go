package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"loom/internal/graph"
)

// ProfileOwnershipPlan is the read-only result of PlanProfileOwnership. The
// exported Report is what the CLI writes for review; the per-workflow update
// payloads stay package-private so only ApplyProfileOwnership can write them.
type ProfileOwnershipPlan struct {
	Report  OwnershipMigrationReport
	updates []ownershipWorkflowUpdate
}

// HasChanges reports whether the plan would stamp at least one node.
func (p *ProfileOwnershipPlan) HasChanges() bool {
	return p != nil && len(p.updates) > 0
}

// ownershipWorkflowUpdate is one workflow's pending migration: the exact
// bytes before and after, the nodes that changed, and their decisions. The
// original bytes make the apply a compare-and-swap and feed the rollback
// table.
type ownershipWorkflowUpdate struct {
	ID           string
	Name         string
	OriginalDoc  []byte
	MigratedDoc  []byte
	ChangedNodes []string
	Changes      []OwnershipMigrationChange
}

// ApplyProfileOwnership stamps a plan's drafts in one transaction, after
// refusing if any workflow changed since the plan was computed. Each changed
// node gets an ownership_migration_backup row holding the exact original and
// migrated docs, and every decision is written to the auth audit log as an
// ownership.migrated event.
func (s *Store) ApplyProfileOwnership(plan *ProfileOwnershipPlan, migrationID string) error {
	if !plan.HasChanges() {
		return nil
	}
	if migrationID == "" {
		return errors.New("apply ownership migration: migration id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("apply ownership migration: %w", err)
	}
	defer tx.Rollback()
	at := now()
	for _, update := range plan.updates {
		var current []byte
		if err := tx.QueryRow(`SELECT doc FROM workflows WHERE id = ?`, update.ID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("apply ownership migration: workflow %q no longer exists; re-run the dry-run", update.ID)
			}
			return fmt.Errorf("apply ownership migration: workflow %q: %w", update.ID, err)
		}
		if !bytes.Equal(current, update.OriginalDoc) {
			return fmt.Errorf("apply ownership migration: workflow %q changed since the dry-run; re-run the dry-run", update.ID)
		}
		for _, nodeID := range update.ChangedNodes {
			originalNode, err := nodeJSON(update.OriginalDoc, nodeID)
			if err != nil {
				return fmt.Errorf("apply ownership migration: workflow %q node %q: %w", update.ID, nodeID, err)
			}
			if _, err := tx.Exec(`INSERT INTO ownership_migration_backup
				(migration_id, workflow_id, node_id, original_node, original_doc, migrated_doc, created_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				migrationID, update.ID, nodeID, string(originalNode),
				update.OriginalDoc, update.MigratedDoc, at); err != nil {
				return fmt.Errorf("apply ownership migration: backup workflow %q node %q: %w", update.ID, nodeID, err)
			}
		}
		if _, err := tx.Exec(`UPDATE workflows SET doc = ?, updated_at = ? WHERE id = ?`,
			update.MigratedDoc, at, update.ID); err != nil {
			return fmt.Errorf("apply ownership migration: workflow %q: %w", update.ID, err)
		}
		for _, change := range update.Changes {
			detail, err := json.Marshal(change)
			if err != nil {
				return fmt.Errorf("apply ownership migration: audit detail: %w", err)
			}
			if _, err := tx.Exec(`INSERT INTO auth_events (at, type, user_id, actor, ip, detail)
				VALUES (?, ?, '', 'system:ownership-migrate', '', ?)`,
				at, "ownership.migrated", string(detail)); err != nil {
				return fmt.Errorf("apply ownership migration: audit event: %w", err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("apply ownership migration: %w", err)
	}
	return nil
}

// LatestOwnershipMigrationID returns the most recently applied migration id,
// or "" when no migration has been applied. The CLI uses it for the default
// `--rollback` target.
func (s *Store) LatestOwnershipMigrationID() (string, error) {
	var id string
	err := s.readRow(`SELECT migration_id FROM ownership_migration_backup
		ORDER BY created_at DESC, migration_id DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// OwnershipRollbackReport summarizes one rollback.
type OwnershipRollbackReport struct {
	MigrationID string
	Workflows   int
	Nodes       int
}

// RollbackProfileOwnership restores the exact draft bytes captured by one
// applied migration. It refuses when a draft changed after the apply, because
// silently discarding those edits would be worse than a failed rollback.
func (s *Store) RollbackProfileOwnership(migrationID string) (OwnershipRollbackReport, error) {
	report := OwnershipRollbackReport{MigrationID: migrationID}
	if migrationID == "" {
		return report, errors.New("rollback ownership migration: migration id is required")
	}
	rows, err := s.read(`SELECT workflow_id, original_doc, migrated_doc
		FROM ownership_migration_backup WHERE migration_id = ? ORDER BY workflow_id`, migrationID)
	if err != nil {
		return report, fmt.Errorf("rollback ownership migration: %w", err)
	}
	type workflowBackup struct {
		original []byte
		migrated []byte
	}
	backups := map[string]*workflowBackup{}
	order := []string{}
	for rows.Next() {
		var workflowID string
		var original, migrated []byte
		if err := rows.Scan(&workflowID, &original, &migrated); err != nil {
			rows.Close()
			return report, fmt.Errorf("rollback ownership migration: %w", err)
		}
		if _, ok := backups[workflowID]; !ok {
			backups[workflowID] = &workflowBackup{original: original, migrated: migrated}
			order = append(order, workflowID)
		}
		report.Nodes++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return report, fmt.Errorf("rollback ownership migration: %w", err)
	}
	rows.Close()
	if len(order) == 0 {
		return report, fmt.Errorf("rollback ownership migration: no backup for migration %q", migrationID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return report, fmt.Errorf("rollback ownership migration: %w", err)
	}
	defer tx.Rollback()
	for _, workflowID := range order {
		backup := backups[workflowID]
		var current []byte
		if err := tx.QueryRow(`SELECT doc FROM workflows WHERE id = ?`, workflowID).Scan(&current); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return report, fmt.Errorf("rollback ownership migration: workflow %q was deleted after the migration", workflowID)
			}
			return report, fmt.Errorf("rollback ownership migration: workflow %q: %w", workflowID, err)
		}
		if !bytes.Equal(current, backup.migrated) {
			return report, fmt.Errorf("rollback ownership migration: workflow %q changed after the migration; restore the backup archive instead", workflowID)
		}
		if _, err := tx.Exec(`UPDATE workflows SET doc = ?, updated_at = ? WHERE id = ?`,
			backup.original, now(), workflowID); err != nil {
			return report, fmt.Errorf("rollback ownership migration: workflow %q: %w", workflowID, err)
		}
		report.Workflows++
	}
	if err := tx.Commit(); err != nil {
		return report, fmt.Errorf("rollback ownership migration: %w", err)
	}
	return report, nil
}

// nodeJSON returns one node's stored JSON from a workflow doc.
func nodeJSON(doc []byte, nodeID string) ([]byte, error) {
	var wf graph.Workflow
	if err := json.Unmarshal(doc, &wf); err != nil {
		return nil, err
	}
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == nodeID {
			return json.Marshal(wf.Nodes[i])
		}
	}
	return nil, fmt.Errorf("node %q not found", nodeID)
}
