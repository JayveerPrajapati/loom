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
)

// Mapping store (Configuration Hub v2, plan D13): a mapping is a reusable,
// versioned set of target←source rows a data.map node applies. Version rows
// are append-only; the usage index records which published workflow version
// pinned which mapping version. The layout mirrors config_profiles
// deliberately — one proven machinery.

var (
	ErrMappingNotFound = errors.New("mapping not found")
	ErrStaleMapping    = errors.New("mapping was modified concurrently")
	ErrMappingInUse    = errors.New("mapping is referenced by published workflows")
)

// MappingRow maps one target field from a source expression. Required rows
// that render empty fail the data.map node (unlike contracts, which
// annotate — a mapping is an explicit transform the author asserts). For
// the parameter kinds the target is a parameter name and the source is
// its value template.
type MappingRow struct {
	Target   string `json:"target"`
	Source   string `json:"source"`
	Required bool   `json:"required,omitempty"`
}

// Mapping kinds (Configuration Hub v3 plan D4): "item" rows build the
// outgoing item on a data.map node; the parameter kinds carry reusable
// value rows that publish compiles into the matching HTTP parameter group.
const (
	MappingKindItem    = "item"
	MappingKindBody    = "body"
	MappingKindQuery   = "query"
	MappingKindHeaders = "headers"
	MappingKindPath    = "pathParams"
)

// ValidMappingKind reports whether kind names an allowed mapping kind.
// The empty kind is valid: it is how pre-v3 version docs read, and it
// normalizes to MappingKindItem.
func ValidMappingKind(kind string) bool {
	switch kind {
	case "", MappingKindItem, MappingKindBody, MappingKindQuery, MappingKindHeaders, MappingKindPath:
		return true
	default:
		return false
	}
}

// normalizeMappingKind returns the storage form of kind: empty (legacy)
// becomes item, everything else passes through.
func normalizeMappingKind(kind string) string {
	if kind == "" {
		return MappingKindItem
	}
	return kind
}

// Mapping is a reusable, versioned data contract.
type Mapping struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Kind        string       `json:"kind"`
	Version     int          `json:"version"`
	Rows        []MappingRow `json:"rows"`
	Tags        []string     `json:"tags,omitempty"`
	Owner       string       `json:"owner,omitempty"`
	// Visibility/CreatorID are governance columns on the entity table
	// (Phase 1 D-P0b); they never enter version docs.
	Visibility string    `json:"visibility,omitempty"`
	CreatorID  string    `json:"creatorId,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
	// UsageCount is how many published workflow versions pin any version
	// of this schema; populated only by ListMappings.
	UsageCount int `json:"usageCount,omitempty"`
}

// MappingVersion is one immutable snapshot in a contract's history.
type MappingVersion struct {
	MappingID   string    `json:"mappingId"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}

