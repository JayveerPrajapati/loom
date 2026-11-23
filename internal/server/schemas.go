package server

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/schemas"
	"loom/internal/store"
)

// Data-contract API (Configuration Hub v2, plan D10–D12): reusable,
// versioned field lists describing the shape of data. Viewers read,
// editors manage, admins delete — the same governance as profiles.

type schemaRequest struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Fields      []schemas.Field `json:"fields"`
	Tags        []string        `json:"tags"`
	Owner       string          `json:"owner"`
	// Visibility is private by default for new assets (Phase 1 D-P0c).
	Visibility string `json:"visibility"`
	// Version is the caller's current version; a stale value is a 409.
	Version int `json:"version"`
}

func (s *api) auditSchema(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

// validateSchemaBody enforces contract integrity for create and update
// alike: a non-empty name, structurally valid fields (names, types,
// per-type child rules), bounded depth, and normalized governance tags.
func (s *api) validateSchemaBody(req *schemaRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if len(req.Fields) == 0 {
		return errors.New("a data schema needs at least one field")
	}
	if err := schemas.EnforceDepth(req.Fields); err != nil {
		return err
	}
	if err := schemas.Validate(req.Fields); err != nil {
		return errors.New("invalid contract: " + err.Error())
	}
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
	// Phase 1 D-P0c: new assets default to private.
	if req.Visibility == "" {
		req.Visibility = store.VisibilityPrivate
	}
	if !store.ValidVisibility(req.Visibility) {
		return errors.New("visibility must be private or shared")
	}
	return nil
}

func (s *api) listSchemas(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListDataSchemasPage(params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "schemas", page)
		return
	}
	list, err := s.deps.Store.ListDataSchemas()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	visible := list[:0]
	for _, item := range list {
		if v.schemaVisibleTo(&item) {
			visible = append(visible, item)
		}
	}
	if visible == nil {
		visible = []store.DataSchema{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"schemas": visible, "count": len(visible)})
}

