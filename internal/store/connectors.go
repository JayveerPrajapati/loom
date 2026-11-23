package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"loom/internal/graph"
	"loom/internal/nodes"
)

// Connector store (operational-trust Campaign 4, D10-1): a connector is
// a versioned DATA asset that declares node types over http.request's
// trusted pipeline — field schemas plus a request template. Nothing new
// is executable; execution delegates to httpNode so SSRF, egress,
// per-credential allowedHosts, and cassette replay all apply by
// construction. The layout mirrors mappings/scripts deliberately — one
// proven machinery.

var (
	ErrConnectorNotFound = errors.New("connector not found")
	ErrStaleConnector    = errors.New("connector was modified concurrently")
	ErrConnectorInUse    = errors.New("connector is referenced by published workflows")
)

const (
	// ConnectorTypePrefix aliases the nodes-package constant (the
	// authoritative spelling — graph validation uses it too).
	ConnectorTypePrefix = nodes.ConnectorTypePrefix
	// maxConnectorOperations caps the op set per version.
	maxConnectorOperations = 20
	// maxConnectorTemplateBytes caps each template string.
	maxConnectorTemplateBytes = 64 << 10
)

var connectorOpPattern = regexp.MustCompile(`^[a-z][a-z0-9]{0,39}$`)

// ConnectorOperation is one node type: author-facing fields plus the
// request template rendered at execution. Template strings may use
// {{config.<field>}} and {{cred.<field>}} placeholders — resolved by
// connectorNode before the config reaches httpNode, which then applies
// its own item-expression rendering to whatever remains.
type ConnectorOperation struct {
	Op           string            `json:"op"`
	Label        string            `json:"label,omitempty"`
	Description  string            `json:"description,omitempty"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Query        map[string]string `json:"query,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	SuccessCodes string            `json:"successCodes,omitempty"`
	ResponseMode string            `json:"responseMode,omitempty"`
	// Fields are the author-facing config fields (catalog-shaped).
	Fields map[string]nodes.Field `json:"fields,omitempty"`
}

