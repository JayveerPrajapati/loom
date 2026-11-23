package server

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
)

// Mapping API (Configuration Hub v2, plan D13): reusable target←source row
// sets consumed by the data.map node. Viewers read, editors manage, admins
// delete — the same governance as profiles and contracts.

type mappingRequest struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Kind        string             `json:"kind"`
	Rows        []store.MappingRow `json:"rows"`
	Tags        []string           `json:"tags"`
	Owner       string             `json:"owner"`
	// Visibility is private by default for new assets (Phase 1 D-P0c).
	Visibility string `json:"visibility"`
	// Version is the caller's current version; a stale value is a 409.
	Version int `json:"version"`
}

func (s *api) auditMapping(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

// validateMappingBody enforces mapping integrity for create and update
// alike: a non-empty name, a valid kind, at least one row, non-empty
// unique targets and non-empty sources.
func (s *api) validateMappingBody(req *mappingRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return errors.New("name is required")
	}
	if err := store.CheckMappingKind(req.Kind); err != nil {
		return err
	}
	if len(req.Rows) == 0 {
		return errors.New("a mapping needs at least one row")
	}
	seen := map[string]bool{}
	for i := range req.Rows {
		req.Rows[i].Target = strings.TrimSpace(req.Rows[i].Target)
		req.Rows[i].Source = strings.TrimSpace(req.Rows[i].Source)
		if req.Rows[i].Target == "" {
			return errors.New("row targets are required")
		}
		if seen[req.Rows[i].Target] {
			return errors.New("duplicate row target " + req.Rows[i].Target)
		}
		seen[req.Rows[i].Target] = true
		if req.Rows[i].Source == "" {
			return errors.New("row " + req.Rows[i].Target + " needs a source expression")
		}
	}
	seenTags := map[string]bool{}
	tags := []string{}
	for _, tag := range req.Tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seenTags[tag] || len(tags) >= 12 {
			continue
		}
		seenTags[tag] = true
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

func (s *api) listMappings(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListMappingsPage(nil, params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "mappings", page)
		return
	}
	list, err := s.deps.Store.ListMappings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	visible := list[:0]
	for _, m := range list {
		if v.mappingVisibleTo(&m) {
			visible = append(visible, m)
		}
	}
	if visible == nil {
		visible = []store.Mapping{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"mappings": visible, "count": len(visible)})
}

func (s *api) createMapping(w http.ResponseWriter, r *http.Request) {
	var req mappingRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateMappingBody(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = flow.NewID("mapping")
	}
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateMapping(&store.Mapping{
		ID: req.ID, Name: req.Name, Description: req.Description, Kind: req.Kind,
		Rows: req.Rows, Tags: req.Tags, Owner: req.Owner,
		Visibility: req.Visibility, CreatorID: creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditMapping(r, "mapping.created", req.ID+" ("+req.Name+")")
	writeJSON(w, http.StatusCreated, created)
}

func (s *api) getMapping(w http.ResponseWriter, r *http.Request) {
	mapping, err := s.deps.Store.GetMapping(r.PathValue("id"))
	if errors.Is(err, store.ErrMappingNotFound) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !viewerFrom(r).mappingVisibleTo(mapping) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	writeJSON(w, http.StatusOK, mapping)
}

func (s *api) updateMapping(w http.ResponseWriter, r *http.Request) {
	var req mappingRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateMappingBody(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Version < 1 {
		writeError(w, http.StatusBadRequest, "version is required (the mapping version this update is based on)")
		return
	}
	id := r.PathValue("id")
	current, err := s.deps.Store.GetMapping(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	v := viewerFrom(r)
	if !v.mappingVisibleTo(current) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	if !v.canGovern(current.CreatorID) {
		writeError(w, http.StatusForbidden, "only the creator or an admin can edit this mapping")
		return
	}
	updated, err := s.deps.Store.UpdateMapping(&store.Mapping{
		ID: id, Name: req.Name, Description: req.Description, Kind: req.Kind,
		Rows: req.Rows, Tags: req.Tags, Owner: req.Owner,
	}, req.Version, actorFor(r))
	if errors.Is(err, store.ErrStaleMapping) {
		writeError(w, http.StatusConflict, "mapping was modified concurrently — reload and retry")
		return
	}
	if errors.Is(err, store.ErrMappingNotFound) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// D-P0b: visibility is governance, applied without a new version.
	if req.Visibility != "" && req.Visibility != current.Visibility {
		if err := s.deps.Store.SetMappingVisibility(id, req.Visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated.Visibility = req.Visibility
	}
	s.auditMapping(r, "mapping.updated", updated.ID+" -> v"+strconv.Itoa(updated.Version))
	writeJSON(w, http.StatusOK, updated)
}

// shareMapping answers POST /api/mappings/{id}/share|unshare (Phase 1
// D-P0b).
func (s *api) shareMapping(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		mapping, err := s.deps.Store.GetMapping(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "mapping not found")
			return
		}
		v := viewerFrom(r)
		// Hidden assets 404 before the governance check (D-P0a).
		if !v.mappingVisibleTo(mapping) {
			writeError(w, http.StatusNotFound, "mapping not found")
			return
		}
		if !v.canGovern(mapping.CreatorID) {
			writeError(w, http.StatusForbidden, "only the creator or an admin can change this mapping's visibility")
			return
		}
		if err := s.deps.Store.SetMappingVisibility(id, visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditMapping(r, "mapping.visibility", id+" -> "+visibility)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "visibility": visibility})
	}
}

func (s *api) deleteMapping(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Store.DeleteMapping(r.PathValue("id"))
	if errors.Is(err, store.ErrMappingInUse) {
		// The store wraps the guard with the referencing profile names when
		// a bundle (not a publish pin) holds the mapping — surface them.
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, store.ErrMappingNotFound) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditMapping(r, "mapping.deleted", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) listMappingVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requireMappingVisible(w, r) {
		return
	}
	versions, err := s.deps.Store.ListMappingVersions(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []store.MappingVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "count": len(versions)})
}

func (s *api) getMappingVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requireMappingVisible(w, r) {
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "version must be a number")
		return
	}
	version, err := s.deps.Store.GetMappingVersion(r.PathValue("id"), versionNumber)
	if errors.Is(err, store.ErrMappingNotFound) {
		writeError(w, http.StatusNotFound, "mapping version not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *api) mappingUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireMappingVisible(w, r) {
		return
	}
	usage, err := s.deps.Store.MappingUsage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if usage == nil {
		usage = []store.MappingUsage{}
	}
	profiles, err := s.bundleProfilesFor(r, "mapping", r.PathValue("id"))
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

// requireMappingVisible answers 404 for private mappings the caller may
// not see; false means the response is already written.
func (s *api) requireMappingVisible(w http.ResponseWriter, r *http.Request) bool {
	mapping, err := s.deps.Store.GetMapping(r.PathValue("id"))
	if err != nil || !viewerFrom(r).mappingVisibleTo(mapping) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return false
	}
	return true
}

