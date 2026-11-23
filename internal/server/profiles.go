package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"loom/internal/expr"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/pyenv"
	"loom/internal/store"
)

// Configuration Hub API (plan §12, §18): reusable, versioned, non-secret
// node configuration profiles. Viewers read (they need profiles to
// understand workflows), editors manage, admins delete (decision D7).

type profileRequest struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	NodeType       string             `json:"nodeType"`
	Description    string             `json:"description"`
	Config         map[string]any     `json:"config"`
	CredentialRefs map[string]string  `json:"credentialRefs"`
	Retry          *graph.RetryPolicy `json:"retry"`
	Tags           []string           `json:"tags"`
	Owner          string             `json:"owner"`
	// Bundle composes reusable mappings and schemas (Phase 1 P0-1). It
	// deliberately bypasses the config allowlist: references are
	// validated as asset ids against the catalog's mapping kinds and the
	// input/output schema slots, never as node configuration.
	Bundle *store.ProfileBundle `json:"bundle"`
	// TestInput is the saved test fixture (Phase 5): a JSON sample item,
	// size-capped, validated as JSON, stored on the version doc.
	TestInput json.RawMessage `json:"testInput"`
	// Visibility is private by default for new assets (D-P0c); on update
	// a change is applied without forking a version (D-P0b).
	Visibility string `json:"visibility"`
	// Version is the caller's current version; a stale value is a 409
	// (decision D3).
	Version int `json:"version"`
}

func (s *api) auditProfile(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

// validateProfileBody enforces the catalog allowlist and credential
// reference integrity for create and update alike.
func (s *api) validateProfileBody(r *http.Request, req *profileRequest) (*nodes.Definition, error) {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return nil, errors.New("name is required")
	}
	req.NodeType = strings.TrimSpace(req.NodeType)
	def, ok := nodes.Get(req.NodeType)
	if !ok {
		return nil, errors.New("unknown node type " + req.NodeType)
	}
	hasProfileable := false
	for _, field := range def.Config {
		if field.Profileable {
			hasProfileable = true
			break
		}
	}
	// A bundle counts as profile content (Phase 1 P0-1): a data.map
	// profile is exactly one item mapping and no config fields.
	if !hasProfileable && req.Bundle == nil {
		return nil, errors.New(def.Type + " has no profileable config fields")
	}
	// Configuration Hub v4 plan D5-B: a profile must carry at least one
	// profileable field (or a retry policy, or a bundle) — an empty
	// profile does nothing, and the dedicated form surfaces the same rule
	// client-side.
	if len(req.Config) == 0 && req.Retry == nil && req.Bundle == nil {
		return nil, errors.New("profile config must set at least one field (or a retry policy)")
	}
	if err := nodes.ValidateProfileConfig(def, req.Config); err != nil {
		return nil, err
	}
	// Phase 5: the test fixture is optional JSON, size-capped so a
	// fixture cannot become a document store.
	trimmed := bytes.TrimSpace(req.TestInput)
	if len(trimmed) == 0 {
		req.TestInput = nil
	} else {
		if len(trimmed) > 8192 {
			return nil, errors.New("testInput must be at most 8 KiB of JSON")
		}
		if !json.Valid(trimmed) {
			return nil, errors.New("testInput must be valid JSON")
		}
		req.TestInput = trimmed
	}
	// v4.1 E4: parameter-group row arrays in profiles are validated with
	// the same parser the node editor uses, so malformed rows never
	// publish.
	if def.Type == "http.request" {
		for _, key := range []string{"bodyFields", "queryFields", "headerFields", "pathFields"} {
			raw, ok := req.Config[key]
			if !ok {
				continue
			}
			if _, err := graph.ParseParamRows(raw); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
		}
	}
	if len(req.CredentialRefs) > 0 {
		slots := nodes.ProfileableCredentialSlots(def)
		for slot, credID := range req.CredentialRefs {
			if !slices.Contains(slots, slot) {
				return nil, errors.New(def.Type + " has no credential slot " + slot)
			}
			if strings.TrimSpace(credID) == "" {
				return nil, errors.New("credential reference for " + slot + " is empty")
			}
			if _, _, err := s.deps.Store.GetCredential(credID); err != nil {
				return nil, errors.New("credential " + credID + " not found")
			}
		}
	}
	if req.Retry != nil {
		if req.Retry.MaxAttempts < 1 || req.Retry.MaxAttempts > 10 {
			return nil, errors.New("retry maxAttempts must be between 1 and 10")
		}
		if req.Retry.BackoffMs < 0 || req.Retry.BackoffMs > 600000 {
			return nil, errors.New("retry backoffMs must be between 0 and 600000")
		}
		if req.Retry.MaxDelayMs < 0 || req.Retry.MaxDelayMs > 3600000 {
			return nil, errors.New("retry maxDelayMs must be between 0 and 3600000")
		}
	}
	// Tags are free-form labels for governance; deduplicated and capped so
	// the list stays readable.
	seen := map[string]bool{}
	tags := []string{}
	for _, tag := range req.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] || len(tags) >= 12 {
			continue
		}
		seen[tag] = true
		tags = append(tags, tag)
	}
	req.Tags = tags
	req.Owner = strings.TrimSpace(req.Owner)
	// Phase 1 D-P0c: new assets default to private; the CLI import path
	// (store-direct, no request) explicitly passes shared.
	if req.Visibility == "" {
		req.Visibility = store.VisibilityPrivate
	}
	if !store.ValidVisibility(req.Visibility) {
		return nil, errors.New("visibility must be private or shared")
	}
	if req.Bundle != nil {
		if err := s.validateProfileBundle(&def, req.Bundle, viewerFrom(r)); err != nil {
			return nil, err
		}
	}
	return &def, nil
}