// MappingUsage records a published workflow version pinned to a
// contract version — the delete guard and usage-count source.
type MappingUsage struct {
	MappingID       string    `json:"mappingId"`
	MappingVersion  int       `json:"mappingVersion"`
	WorkflowID      string    `json:"workflowId"`
	WorkflowVersion int       `json:"workflowVersion"`
	WorkflowName    string    `json:"workflowName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// mappingDoc is what a version row stores: everything that can change
// between versions, so any version is self-contained. The kind is
// omitted in pre-v3 docs and reads back as item.
type mappingDoc struct {
	Name        string       `json:"name"`
	Description string       `json:"description,omitempty"`
	Kind        string       `json:"kind,omitempty"`
	Rows        []MappingRow `json:"rows"`
	Tags        []string     `json:"tags,omitempty"`
	Owner       string       `json:"owner,omitempty"`
}

// CheckMappingKind validates the kind once for both write paths and for
// API request validation.
func CheckMappingKind(kind string) error {
	if !ValidMappingKind(kind) {
		return fmt.Errorf("unknown mapping kind %q (want item, body, query, headers or pathParams)", kind)
	}
	return nil
}

// CreateMapping stores version 1 of a new contract.
func (s *Store) CreateMapping(d *Mapping, actor string) (Mapping, error) {
	if d.Rows == nil {
		d.Rows = []MappingRow{}
	}
	if err := CheckMappingKind(d.Kind); err != nil {
		return Mapping{}, err
	}
	d.Kind = normalizeMappingKind(d.Kind)
	d.Visibility = normalizeVisibility(d.Visibility)
	at := time.Now().UTC()
	doc := mappingDoc{Name: d.Name, Description: d.Description, Kind: d.Kind, Rows: d.Rows, Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return Mapping{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Mapping{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO mappings (id, name, kind, description, current_version, visibility, creator_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Kind, d.Description, d.Visibility, d.CreatorID, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		return Mapping{}, err
	}
	if _, err := tx.Exec(`INSERT INTO mapping_versions (mapping_id, version, doc, content_hash, actor, created_at)
		VALUES (?, 1, ?, ?, ?, ?)`,
		d.ID, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return Mapping{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mapping{}, err
	}
	d.Version = 1
	d.CreatedAt = at
	d.UpdatedAt = at
	return *d, nil
}

// UpdateMapping appends the next version. expectedVersion is the
// caller's current version: a mismatch means someone else wrote first and
// the update is rejected with ErrStaleMapping.
func (s *Store) UpdateMapping(d *Mapping, expectedVersion int, actor string) (Mapping, error) {
	if d.Rows == nil {
		d.Rows = []MappingRow{}
	}
	if err := CheckMappingKind(d.Kind); err != nil {
		return Mapping{}, err
	}
	d.Kind = normalizeMappingKind(d.Kind)
	at := time.Now().UTC()
	doc := mappingDoc{Name: d.Name, Description: d.Description, Kind: d.Kind, Rows: d.Rows, Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return Mapping{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Mapping{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE mappings
		SET name = ?, kind = ?, description = ?, current_version = current_version + 1, updated_at = ?
		WHERE id = ? AND current_version = ?`,
		d.Name, d.Kind, d.Description, at.Format(time.RFC3339Nano), d.ID, expectedVersion)
	if err != nil {
		return Mapping{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM mappings WHERE id = ?`, d.ID).Scan(&exists); err != nil {
			return Mapping{}, err
		}
		if exists == 0 {
			return Mapping{}, ErrMappingNotFound
		}
		return Mapping{}, ErrStaleMapping
	}
	if err := tx.QueryRow(`SELECT current_version FROM mappings WHERE id = ?`, d.ID).Scan(&d.Version); err != nil {
		return Mapping{}, err
	}
	if _, err := tx.Exec(`INSERT INTO mapping_versions (mapping_id, version, doc, content_hash, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Version, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return Mapping{}, err
	}
	if err := tx.Commit(); err != nil {
		return Mapping{}, err
	}
	d.UpdatedAt = at
	return *d, nil
}

func (s *Store) GetMapping(id string) (*Mapping, error) {
	var out Mapping
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc
		FROM mappings p
		JOIN mapping_versions v ON v.mapping_id = p.id AND v.version = p.current_version
		WHERE p.id = ?`, id).Scan(&out.ID, &out.Name, &out.Description, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMappingNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanMappingFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func scanMappingFinish(out *Mapping, created, updated string, doc []byte) error {
	var err error
	if out.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return err
	}
	if out.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return err
	}
	var d mappingDoc
	if err := json.Unmarshal(doc, &d); err != nil {
		return err
	}
	// The doc is the source of truth: for the current version it matches
	// the parent row, and for historical reconstruction it restores the
	// name/description as they were at that version. Pre-v3 docs carry no
	// kind and read back as item mappings.
	out.Name = d.Name
	out.Description = d.Description
	out.Kind = normalizeMappingKind(d.Kind)
	out.Rows = d.Rows
	out.Tags = d.Tags
	out.Owner = d.Owner
	out.Visibility = normalizeVisibility(out.Visibility)
	return nil
}

// ListMappings returns every mapping with its current doc plus its
// usage count from the publish-time usage index.
func (s *Store) ListMappings() ([]Mapping, error) {
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM mapping_usage u WHERE u.mapping_id = p.id)
		FROM mappings p
		JOIN mapping_versions v ON v.mapping_id = p.id AND v.version = p.current_version
		ORDER BY p.name, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mapping
	usage := map[string]int{}
	for rows.Next() {
		var item Mapping
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanMappingFinish(&item, created, updated, doc); err != nil {
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

// ListMappingsByKinds returns every mapping whose kind is in kinds, with
// its current doc plus usage count. The hub page and the ?kind= list
// filter both go through here (v3 plan D4).
func (s *Store) ListMappingsByKinds(kinds []string) ([]Mapping, error) {
	if len(kinds) == 0 {
		return []Mapping{}, nil
	}
	placeholders := make([]string, len(kinds))
	args := make([]any, len(kinds))
	for i, kind := range kinds {
		placeholders[i] = "?"
		args[i] = kind
	}
	rows, err := s.read(`SELECT p.id, p.name, p.kind, p.description, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM mapping_usage u WHERE u.mapping_id = p.id)
		FROM mappings p
		JOIN mapping_versions v ON v.mapping_id = p.id AND v.version = p.current_version
		WHERE p.kind IN (`+strings.Join(placeholders, ",")+`)
		ORDER BY p.name, p.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Mapping
	usage := map[string]int{}
	for rows.Next() {
		var item Mapping
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Description, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanMappingFinish(&item, created, updated, doc); err != nil {
			return nil, err
		}
		usage[item.ID] = count
		out = append(out, item)
	}
	for i := range out {
		out[i].UsageCount = usage[out[i].ID]
	}
	if out == nil {
		out = []Mapping{}
	}
	return out, rows.Err()
}