// Connector is a reusable, versioned set of operations.
type Connector struct {
	ID          string               `json:"id"`
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Version     int                  `json:"version"`
	Operations  []ConnectorOperation `json:"operations"`
	Tags        []string             `json:"tags,omitempty"`
	Owner       string               `json:"owner,omitempty"`
	Visibility  string               `json:"visibility,omitempty"`
	CreatorID   string               `json:"creatorId,omitempty"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
	// UsageCount is how many published workflow versions pin any version
	// of this connector; populated only by ListConnectors.
	UsageCount int `json:"usageCount,omitempty"`
}

// connectorDoc is the append-only version document.
type connectorDoc struct {
	Name        string               `json:"name"`
	Description string               `json:"description,omitempty"`
	Operations  []ConnectorOperation `json:"operations"`
	Tags        []string             `json:"tags,omitempty"`
	Owner       string               `json:"owner,omitempty"`
}

// ConnectorVersion is one immutable snapshot in a connector's history.
type ConnectorVersion struct {
	ConnectorID string    `json:"connectorId"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ConnectorUsage records a published workflow version pinned to a
// connector version — the delete guard and usage-count source.
type ConnectorUsage struct {
	ConnectorID      string    `json:"connectorId"`
	ConnectorVersion int       `json:"connectorVersion"`
	WorkflowID       string    `json:"workflowId"`
	WorkflowVersion  int       `json:"workflowVersion"`
	WorkflowName     string    `json:"workflowName,omitempty"`
	CreatedAt        time.Time `json:"createdAt"`
}

// CheckConnector validates a connector document once for both write
// paths: unique op names, bounded template sizes, valid methods, and
// catalog-rule field shapes (Secret fields are never Profileable — the
// same pairing the built-in freeze test enforces).
func CheckConnector(d *Connector) error {
	d.Name = strings.TrimSpace(d.Name)
	if d.Name == "" {
		return fmt.Errorf("connector name is required")
	}
	if len(d.Operations) == 0 {
		return fmt.Errorf("a connector needs at least one operation")
	}
	if len(d.Operations) > maxConnectorOperations {
		return fmt.Errorf("a connector may declare at most %d operations (have %d)", maxConnectorOperations, len(d.Operations))
	}
	seen := map[string]bool{}
	for _, op := range d.Operations {
		if !connectorOpPattern.MatchString(op.Op) {
			return fmt.Errorf("operation name %q must be lowercase alphanumerics starting with a letter", op.Op)
		}
		if seen[op.Op] {
			return fmt.Errorf("duplicate operation %q", op.Op)
		}
		seen[op.Op] = true
		switch strings.ToUpper(strings.TrimSpace(op.Method)) {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
		default:
			return fmt.Errorf("operation %q: method %q is not an HTTP method", op.Op, op.Method)
		}
		if strings.TrimSpace(op.URL) == "" {
			return fmt.Errorf("operation %q: url template is required", op.Op)
		}
		for name, size := range map[string]int{
			"url": len(op.URL), "body": len(op.Body),
		} {
			if size > maxConnectorTemplateBytes {
				return fmt.Errorf("operation %q: %s template exceeds %d bytes", op.Op, name, maxConnectorTemplateBytes)
			}
		}
		for key, field := range op.Fields {
			if !field.Type.Valid() {
				return fmt.Errorf("operation %q field %q: unknown type %q", op.Op, key, field.Type)
			}
			if field.Secret && field.Profileable {
				return fmt.Errorf("operation %q field %q: secret fields are never profileable", op.Op, key)
			}
		}
	}
	return nil
}

// CreateConnector stores version 1.
func (s *Store) CreateConnector(d *Connector, actor string) (Connector, error) {
	if err := CheckConnector(d); err != nil {
		return Connector{}, err
	}
	d.Visibility = normalizeVisibility(d.Visibility)
	at := time.Now().UTC()
	doc := connectorDoc{Name: d.Name, Description: d.Description, Operations: d.Operations, Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return Connector{}, err
	}
	tags, err := json.Marshal(d.Tags)
	if err != nil {
		return Connector{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Connector{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO connectors (id, name, description, current_version, tags, owner, visibility, creator_id, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Description, string(tags), d.Owner, d.Visibility, d.CreatorID, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		return Connector{}, err
	}
	if _, err := tx.Exec(`INSERT INTO connector_versions (connector_id, version, doc, content_hash, actor, created_at)
		VALUES (?, 1, ?, ?, ?, ?)`,
		d.ID, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return Connector{}, err
	}
	if err := tx.Commit(); err != nil {
		return Connector{}, err
	}
	d.Version = 1
	d.CreatedAt = at
	d.UpdatedAt = at
	return *d, nil
}

// UpdateConnector appends the next version; expectedVersion guards
// concurrent writes with ErrStaleConnector.
func (s *Store) UpdateConnector(d *Connector, expectedVersion int, actor string) (Connector, error) {
	if err := CheckConnector(d); err != nil {
		return Connector{}, err
	}
	d.Visibility = normalizeVisibility(d.Visibility)
	at := time.Now().UTC()
	doc := connectorDoc{Name: d.Name, Description: d.Description, Operations: d.Operations, Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return Connector{}, err
	}
	tags, err := json.Marshal(d.Tags)
	if err != nil {
		return Connector{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Connector{}, err
	}
	defer tx.Rollback()
	var current int
	if err := tx.QueryRow(`SELECT current_version FROM connectors WHERE id = ?`, d.ID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Connector{}, ErrConnectorNotFound
		}
		return Connector{}, err
	}
	if current != expectedVersion {
		return Connector{}, ErrStaleConnector
	}
	if _, err := tx.Exec(`UPDATE connectors SET name = ?, description = ?, tags = ?, owner = ?, current_version = current_version + 1, visibility = ?, updated_at = ? WHERE id = ?`,
		d.Name, d.Description, string(tags), d.Owner, d.Visibility, at.Format(time.RFC3339Nano), d.ID); err != nil {
		return Connector{}, err
	}
	if _, err := tx.Exec(`INSERT INTO connector_versions (connector_id, version, doc, content_hash, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, current+1, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return Connector{}, err
	}
	if err := tx.Commit(); err != nil {
		return Connector{}, err
	}
	d.Version = current + 1
	d.UpdatedAt = at
	return *d, nil
}

func scanConnectorRow(row interface{ Scan(...any) error }) (*Connector, error) {
	var d Connector
	var tags string
	var created, updated string
	if err := row.Scan(&d.ID, &d.Name, &d.Description, &d.Version, &tags, &d.Owner, &d.Visibility, &d.CreatorID, &created, &updated); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(tags), &d.Tags)
	d.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	d.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return &d, nil
}

const connectorColumns = `id, name, description, current_version, tags, owner, visibility, creator_id, created_at, updated_at`

// GetConnector returns the entity with the CURRENT version's
// operations.
func (s *Store) GetConnector(id string) (*Connector, error) {
	row := s.readRow(`SELECT `+connectorColumns+` FROM connectors WHERE id = ?`, id)
	d, err := scanConnectorRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConnectorNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Operations = s.connectorOperations(d.ID, d.Version)
	return d, nil
}

// GetConnectorVersion returns the entity with a specific version's
// operations.
func (s *Store) GetConnectorVersion(id string, version int) (*Connector, error) {
	row := s.readRow(`SELECT `+connectorColumns+` FROM connectors WHERE id = ?`, id)
	d, err := scanConnectorRow(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrConnectorNotFound
	}
	if err != nil {
		return nil, err
	}
	d.Version = version
	d.Operations = s.connectorOperations(id, version)
	if len(d.Operations) == 0 {
		return nil, fmt.Errorf("%w: version %d", ErrConnectorNotFound, version)
	}
	return d, nil
}

// connectorOperations loads one version's operation set.
func (s *Store) connectorOperations(id string, version int) []ConnectorOperation {
	var doc []byte
	if err := s.readRow(`SELECT doc FROM connector_versions WHERE connector_id = ? AND version = ?`, id, version).Scan(&doc); err != nil {
		return nil
	}
	var parsed connectorDoc
	if json.Unmarshal(doc, &parsed) != nil {
		return nil
	}
	return parsed.Operations
}

// ListConnectors returns every connector with usage counts.
func (s *Store) ListConnectors() ([]Connector, error) {
	rows, err := s.read(`SELECT ` + connectorColumns + ` FROM connectors ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Connector
	for rows.Next() {
		d, err := scanConnectorRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Operations = s.connectorOperations(out[i].ID, out[i].Version)
		count, err := s.ConnectorUsageCount(out[i].ID)
		if err == nil {
			out[i].UsageCount = count
		}
	}
	return out, nil
}

// ListConnectorVersions returns the version history.
func (s *Store) ListConnectorVersions(id string) ([]ConnectorVersion, error) {
	rows, err := s.read(`SELECT version, content_hash, actor, created_at FROM connector_versions WHERE connector_id = ? ORDER BY version`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectorVersion
	for rows.Next() {
		var v ConnectorVersion
		var created string
		if err := rows.Scan(&v.Version, &v.ContentHash, &v.Actor, &created); err != nil {
			return nil, err
		}
		v.ConnectorID = id
		v.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, v)
	}
	return out, rows.Err()
}

// DeleteConnector refuses while any published workflow pins it.
func (s *Store) DeleteConnector(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM connector_usage WHERE connector_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrConnectorInUse
	}
	res, err := tx.Exec(`DELETE FROM connectors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrConnectorNotFound
	}
	if _, err := tx.Exec(`DELETE FROM connector_versions WHERE connector_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordConnectorUsage pins a published workflow version to a connector
// version.
func (s *Store) RecordConnectorUsage(connectorID string, connectorVersion int, workflowID string, workflowVersion int) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO connector_usage (connector_id, connector_version, workflow_id, workflow_version, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		connectorID, connectorVersion, workflowID, workflowVersion, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

// ConnectorUsage lists the published workflow versions pinning the
// connector, newest first.
func (s *Store) ConnectorUsage(connectorID string) ([]ConnectorUsage, error) {
	rows, err := s.read(`
		SELECT u.connector_version, u.workflow_id, u.workflow_version, u.created_at,
			COALESCE((SELECT name FROM workflows w WHERE w.id = u.workflow_id), u.workflow_id)
		FROM connector_usage u WHERE u.connector_id = ?
		ORDER BY u.created_at DESC, u.workflow_id`, connectorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectorUsage
	for rows.Next() {
		var u ConnectorUsage
		var created string
		if err := rows.Scan(&u.ConnectorVersion, &u.WorkflowID, &u.WorkflowVersion, &created, &u.WorkflowName); err != nil {
			return nil, err
		}
		u.ConnectorID = connectorID
		u.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, u)
	}
	return out, rows.Err()
}

// ConnectorUsageCount counts distinct pinned workflow versions.
func (s *Store) ConnectorUsageCount(connectorID string) (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM connector_usage WHERE connector_id = ?`, connectorID).Scan(&count)
	return count, err
}

// ConnectorNodeTypes returns catalog-shaped Definitions for every
// operation of the CURRENT version of one connector — the listing the
// server merges into /api/node-types.
func ConnectorNodeTypes(c *Connector) []nodes.Definition {
	out := make([]nodes.Definition, 0, len(c.Operations))
	for _, op := range c.Operations {
		label := op.Label
		if label == "" {
			label = op.Op
		}
		def := nodes.Definition{
			Type:        ConnectorTypePrefix + c.ID + "." + op.Op,
			TypeVersion: 1,
			Category:    "connector",
			DisplayName: c.Name + " · " + label,
			Description: op.Description,
			Inputs:      []string{"main"},
			Outputs:     []string{"main"},
			Config:      op.Fields,
			// The credential slot mirrors http.request's own: offered,
			// never forced — public APIs need no credential, and
			// {{cred.*}} placeholders render empty without one.
			Credentials:         []string{"http"},
			OptionalCredentials: []string{"http"},
			Effects:             "http: outbound HTTP via the connector's template (SSRF guard, egress policy, and cassette replay all apply)",
		}
		if def.Description == "" {
			def.Description = "Connector operation " + op.Op + " from " + c.Name
		}
		out = append(out, def)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// connectorRefInConfig reads the pinned connector identity from a
// connector.* node's config (written at publish).
func connectorRefInConfig(config map[string]any) (id string, version int, ok bool) {
	if config == nil {
		return "", 0, false
	}
	ref, _ := config["_connector"].(map[string]any)
	if ref == nil {
		return "", 0, false
	}
	id, _ = ref["id"].(string)
	// Numbers survive JSON round-trips as float64 but in-memory writes
	// as int; accept both so a pin never silently reads as zero.
	versionFloat, _ := ref["version"].(float64)
	if versionFloat == 0 {
		versionInt, _ := ref["version"].(int)
		versionFloat = float64(versionInt)
	}
	return id, int(versionFloat), id != "" && versionFloat > 0
}

// ResolveConnectorRefs inlines the pinned request template into every
// connector.* node (D10-4): config["request"] carries method/url/query/
// headers/body rendered from the pinned version so replay is
// self-contained, and usage pins are recorded by the caller.
func (s *Store) ResolveConnectorRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.ResolveConnectorRefsWith(wf, NewLookupCache())
}

func (s *Store) ResolveConnectorRefsWith(wf *graph.Workflow, cache *LookupCache) (map[string]int, error) {
	if cache == nil {
		cache = NewLookupCache()
	}
	refs := map[string]int{}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if !strings.HasPrefix(n.Type, ConnectorTypePrefix) {
			continue
		}
		id, version, ok := connectorRefInConfig(n.Config)
		if !ok {
			// Draft nodes carry the authored form: type names the
			// connector id + op; resolve to the current version.
			rest := strings.TrimPrefix(n.Type, ConnectorTypePrefix)
			parts := strings.SplitN(rest, ".", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("node %q: connector node type %q must be connector.<id>.<op>", n.ID, n.Type)
			}
			id = parts[0]
			c, err := cache.Connector(s, id)
			if err != nil {
				return nil, fmt.Errorf("node %q connector: %w", n.ID, err)
			}
			version = c.Version
		}
		c, err := cache.ConnectorVersion(s, id, version)
		if err != nil {
			return nil, fmt.Errorf("node %q connector: %w", n.ID, err)
		}
		opName := strings.TrimPrefix(n.Type, ConnectorTypePrefix+c.ID+".")
		if !strings.HasPrefix(n.Type, ConnectorTypePrefix+id+".") {
			return nil, fmt.Errorf("node %q: connector type %q does not match pinned connector %q", n.ID, n.Type, id)
		}
		var op *ConnectorOperation
		for j := range c.Operations {
			if c.Operations[j].Op == opName {
				op = &c.Operations[j]
				break
			}
		}
		if op == nil {
			return nil, fmt.Errorf("node %q: connector %q has no operation %q", n.ID, id, opName)
		}
		refs[id] = version
		if n.Config == nil {
			n.Config = map[string]any{}
		}
		n.Config["_connector"] = map[string]any{"id": id, "version": version}
		n.Config["request"] = map[string]any{
			"method":       strings.ToUpper(strings.TrimSpace(op.Method)),
			"url":          op.URL,
			"query":        op.Query,
			"headers":      op.Headers,
			"body":         op.Body,
			"successCodes": op.SuccessCodes,
			"responseMode": op.ResponseMode,
		}
	}
	return refs, nil
}