// validateProfileBundle checks bundle references as asset ids, never as
// node configuration (Phase 1 P0-6): mapping groups must be kinds the node
// type actually supports, the referenced mappings must carry that kind,
// schema references are limited to the input/output slots, and every
// referenced asset must exist and be visible to the caller.
func (s *api) validateProfileBundle(def *nodes.Definition, bundle *store.ProfileBundle, v viewer) error {
	return s.validateProfileBundleWith(def, bundle, v, store.NewLookupCache())
}

func (s *api) validateProfileBundleWith(def *nodes.Definition, bundle *store.ProfileBundle, v viewer, cache *store.LookupCache) error {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	if bundle.Mappings == nil && bundle.Schemas == nil && bundle.Script == "" {
		return nil
	}
	for group, id := range bundle.Mappings {
		if !slices.Contains(def.MappingKinds, group) {
			return fmt.Errorf("bundle mapping group %q is not supported by node type %s", group, def.Type)
		}
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("bundle mapping reference for %q is empty", group)
		}
		mapping, err := s.deps.Store.GetMapping(id)
		if err != nil {
			return fmt.Errorf("bundle mapping %q not found", id)
		}
		if mapping.Kind != group {
			return fmt.Errorf("bundle mapping %q has kind %s but group %q needs a %s mapping", id, mapping.Kind, group, group)
		}
		if !v.mappingVisibleTo(mapping) {
			return fmt.Errorf("bundle mapping %q is private to another user", id)
		}
	}
	for slot, id := range bundle.Schemas {
		if slot != "input" && slot != "output" {
			return fmt.Errorf("bundle schema slot %q must be input or output", slot)
		}
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("bundle schema reference for %q is empty", slot)
		}
		schema, err := s.deps.Store.GetDataSchema(id)
		if err != nil {
			return fmt.Errorf("bundle schema %q not found", id)
		}
		if !v.schemaVisibleTo(schema) {
			return fmt.Errorf("bundle schema %q is private to another user", id)
		}
	}
	// Phase 4 D4-1: the bundle's script must exist, match the node
	// type's language (javascript → code.run, python → code.python) and
	// be visible to the caller.
	if bundle.Script != "" {
		if strings.TrimSpace(bundle.Script) == "" {
			return fmt.Errorf("bundle script reference is empty")
		}
		script, err := cache.Script(s.deps.Store, bundle.Script)
		if err != nil {
			return fmt.Errorf("bundle script %q not found", bundle.Script)
		}
		var want string
		switch def.Type {
		case "code.run":
			want = store.ScriptLanguageJavaScript
		case "code.python":
			want = store.ScriptLanguagePython
		default:
			return fmt.Errorf("bundle scripts are not supported by node type %s", def.Type)
		}
		if script.Language != want {
			return fmt.Errorf("bundle script %q is %s but node type %s needs a %s script", bundle.Script, script.Language, def.Type, want)
		}
		if !v.scriptVisibleTo(script) {
			return fmt.Errorf("bundle script %q is private to another user", bundle.Script)
		}
	}
	return nil
}

