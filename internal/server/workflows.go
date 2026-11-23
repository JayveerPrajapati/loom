package server

import (
	"errors"
	"fmt"
	"io"
	"loom/internal/graph"
	"loom/internal/pyenv"
	"loom/internal/runbook"
	"loom/internal/store"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func (s *api) listWorkflows(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if params.Paged {
		page, err := s.deps.Store.ListWorkflowsPage(params.Query, params.Cursor, params.Limit)
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "workflows", page)
		return
	}
	list, err := s.deps.Store.ListWorkflows()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []store.Summary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"workflows": list, "count": len(list)})
}

func (s *api) createWorkflow(w http.ResponseWriter, r *http.Request) {
	var wf graph.Workflow
	if err := readJSON(r, &wf); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	issues, err := s.validateEffectiveWorkflow(&wf, viewerFrom(r))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_profile", Node: "", Message: err.Error()}}})
		return
	}
	if hasErrorIssues(issues) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": issues})
		return
	}
	s.webhookPathMu.Lock()
	if err := s.validateWebhookPaths(&wf); err != nil {
		s.webhookPathMu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.deps.Store.SaveWorkflow(&wf); err != nil {
		s.webhookPathMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.webhookPathMu.Unlock()
	s.reload()
	writeJSON(w, http.StatusCreated, wf)
}

func (s *api) getWorkflow(w http.ResponseWriter, r *http.Request) {
	wf, err := s.deps.Store.GetWorkflow(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workflowForResponse(r, wf))
}

func (s *api) workflowState(w http.ResponseWriter, r *http.Request) {
	state, err := s.deps.Store.WorkflowState(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *api) publishWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := s.deps.Store.GetWorkflow(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	publishID := r.URL.Query().Get("publishId")
	if publishID != "" && !publishIDPattern.MatchString(publishID) {
		writeError(w, http.StatusBadRequest, errInvalidPublishID.Error())
		return
	}
	progress := s.publishProgresses.start(publishID, id, viewerFrom(r).userID)
	result := s.publishWorkflowDoc(w, r, wf, progress)
	if result == nil {
		s.publishProgresses.finish(progress, errors.New("publish failed"))
		return
	}
	s.publishProgresses.finish(progress, nil)
	writeJSON(w, http.StatusCreated, result)
}

// publishStatus answers GET /api/workflows/{id}/publish/status
// (audit 4E): the polling view of a synchronous publish. Editors see
// their own publishes; admins may inspect any. The route's editor gate
// keeps viewers out entirely.
func (s *api) publishStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	publishID := r.URL.Query().Get("publishId")
	snap, ok := s.publishProgresses.snapshot(publishID)
	if !ok || snap.WorkflowID != id {
		writeError(w, http.StatusNotFound, "publish progress not found")
		return
	}
	viewer := viewerFrom(r)
	if !viewer.isAdmin && viewer.userID != snap.Actor {
		writeError(w, http.StatusForbidden, "not your publish")
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// publishWorkflowDoc validates, resolves and publishes an in-memory
// workflow doc (the shared core of publish and upgrade-apply, plan §18
// D2/D4/D5 + UX Phase 2 D2-6). On failure it writes the error itself
// and returns nil; on success it returns the response payload.
func (s *api) publishWorkflowDoc(w http.ResponseWriter, r *http.Request, wf *graph.Workflow, progress *publishProgressRecord) map[string]any {
	cache := store.NewLookupCache()
	s.publishProgresses.phase(progress, "validate")
	issues, err := s.validateEffectiveWorkflowWith(wf, viewerFrom(r), cache)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_profile", Node: "", Message: err.Error()}}})
		return nil
	}
	if hasErrorIssues(issues) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": issues})
		return nil
	}
	s.publishProgresses.phase(progress, "resolve")
	// Inline resolution (plan §18 D4): the published snapshot carries the
	// resolved config, so runs and replays never read profiles at
	// execution time.
	profileRefs, err := s.resolveProfileRefsWith(wf, cache)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_profile", Node: "", Message: err.Error()}}})
		return nil
	}
	// Schema resolution (v2 plan, D4-style): the snapshot carries the
	// resolved contracts inline, so runs, webhooks and replays never read
	// the contract store at execution time.
	schemaRefs, err := s.resolveSchemaRefsWith(wf, cache)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_schema", Node: "", Message: err.Error()}}})
		return nil
	}
	// Mapping resolution (v2 plan D13): data.map nodes carry the resolved
	// rows inline so runs and replays never read the mapping store.
	mappingRefs, err := s.resolveMappingRefsWith(wf, cache)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_mapping", Node: "", Message: err.Error()}}})
		return nil
	}
	// Script resolution (Phase 4 D4-1, after profile refs so bundle
	// scripts land first): code nodes carry the pinned source, merged
	// requirements and script runtime defaults inline so runs and replays
	// never read the script store.
	scriptRefs, err := s.deps.Store.ResolveScriptRefsWith(wf, cache)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_script", Node: "", Message: err.Error()}}})
		return nil
	}
	// Connector resolution (Campaign 4 D10-4): connector nodes carry the
	// pinned identity and the request template inline so runs and
	// replays never read the connector store.
	connectorRefs, err := s.deps.Store.ResolveConnectorRefsWith(wf, cache)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_connector", Node: "", Message: err.Error()}}})
		return nil
	}
	// Catalog defaults are the final effective-value layer. They run after
	// script and mapping resolution so an asset's richer runtime defaults
	// win over the generic catalog fallback.
	s.deps.Store.ApplyCatalogDefaults(wf)
	s.publishProgresses.phase(progress, "environments")
	// Python dependency environments are built at publish time — the only
	// phase allowed to install packages. Runs never touch the network.
	// Each distinct node requirement set gets its own environment
	// (Phase 5): the response reports what was built (key, requirements,
	// size) so the operator sees the environment hash on publish.
	requirements, err := pythonRequirements(wf)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, err.Error())
		return nil
	}
	var builtEnvKeys []string
	for _, set := range requirements {
		if s.deps.BuildEnvs == nil {
			writeJSON(w, http.StatusUnprocessableEntity,
				"this server cannot build Python environments: start serve with --enable-subprocess-nodes (and uv on PATH) to publish workflows with code.python requirements")
			return nil
		}
		if err := s.deps.BuildEnvs(set); err != nil {
			writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("build python environments: %v", err))
			return nil
		}
		builtEnvKeys = append(builtEnvKeys, pyenv.Key(set))
	}
	s.publishProgresses.phase(progress, "publish")
	s.webhookPathMu.Lock()
	if err := s.validateWebhookPaths(wf); err != nil {
		s.webhookPathMu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return nil
	}
	version, err := s.deps.Store.PublishWorkflow(wf)
	if err != nil {
		s.webhookPathMu.Unlock()
		writeJSON(w, http.StatusInternalServerError, err.Error())
		return nil
	}
	s.webhookPathMu.Unlock()
	s.publishProgresses.phase(progress, "finalize")
	// The usage index records which workflow version pinned which profile
	// version (plan §18 D5): the delete guard and usage-count source.
	for profileID, profileVersion := range profileRefs {
		if err := s.deps.Store.RecordConfigProfileUsage(profileID, profileVersion, wf.ID, version.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("record profile usage: %v", err))
			return nil
		}
	}
	for schemaID, schemaVersion := range schemaRefs {
		if err := s.deps.Store.RecordDataSchemaUsage(schemaID, schemaVersion, wf.ID, version.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("record schema usage: %v", err))
			return nil
		}
	}
	for mappingID, mappingVersion := range mappingRefs {
		if err := s.deps.Store.RecordMappingUsage(mappingID, mappingVersion, wf.ID, version.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("record mapping usage: %v", err))
			return nil
		}
	}
	for scriptID, scriptVersion := range scriptRefs {
		if err := s.deps.Store.RecordScriptUsage(scriptID, scriptVersion, wf.ID, version.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("record script usage: %v", err))
			return nil
		}
	}
	for connectorID, connectorVersion := range connectorRefs {
		if err := s.deps.Store.RecordConnectorUsage(connectorID, connectorVersion, wf.ID, version.Version); err != nil {
			writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("record connector usage: %v", err))
			return nil
		}
	}
	detail := fmt.Sprintf("%s v%d profiles=%d schemas=%d mappings=%d scripts=%d connectors=%d",
		wf.ID, version.Version, len(profileRefs), len(schemaRefs), len(mappingRefs), len(scriptRefs), len(connectorRefs))
	if private := s.privatePublishedAssets(r, cache, profileRefs, schemaRefs, mappingRefs, scriptRefs, connectorRefs); len(private) > 0 {
		detail += " private=" + strings.Join(private, ",")
	}
	s.auditWorkflow(r, "workflow.published", detail)
	s.reload()
	response := map[string]any{"workflow": wf, "version": version}
	// Phase 6 D6-4: mapping-contract compatibility warnings — recorded,
	// never blocking. The editor surfaces them on publish.
	if warnings := mappingSchemaWarnings(wf); len(warnings) > 0 {
		response["mappingWarnings"] = warnings
	}
	// Phase 5: the publish response carries the environments it built
	// (hash, requirements, size) when the server can report them.
	if len(builtEnvKeys) > 0 && s.deps.ListPythonEnvs != nil {
		if envs, err := s.deps.ListPythonEnvs(); err == nil {
			wanted := map[string]bool{}
			for _, key := range builtEnvKeys {
				wanted[key] = true
			}
			built := make([]pyenv.EnvInfo, 0, len(builtEnvKeys))
			for _, env := range envs {
				if wanted[env.Key] {
					built = append(built, env)
				}
			}
			response["pythonEnvs"] = built
		}
	}
	return response
}

