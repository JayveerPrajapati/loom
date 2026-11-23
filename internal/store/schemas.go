package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"loom/internal/graph"
	"loom/internal/schemas"
)

// Data-contract store (Configuration Hub v2, plan D10/D12): contracts are
// reusable, versioned field lists describing the shape of data entering or
// leaving a surface. Version rows are append-only; the usage index records
// which published workflow version pinned which contract version. The
// layout mirrors config_profiles deliberately — one proven machinery.

var (
	ErrDataSchemaNotFound = errors.New("data schema not found")
	ErrStaleDataSchema    = errors.New("data schema was modified concurrently")
	ErrDataSchemaInUse    = errors.New("data schema is referenced by published workflows")
)

// DataSchema is a reusable, versioned data contract.
type DataSchema struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     int             `json:"version"`
	Fields      []schemas.Field `json:"fields"`
	Tags        []string        `json:"tags,omitempty"`
	Owner       string          `json:"owner,omitempty"`
	// Visibility/CreatorID are governance columns on the entity table
	// (Phase 1 D-P0b); they never enter version docs.
	Visibility string    `json:"visibility,omitempty"`
	CreatorID  string    `json:"creatorId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// UsageCount is how many published workflow versions pin any version
	// of this schema; populated only by ListDataSchemas.
	UsageCount int `json:"usageCount,omitempty"`
}

// DataSchemaVersion is one immutable snapshot in a contract's history.
type DataSchemaVersion struct {
	SchemaID    string    `json:"schemaId"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}

