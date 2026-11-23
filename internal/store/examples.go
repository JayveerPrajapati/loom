package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"loom/examples"
	"loom/internal/graph"
)

// removedNodeTypes lists node types that no longer exist in the catalog. Only
// stored copies of the bundled examples are rewritten; user-authored workflows
// keep their nodes and surface `unknown_node_type` validation instead.
var removedNodeTypes = map[string]bool{
	"chat.slack":     true,
	"data.csv.parse": true,
}

func (s *Store) refreshBundledExamples() error {
	list, err := examples.All()
	if err != nil {
		return err
	}
	for _, ex := range list {
		var current graph.Workflow
		if err := json.Unmarshal(ex.Workflow, &current); err != nil {
			return fmt.Errorf("example %s: %w", ex.ID, err)
		}
		replacements := make(map[string]graph.Node, len(current.Nodes))
		for _, node := range current.Nodes {
			replacements[node.ID] = node
		}
		if err := s.rewriteStoredExample(ex.ID, replacements); err != nil {
			return fmt.Errorf("example %s: %w", ex.ID, err)
		}
	}
	return nil
}

func (s *Store) rewriteStoredExample(id string, replacements map[string]graph.Node) error {
	var doc []byte
	// Migration-time read: runs from migrate() before the read pool
	// exists, so it stays on the writer connection.
	err := s.db.QueryRow(`SELECT doc FROM workflows WHERE id = ?`, id).Scan(&doc)
	switch {
	case err == nil:
		out, changed, err := replaceRemovedNodes(doc, replacements)
		if err != nil {
			return err
		}
		if changed {
			if _, err := s.db.Exec(`UPDATE workflows SET doc = ?, updated_at = ? WHERE id = ?`, out, now(), id); err != nil {
				return err
			}
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return err
	}

	rows, err := s.db.Query(`SELECT version, doc FROM workflow_versions WHERE workflow_id = ?`, id)
	if err != nil {
		return err
	}
	type versionDoc struct {
		version int
		doc     []byte
	}
	var versions []versionDoc
	for rows.Next() {
		var v versionDoc
		if err := rows.Scan(&v.version, &v.doc); err != nil {
			rows.Close()
			return err
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, v := range versions {
		out, changed, err := replaceRemovedNodes(v.doc, replacements)
		if err != nil {
			return err
		}
		if !changed {
			continue
		}
		// A published version is a snapshot of a bundled example, not user
		// history. Keep its number and timestamp but replace the retired node
		// and refresh the stored hash so the version list stays consistent.
		if _, err := s.db.Exec(`UPDATE workflow_versions SET doc = ?, content_hash = ? WHERE workflow_id = ? AND version = ?`,
			out, contentHash(out), id, v.version); err != nil {
			return err
		}
	}
	return nil
}

// replaceRemovedNodes swaps a retired node for the node with the same id in the
// current bundled example. It refuses when the node sets differ, so a stored
// workflow a user has restructured is left alone rather than silently rewritten.
func replaceRemovedNodes(doc []byte, replacements map[string]graph.Node) ([]byte, bool, error) {
	var wf graph.Workflow
	if err := json.Unmarshal(doc, &wf); err != nil {
		return nil, false, err
	}
	if len(wf.Nodes) != len(replacements) {
		return nil, false, nil
	}
	for _, node := range wf.Nodes {
		if _, ok := replacements[node.ID]; !ok {
			return nil, false, nil
		}
	}
	changed := false
	for i := range wf.Nodes {
		node := &wf.Nodes[i]
		if !removedNodeTypes[node.Type] {
			continue
		}
		rep, ok := replacements[node.ID]
		if !ok || rep.Type == node.Type || removedNodeTypes[rep.Type] {
			return nil, false, nil
		}
		// Keep the placement and label the user sees; take the working type,
		// config and credentials from the current example.
		rep.Name = node.Name
		rep.Position = node.Position
		wf.Nodes[i] = rep
		changed = true
	}
	if !changed {
		return nil, false, nil
	}
	out, err := json.Marshal(&wf)
	if err != nil {
		return nil, false, err
	}
	return out, true, nil
}
