package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/store"
)

// Connector API (operational-trust Campaign 4): versioned connector
// assets whose operations become node types over http.request's
// trusted pipeline. Same machinery as scripts — stale updates are 409s,
// deletes guarded by usage pins, Phase 1 visibility rules.

type connectorOperationRequest struct {
	Op           string                 `json:"op"`
	Label        string                 `json:"label"`
	Description  string                 `json:"description"`
	Method       string                 `json:"method"`
	URL          string                 `json:"url"`
	Query        map[string]string      `json:"query"`
	Headers      map[string]string      `json:"headers"`
	Body         string                 `json:"body"`
	SuccessCodes string                 `json:"successCodes"`
	ResponseMode string                 `json:"responseMode"`
	Fields       map[string]nodes.Field `json:"fields"`
}

type connectorRequest struct {
	ID          string                      `json:"id"`
	Name        string                      `json:"name"`
	Description string                      `json:"description"`
	Operations  []connectorOperationRequest `json:"operations"`
	Tags        []string                    `json:"tags"`
	Owner       string                      `json:"owner"`
	Visibility  string                      `json:"visibility"`
	Version     int                         `json:"version"`
}

func (s *api) auditConnector(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

func connectorRequestToStore(req *connectorRequest) store.Connector {
	ops := make([]store.ConnectorOperation, 0, len(req.Operations))
	for _, op := range req.Operations {
		ops = append(ops, store.ConnectorOperation{
			Op: op.Op, Label: op.Label, Description: op.Description,
			Method: op.Method, URL: op.URL,
			Query: op.Query, Headers: op.Headers, Body: op.Body,
			SuccessCodes: op.SuccessCodes, ResponseMode: op.ResponseMode,
			Fields: op.Fields,
		})
	}
	return store.Connector{
		ID: req.ID, Name: req.Name, Description: req.Description,
		Operations: ops, Tags: req.Tags, Owner: req.Owner,
		Visibility: req.Visibility,
	}
}

func (s *api) listConnectors(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListConnectorsPage(params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "connectors", page)
		return
	}
	list, err := s.deps.Store.ListConnectors()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	visible := list[:0]
	for _, c := range list {
		if v.canSee(c.Visibility, c.CreatorID) {
			visible = append(visible, c)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"connectors": visible, "count": len(visible)})
}

func (s *api) getConnector(w http.ResponseWriter, r *http.Request) {
	c, err := s.deps.Store.GetConnector(r.PathValue("id"))
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	if !viewerFrom(r).canSee(c.Visibility, c.CreatorID) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connector": c})
}