func (s *api) createSchema(w http.ResponseWriter, r *http.Request) {
	var req schemaRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateSchemaBody(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = flow.NewID("schema")
	}
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateDataSchema(&store.DataSchema{
		ID: req.ID, Name: req.Name, Description: req.Description,
		Fields: req.Fields, Tags: req.Tags, Owner: req.Owner,
		Visibility: req.Visibility, CreatorID: creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditSchema(r, "schema.created", req.ID+" ("+req.Name+")")
	writeJSON(w, http.StatusCreated, created)
}

func (s *api) getSchema(w http.ResponseWriter, r *http.Request) {
	schema, err := s.deps.Store.GetDataSchema(r.PathValue("id"))
	if errors.Is(err, store.ErrDataSchemaNotFound) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !viewerFrom(r).schemaVisibleTo(schema) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (s *api) updateSchema(w http.ResponseWriter, r *http.Request) {
	var req schemaRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateSchemaBody(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Version < 1 {
		writeError(w, http.StatusBadRequest, "version is required (the schema version this update is based on)")
		return
	}
	id := r.PathValue("id")
	current, err := s.deps.Store.GetDataSchema(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	v := viewerFrom(r)
	if !v.schemaVisibleTo(current) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	if !v.canGovern(current.CreatorID) {
		writeError(w, http.StatusForbidden, "only the creator or an admin can edit this data contract")
		return
	}
	updated, err := s.deps.Store.UpdateDataSchema(&store.DataSchema{
		ID: id, Name: req.Name, Description: req.Description,
		Fields: req.Fields, Tags: req.Tags, Owner: req.Owner,
	}, req.Version, actorFor(r))
	if errors.Is(err, store.ErrStaleDataSchema) {
		writeError(w, http.StatusConflict, "data schema was modified concurrently — reload and retry")
		return
	}
	if errors.Is(err, store.ErrDataSchemaNotFound) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// D-P0b: visibility is governance, applied without a new version.
	if req.Visibility != "" && req.Visibility != current.Visibility {
		if err := s.deps.Store.SetDataSchemaVisibility(id, req.Visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated.Visibility = req.Visibility
	}
	s.auditSchema(r, "schema.updated", updated.ID+" -> v"+strconv.Itoa(updated.Version))
	writeJSON(w, http.StatusOK, updated)
}

// shareSchema answers POST /api/data-schemas/{id}/share|unshare
// (Phase 1 D-P0b).
func (s *api) shareSchema(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		schema, err := s.deps.Store.GetDataSchema(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "data schema not found")
			return
		}
		v := viewerFrom(r)
		// Hidden assets 404 before the governance check (D-P0a).
		if !v.schemaVisibleTo(schema) {
			writeError(w, http.StatusNotFound, "data schema not found")
			return
		}
		if !v.canGovern(schema.CreatorID) {
			writeError(w, http.StatusForbidden, "only the creator or an admin can change this schema's visibility")
			return
		}
		if err := s.deps.Store.SetDataSchemaVisibility(id, visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditSchema(r, "schema.visibility", id+" -> "+visibility)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "visibility": visibility})
	}
}

func (s *api) deleteSchema(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Store.DeleteDataSchema(r.PathValue("id"))
	if errors.Is(err, store.ErrDataSchemaInUse) {
		// The store wraps the guard with the referencing profile names
		// when a bundle (not a publish pin) holds the schema.
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, store.ErrDataSchemaNotFound) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditSchema(r, "schema.deleted", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// requireSchemaVisible answers 404 for private schemas the caller may
// not see; false means the response is already written.
func (s *api) requireSchemaVisible(w http.ResponseWriter, r *http.Request) bool {
	schema, err := s.deps.Store.GetDataSchema(r.PathValue("id"))
	if err != nil || !viewerFrom(r).schemaVisibleTo(schema) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return false
	}
	return true
}

func (s *api) listSchemaVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requireSchemaVisible(w, r) {
		return
	}
	versions, err := s.deps.Store.ListDataSchemaVersions(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []store.DataSchemaVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "count": len(versions)})
}

func (s *api) getSchemaVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requireSchemaVisible(w, r) {
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "version must be a number")
		return
	}
	version, err := s.deps.Store.GetDataSchemaVersion(r.PathValue("id"), versionNumber)
	if errors.Is(err, store.ErrDataSchemaNotFound) {
		writeError(w, http.StatusNotFound, "data schema version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *api) schemaUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireSchemaVisible(w, r) {
		return
	}
	usage, err := s.deps.Store.DataSchemaUsage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if usage == nil {
		usage = []store.DataSchemaUsage{}
	}
	profiles, err := s.bundleProfilesFor(r, "schema", r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	profiles = bundleProfilesJSON(profiles)
	writeJSON(w, http.StatusOK, map[string]any{
		"usage": usage, "count": len(usage) + len(profiles),
		"pins": len(usage), "profiles": profiles,
	})
}

func (s *api) duplicateSchema(w http.ResponseWriter, r *http.Request) {
	source, err := s.deps.Store.GetDataSchema(r.PathValue("id"))
	if errors.Is(err, store.ErrDataSchemaNotFound) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !viewerFrom(r).schemaVisibleTo(source) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	// The duplicate is the caller's own new asset (D-P0c).
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateDataSchema(&store.DataSchema{
		ID:          flow.NewID("schema"),
		Name:        source.Name + " (copy)",
		Description: source.Description,
		Fields:      source.Fields,
		Tags:        source.Tags,
		Owner:       source.Owner,
		Visibility:  store.VisibilityPrivate,
		CreatorID:   creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditSchema(r, "schema.created", created.ID+" (duplicate of "+source.ID+")")
	writeJSON(w, http.StatusCreated, created)
}

// testSchema answers POST /api/data-schemas/{id}/test: checks a sample
// payload against the contract and returns the violations. The result
// never gates a save — it is information, not a verdict.
func (s *api) testSchema(w http.ResponseWriter, r *http.Request) {
	schema, err := s.deps.Store.GetDataSchema(r.PathValue("id"))
	if err != nil || !viewerFrom(r).schemaVisibleTo(schema) {
		writeError(w, http.StatusNotFound, "data schema not found")
		return
	}
	var req struct {
		Sample any `json:"sample"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	violations := schemas.Check(req.Sample, schema.Fields)
	if violations == nil {
		violations = []schemas.Violation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(violations) == 0, "violations": violations})
}

// validateSchemaRefs enforces deleted-contract reference integrity at draft
// save and publish (v2 plan, D2/D6 pattern): every workflow- and node-level
// schema ref must resolve to an existing contract.
func (s *api) validateSchemaRefs(wf *graph.Workflow, v viewer) error {
	return s.validateSchemaRefsWith(wf, v, store.NewLookupCache())
}

func (s *api) validateSchemaRefsWith(wf *graph.Workflow, v viewer, cache *store.LookupCache) error {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	check := func(ref, where string) error {
		if ref == "" {
			return nil
		}
		schema, err := cache.DataSchema(s.deps.Store, ref)
		if err != nil {
			return fmt.Errorf("%s references missing data schema %q", where, ref)
		}
		if !v.schemaVisibleTo(schema) {
			return fmt.Errorf("%s references data schema %q which is private to another user", where, ref)
		}
		return nil
	}
	if err := check(wf.InputSchemaRef, "workflow input"); err != nil {
		return err
	}
	if err := check(wf.OutputSchemaRef, "workflow output"); err != nil {
		return err
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if err := check(n.InputSchemaRef, fmt.Sprintf("node %q input", n.ID)); err != nil {
			return err
		}
		if err := check(n.OutputSchemaRef, fmt.Sprintf("node %q output", n.ID)); err != nil {
			return err
		}
	}
	return nil
}

// resolveSchemaRefs inlines every schema reference into resolved field
// lists on the doc (v2 plan, D4-style) and returns the usage pins for the
// publish path to record.
func (s *api) resolveSchemaRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.resolveSchemaRefsWith(wf, store.NewLookupCache())
}

func (s *api) resolveSchemaRefsWith(wf *graph.Workflow, cache *store.LookupCache) (map[string]int, error) {
	return s.deps.Store.ResolveSchemaRefsWith(wf, cache)
}