func (s *api) duplicateMapping(w http.ResponseWriter, r *http.Request) {
	source, err := s.deps.Store.GetMapping(r.PathValue("id"))
	if errors.Is(err, store.ErrMappingNotFound) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !viewerFrom(r).mappingVisibleTo(source) {
		writeError(w, http.StatusNotFound, "mapping not found")
		return
	}
	// The duplicate is the caller's own new asset (D-P0c).
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateMapping(&store.Mapping{
		ID:          flow.NewID("mapping"),
		Name:        source.Name + " (copy)",
		Description: source.Description,
		Kind:        source.Kind,
		Rows:        source.Rows,
		Tags:        source.Tags,
		Owner:       source.Owner,
		Visibility:  store.VisibilityPrivate,
		CreatorID:   creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditMapping(r, "mapping.created", created.ID+" (duplicate of "+source.ID+")")
	writeJSON(w, http.StatusCreated, created)
}

// validateMappingRefs enforces deleted-mapping reference integrity at draft
// save and publish: every data.map node's mappingRef must resolve, and
// every http.request parameter-group ref must resolve to a mapping of the
// matching kind (v3 plan D4).
func (s *api) validateMappingRefs(wf *graph.Workflow, v viewer) error {
	return s.validateMappingRefsWith(wf, v, store.NewLookupCache())
}

func (s *api) validateMappingRefsWith(wf *graph.Workflow, v viewer, cache *store.LookupCache) error {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		switch n.Type {
		case "data.map":
			ref, _ := n.Config["mappingRef"].(string)
			if ref == "" {
				return errors.New("data.map node " + strconv.Quote(n.ID) + " requires a mappingRef")
			}
			mapping, err := cache.Mapping(s.deps.Store, ref)
			if err != nil {
				return errors.New("node " + strconv.Quote(n.ID) + " references missing mapping " + strconv.Quote(ref))
			}
			if !v.mappingVisibleTo(mapping) {
				return errors.New("node " + strconv.Quote(n.ID) + " references mapping " + strconv.Quote(ref) + " which is private to another user")
			}
		case "http.request":
			groupRefs, _ := n.Config["mappingRefs"].(map[string]any)
			for group, raw := range groupRefs {
				ref, _ := raw.(string)
				if ref == "" {
					continue
				}
				mapping, err := cache.Mapping(s.deps.Store, ref)
				if err != nil {
					return errors.New("node " + strconv.Quote(n.ID) + " references missing mapping " + strconv.Quote(ref))
				}
				if !v.mappingVisibleTo(mapping) {
					return errors.New("node " + strconv.Quote(n.ID) + " references mapping " + strconv.Quote(ref) + " which is private to another user")
				}
				// Every group's mapping kind equals its name (body, query,
				// headers, pathParams).
				if mapping.Kind != group {
					return errors.New("node " + strconv.Quote(n.ID) + ": mapping " + strconv.Quote(ref) +
						" is a " + mapping.Kind + " mapping but the " + group + " group needs a " + group + " mapping")
				}
			}
		}
	}
	return nil
}

// resolveMappingRefs inlines every data.map node's mapping rows into its
// config (v2 plan D13, D4-style) and returns the usage pins for the publish
// path to record.
func (s *api) resolveMappingRefs(wf *graph.Workflow) (map[string]int, error) {
	return s.resolveMappingRefsWith(wf, store.NewLookupCache())
}

func (s *api) resolveMappingRefsWith(wf *graph.Workflow, cache *store.LookupCache) (map[string]int, error) {
	return s.deps.Store.ResolveMappingRefsWith(wf, cache)
}