func storeConnectorError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrConnectorNotFound):
		writeError(w, http.StatusNotFound, "connector not found")
	case errors.Is(err, store.ErrStaleConnector):
		writeError(w, http.StatusConflict, "connector was modified concurrently")
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *api) createConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	d := connectorRequestToStore(&req)
	creator, _ := actorIdentity(r)
	d.CreatorID = creator
	if d.ID == "" {
		d.ID = flow.NewID("connector")
	}
	if d.Visibility == "" {
		d.Visibility = store.VisibilityPrivate
	}
	if !store.ValidVisibility(d.Visibility) {
		writeError(w, http.StatusBadRequest, "visibility must be private or shared")
		return
	}
	created, err := s.deps.Store.CreateConnector(&d, actorFor(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditConnector(r, "connector.create", created.ID)
	writeJSON(w, http.StatusOK, map[string]any{"connector": created})
}

// duplicateConnector answers POST /api/connectors/{id}/duplicate: a
// private copy of the current version under a new id. Anyone who can
// see the source may copy it; the copy belongs to the caller.
func (s *api) duplicateConnector(w http.ResponseWriter, r *http.Request) {
	source, err := s.deps.Store.GetConnector(r.PathValue("id"))
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	if !viewerFrom(r).canSee(source.Visibility, source.CreatorID) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateConnector(&store.Connector{
		ID:          flow.NewID("connector"),
		Name:        source.Name + " (copy)",
		Description: source.Description,
		Operations:  source.Operations,
		Tags:        source.Tags,
		Owner:       source.Owner,
		Visibility:  store.VisibilityPrivate,
		CreatorID:   creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditConnector(r, "connector.create", created.ID+" (duplicate of "+source.ID+")")
	writeJSON(w, http.StatusCreated, map[string]any{"connector": created})
}

func (s *api) updateConnector(w http.ResponseWriter, r *http.Request) {
	var req connectorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	id := r.PathValue("id")
	existing, err := s.deps.Store.GetConnector(id)
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	v := viewerFrom(r)
	if !v.canSee(existing.Visibility, existing.CreatorID) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	if !v.canGovern(existing.CreatorID) {
		writeError(w, http.StatusForbidden, "only the creator or an admin can edit this connector")
		return
	}
	d := connectorRequestToStore(&req)
	d.ID = id
	if d.Visibility == "" {
		d.Visibility = existing.Visibility
	}
	updated, err := s.deps.Store.UpdateConnector(&d, req.Version, actorFor(r))
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	s.auditConnector(r, "connector.update", id+" v"+strconv.Itoa(updated.Version))
	writeJSON(w, http.StatusOK, map[string]any{"connector": updated})
}

func (s *api) deleteConnector(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	existing, err := s.deps.Store.GetConnector(id)
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	v := viewerFrom(r)
	if !v.canSee(existing.Visibility, existing.CreatorID) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	if !v.canGovern(existing.CreatorID) {
		writeError(w, http.StatusForbidden, "only the creator or an admin can delete this connector")
		return
	}
	if err := s.deps.Store.DeleteConnector(id); err != nil {
		if errors.Is(err, store.ErrConnectorInUse) {
			writeError(w, http.StatusConflict, "connector is referenced by published workflows")
			return
		}
		storeConnectorError(w, err)
		return
	}
	s.auditConnector(r, "connector.delete", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *api) listConnectorVersions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.deps.Store.GetConnector(id)
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	if !viewerFrom(r).canSee(c.Visibility, c.CreatorID) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	versions, err := s.deps.Store.ListConnectorVersions(id)
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions})
}

func (s *api) connectorUsage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.deps.Store.GetConnector(id)
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	if !viewerFrom(r).canSee(c.Visibility, c.CreatorID) {
		writeError(w, http.StatusNotFound, "connector not found")
		return
	}
	usage, err := s.deps.Store.ConnectorUsage(id)
	if err != nil {
		storeConnectorError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": usage})
}

func (s *api) shareConnector(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		existing, err := s.deps.Store.GetConnector(id)
		if err != nil {
			storeConnectorError(w, err)
			return
		}
		v := viewerFrom(r)
		if !v.canSee(existing.Visibility, existing.CreatorID) {
			writeError(w, http.StatusNotFound, "connector not found")
			return
		}
		if !v.canGovern(existing.CreatorID) {
			writeError(w, http.StatusForbidden, "only the creator or an admin can change sharing")
			return
		}
		req := connectorRequest{
			ID: existing.ID, Name: existing.Name, Description: existing.Description,
			Operations: operationsToRequest(existing.Operations),
			Tags:       existing.Tags, Owner: existing.Owner, Visibility: visibility,
			Version: existing.Version,
		}
		d := connectorRequestToStore(&req)
		if _, err := s.deps.Store.UpdateConnector(&d, existing.Version, actorFor(r)); err != nil {
			storeConnectorError(w, err)
			return
		}
		s.auditConnector(r, "connector.share", id+" "+visibility)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func operationsToRequest(ops []store.ConnectorOperation) []connectorOperationRequest {
	out := make([]connectorOperationRequest, 0, len(ops))
	for _, op := range ops {
		out = append(out, connectorOperationRequest{
			Op: op.Op, Label: op.Label, Description: op.Description,
			Method: op.Method, URL: op.URL, Query: op.Query, Headers: op.Headers,
			Body: op.Body, SuccessCodes: op.SuccessCodes, ResponseMode: op.ResponseMode,
			Fields: op.Fields,
		})
	}
	return out
}

// validateConnectorNodes names every connector.* node referencing a
// private connector the viewer cannot see (Phase 1 rule for a new asset
// family), and resolves each node's connector to prove it exists.
func (s *api) validateConnectorNodes(wf *graph.Workflow, v viewer) error {
	return s.validateConnectorNodesWith(wf, v, store.NewLookupCache())
}

func (s *api) validateConnectorNodesWith(wf *graph.Workflow, v viewer, cache *store.LookupCache) error {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	sawConnector := false
	for i := range wf.Nodes {
		if !strings.HasPrefix(wf.Nodes[i].Type, nodes.ConnectorTypePrefix) {
			continue
		}
		sawConnector = true
		rest := strings.TrimPrefix(wf.Nodes[i].Type, nodes.ConnectorTypePrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 {
			return fmt.Errorf("node %q: connector node type %q must be connector.<id>.<op>", wf.Nodes[i].ID, wf.Nodes[i].Type)
		}
		c, err := cache.Connector(s.deps.Store, parts[0])
		if err != nil {
			return fmt.Errorf("node %q connector: %w", wf.Nodes[i].ID, err)
		}
		if !v.canSee(c.Visibility, c.CreatorID) {
			return fmt.Errorf("node %q: connector %q is private", wf.Nodes[i].ID, parts[0])
		}
	}
	if sawConnector {
		if _, err := s.deps.Store.ResolveConnectorRefsWith(wf, cache); err != nil {
			return err
		}
	}
	return nil
}

// connectorExtraDefs builds the graph.ExtraDefs resolver from the
// current connector versions — the merge used by workflow validation
// and the node-types listing.
func (s *api) connectorExtraDefs() graph.ExtraDefs {
	return s.connectorExtraDefsWith(store.NewLookupCache())
}

func (s *api) connectorExtraDefsWith(lookup *store.LookupCache) graph.ExtraDefs {
	if lookup == nil {
		lookup = store.NewLookupCache()
	}
	cache := map[string]nodes.Definition{}
	return func(nodeType string) (nodes.Definition, bool) {
		if def, ok := cache[nodeType]; ok {
			return def, def.Type != ""
		}
		if !strings.HasPrefix(nodeType, nodes.ConnectorTypePrefix) {
			return nodes.Definition{}, false
		}
		rest := strings.TrimPrefix(nodeType, nodes.ConnectorTypePrefix)
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 {
			return nodes.Definition{}, false
		}
		c, err := lookup.Connector(s.deps.Store, parts[0])
		if err != nil {
			return nodes.Definition{}, false
		}
		for _, def := range store.ConnectorNodeTypes(c) {
			cache[def.Type] = def
		}
		def, ok := cache[nodeType]
		return def, ok
	}
}