// DataSchemaUsage records a published workflow version pinned to a
// contract version — the delete guard and usage-count source.
type DataSchemaUsage struct {
	SchemaID        string    `json:"schemaId"`
	SchemaVersion   int       `json:"schemaVersion"`
	WorkflowID      string    `json:"workflowId"`
	WorkflowVersion int       `json:"workflowVersion"`
	WorkflowName    string    `json:"workflowName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// dataSchemaDoc is what a version row stores: everything that can change
// between versions, so any version is self-contained.
type dataSchemaDoc struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Fields      []schemas.Field `json:"fields"`
	Tags        []string        `json:"tags,omitempty"`
	Owner       string          `json:"owner,omitempty"`
}

// CreateDataSchema stores version 1 of a new contract.
func (s *Store) CreateDataSchema(d *DataSchema, actor string) (DataSchema, error) {
	if d.Fields == nil {
		d.Fields = []schemas.Field{}
	}
	d.Visibility = normalizeVisibility(d.Visibility)
	at := time.Now().UTC()
	doc := dataSchemaDoc{Name: d.Name, Description: d.Description, Fields: d.Fields, Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return DataSchema{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DataSchema{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO data_schemas (id, name, description, current_version, visibility, creator_id, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Description, d.Visibility, d.CreatorID, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		return DataSchema{}, err
	}
	if _, err := tx.Exec(`INSERT INTO data_schema_versions (schema_id, version, doc, content_hash, actor, created_at)
		VALUES (?, 1, ?, ?, ?, ?)`,
		d.ID, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return DataSchema{}, err
	}
	if err := tx.Commit(); err != nil {
		return DataSchema{}, err
	}
	d.Version = 1
	d.CreatedAt = at
	d.UpdatedAt = at
	return *d, nil
}

// UpdateDataSchema appends the next version. expectedVersion is the
// caller's current version: a mismatch means someone else wrote first and
// the update is rejected with ErrStaleDataSchema.
func (s *Store) UpdateDataSchema(d *DataSchema, expectedVersion int, actor string) (DataSchema, error) {
	if d.Fields == nil {
		d.Fields = []schemas.Field{}
	}
	at := time.Now().UTC()
	doc := dataSchemaDoc{Name: d.Name, Description: d.Description, Fields: d.Fields, Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return DataSchema{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return DataSchema{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE data_schemas
		SET name = ?, description = ?, current_version = current_version + 1, updated_at = ?
		WHERE id = ? AND current_version = ?`,
		d.Name, d.Description, at.Format(time.RFC3339Nano), d.ID, expectedVersion)
	if err != nil {
		return DataSchema{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM data_schemas WHERE id = ?`, d.ID).Scan(&exists); err != nil {
			return DataSchema{}, err
		}
		if exists == 0 {
			return DataSchema{}, ErrDataSchemaNotFound
		}
		return DataSchema{}, ErrStaleDataSchema
	}
	if err := tx.QueryRow(`SELECT current_version FROM data_schemas WHERE id = ?`, d.ID).Scan(&d.Version); err != nil {
		return DataSchema{}, err
	}
	if _, err := tx.Exec(`INSERT INTO data_schema_versions (schema_id, version, doc, content_hash, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Version, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return DataSchema{}, err
	}
	if err := tx.Commit(); err != nil {
		return DataSchema{}, err
	}
	d.UpdatedAt = at
	return *d, nil
}

func (s *Store) GetDataSchema(id string) (*DataSchema, error) {
	var out DataSchema
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc
		FROM data_schemas p
		JOIN data_schema_versions v ON v.schema_id = p.id AND v.version = p.current_version
		WHERE p.id = ?`, id).Scan(&out.ID, &out.Name, &out.Description, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDataSchemaNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanDataSchemaFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func scanDataSchemaFinish(out *DataSchema, created, updated string, doc []byte) error {
	var err error
	if out.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return err
	}
	if out.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return err
	}
	var d dataSchemaDoc
	if err := json.Unmarshal(doc, &d); err != nil {
		return err
	}
	// The doc is the source of truth: for the current version it matches
	// the parent row, and for historical reconstruction it restores the
	// name/description as they were at that version.
	out.Name = d.Name
	out.Description = d.Description
	out.Fields = d.Fields
	out.Tags = d.Tags
	out.Owner = d.Owner
	out.Visibility = normalizeVisibility(out.Visibility)
	return nil
}

// ListDataSchemas returns every contract with its current doc plus its
// usage count from the publish-time usage index.
func (s *Store) ListDataSchemas() ([]DataSchema, error) {
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM data_schema_usage u WHERE u.schema_id = p.id)
		FROM data_schemas p
		JOIN data_schema_versions v ON v.schema_id = p.id AND v.version = p.current_version
		ORDER BY p.name, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataSchema
	usage := map[string]int{}
	for rows.Next() {
		var item DataSchema
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanDataSchemaFinish(&item, created, updated, doc); err != nil {
			return nil, err
		}
		usage[item.ID] = count
		out = append(out, item)
	}
	for i := range out {
		out[i].UsageCount = usage[out[i].ID]
	}
	return out, rows.Err()
}

// GetDataSchemaVersion reconstructs the contract as of one version.
func (s *Store) GetDataSchemaVersion(id string, version int) (*DataSchema, error) {
	var out DataSchema
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.description, v.version, p.visibility, p.creator_id, p.created_at, p.created_at, v.doc
		FROM data_schemas p
		JOIN data_schema_versions v ON v.schema_id = p.id
		WHERE p.id = ? AND v.version = ?`, id, version).
		Scan(&out.ID, &out.Name, &out.Description, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrDataSchemaNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanDataSchemaFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) ListDataSchemaVersions(id string) ([]DataSchemaVersion, error) {
	rows, err := s.read(`SELECT schema_id, version, content_hash, actor, created_at
		FROM data_schema_versions WHERE schema_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataSchemaVersion
	for rows.Next() {
		var item DataSchemaVersion
		var created string
		if err := rows.Scan(&item.SchemaID, &item.Version, &item.ContentHash, &item.Actor, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteDataSchema removes a contract and its version history, but only
// when the usage index shows no published workflow pinned it.
func (s *Store) DeleteDataSchema(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM data_schema_usage WHERE schema_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrDataSchemaInUse
	}
	// Phase 1 P0-4: a profile bundle reference guards the schema too —
	// deleting it would break every profile that composes it.
	if refs, err := tx.Query(`SELECT p.name FROM profile_bundle_refs r
		JOIN config_profiles p ON p.id = r.profile_id
		WHERE r.asset_kind = 'schema' AND r.asset_id = ?`, id); err == nil {
		var referencing []string
		for refs.Next() {
			var name string
			if err := refs.Scan(&name); err == nil {
				referencing = append(referencing, strconv.Quote(name))
			}
		}
		refs.Close()
		if len(referencing) > 0 {
			return fmt.Errorf("%w: %s", ErrDataSchemaInUse, strings.Join(referencing, ", "))
		}
	} else {
		return err
	}
	res, err := tx.Exec(`DELETE FROM data_schemas WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrDataSchemaNotFound
	}
	if _, err := tx.Exec(`DELETE FROM data_schema_versions WHERE schema_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordDataSchemaUsage pins a published workflow version to the contract
// version it resolved at publish time.
func (s *Store) RecordDataSchemaUsage(schemaID string, schemaVersion int, workflowID string, workflowVersion int) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO data_schema_usage
		(schema_id, schema_version, workflow_id, workflow_version, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		schemaID, schemaVersion, workflowID, workflowVersion, now())
	return err
}

func (s *Store) DataSchemaUsage(schemaID string) ([]DataSchemaUsage, error) {
	rows, err := s.read(`SELECT u.schema_id, u.schema_version, u.workflow_id, u.workflow_version, u.created_at, COALESCE(w.name, u.workflow_id)
		FROM data_schema_usage u LEFT JOIN workflows w ON w.id = u.workflow_id
		WHERE u.schema_id = ? ORDER BY u.created_at`, schemaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataSchemaUsage
	for rows.Next() {
		var item DataSchemaUsage
		var created string
		if err := rows.Scan(&item.SchemaID, &item.SchemaVersion, &item.WorkflowID, &item.WorkflowVersion, &created, &item.WorkflowName); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DataSchemaUsageCount returns how many published workflow versions
// reference the contract across all versions of it.
func (s *Store) DataSchemaUsageCount(schemaID string) (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM data_schema_usage WHERE schema_id = ?`, schemaID).Scan(&count)
	return count, err
}

// ResolveSchemaRefs inlines every schema reference into resolved field
// lists on the workflow and its nodes (v2 plan, D4-style resolution: the
// published snapshot is self-contained, so the engine, webhook handler and
// replay need zero store awareness). Refs are kept on the doc for display;
// usage pins are returned for the publish path to record.
func (s *Store) ResolveSchemaRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.ResolveSchemaRefsWith(wf, NewLookupCache())
}