// GetMappingVersion reconstructs the contract as of one version.
func (s *Store) GetMappingVersion(id string, version int) (*Mapping, error) {
	var out Mapping
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.description, v.version, p.visibility, p.creator_id, p.created_at, p.created_at, v.doc
		FROM mappings p
		JOIN mapping_versions v ON v.mapping_id = p.id
		WHERE p.id = ? AND v.version = ?`, id, version).
		Scan(&out.ID, &out.Name, &out.Description, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrMappingNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanMappingFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) ListMappingVersions(id string) ([]MappingVersion, error) {
	rows, err := s.read(`SELECT mapping_id, version, content_hash, actor, created_at
		FROM mapping_versions WHERE mapping_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MappingVersion
	for rows.Next() {
		var item MappingVersion
		var created string
		if err := rows.Scan(&item.MappingID, &item.Version, &item.ContentHash, &item.Actor, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteMapping removes a contract and its version history, but only
// when the usage index shows no published workflow pinned it.
func (s *Store) DeleteMapping(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM mapping_usage WHERE mapping_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrMappingInUse
	}
	// Phase 1 P0-4: a profile bundle reference guards the mapping too —
	// deleting it would break every profile that composes it.
	if refs, err := tx.Query(`SELECT p.name FROM profile_bundle_refs r
		JOIN config_profiles p ON p.id = r.profile_id
		WHERE r.asset_kind = 'mapping' AND r.asset_id = ?`, id); err == nil {
		var referencing []string
		for refs.Next() {
			var name string
			if err := refs.Scan(&name); err == nil {
				referencing = append(referencing, strconv.Quote(name))
			}
		}
		refs.Close()
		if len(referencing) > 0 {
			return fmt.Errorf("%w: %s", ErrMappingInUse, strings.Join(referencing, ", "))
		}
	} else {
		return err
	}
	res, err := tx.Exec(`DELETE FROM mappings WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrMappingNotFound
	}
	if _, err := tx.Exec(`DELETE FROM mapping_versions WHERE mapping_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordMappingUsage pins a published workflow version to the contract
// version it resolved at publish time.
func (s *Store) RecordMappingUsage(schemaID string, schemaVersion int, workflowID string, workflowVersion int) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO mapping_usage
		(mapping_id, mapping_version, workflow_id, workflow_version, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		schemaID, schemaVersion, workflowID, workflowVersion, now())
	return err
}

func (s *Store) MappingUsage(schemaID string) ([]MappingUsage, error) {
	rows, err := s.read(`SELECT u.mapping_id, u.mapping_version, u.workflow_id, u.workflow_version, u.created_at, COALESCE(w.name, u.workflow_id)
		FROM mapping_usage u LEFT JOIN workflows w ON w.id = u.workflow_id
		WHERE u.mapping_id = ? ORDER BY u.created_at`, schemaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MappingUsage
	for rows.Next() {
		var item MappingUsage
		var created string
		if err := rows.Scan(&item.MappingID, &item.MappingVersion, &item.WorkflowID, &item.WorkflowVersion, &created, &item.WorkflowName); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// MappingUsageCount returns how many published workflow versions
// reference the mapping across all versions of it.
func (s *Store) MappingUsageCount(schemaID string) (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM mapping_usage WHERE mapping_id = ?`, schemaID).Scan(&count)
	return count, err
}

// ResolveMappingRefs inlines every data.map node's mappingRef into
// resolved rows on the node config (v2 plan D13, D4-style resolution: the
// published snapshot is self-contained, so the engine and replay need zero
// store awareness). The ref stays on the config for display; usage pins are
// returned for the publish path to record.
//
// v3 plan D4/D6: http.request nodes resolve their per-group mappingRefs
// plus the typed parameter rows the same way — mapping rows first, the
// node's own *Fields rows on top (the node wins per parameter), compiled
// into the plain body/query/headers/pathParams the engine already
// executes. A group is rewritten only when it has a mapping ref or a
// fields array; otherwise the raw object passes through untouched.
func (s *Store) ResolveMappingRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.ResolveMappingRefsWith(wf, NewLookupCache())
}

func (s *Store) ResolveMappingRefsWith(wf *graph.Workflow, cache *LookupCache) (map[string]int, error) {
	if cache == nil {
		cache = NewLookupCache()
	}
	refs := map[string]int{}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		switch n.Type {
		case "data.map":
			ref, _ := n.Config["mappingRef"].(string)
			if ref == "" {
				continue
			}
			mapping, err := cache.Mapping(s, ref)
			if err != nil {
				return nil, fmt.Errorf("node %q mapping: %w", n.ID, err)
			}
			refs[mapping.ID] = mapping.Version
			rows := make([]map[string]any, 0, len(mapping.Rows))
			for _, row := range mapping.Rows {
				rows = append(rows, map[string]any{"target": row.Target, "source": row.Source, "required": row.Required})
			}
			if n.Config == nil {
				n.Config = map[string]any{}
			}
			n.Config["mappingRows"] = rows
		case "http.request":
			groupRefs, _ := n.Config["mappingRefs"].(map[string]any)
			for _, g := range httpParamGroups {
				ref, _ := groupRefs[g.target].(string)
				fields, fieldsPresent := n.Config[g.fields]
				if ref == "" && !fieldsPresent {
					continue
				}
				compiled := map[string]any{}
				if ref != "" {
					mapping, err := cache.Mapping(s, ref)
					if err != nil {
						return nil, fmt.Errorf("node %q %s mapping: %w", n.ID, g.target, err)
					}
					if mapping.Kind != g.kind {
						return nil, fmt.Errorf("node %q: mapping %q has kind %s but the %s group needs a %s mapping",
							n.ID, ref, mapping.Kind, g.target, g.kind)
					}
					refs[mapping.ID] = mapping.Version
					overlay := make([]graph.OverlayRow, 0, len(mapping.Rows))
					for _, row := range mapping.Rows {
						overlay = append(overlay, graph.OverlayRow{Name: row.Target, Source: row.Source})
					}
					compiled = graph.CompileOverlayRows(g.target, overlay)
				}
				if fieldsPresent {
					rows, err := graph.ParseParamRows(fields)
					if err != nil {
						return nil, fmt.Errorf("node %q %s: %w", n.ID, g.fields, err)
					}
					for name, value := range graph.CompileParamRows(rows) {
						compiled[name] = value
					}
				}
				if n.Config == nil {
					n.Config = map[string]any{}
				}
				n.Config[g.target] = compiled
			}
		}
	}
	return refs, nil
}

// httpParamGroups maps the editor's parameter groups onto the config keys
// they compile into (v3 plan §5.2). The kind column gates which mappings
// may attach to which group.
var httpParamGroups = []struct {
	target string
	fields string
	kind   string
}{
	{"body", "bodyFields", MappingKindBody},
	{"query", "queryFields", MappingKindQuery},
	{"headers", "headerFields", MappingKindHeaders},
	{"pathParams", "pathFields", MappingKindPath},
}