// upgradePreviewWorkflow answers POST /api/workflows/{id}/upgrade-preview
// (UX Phase 2 D2-6): the per-key plan for re-resolving the workflow's
// profile refs against the profiles' current versions, with effective
// validation of the upgraded doc. {"apply": true} applies the upgrade to
// the draft and publishes it in one step.
func (s *api) upgradePreviewWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	wf, err := s.deps.Store.GetWorkflow(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var body struct {
		Apply bool `json:"apply"`
	}
	_ = readJSON(r, &body)
	upgraded, plan, err := s.deps.Store.UpgradeWorkflowProfiles(wf)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_profile", Node: "", Message: err.Error()}}})
		return
	}
	if !body.Apply {
		issues, err := s.validateEffectiveWorkflow(upgraded, viewerFrom(r))
		if err != nil {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_profile", Node: "", Message: err.Error()}}})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"applied": false, "plan": plan, "issues": issues})
		return
	}
	result := s.publishWorkflowDoc(w, r, upgraded, nil)
	if result == nil {
		return
	}
	if version, ok := result["version"].(store.WorkflowVersion); ok {
		s.auditWorkflow(r, "workflow.profile_upgrade",
			fmt.Sprintf("%s v%d changes=%d", wf.ID, version.Version, len(plan)))
	}
	result["applied"] = true
	result["plan"] = plan
	writeJSON(w, http.StatusCreated, result)
}
func (s *api) listWorkflowVersions(w http.ResponseWriter, r *http.Request) {
	versions, err := s.deps.Store.ListWorkflowVersions(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []store.WorkflowVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "count": len(versions)})
}