func (s *Store) ResolveSchemaRefsWith(wf *graph.Workflow, cache *LookupCache) (map[string]int, error) {
	if cache == nil {
		cache = NewLookupCache()
	}
	refs := map[string]int{}
	resolve := func(ref string) ([]schemas.Field, int, error) {
		if ref == "" {
			return nil, 0, nil
		}
		schema, err := cache.DataSchema(s, ref)
		if err != nil {
			return nil, 0, err
		}
		refs[schema.ID] = schema.Version
		return schema.Fields, schema.Version, nil
	}
	var err error
	if wf.InputSchemaFields, _, err = resolve(wf.InputSchemaRef); err != nil {
		return nil, fmt.Errorf("workflow input schema: %w", err)
	}
	if wf.OutputSchemaFields, _, err = resolve(wf.OutputSchemaRef); err != nil {
		return nil, fmt.Errorf("workflow output schema: %w", err)
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.InputSchemaFields, _, err = resolve(n.InputSchemaRef); err != nil {
			return nil, fmt.Errorf("node %q input schema: %w", n.ID, err)
		}
		if n.OutputSchemaFields, _, err = resolve(n.OutputSchemaRef); err != nil {
			return nil, fmt.Errorf("node %q output schema: %w", n.ID, err)
		}
	}
	return refs, nil
}
