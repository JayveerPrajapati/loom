package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"loom/internal/graph"
	"loom/internal/pyenv"
)

// Script store (Profile-Centric UX Phase 4): a script is reusable,
// versioned source (javascript or python) referenced by code.run and
// code.python nodes and composed by profile bundles. The layout mirrors
// mappings/config_profiles deliberately — one proven machinery. The
// language lives on the entity row (for the language-filtered list the
// pickers use) and inside the version doc (so any version is
// self-contained).

var (
	ErrScriptNotFound = errors.New("script not found")
	ErrStaleScript    = errors.New("script was modified concurrently")
	ErrScriptInUse    = errors.New("script is referenced by published workflows")
)

// Script languages (Phase 4 D4-1): javascript joins code.run, python joins
// code.python. The test capability differs — JS runs in-process on any
// server, python needs the subprocess host.
const (
	ScriptLanguageJavaScript = "javascript"
	ScriptLanguagePython     = "python"
)

// ValidScriptLanguage reports whether lang names an allowed script
// language.
func ValidScriptLanguage(lang string) bool {
	switch lang {
	case ScriptLanguageJavaScript, ScriptLanguagePython:
		return true
	default:
		return false
	}
}

// CheckScriptLanguage validates the language once for both write paths
// and for API request validation.
func CheckScriptLanguage(lang string) error {
	if !ValidScriptLanguage(lang) {
		return fmt.Errorf("unknown script language %q (want javascript or python)", lang)
	}
	return nil
}

// Script is a reusable, versioned piece of source. TimeoutSeconds and
// MaxMemoryMB are runtime defaults (Phase 4 D4-4): they apply to a
// referencing node only when the node does not set its own.
type Script struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Language    string `json:"language"`
	Version     int    `json:"version"`
	Source      string `json:"source"`
	// Requirements is python-only: pinned entries like "idna==3.10".
	Requirements   []string  `json:"requirements,omitempty"`
	TimeoutSeconds int       `json:"timeoutSeconds,omitempty"`
	MaxMemoryMB    int       `json:"maxMemoryMB,omitempty"`
	Tags           []string  `json:"tags,omitempty"`
	Owner          string    `json:"owner,omitempty"`
	Visibility     string    `json:"visibility,omitempty"`
	CreatorID      string    `json:"creatorId,omitempty"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	// UsageCount is how many published workflow versions pin any version
	// of this script; populated only by ListScripts.
	UsageCount int `json:"usageCount,omitempty"`
}

// ScriptVersion is one immutable snapshot in a script's history.
type ScriptVersion struct {
	ScriptID    string    `json:"scriptId"`
	Version     int       `json:"version"`
	ContentHash string    `json:"contentHash"`
	Actor       string    `json:"actor"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ScriptUsage records a published workflow version pinned to a script
// version — the delete guard and usage-count source.
type ScriptUsage struct {
	ScriptID        string    `json:"scriptId"`
	ScriptVersion   int       `json:"scriptVersion"`
	WorkflowID      string    `json:"workflowId"`
	WorkflowVersion int       `json:"workflowVersion"`
	WorkflowName    string    `json:"workflowName,omitempty"`
	CreatedAt       time.Time `json:"createdAt"`
}

// scriptDoc is what a version row stores: everything that can change
// between versions, so any version is self-contained.
type scriptDoc struct {
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Language       string   `json:"language"`
	Source         string   `json:"source"`
	Requirements   []string `json:"requirements,omitempty"`
	TimeoutSeconds int      `json:"timeoutSeconds,omitempty"`
	MaxMemoryMB    int      `json:"maxMemoryMB,omitempty"`
	Tags           []string `json:"tags,omitempty"`
	Owner          string   `json:"owner,omitempty"`
}