func (s *api) getWorkflowVersion(w http.ResponseWriter, r *http.Request) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "version must be a number")
		return
	}
	wf, err := s.deps.Store.GetWorkflowVersion(r.PathValue("id"), version)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workflowForResponse(r, wf))
}

func (s *api) workflowRunbook(w http.ResponseWriter, r *http.Request) {
	wf, err := s.deps.Store.GetWorkflow(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", wf.ID+"-runbook.md"))
	_, _ = io.WriteString(w, runbook.Render(wf))
}

func (s *api) putWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var wf graph.Workflow
	if err := readJSON(r, &wf); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if wf.ID != id {
		writeError(w, http.StatusBadRequest, "workflow id in body does not match path")
		return
	}
	previous, _ := s.deps.Store.GetWorkflow(id)
	issues, err := s.validateEffectiveWorkflow(&wf, viewerFrom(r))
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": []graph.Issue{{Code: "missing_profile", Node: "", Message: err.Error()}}})
		return
	}
	if hasErrorIssues(issues) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": issues})
		return
	}
	s.webhookPathMu.Lock()
	if err := s.validateWebhookPaths(&wf); err != nil {
		s.webhookPathMu.Unlock()
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.deps.Store.SaveWorkflow(&wf); err != nil {
		s.webhookPathMu.Unlock()
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.webhookPathMu.Unlock()
	s.reload()
	if previous != nil {
		if detached := detachedProfileRefs(previous, &wf); len(detached) > 0 {
			s.auditWorkflow(r, "workflow.profile_detached", id+" nodes="+strings.Join(detached, ","))
		}
	}
	writeJSON(w, http.StatusOK, wf)
}

func (s *api) deleteWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	count, err := s.deps.Store.CountJobsByTemplate(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if count > 0 {
		writeError(w, http.StatusConflict, "delete the jobs using this template first")
		return
	}
	if err := s.deps.Store.DeleteWorkflow(id); err != nil {
		if errors.Is(err, store.ErrWorkflowNotFound) {
			writeError(w, http.StatusNotFound, fmt.Sprintf("workflow %q not found", id))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditWorkflow(r, "workflow.deleted", id)
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) auditWorkflow(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

// privatePublishedAssets names the caller-visible private assets that a
// publish resolves into the immutable workflow version. Foreign private
// assets never reach this point (validation rejects them).
func (s *api) privatePublishedAssets(r *http.Request, cache *store.LookupCache,
	profileRefs, schemaRefs, mappingRefs, scriptRefs, connectorRefs map[string]int) []string {
	viewer := viewerFrom(r)
	private := make([]string, 0)
	for id := range profileRefs {
		if item, err := cache.ConfigProfile(s.deps.Store, id); err == nil &&
			item.Visibility == store.VisibilityPrivate && viewer.canSee(item.Visibility, item.CreatorID) {
			private = append(private, "profile:"+id)
		}
	}
	for id := range schemaRefs {
		if item, err := cache.DataSchema(s.deps.Store, id); err == nil &&
			item.Visibility == store.VisibilityPrivate && viewer.canSee(item.Visibility, item.CreatorID) {
			private = append(private, "schema:"+id)
		}
	}
	for id := range mappingRefs {
		if item, err := cache.Mapping(s.deps.Store, id); err == nil &&
			item.Visibility == store.VisibilityPrivate && viewer.canSee(item.Visibility, item.CreatorID) {
			private = append(private, "mapping:"+id)
		}
	}
	for id := range scriptRefs {
		if item, err := cache.Script(s.deps.Store, id); err == nil &&
			item.Visibility == store.VisibilityPrivate && viewer.canSee(item.Visibility, item.CreatorID) {
			private = append(private, "script:"+id)
		}
	}
	for id := range connectorRefs {
		if item, err := cache.Connector(s.deps.Store, id); err == nil &&
			item.Visibility == store.VisibilityPrivate && viewer.canSee(item.Visibility, item.CreatorID) {
			private = append(private, "connector:"+id)
		}
	}
	sort.Strings(private)
	return private
}

func detachedProfileRefs(before, after *graph.Workflow) []string {
	beforeRefs := map[string]string{}
	for _, node := range before.Nodes {
		if node.ProfileRef != "" {
			beforeRefs[node.ID] = node.ProfileRef
		}
	}
	afterRefs := map[string]string{}
	for _, node := range after.Nodes {
		if node.ProfileRef != "" {
			afterRefs[node.ID] = node.ProfileRef
		}
	}
	var detached []string
	for id, ref := range beforeRefs {
		if afterRefs[id] != ref {
			detached = append(detached, id+":"+ref)
		}
	}
	sort.Strings(detached)
	return detached
}

// pythonRequirements collects the distinct requirement sets of the
// workflow's code.python nodes (after resolution), each sorted. Each
// set becomes its own hash-keyed environment: a node at exec time
// references its own set's key, so publishing must build every distinct
// set — one union env would leave per-node keys unbuilt (Phase 5 fix).
func pythonRequirements(wf *graph.Workflow) ([][]string, error) {
	seen := map[string]bool{}
	var sets [][]string
	for _, node := range wf.Nodes {
		if node.Type != "code.python" {
			continue
		}
		requirements, ok := node.Config["requirements"].([]any)
		if !ok || len(requirements) == 0 {
			continue
		}
		set := make([]string, 0, len(requirements))
		for _, raw := range requirements {
			req := strings.TrimSpace(fmt.Sprint(raw))
			if req != "" {
				set = append(set, req)
			}
		}
		if len(set) == 0 {
			continue
		}
		sort.Strings(set)
		id := strings.Join(set, "\x00")
		if !seen[id] {
			seen[id] = true
			sets = append(sets, set)
		}
	}
	return sets, nil
}