// validateProfileRefs enforces deleted-profile reference integrity at draft
// save and publish (plan §18 D2/D6): every node's profileRef must resolve
// to an existing profile for that node's type. Phase 1 D-P0a: a private
// profile of another user fails with a named error, and the profile's own
// bundle references are checked for existence, kind and visibility.
func (s *api) validateProfileRefs(wf *graph.Workflow, v viewer) error {
	return s.validateProfileRefsWith(wf, v, store.NewLookupCache())
}

func (s *api) validateProfileRefsWith(wf *graph.Workflow, v viewer, cache *store.LookupCache) error {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.ProfileRef == "" {
			continue
		}
		profile, err := cache.ConfigProfile(s.deps.Store, n.ProfileRef)
		if err != nil {
			return fmt.Errorf("node %q references missing config profile %q", n.ID, n.ProfileRef)
		}
		if profile.NodeType != n.Type {
			return fmt.Errorf("node %q (type %s) references config profile %q which is for node type %s",
				n.ID, n.Type, n.ProfileRef, profile.NodeType)
		}
		if !v.profileVisibleTo(profile) {
			return fmt.Errorf("node %q references config profile %q which is private to another user", n.ID, n.ProfileRef)
		}
		if profile.Bundle != nil {
			def, ok := nodes.Get(profile.NodeType)
			if ok {
				if err := s.validateProfileBundleWith(&def, profile.Bundle, v, cache); err != nil {
					return fmt.Errorf("config profile %q: %w", profile.Name, err)
				}
			}
		}
	}
	return nil
}

// resolveProfileRefs merges each node's profile config under its own config
// (the node's own value wins per key) and folds the profile's credential
// references into slots the node has not filled itself. The published
// snapshot is fully resolved, so the engine and replay need zero
// profile-awareness (plan §18 D4). The merge itself lives in the store so
// the CLI export command shares one semantics.
func (s *api) resolveProfileRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.resolveProfileRefsWith(wf, store.NewLookupCache())
}

func (s *api) resolveProfileRefsWith(wf *graph.Workflow, cache *store.LookupCache) (map[string]int, error) {
	return s.deps.Store.ResolveProfileRefsWith(wf, cache)
}