// CreateScript stores version 1 of a new script.
func (s *Store) CreateScript(d *Script, actor string) (Script, error) {
	if err := CheckScriptLanguage(d.Language); err != nil {
		return Script{}, err
	}
	if err := CheckScriptRequirements(d.Language, d.Requirements); err != nil {
		return Script{}, err
	}
	if d.Requirements == nil {
		d.Requirements = []string{}
	}
	d.Visibility = normalizeVisibility(d.Visibility)
	at := time.Now().UTC()
	doc := scriptDoc{Name: d.Name, Description: d.Description, Language: d.Language, Source: d.Source,
		Requirements: d.Requirements, TimeoutSeconds: d.TimeoutSeconds, MaxMemoryMB: d.MaxMemoryMB,
		Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return Script{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Script{}, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO scripts (id, name, language, description, current_version, visibility, creator_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?)`,
		d.ID, d.Name, d.Language, d.Description, d.Visibility, d.CreatorID, at.Format(time.RFC3339Nano), at.Format(time.RFC3339Nano)); err != nil {
		return Script{}, err
	}
	if _, err := tx.Exec(`INSERT INTO script_versions (script_id, version, doc, content_hash, actor, created_at)
		VALUES (?, 1, ?, ?, ?, ?)`,
		d.ID, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return Script{}, err
	}
	if err := tx.Commit(); err != nil {
		return Script{}, err
	}
	d.Version = 1
	d.CreatedAt = at
	d.UpdatedAt = at
	return *d, nil
}

// UpdateScript appends the next version. expectedVersion is the caller's
// current version: a mismatch means someone else wrote first and the
// update is rejected with ErrStaleScript. The language is immutable — a
// javascript script cannot silently become a python one under the same
// id, because node refs and bundles pin by language.
func (s *Store) UpdateScript(d *Script, expectedVersion int, actor string) (Script, error) {
	if err := CheckScriptLanguage(d.Language); err != nil {
		return Script{}, err
	}
	if err := CheckScriptRequirements(d.Language, d.Requirements); err != nil {
		return Script{}, err
	}
	if d.Requirements == nil {
		d.Requirements = []string{}
	}
	at := time.Now().UTC()
	doc := scriptDoc{Name: d.Name, Description: d.Description, Language: d.Language, Source: d.Source,
		Requirements: d.Requirements, TimeoutSeconds: d.TimeoutSeconds, MaxMemoryMB: d.MaxMemoryMB,
		Tags: d.Tags, Owner: d.Owner}
	data, err := json.Marshal(doc)
	if err != nil {
		return Script{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Script{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE scripts
		SET name = ?, description = ?, current_version = current_version + 1, updated_at = ?
		WHERE id = ? AND current_version = ?`,
		d.Name, d.Description, at.Format(time.RFC3339Nano), d.ID, expectedVersion)
	if err != nil {
		return Script{}, err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		var exists int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM scripts WHERE id = ?`, d.ID).Scan(&exists); err != nil {
			return Script{}, err
		}
		if exists == 0 {
			return Script{}, ErrScriptNotFound
		}
		return Script{}, ErrStaleScript
	}
	var language string
	if err := tx.QueryRow(`SELECT language FROM scripts WHERE id = ?`, d.ID).Scan(&language); err != nil {
		return Script{}, err
	}
	if language != d.Language {
		return Script{}, fmt.Errorf("script language is immutable (%s cannot become %s): create a new script instead", language, d.Language)
	}
	if err := tx.QueryRow(`SELECT current_version FROM scripts WHERE id = ?`, d.ID).Scan(&d.Version); err != nil {
		return Script{}, err
	}
	if _, err := tx.Exec(`INSERT INTO script_versions (script_id, version, doc, content_hash, actor, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Version, data, contentHash(data), actor, at.Format(time.RFC3339Nano)); err != nil {
		return Script{}, err
	}
	if err := tx.Commit(); err != nil {
		return Script{}, err
	}
	d.UpdatedAt = at
	return *d, nil
}

// CheckScriptRequirements enforces the same pinned-entry rule as the
// code.python node (Phase 4 D4-3: scripts join the publish-time
// environment build, so unpinned names are rejected at save time).
func CheckScriptRequirements(language string, requirements []string) error {
	if language != ScriptLanguagePython {
		if len(requirements) > 0 {
			return fmt.Errorf("requirements are a python-script field; %s scripts carry none", language)
		}
		return nil
	}
	for _, raw := range requirements {
		req := strings.TrimSpace(raw)
		if !pyenv.ValidRequirement(req) {
			return fmt.Errorf("requirement %q must be pinned as name==version", raw)
		}
	}
	return nil
}

func (s *Store) GetScript(id string) (*Script, error) {
	var out Script
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.description, p.language, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc
		FROM scripts p
		JOIN script_versions v ON v.script_id = p.id AND v.version = p.current_version
		WHERE p.id = ?`, id).Scan(&out.ID, &out.Name, &out.Description, &out.Language, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScriptNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanScriptFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func scanScriptFinish(out *Script, created, updated string, doc []byte) error {
	var err error
	if out.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return err
	}
	if out.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return err
	}
	var d scriptDoc
	if err := json.Unmarshal(doc, &d); err != nil {
		return err
	}
	// The doc is the source of truth: for the current version it matches
	// the parent row, and for historical reconstruction it restores the
	// fields as they were at that version.
	out.Name = d.Name
	out.Description = d.Description
	out.Language = d.Language
	out.Source = d.Source
	out.Requirements = d.Requirements
	out.TimeoutSeconds = d.TimeoutSeconds
	out.MaxMemoryMB = d.MaxMemoryMB
	out.Tags = d.Tags
	out.Owner = d.Owner
	out.Visibility = normalizeVisibility(out.Visibility)
	return nil
}

// ListScripts returns every script with its current doc plus its usage
// count from the publish-time usage index.
func (s *Store) ListScripts() ([]Script, error) {
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.language, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM script_usage u WHERE u.script_id = p.id)
		FROM scripts p
		JOIN script_versions v ON v.script_id = p.id AND v.version = p.current_version
		ORDER BY p.name, p.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Script
	usage := map[string]int{}
	for rows.Next() {
		var item Script
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Language, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanScriptFinish(&item, created, updated, doc); err != nil {
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

// ListScriptsByLanguage returns every script in one language, with its
// current doc plus usage count. The node-form picker (javascript on
// code.run, python on code.python) and the profile bundle picker filter
// through here.
func (s *Store) ListScriptsByLanguage(language string) ([]Script, error) {
	if !ValidScriptLanguage(language) {
		return nil, fmt.Errorf("unknown script language %q", language)
	}
	rows, err := s.read(`SELECT p.id, p.name, p.description, p.language, p.current_version, p.visibility, p.creator_id, p.created_at, p.updated_at, v.doc,
		(SELECT COUNT(*) FROM script_usage u WHERE u.script_id = p.id)
		FROM scripts p
		JOIN script_versions v ON v.script_id = p.id AND v.version = p.current_version
		WHERE p.language = ?
		ORDER BY p.name, p.id`, language)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Script
	usage := map[string]int{}
	for rows.Next() {
		var item Script
		var created, updated string
		var doc []byte
		var count int
		if err := rows.Scan(&item.ID, &item.Name, &item.Description, &item.Language, &item.Version,
			&item.Visibility, &item.CreatorID, &created, &updated, &doc, &count); err != nil {
			return nil, err
		}
		if err := scanScriptFinish(&item, created, updated, doc); err != nil {
			return nil, err
		}
		usage[item.ID] = count
		out = append(out, item)
	}
	for i := range out {
		out[i].UsageCount = usage[out[i].ID]
	}
	if out == nil {
		out = []Script{}
	}
	return out, rows.Err()
}

// GetScriptVersion reconstructs the script as of one version.
func (s *Store) GetScriptVersion(id string, version int) (*Script, error) {
	var out Script
	var created, updated string
	var doc []byte
	err := s.readRow(`SELECT p.id, p.name, p.description, p.language, v.version, p.visibility, p.creator_id, p.created_at, p.created_at, v.doc
		FROM scripts p
		JOIN script_versions v ON v.script_id = p.id
		WHERE p.id = ? AND v.version = ?`, id, version).
		Scan(&out.ID, &out.Name, &out.Description, &out.Language, &out.Version, &out.Visibility, &out.CreatorID, &created, &updated, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScriptNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := scanScriptFinish(&out, created, updated, doc); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *Store) ListScriptVersions(id string) ([]ScriptVersion, error) {
	rows, err := s.read(`SELECT script_id, version, content_hash, actor, created_at
		FROM script_versions WHERE script_id = ? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScriptVersion
	for rows.Next() {
		var item ScriptVersion
		var created string
		if err := rows.Scan(&item.ScriptID, &item.Version, &item.ContentHash, &item.Actor, &created); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeleteScript removes a script and its version history, but only when
// the usage index shows no published workflow pinned it and no profile
// bundle composes it.
func (s *Store) DeleteScript(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM script_usage WHERE script_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrScriptInUse
	}
	// Profile bundles reference scripts the same way they reference
	// mappings and schemas (Phase 4 D4-1): deleting one would break
	// every profile that composes it.
	if refs, err := tx.Query(`SELECT p.name FROM profile_bundle_refs r
		JOIN config_profiles p ON p.id = r.profile_id
		WHERE r.asset_kind = 'script' AND r.asset_id = ?`, id); err == nil {
		var referencing []string
		for refs.Next() {
			var name string
			if err := refs.Scan(&name); err == nil {
				referencing = append(referencing, fmt.Sprintf("%q", name))
			}
		}
		refs.Close()
		if len(referencing) > 0 {
			return fmt.Errorf("%w (profiles: %s)", ErrScriptInUse, strings.Join(referencing, ", "))
		}
	} else {
		return err
	}
	res, err := tx.Exec(`DELETE FROM scripts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if affected, _ := res.RowsAffected(); affected == 0 {
		return ErrScriptNotFound
	}
	if _, err := tx.Exec(`DELETE FROM script_versions WHERE script_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordScriptUsage pins a published workflow version to the script
// version it resolved at publish time.
func (s *Store) RecordScriptUsage(scriptID string, scriptVersion int, workflowID string, workflowVersion int) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO script_usage
		(script_id, script_version, workflow_id, workflow_version, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		scriptID, scriptVersion, workflowID, workflowVersion, now())
	return err
}

func (s *Store) ScriptUsage(scriptID string) ([]ScriptUsage, error) {
	rows, err := s.read(`SELECT u.script_id, u.script_version, u.workflow_id, u.workflow_version, u.created_at, COALESCE(w.name, u.workflow_id)
		FROM script_usage u LEFT JOIN workflows w ON w.id = u.workflow_id
		WHERE u.script_id = ? ORDER BY u.created_at`, scriptID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ScriptUsage
	for rows.Next() {
		var item ScriptUsage
		var created string
		if err := rows.Scan(&item.ScriptID, &item.ScriptVersion, &item.WorkflowID, &item.WorkflowVersion, &created, &item.WorkflowName); err != nil {
			return nil, err
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		out = append(out, item)
	}
	return out, rows.Err()
}

// ScriptUsageCount returns how many published workflow versions
// reference the script across all versions of it.
func (s *Store) ScriptUsageCount(scriptID string) (int, error) {
	var count int
	err := s.readRow(`SELECT COUNT(*) FROM script_usage WHERE script_id = ?`, scriptID).Scan(&count)
	return count, err
}

// pinnedScriptVersion returns the script version this workflow last
// published against (0 when never) — the upgrade path's baseline for
// telling authored source apart from publish-inlined source.
func (s *Store) pinnedScriptVersion(workflowID, scriptID string) int {
	var version int
	_ = s.readRow(`SELECT script_version FROM script_usage WHERE workflow_id = ? AND script_id = ?
		ORDER BY created_at DESC LIMIT 1`, workflowID, scriptID).Scan(&version)
	return version
}

// ResolveScriptRefs inlines every code node's scriptRef into resolved
// source on the node config (Phase 4 D4-1: the published snapshot is
// self-contained, so the engine and replay need zero script awareness —
// exactly like mapping rows). Precedence: a node-local source that
// DIVERGES from the referenced script (the advanced escape hatch) wins
// and the ref is skipped; a node scriptRef wins over the profile
// bundle's script (injectBundleRefs only fills an empty slot). A source
// that matches one of the script's own versions is publish-inlined
// (memory #1385: published docs read node-origin), so the script
// follows forward on the next publish; the ref stays on the config for
// display; usage pins are returned for the publish path to record.
//
// Runtime defaults (D4-4) apply only where the node sets none of its
// own: script timeoutSeconds/maxMemoryMB fill absent keys. Requirements
// are a union (D4-3): the node's own pinned entries plus the script's,
// deduped, node order first — pythonRequirements at publish then builds
// one merged environment.
func (s *Store) ResolveScriptRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.ResolveScriptRefsWith(wf, NewLookupCache())
}

func (s *Store) ResolveScriptRefsWith(wf *graph.Workflow, cache *LookupCache) (map[string]int, error) {
	if cache == nil {
		cache = NewLookupCache()
	}
	refs := map[string]int{}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		language := scriptRefLanguage(n.Type)
		if language == "" {
			continue
		}
		ref, _ := n.Config["scriptRef"].(string)
		if ref == "" {
			continue
		}
		source := nodeSource(n.Config["source"])
		if source != "" && !cache.SourceMatchesScriptVersion(s, ref, source) {
			// Authored escape hatch: the node's own source wins.
			continue
		}
		script, err := cache.Script(s, ref)
		if err != nil {
			return nil, fmt.Errorf("node %q script: %w", n.ID, err)
		}
		if script.Language != language {
			return nil, fmt.Errorf("node %q: script %q is %s but %s needs a %s script",
				n.ID, ref, script.Language, n.Type, language)
		}
		refs[script.ID] = script.Version
		if n.Config == nil {
			n.Config = map[string]any{}
		}
		n.Config["source"] = script.Source
		n.SetConfigSource("source", graph.ConfigSourceScript)
		if _, ok := n.Config["timeoutSeconds"]; !ok && script.TimeoutSeconds > 0 {
			n.Config["timeoutSeconds"] = script.TimeoutSeconds
			n.SetConfigSource("timeoutSeconds", graph.ConfigSourceScript)
		}
		if language == ScriptLanguagePython {
			if _, ok := n.Config["maxMemoryMB"]; !ok && script.MaxMemoryMB > 0 {
				n.Config["maxMemoryMB"] = script.MaxMemoryMB
				n.SetConfigSource("maxMemoryMB", graph.ConfigSourceScript)
			}
			if len(script.Requirements) > 0 {
				n.Config["requirements"] = unionRequirements(n.Config["requirements"], script.Requirements)
				n.SetConfigSource("requirements", graph.ConfigSourceScript)
			}
		}
	}
	return refs, nil
}

// sourceMatchesScriptVersion reports whether source is (or was) the
// content of one of the script's versions — i.e. publish-inlined rather
// than authored. Store errors read as no match: the conservative path
// leaves the node's source alone.
func (s *Store) sourceMatchesScriptVersion(id, source string) bool {
	versions, err := s.ListScriptVersions(id)
	if err != nil {
		return false
	}
	for _, v := range versions {
		if sv, err := s.GetScriptVersion(id, v.Version); err == nil && sv.Source == source {
			return true
		}
	}
	return false
}

// nodeSource reads a config value as trimmed text; absent values are
// empty.
func nodeSource(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// unionRequirements merges the node's requirements (JSON-decoded as
// []any) with the script's ([]string), deduped, node entries first.
func unionRequirements(nodeValue any, scriptReqs []string) []any {
	seen := map[string]bool{}
	out := []any{}
	add := func(req string) {
		req = strings.TrimSpace(req)
		if req == "" || seen[req] {
			return
		}
		seen[req] = true
		out = append(out, req)
	}
	if rows, ok := nodeValue.([]any); ok {
		for _, raw := range rows {
			add(fmt.Sprint(raw))
		}
	} else if rows, ok := nodeValue.([]string); ok {
		for _, raw := range rows {
			add(raw)
		}
	}
	for _, req := range scriptReqs {
		add(req)
	}
	return out
}
