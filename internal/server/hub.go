package server

import (
	"net/http"
	"strings"

	"loom/internal/nodes"
	"loom/internal/store"
)

// Hub API (Configuration Hub v3 plan D1): the node-type-centric views
// over the profiles, contracts and mappings. One page per node type
// (/hub/:nodeType) plus a landing overview (/hub) with per-type counts.
// Read-only composition — writes go through the existing entity
// endpoints, so the hub can never bypass their RBAC or versioning.

// hubOverview answers GET /api/hub: counts for the landing page. The
// per-node-type mapping counts are derived client-side from the kind
// counts plus the catalog's MappingKinds, so the server never duplicates
// that mapping. Counts are visibility-filtered (Phase 1 D-P0a): the
// landing page shows what the caller can actually open.
func (s *api) hubOverview(w http.ResponseWriter, r *http.Request) {
	v := viewerFrom(r)
	profiles, err := s.deps.Store.ListConfigProfiles()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	profileCounts := map[string]int{}
	for i := range profiles {
		if v.profileVisibleTo(&profiles[i]) {
			profileCounts[profiles[i].NodeType]++
		}
	}
	mappings, err := s.deps.Store.ListMappings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mappingCounts := map[string]int{}
	for i := range mappings {
		if v.mappingVisibleTo(&mappings[i]) {
			mappingCounts[mappings[i].Kind]++
		}
	}
	schemas, err := s.deps.Store.ListDataSchemas()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	schemaCount := 0
	for i := range schemas {
		if v.schemaVisibleTo(&schemas[i]) {
			schemaCount++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"profiles":     profileCounts,
		"mappingKinds": mappingCounts,
		"schemas":      schemaCount,
	})
}

// hubNodeType answers GET /api/hub/{nodeType}: everything a node type's
// hub page needs in one round-trip — the catalog definition, the type's
// profiles, the mappings of the kinds it consumes, and the data contracts
// (all of them: contracts are not node-typed; schemaSlot says which ref
// slot this type uses — trigger.webhook validates input, everything else
// records output verdicts). Every list is visibility-filtered (D-P0a).
// Pass ?profiles=0 to skip the profile rows; the hub page then reads them
// from the paged /api/config-profiles?nodeType=… endpoint.
func (s *api) hubNodeType(w http.ResponseWriter, r *http.Request) {
	nodeType := r.PathValue("nodeType")
	def, ok := nodes.Get(nodeType)
	if !ok {
		writeError(w, http.StatusNotFound, "unknown node type "+nodeType)
		return
	}
	v := viewerFrom(r)
	profiles := []store.ConfigProfile{}
	if r.URL.Query().Get("profiles") != "0" {
		list, err := s.deps.Store.ListConfigProfilesForType(nodeType)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		profiles = filterProfilesVisible(list, v)
	}
	mappings, err := s.deps.Store.ListMappingsByKinds(def.MappingKinds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mappings = filterMappingsVisible(mappings, v)
	schemas, err := s.deps.Store.ListDataSchemas()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	schemas = filterSchemasVisible(schemas, v)
	if schemas == nil {
		schemas = []store.DataSchema{}
	}
	slot := "output"
	if nodeType == "trigger.webhook" {
		slot = "input"
	}
	// A nil slice (types without mappings) marshals as JSON null; the
	// page expects a list.
	kinds := def.MappingKinds
	if kinds == nil {
		kinds = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"nodeType":     nodeType,
		"mappingKinds": kinds,
		"profiles":     profiles,
		"mappings":     mappings,
		"schemas":      schemas,
		"schemaSlot":   slot,
	})
}

func filterProfilesVisible(list []store.ConfigProfile, v viewer) []store.ConfigProfile {
	visible := list[:0]
	for i := range list {
		if v.profileVisibleTo(&list[i]) {
			visible = append(visible, list[i])
		}
	}
	return visible
}

func filterMappingsVisible(list []store.Mapping, v viewer) []store.Mapping {
	visible := list[:0]
	for i := range list {
		if v.mappingVisibleTo(&list[i]) {
			visible = append(visible, list[i])
		}
	}
	return visible
}

func filterSchemasVisible(list []store.DataSchema, v viewer) []store.DataSchema {
	visible := list[:0]
	for i := range list {
		if v.schemaVisibleTo(&list[i]) {
			visible = append(visible, list[i])
		}
	}
	return visible
}

// listMappingsFiltered answers GET /api/mappings?kind=…&nodeType=… —
// the entity page's kind filter (v3 plan D4) and the merged hub page's
// per-type stat-row link (v4.1 E2: nodeType narrows to the kinds that
// type consumes). Without params it behaves exactly like the unfiltered
// list.
func (s *api) listMappingsFiltered(w http.ResponseWriter, r *http.Request) {
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	nodeType := strings.TrimSpace(r.URL.Query().Get("nodeType"))
	if kind == "" && nodeType == "" {
		s.listMappings(w, r)
		return
	}
	kinds := []string{}
	if nodeType != "" {
		def, ok := nodes.Get(nodeType)
		if !ok {
			writeError(w, http.StatusBadRequest, "unknown node type "+nodeType)
			return
		}
		kinds = def.MappingKinds
		if kind != "" {
			known := false
			for _, k := range kinds {
				if k == kind {
					known = true
					break
				}
			}
			if !known {
				writeError(w, http.StatusBadRequest, "kind "+kind+" is not used by node type "+nodeType)
				return
			}
			kinds = []string{kind}
		}
	} else {
		if !store.ValidMappingKind(kind) {
			writeError(w, http.StatusBadRequest, "unknown mapping kind "+kind)
			return
		}
		kinds = []string{kind}
	}
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListMappingsPage(kinds, params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "mappings", page)
		return
	}
	list, err := s.deps.Store.ListMappingsByKinds(kinds)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list = filterMappingsVisible(list, v)
	writeJSON(w, http.StatusOK, map[string]any{"mappings": list, "count": len(list)})
}

// listProfilesFiltered answers GET /api/config-profiles?nodeType=... —
// the entity page's node-type filter (v3 plan D1).
func (s *api) listProfilesFiltered(w http.ResponseWriter, r *http.Request) {
	nodeType := strings.TrimSpace(r.URL.Query().Get("nodeType"))
	if nodeType == "" {
		s.listProfiles(w, r)
		return
	}
	if _, valid := nodes.Get(nodeType); !valid {
		writeError(w, http.StatusBadRequest, "unknown node type "+nodeType)
		return
	}
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListConfigProfilesPage(nodeType, params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "profiles", page)
		return
	}
	list, err := s.deps.Store.ListConfigProfilesForType(nodeType)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	list = filterProfilesVisible(list, v)
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list, "count": len(list)})
}