// cloneWorkflow deep-copies a workflow doc (JSON round-trip: the docs are
// plain JSON by design).
func cloneWorkflow(wf *graph.Workflow) (*graph.Workflow, error) {
	data, err := json.Marshal(wf)
	if err != nil {
		return nil, err
	}
	var out graph.Workflow
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// validateEffectiveWorkflow validates the workflow as its config will
// actually run: profile refs resolved, node overrides on top (plan §18 D2).
// The stored draft stays sparse — only validation sees the merge — so a
// node whose required fields come from its profile saves cleanly, while a
// profile can never become a validation bypass.
func (s *api) validateEffectiveWorkflow(wf *graph.Workflow, v viewer) ([]graph.Issue, error) {
	return s.validateEffectiveWorkflowWith(wf, v, store.NewLookupCache())
}

func (s *api) validateEffectiveWorkflowWith(wf *graph.Workflow, v viewer, cache *store.LookupCache) ([]graph.Issue, error) {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	if err := s.validateProfileRefsWith(wf, v, cache); err != nil {
		return nil, err
	}
	merged, err := cloneWorkflow(wf)
	if err != nil {
		return nil, err
	}
	if _, err := s.resolveProfileRefsWith(merged, cache); err != nil {
		return nil, err
	}
	// Mapping and schema validation run on the resolved workflow (Phase 1
	// P0-2): a data.map node whose mapping arrives via a profile bundle
	// has a mappingRef only after resolution. Same for injected schema
	// refs.
	if err := s.validateSchemaRefsWith(merged, v, cache); err != nil {
		return nil, err
	}
	if err := s.validateMappingRefsWith(merged, v, cache); err != nil {
		return nil, err
	}
	// Script refs validate on the resolved workflow too (Phase 4): a
	// bundle-injected scriptRef appears only after profile resolution.
	if err := s.validateScriptRefsWith(merged, v, cache); err != nil {
		return nil, err
	}
	// Connector node types merge into validation (Campaign 4): a draft
	// referencing connector.<id>.<op> validates against the connector's
	// current version here; publish pins and inlines the template.
	if err := s.validateConnectorNodesWith(merged, v, cache); err != nil {
		return nil, err
	}
	report := graph.ValidateWithExtra(merged, s.connectorExtraDefsWith(cache))
	return report.Issues, nil
}

// hasErrorIssues reports whether any validation issue is an error.
func hasErrorIssues(issues []graph.Issue) bool {
	for _, issue := range issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

// ProfileTester runs a Configuration Hub connection test through the live
// host: the exact guard, proxy and TLS rules of real runs apply. Simulated
// servers have none, and the endpoint answers 503 with that reason.
type ProfileTester interface {
	TestHTTPProfile(ctx context.Context, rawURL string, timeout time.Duration) (status int, latencyMs int64, err error)
	TestPostgresProfile(ctx context.Context, credID string, timeout time.Duration) (latencyMs int64, err error)
}

// profileBundleMappingRow and profileBundleSchemaRow report a resolved
// bundle reference in the test response (UX Phase 2 D2-7): refs resolve
// to the asset's current version, with counts so the user sees what
// would compose.
type profileBundleMappingRow struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Rows    int    `json:"rows"`
}

type profileBundleSchemaRow struct {
	Slot    string `json:"slot"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int    `json:"version"`
	Fields  int    `json:"fields"`
}

type profileBundleParamRows struct {
	Group string `json:"group"`
	Rows  int    `json:"rows"`
}

// profileBundleScriptRow reports the bundle's resolved script (Phase 4
// D4-1) in the test response.
type profileBundleScriptRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  int    `json:"version"`
	Language string `json:"language"`
	Bytes    int    `json:"bytes"`
}

type profileBundleIssue struct {
	Name    string `json:"name"`
	Message string `json:"message"`
}

type profileBundleSection struct {
	Mappings  []profileBundleMappingRow `json:"mappings"`
	Schemas   []profileBundleSchemaRow  `json:"schemas"`
	Script    *profileBundleScriptRow   `json:"script,omitempty"`
	ParamRows []profileBundleParamRows  `json:"paramRows"`
	Issues    []profileBundleIssue      `json:"issues"`
}

func (b *profileBundleSection) hasWork() bool {
	return b.Script != nil || len(b.Mappings) > 0 || len(b.Schemas) > 0 || len(b.ParamRows) > 0
}

// bundleSection resolves the profile's bundle against the store (UX
// Phase 2 D2-7): every ref to its current version, parameter-group rows
// compiled, and named issues for anything that no longer resolves. Pure
// store work — it never dials anything, so a simulated server can still
// answer it.
func (s *api) bundleSection(r *http.Request, profile *store.ConfigProfile) profileBundleSection {
	section := profileBundleSection{
		Mappings:  []profileBundleMappingRow{},
		Schemas:   []profileBundleSchemaRow{},
		ParamRows: []profileBundleParamRows{},
		Issues:    []profileBundleIssue{},
	}
	v := viewerFrom(r)
	cache := store.NewLookupCache()
	if profile.Bundle != nil {
		for kind, id := range profile.Bundle.Mappings {
			if strings.TrimSpace(id) == "" {
				continue
			}
			mapping, err := cache.Mapping(s.deps.Store, id)
			if err != nil {
				section.Issues = append(section.Issues, profileBundleIssue{
					Name:    fmt.Sprintf("%s mapping", kind),
					Message: fmt.Sprintf("referenced mapping %q no longer exists — publish would fail until the bundle drops it", id),
				})
				continue
			}
			if !v.mappingVisibleTo(mapping) {
				section.Issues = append(section.Issues, profileBundleIssue{
					Name:    fmt.Sprintf("%s mapping", kind),
					Message: fmt.Sprintf("referenced mapping %q is private to another editor", id),
				})
				continue
			}
			section.Mappings = append(section.Mappings, profileBundleMappingRow{
				Kind: kind, ID: id, Name: mapping.Name, Version: mapping.Version, Rows: len(mapping.Rows),
			})
		}
		for slot, id := range profile.Bundle.Schemas {
			if strings.TrimSpace(id) == "" {
				continue
			}
			schema, err := cache.DataSchema(s.deps.Store, id)
			if err != nil {
				section.Issues = append(section.Issues, profileBundleIssue{
					Name:    fmt.Sprintf("%s data contract", slot),
					Message: fmt.Sprintf("referenced data contract %q no longer exists — publish would fail until the bundle drops it", id),
				})
				continue
			}
			if !v.schemaVisibleTo(schema) {
				section.Issues = append(section.Issues, profileBundleIssue{
					Name:    fmt.Sprintf("%s data contract", slot),
					Message: fmt.Sprintf("referenced data contract %q is private to another editor", id),
				})
				continue
			}
			section.Schemas = append(section.Schemas, profileBundleSchemaRow{
				Slot: slot, ID: id, Name: schema.Name, Version: schema.Version, Fields: len(schema.Fields),
			})
		}
		// Phase 4 D4-1: the bundle's script resolves like every other
		// bundled asset — current version, or a named issue.
		if profile.Bundle != nil && profile.Bundle.Script != "" {
			id := profile.Bundle.Script
			script, err := s.deps.Store.GetScript(id)
			if err != nil {
				section.Issues = append(section.Issues, profileBundleIssue{
					Name:    "script",
					Message: fmt.Sprintf("referenced script %q no longer exists — publish would fail until the bundle drops it", id),
				})
			} else if !v.scriptVisibleTo(script) {
				section.Issues = append(section.Issues, profileBundleIssue{
					Name:    "script",
					Message: fmt.Sprintf("referenced script %q is private to another editor", id),
				})
			} else {
				section.Script = &profileBundleScriptRow{
					ID: id, Name: script.Name, Version: script.Version,
					Language: script.Language, Bytes: len(script.Source),
				}
			}
		}
	}
	// Parameter-group rows compile exactly as publish would (v4.1 E4);
	// invalid rows cannot pass create/update validation but may exist in
	// older or imported profiles — surface them as named issues.
	for _, group := range []string{"bodyFields", "queryFields", "headerFields", "pathFields"} {
		raw, ok := profile.Config[group]
		if !ok || raw == nil {
			continue
		}
		rows, err := graph.ParseParamRows(raw)
		if err != nil {
			section.Issues = append(section.Issues, profileBundleIssue{
				Name:    group,
				Message: fmt.Sprintf("rows do not compile: %v", err),
			})
			continue
		}
		section.ParamRows = append(section.ParamRows, profileBundleParamRows{Group: group, Rows: len(rows)})
	}
	return section
}

// testProfile answers POST /api/config-profiles/{id}/test: a connection
// check the user can run before publishing. The result never gates a save
// — a failing test is information, not a verdict. UX Phase 2 D2-7 adds a
// bundle section alongside connectivity: refs resolved to their current
// versions, parameter rows compiled, named issues for anything that no
// longer resolves. Bundle resolution is store-only, so a simulated
// server still answers profiles that carry bundle work; the connectivity
// half needs a live host and is reported as skipped there.
func (s *api) testProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.deps.Store.GetConfigProfile(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	if !viewerFrom(r).profileVisibleTo(profile) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	bundle := s.bundleSection(r, profile)
	tester := s.deps.TestProfile
	// code.run profiles test in-process (Phase 5): no live host needed,
	// so a simulated server still executes them.
	if tester == nil && !bundle.hasWork() && profile.NodeType != "code.run" {
		writeError(w, http.StatusServiceUnavailable,
			"profile testing needs a live server: this instance runs a simulated host")
		return
	}

	response := map[string]any{"bundle": bundle, "bundleOk": len(bundle.Issues) == 0}
	connectivity := func() map[string]any {
		// Code profiles test in-process or through the Python executor
		// gate (Phase 5), not through the connectivity tester.
		switch profile.NodeType {
		case "code.run", "code.python":
			return s.testCodeProfile(r, profile)
		}
		if tester == nil {
			return map[string]any{"skipped": "profile testing needs a live server: this instance runs a simulated host"}
		}
		ctxTimeout := 15 * time.Second
		switch profile.NodeType {
		case "http.request":
			rawURL, _ := profile.Config["url"].(string)
			rawURL = strings.TrimSpace(rawURL)
			if rawURL == "" {
				return map[string]any{"skipped": "profile has no url to test"}
			}
			// The test honors the profile's configured timeout (clamped to a
			// sane bound) plus a margin for dialing, so the request context
			// never cuts a configured test short.
			timeout := 5 * time.Second
			if secs, ok := profile.Config["timeout"].(float64); ok && secs > 0 {
				timeout = time.Duration(secs * float64(time.Second))
				if timeout > 30*time.Second {
					timeout = 30 * time.Second
				}
			}
			ctxTimeout = timeout + 5*time.Second
			ctx, cancel := context.WithTimeout(r.Context(), ctxTimeout)
			defer cancel()
			status, latencyMs, err := tester.TestHTTPProfile(ctx, rawURL, timeout)
			if err != nil {
				return map[string]any{"ok": false, "error": err.Error()}
			}
			s.auditProfile(r, "profile.tested", profile.ID+" -> "+strconv.Itoa(status))
			return map[string]any{"ok": true, "status": status, "latencyMs": latencyMs}
		case "db.postgres":
			credID, _ := profile.CredentialRefs["postgres"]
			if strings.TrimSpace(credID) == "" {
				return map[string]any{"skipped": "profile needs a postgres credential reference to test (add it under credential references)"}
			}
			ctx, cancel := context.WithTimeout(r.Context(), ctxTimeout)
			defer cancel()
			latencyMs, err := tester.TestPostgresProfile(ctx, credID, 5*time.Second)
			if err != nil {
				return map[string]any{"ok": false, "error": err.Error()}
			}
			s.auditProfile(r, "profile.tested", profile.ID+" -> postgres")
			return map[string]any{"ok": true, "latencyMs": latencyMs}
		default:
			return map[string]any{"skipped": "profile testing is not supported for node type " + strconv.Quote(profile.NodeType)}
		}
	}()

	// D2-7: 422 only when there is nothing to test at all — no
	// connectivity target and no bundle work to report.
	if conn, ok := connectivity["skipped"].(string); ok {
		if !bundle.hasWork() && tester != nil {
			writeError(w, http.StatusUnprocessableEntity, conn)
			return
		}
		response["skipped"] = conn
	} else {
		for key, value := range connectivity {
			response[key] = value
		}
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *api) listProfiles(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListConfigProfilesPage("", params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "profiles", page)
		return
	}
	list, err := s.deps.Store.ListConfigProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	visible := list[:0]
	for _, p := range list {
		if v.profileVisibleTo(&p) {
			visible = append(visible, p)
		}
	}
	if visible == nil {
		visible = []store.ConfigProfile{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": visible, "count": len(visible)})
}

func (s *api) createProfile(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.validateProfileBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = flow.NewID("profile")
	}
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateConfigProfile(&store.ConfigProfile{
		ID: req.ID, Name: req.Name, NodeType: req.NodeType, Description: req.Description,
		Config: req.Config, CredentialRefs: req.CredentialRefs, Retry: req.Retry,
		Tags: req.Tags, Owner: req.Owner, Bundle: req.Bundle,
		Visibility: req.Visibility, CreatorID: creator, TestInput: req.TestInput,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditProfile(r, "profile.created", req.ID+" ("+req.NodeType+")")
	writeJSON(w, http.StatusCreated, created)
}

func (s *api) getProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.deps.Store.GetConfigProfile(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	if !viewerFrom(r).profileVisibleTo(profile) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *api) updateProfile(w http.ResponseWriter, r *http.Request) {
	var req profileRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.validateProfileBody(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Version < 1 {
		writeError(w, http.StatusBadRequest, "version is required (the profile version this update is based on)")
		return
	}
	id := r.PathValue("id")
	current, err := s.deps.Store.GetConfigProfile(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	v := viewerFrom(r)
	if !v.profileVisibleTo(current) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	if !v.canGovern(current.CreatorID) {
		writeError(w, http.StatusForbidden, "only the creator or an admin can edit this config profile")
		return
	}
	updated, err := s.deps.Store.UpdateConfigProfile(&store.ConfigProfile{
		ID: id, Name: req.Name, NodeType: req.NodeType, Description: req.Description,
		Config: req.Config, CredentialRefs: req.CredentialRefs, Retry: req.Retry,
		Tags: req.Tags, Owner: req.Owner, Bundle: req.Bundle, TestInput: req.TestInput,
	}, req.Version, actorFor(r))
	if errors.Is(err, store.ErrStaleConfigProfile) {
		writeError(w, http.StatusConflict, "config profile was modified concurrently — reload and retry")
		return
	}
	if errors.Is(err, store.ErrConfigProfileNotFound) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// D-P0b: a visibility change is governance, not content — it applies
	// without forking a version.
	if req.Visibility != "" && req.Visibility != current.Visibility {
		if err := s.deps.Store.SetConfigProfileVisibility(id, req.Visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated.Visibility = req.Visibility
	}
	s.auditProfile(r, "profile.updated", updated.ID+" -> v"+strconv.Itoa(updated.Version))
	writeJSON(w, http.StatusOK, updated)
}

// shareProfile answers POST /api/config-profiles/{id}/share|unshare
// (Phase 1 D-P0b): the explicit promote/demote action. Admins may govern
// any profile; the creator may govern their own.
func (s *api) shareProfile(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		profile, err := s.deps.Store.GetConfigProfile(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "config profile not found")
			return
		}
		v := viewerFrom(r)
		// Hidden assets 404 before the governance check, so the endpoint
		// never reveals that a private profile exists.
		if !v.profileVisibleTo(profile) {
			writeError(w, http.StatusNotFound, "config profile not found")
			return
		}
		if !v.canGovern(profile.CreatorID) {
			writeError(w, http.StatusForbidden, "only the creator or an admin can change this profile's visibility")
			return
		}
		if err := s.deps.Store.SetConfigProfileVisibility(id, visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditProfile(r, "profile.visibility", id+" -> "+visibility)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "visibility": visibility})
	}
}

func (s *api) deleteProfile(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Store.DeleteConfigProfile(r.PathValue("id"))
	if errors.Is(err, store.ErrConfigProfileInUse) {
		writeError(w, http.StatusConflict, "config profile is referenced by published workflows and cannot be deleted")
		return
	}
	if errors.Is(err, store.ErrConfigProfileNotFound) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditProfile(r, "profile.deleted", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) listProfileVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requireProfileVisible(w, r) {
		return
	}
	versions, err := s.deps.Store.ListConfigProfileVersions(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []store.ConfigProfileVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "count": len(versions)})
}

func (s *api) getProfileVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requireProfileVisible(w, r) {
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "version must be a number")
		return
	}
	version, err := s.deps.Store.GetConfigProfileVersion(r.PathValue("id"), versionNumber)
	if err != nil {
		writeError(w, http.StatusNotFound, "config profile version not found")
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *api) profileUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireProfileVisible(w, r) {
		return
	}
	usage, err := s.deps.Store.ConfigProfileUsage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if usage == nil {
		usage = []store.ConfigProfileUsage{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage, "count": len(usage)})
}

// requireProfileVisible answers 404 for private profiles the caller may
// not see; false means the response is already written.
func (s *api) requireProfileVisible(w http.ResponseWriter, r *http.Request) bool {
	profile, err := s.deps.Store.GetConfigProfile(r.PathValue("id"))
	if err != nil || !viewerFrom(r).profileVisibleTo(profile) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return false
	}
	return true
}

func (s *api) duplicateProfile(w http.ResponseWriter, r *http.Request) {
	source, err := s.deps.Store.GetConfigProfile(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	if !viewerFrom(r).profileVisibleTo(source) {
		writeError(w, http.StatusNotFound, "config profile not found")
		return
	}
	// The duplicate is the caller's own new asset: private until they
	// choose to share it (D-P0c).
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateConfigProfile(&store.ConfigProfile{
		ID:             flow.NewID("profile"),
		Name:           source.Name + " (copy)",
		NodeType:       source.NodeType,
		Description:    source.Description,
		Config:         source.Config,
		CredentialRefs: source.CredentialRefs,
		Retry:          source.Retry,
		Tags:           source.Tags,
		Owner:          source.Owner,
		Bundle:         source.Bundle,
		Visibility:     store.VisibilityPrivate,
		CreatorID:      creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditProfile(r, "profile.created", created.ID+" (duplicate of "+source.ID+")")
	writeJSON(w, http.StatusCreated, created)
}

// testCodeProfile executes a code.run / code.python profile's bundle
// script against its saved test fixture (Phase 5). The profile's own
// source is deliberately non-Profileable (Phase 4 freeze), so the code
// always comes from the bundle script; the profileable runtime defaults
// (timeout, memory, requirements) ride the profile config. JavaScript
// runs in-process with the same runtime and clamps as the code.run node;
// Python runs through the live host's subprocess machinery and reports
// skipped on a simulated host.
func (s *api) testCodeProfile(r *http.Request, profile *store.ConfigProfile) map[string]any {
	language := "javascript"
	if profile.NodeType == "code.python" {
		language = "python"
	}
	if profile.Bundle == nil || strings.TrimSpace(profile.Bundle.Script) == "" {
		return map[string]any{"skipped": "profile sets no bundle script to test (code profiles execute their bundled script)"}
	}
	script, err := s.deps.Store.GetScript(profile.Bundle.Script)
	if err != nil || !viewerFrom(r).scriptVisibleTo(script) {
		return map[string]any{"skipped": "bundle script is unavailable"}
	}
	if script.Language == store.ScriptLanguagePython != (language == "python") {
		return map[string]any{"skipped": "bundle script language does not match the profile's node type"}
	}
	source := script.Source
	requirements := script.Requirements
	timeoutSeconds := script.TimeoutSeconds
	maxMemoryMB := script.MaxMemoryMB
	// The fixture is the saved testInput (validated JSON at save time);
	// it defaults to an empty object like a bare node input.
	item := flow.Item{}
	if len(profile.TestInput) > 0 {
		var parsed any
		if err := json.Unmarshal(profile.TestInput, &parsed); err == nil {
			if shaped, err := sampleItem(parsed); err == nil {
				item = shaped
			}
		}
	}
	if language == "javascript" {
		seconds := 2
		if secs, ok := profile.Config["timeoutSeconds"].(float64); ok && secs > 0 {
			seconds = int(secs)
		}
		if timeoutSeconds > 0 {
			seconds = timeoutSeconds
		}
		if seconds < 1 {
			seconds = 1
		}
		if seconds > 300 {
			seconds = 300
		}
		timeout := time.Duration(seconds) * time.Second
		ctx, cancel := context.WithTimeout(r.Context(), timeout+2*time.Second)
		defer cancel()
		var logs []map[string]string
		scope := expr.Scope{Item: item, AllowHostCalls: false}
		scope.Log = func(level, message string) {
			logs = append(logs, map[string]string{"level": level, "message": message})
		}
		result, err := expr.EvalObjectWithTimeout(ctx, source, scope, timeout)
		if err != nil {
			return map[string]any{"ok": false, "error": err.Error()}
		}
		s.auditProfile(r, "profile.tested", profile.ID+" -> javascript")
		return map[string]any{"ok": true, "result": result, "logs": logs}
	}
	executor := s.deps.PythonExecutor
	if executor == nil {
		return map[string]any{"skipped": "python profile testing needs a live server: this instance runs a simulated host"}
	}
	seconds := 60
	if secs, ok := profile.Config["timeoutSeconds"].(float64); ok && secs > 0 {
		seconds = int(secs)
	}
	if timeoutSeconds > 0 {
		seconds = timeoutSeconds
	}
	if seconds < 1 {
		seconds = 1
	}
	if seconds > 900 {
		seconds = 900
	}
	memoryMB := 512
	if mem, ok := profile.Config["maxMemoryMB"].(float64); ok && mem > 0 {
		memoryMB = int(mem)
	}
	if maxMemoryMB > 0 {
		memoryMB = maxMemoryMB
	}
	if memoryMB < 16 {
		memoryMB = 16
	}
	if memoryMB > 8192 {
		memoryMB = 8192
	}
	// Requirements union: the profile's own (profileable) plus the
	// bundle script's, deduplicated — the same union publish applies.
	seen := map[string]bool{}
	if raw, ok := profile.Config["requirements"].([]any); ok {
		for _, entry := range raw {
			req := strings.TrimSpace(fmt.Sprint(entry))
			if req != "" && !seen[req] {
				seen[req] = true
				requirements = append(requirements, req)
			}
		}
	}
	envKey := ""
	if len(requirements) > 0 {
		envKey = pyenv.Key(requirements)
	}
	timeout := time.Duration(seconds) * time.Second
	ctx, cancel := context.WithTimeout(r.Context(), timeout+10*time.Second)
	defer cancel()
	res, err := executor.Exec(ctx, flow.ExecRequest{
		Source: source, Stdin: item, EnvKey: envKey,
		Timeout: timeout, MaxMemoryMB: memoryMB,
	})
	if err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	s.auditProfile(r, "profile.tested", profile.ID+" -> python exit "+strconv.Itoa(res.ExitCode))
	response := map[string]any{
		"ok": res.ExitCode == 0, "exitCode": res.ExitCode,
		"stdout": res.Stdout, "stderr": res.Stderr, "result": res.Result,
	}
	if res.ExitCode != 0 {
		response["error"] = "python exited with status " + strconv.Itoa(res.ExitCode)
	}
	return response
}
