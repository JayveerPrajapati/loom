package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"loom/internal/expr"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/pyenv"
	"loom/internal/store"
)

// Script API (Profile-Centric UX Phase 4): reusable, versioned source
// referenced by code.run / code.python nodes and composed by profile
// bundles. Same machinery as mappings and profiles — stale updates are
// 409s, deletes are guarded by usage pins and bundle references, and
// visibility follows the Phase 1 rules (private = creator + admin).

type scriptRequest struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Language     string   `json:"language"`
	Source       string   `json:"source"`
	Requirements []string `json:"requirements"`
	// TimeoutSeconds/MaxMemoryMB are runtime defaults applied to a
	// referencing node only when the node sets none of its own (D4-4).
	TimeoutSeconds int      `json:"timeoutSeconds"`
	MaxMemoryMB    int      `json:"maxMemoryMB"`
	Tags           []string `json:"tags"`
	Owner          string   `json:"owner"`
	Visibility     string   `json:"visibility"`
	// Version is the caller's current version; a stale value is a 409.
	Version int `json:"version"`
}

func (s *api) auditScript(r *http.Request, eventType, detail string) {
	id, label := actorIdentity(r)
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type:   eventType,
		UserID: id,
		Actor:  label,
		IP:     clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: detail,
	})
}

// validateScriptBody enforces the language, source, runtime bounds and
// requirement rules for create and update alike. The per-language bounds
// mirror the node forms so a script's defaults are always executable.
func (s *api) validateScriptBody(req *scriptRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		return errors.New("name is required")
	}
	req.Language = strings.TrimSpace(req.Language)
	if !store.ValidScriptLanguage(req.Language) {
		return errors.New("language must be javascript or python")
	}
	if strings.TrimSpace(req.Source) == "" {
		return errors.New("source is required")
	}
	if err := store.CheckScriptRequirements(req.Language, req.Requirements); err != nil {
		return err
	}
	maxTimeout := 300
	maxMemory := 0
	if req.Language == store.ScriptLanguagePython {
		maxTimeout = 900
		maxMemory = 8192
	}
	if req.TimeoutSeconds < 0 || req.TimeoutSeconds > maxTimeout {
		return fmt.Errorf("timeoutSeconds must be between 0 and %d (0 = the node's own default)", maxTimeout)
	}
	if maxMemory > 0 {
		if req.MaxMemoryMB < 0 || req.MaxMemoryMB > maxMemory {
			return fmt.Errorf("maxMemoryMB must be between 0 and %d (0 = the node's own default)", maxMemory)
		}
	} else if req.MaxMemoryMB != 0 {
		return errors.New("maxMemoryMB is a python-script field")
	}
	// Tags are free-form governance labels; deduplicated and capped like
	// the other assets.
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
	// New assets default to private (D-P0c); the CLI import path
	// (store-direct) explicitly passes shared.
	if req.Visibility == "" {
		req.Visibility = store.VisibilityPrivate
	}
	if !store.ValidVisibility(req.Visibility) {
		return errors.New("visibility must be private or shared")
	}
	return nil
}

func (s *api) listScripts(w http.ResponseWriter, r *http.Request) {
	params, err := listPageParamsFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	language := r.URL.Query().Get("language")
	if language != "" && !store.ValidScriptLanguage(language) {
		writeError(w, http.StatusBadRequest, "unknown language "+strconv.Quote(language))
		return
	}
	v := viewerFrom(r)
	if params.Paged {
		page, err := s.deps.Store.ListScriptsPage(language, params.Query, params.Cursor, params.Limit,
			store.VisibilityScope{Admin: v.isAdmin, UserID: v.userID})
		if err != nil {
			writeListPageError(w, err)
			return
		}
		writeListPage(w, params, "scripts", page)
		return
	}
	var list []store.Script
	if language != "" {
		list, err = s.deps.Store.ListScriptsByLanguage(language)
		if err != nil {
			writeError(w, http.StatusBadRequest, "unknown language "+strconv.Quote(language))
			return
		}
	} else {
		list, err = s.deps.Store.ListScripts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	visible := list[:0]
	for _, item := range list {
		if v.scriptVisibleTo(&item) {
			visible = append(visible, item)
		}
	}
	if visible == nil {
		visible = []store.Script{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"scripts": visible, "count": len(visible)})
}

func (s *api) createScript(w http.ResponseWriter, r *http.Request) {
	var req scriptRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateScriptBody(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.ID == "" {
		req.ID = flow.NewID("script")
	}
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateScript(&store.Script{
		ID: req.ID, Name: req.Name, Description: req.Description, Language: req.Language,
		Source: req.Source, Requirements: req.Requirements,
		TimeoutSeconds: req.TimeoutSeconds, MaxMemoryMB: req.MaxMemoryMB,
		Tags: req.Tags, Owner: req.Owner,
		Visibility: req.Visibility, CreatorID: creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.auditScript(r, "script.created", req.ID+" ("+req.Language+")")
	writeJSON(w, http.StatusCreated, created)
}

func (s *api) getScript(w http.ResponseWriter, r *http.Request) {
	script, err := s.deps.Store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !viewerFrom(r).scriptVisibleTo(script) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	writeJSON(w, http.StatusOK, script)
}

func (s *api) updateScript(w http.ResponseWriter, r *http.Request) {
	var req scriptRequest
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Version < 1 {
		writeError(w, http.StatusBadRequest, "version is required (the script version this update is based on)")
		return
	}
	id := r.PathValue("id")
	current, err := s.deps.Store.GetScript(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !viewerFrom(r).scriptVisibleTo(current) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !viewerFrom(r).canGovern(current.CreatorID) {
		writeError(w, http.StatusForbidden, "only the creator or an admin can edit this script")
		return
	}
	// Language is immutable; an update that omits it (the web form
	// disables the select) keeps the current one.
	if req.Language == "" {
		req.Language = current.Language
	}
	if err := s.validateScriptBody(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	updated, err := s.deps.Store.UpdateScript(&store.Script{
		ID: id, Name: req.Name, Description: req.Description, Language: req.Language,
		Source: req.Source, Requirements: req.Requirements,
		TimeoutSeconds: req.TimeoutSeconds, MaxMemoryMB: req.MaxMemoryMB,
		Tags: req.Tags, Owner: req.Owner,
	}, req.Version, actorFor(r))
	if errors.Is(err, store.ErrStaleScript) {
		writeError(w, http.StatusConflict, "script was modified concurrently — reload and retry")
		return
	}
	if errors.Is(err, store.ErrScriptNotFound) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Governance is not content: a visibility change applies without
	// forking a version (D-P0b).
	if req.Visibility != "" && req.Visibility != current.Visibility {
		if err := s.deps.Store.SetScriptVisibility(id, req.Visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		updated.Visibility = req.Visibility
	}
	s.auditScript(r, "script.updated", updated.ID+" -> v"+strconv.Itoa(updated.Version))
	writeJSON(w, http.StatusOK, updated)
}

// shareScript answers POST /api/scripts/{id}/share|unshare: admins may
// govern any script; the creator may govern their own.
func (s *api) shareScript(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		script, err := s.deps.Store.GetScript(id)
		if err != nil {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		if !viewerFrom(r).scriptVisibleTo(script) {
			writeError(w, http.StatusNotFound, "script not found")
			return
		}
		if !viewerFrom(r).canGovern(script.CreatorID) {
			writeError(w, http.StatusForbidden, "only the creator or an admin can change this script's visibility")
			return
		}
		if err := s.deps.Store.SetScriptVisibility(id, visibility); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.auditScript(r, "script.visibility", id+" -> "+visibility)
		writeJSON(w, http.StatusOK, map[string]any{"id": id, "visibility": visibility})
	}
}

func (s *api) deleteScript(w http.ResponseWriter, r *http.Request) {
	err := s.deps.Store.DeleteScript(r.PathValue("id"))
	if errors.Is(err, store.ErrScriptInUse) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, store.ErrScriptNotFound) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditScript(r, "script.deleted", r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) listScriptVersions(w http.ResponseWriter, r *http.Request) {
	if !s.requireScriptVisible(w, r) {
		return
	}
	versions, err := s.deps.Store.ListScriptVersions(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if versions == nil {
		versions = []store.ScriptVersion{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"versions": versions, "count": len(versions)})
}

func (s *api) getScriptVersion(w http.ResponseWriter, r *http.Request) {
	if !s.requireScriptVisible(w, r) {
		return
	}
	versionNumber, err := strconv.Atoi(r.PathValue("version"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "version must be a number")
		return
	}
	version, err := s.deps.Store.GetScriptVersion(r.PathValue("id"), versionNumber)
	if err != nil {
		writeError(w, http.StatusNotFound, "script version not found")
		return
	}
	writeJSON(w, http.StatusOK, version)
}

func (s *api) scriptUsage(w http.ResponseWriter, r *http.Request) {
	if !s.requireScriptVisible(w, r) {
		return
	}
	usage, err := s.deps.Store.ScriptUsage(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if usage == nil {
		usage = []store.ScriptUsage{}
	}
	profiles, err := s.bundleProfilesFor(r, "script", r.PathValue("id"))
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

// validateScriptRefs enforces deleted/private-script reference
// integrity at draft save and publish (Phase 4 D4-1): every code node's
// scriptRef — including bundle-injected ones — must resolve to an
// existing, visible script of the right language. Node-local source
// (the escape hatch) needs no script at all.
func (s *api) validateScriptRefs(wf *graph.Workflow, v viewer) error {
	return s.validateScriptRefsWith(wf, v, store.NewLookupCache())
}

func (s *api) validateScriptRefsWith(wf *graph.Workflow, v viewer, cache *store.LookupCache) error {
	if cache == nil {
		cache = store.NewLookupCache()
	}
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		var want string
		switch n.Type {
		case "code.run":
			want = store.ScriptLanguageJavaScript
		case "code.python":
			want = store.ScriptLanguagePython
		default:
			continue
		}
		if nodeLocalSource(n.Config["source"]) != "" {
			continue
		}
		ref, _ := n.Config["scriptRef"].(string)
		if ref == "" {
			return fmt.Errorf("%s node %q needs source or a script reference", n.Type, n.ID)
		}
		script, err := cache.Script(s.deps.Store, ref)
		if err != nil {
			return fmt.Errorf("node %q references missing script %q", n.ID, ref)
		}
		if !v.scriptVisibleTo(script) {
			return fmt.Errorf("node %q references script %q which is private to another user", n.ID, ref)
		}
		if script.Language != want {
			return fmt.Errorf("node %q: script %q is %s but %s needs a %s script", n.ID, ref, script.Language, n.Type, want)
		}
	}
	return nil
}

// nodeLocalSource mirrors store.nodeSource for the server side: nil
// reads empty, everything else as trimmed text.
func nodeLocalSource(v any) string {
	if v == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(v))
}

// requireScriptVisible answers 404 for private scripts the caller may
// not see; false means the response is already written.
func (s *api) requireScriptVisible(w http.ResponseWriter, r *http.Request) bool {
	script, err := s.deps.Store.GetScript(r.PathValue("id"))
	if err != nil || !viewerFrom(r).scriptVisibleTo(script) {
		writeError(w, http.StatusNotFound, "script not found")
		return false
	}
	return true
}

func (s *api) duplicateScript(w http.ResponseWriter, r *http.Request) {
	source, err := s.deps.Store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !viewerFrom(r).scriptVisibleTo(source) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	// The duplicate is the caller's own new asset: private until they
	// choose to share it (D-P0c).
	creator, _ := actorIdentity(r)
	created, err := s.deps.Store.CreateScript(&store.Script{
		ID: flow.NewID("script"), Name: source.Name + " (copy)", Description: source.Description,
		Language: source.Language, Source: source.Source, Requirements: source.Requirements,
		TimeoutSeconds: source.TimeoutSeconds, MaxMemoryMB: source.MaxMemoryMB,
		Tags: source.Tags, Owner: source.Owner,
		Visibility: store.VisibilityPrivate, CreatorID: creator,
	}, actorFor(r))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditScript(r, "script.created", created.ID+" (duplicate of "+source.ID+")")
	writeJSON(w, http.StatusCreated, created)
}

// testScript answers POST /api/scripts/{id}/test (Phase 4): runs the
// current version against the request's sample input. JavaScript runs
// in-process on any server; python needs the live subprocess executor
// and answers 503 on simulated hosts (mirrors profile testing).
func (s *api) testScript(w http.ResponseWriter, r *http.Request) {
	script, err := s.deps.Store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !viewerFrom(r).scriptVisibleTo(script) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	var req struct {
		Input any `json:"input"`
	}
	// An absent body is a valid request: the test runs against {}.
	if r.ContentLength > 0 {
		if err := readJSON(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	item, err := sampleItem(req.Input)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	switch script.Language {
	case store.ScriptLanguageJavaScript:
		// Same runtime, clamps and console capture as the code.run node.
		seconds := script.TimeoutSeconds
		if seconds == 0 {
			seconds = 2
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
		result, err := expr.EvalObjectWithTimeout(ctx, script.Source, scope, timeout)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		s.auditScript(r, "script.tested", script.ID+" -> javascript")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "result": result, "logs": logs})
	case store.ScriptLanguagePython:
		executor := s.deps.PythonExecutor
		if executor == nil {
			writeError(w, http.StatusServiceUnavailable,
				"python script testing needs a live server: this instance runs a simulated host")
			return
		}
		seconds := script.TimeoutSeconds
		if seconds == 0 {
			seconds = 60
		}
		if seconds < 1 {
			seconds = 1
		}
		if seconds > 900 {
			seconds = 900
		}
		memoryMB := script.MaxMemoryMB
		if memoryMB == 0 {
			memoryMB = 512
		}
		if memoryMB < 16 {
			memoryMB = 16
		}
		if memoryMB > 8192 {
			memoryMB = 8192
		}
		envKey := ""
		if len(script.Requirements) > 0 {
			envKey = pyenv.Key(script.Requirements)
		}
		timeout := time.Duration(seconds) * time.Second
		ctx, cancel := context.WithTimeout(r.Context(), timeout+10*time.Second)
		defer cancel()
		res, err := executor.Exec(ctx, flow.ExecRequest{
			Source: script.Source, Stdin: item, EnvKey: envKey,
			Timeout: timeout, MaxMemoryMB: memoryMB,
		})
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		s.auditScript(r, "script.tested", script.ID+" -> python exit "+strconv.Itoa(res.ExitCode))
		response := map[string]any{
			"ok": res.ExitCode == 0, "exitCode": res.ExitCode,
			"stdout": res.Stdout, "stderr": res.Stderr, "result": res.Result,
		}
		if res.ExitCode != 0 {
			response["error"] = "python exited with status " + strconv.Itoa(res.ExitCode)
		}
		writeJSON(w, http.StatusOK, response)
	default:
		writeError(w, http.StatusUnprocessableEntity, "script language "+strconv.Quote(script.Language)+" cannot be tested")
	}
}

// sampleItem shapes the request's input into the item a code node sees.
// Objects pass through; arrays wrap as {"items": [...]}, scalars as
// {"value": ...} — the same convention the engines return.
func sampleItem(input any) (flow.Item, error) {
	switch value := input.(type) {
	case nil:
		return flow.Item{}, nil
	case map[string]any:
		return flow.Item(value), nil
	case []any:
		return flow.Item{"items": value}, nil
	case string:
		if strings.TrimSpace(value) == "" {
			return flow.Item{}, nil
		}
		var parsed any
		if err := json.Unmarshal([]byte(value), &parsed); err != nil {
			return nil, errors.New("input must be a JSON value")
		}
		return sampleItem(parsed)
	case bool, float64, json.Number:
		return flow.Item{"value": value}, nil
	default:
		return nil, errors.New("input must be a JSON value")
	}
}

// listPythonEnvs answers GET /api/python-envs (Phase 5): the built
// dependency environments with hash, requirements and size. On a
// server without subprocess nodes the list is empty and enabled reads
// false — the same signal /healthz carries.
func (s *api) listPythonEnvs(w http.ResponseWriter, _ *http.Request) {
	if s.deps.ListPythonEnvs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"envs": []pyenv.EnvInfo{}, "enabled": false})
		return
	}
	envs, err := s.deps.ListPythonEnvs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if envs == nil {
		envs = []pyenv.EnvInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"envs": envs, "enabled": true})
}

// recordedScriptTest answers GET /api/scripts/{id}/recorded-test
// (Phase 5, D5-4): the most recent recorded execution of this script
// from a published workflow's finished runs, read from the effect
// tape. Nothing executes — the evidence is what hermetic replay
// verifies, so it works on any server including simulated ones.
func (s *api) recordedScriptTest(w http.ResponseWriter, r *http.Request) {
	script, err := s.deps.Store.GetScript(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	if !viewerFrom(r).scriptVisibleTo(script) {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}
	usage, err := s.deps.Store.ScriptUsage(script.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// Pins are newest-first; the latest recorded execution across the
	// pinned workflows' recent runs wins. The recorded source must match
	// the pinned version's source exactly — a diverging node-local
	// source is authored, not this script.
	for _, pin := range usage {
		pinned, err := s.deps.Store.GetScriptVersion(script.ID, pin.ScriptVersion)
		if err != nil {
			continue
		}
		runs, err := s.deps.Store.ListRuns(pin.WorkflowID, 20)
		if err != nil {
			continue
		}
		for _, run := range runs {
			var wf graph.Workflow
			if err := json.Unmarshal(run.Snapshot, &wf); err != nil {
				continue
			}
			for _, node := range wf.Nodes {
				if node.Type != "code.run" && node.Type != "code.python" {
					continue
				}
				if ref, _ := node.Config["scriptRef"].(string); ref != script.ID {
					continue
				}
				records, err := s.deps.Engine.RecordedExecs(run.ID, node.ID)
				if err != nil {
					continue
				}
				for i := len(records) - 1; i >= 0; i-- {
					if records[i].Source != pinned.Source {
						continue
					}
					record := records[i]
					response := map[string]any{
						"found":           true,
						"runId":           run.ID,
						"workflowName":    pin.WorkflowName,
						"workflowVersion": pin.WorkflowVersion,
						"scriptVersion":   pin.ScriptVersion,
						"nodeId":          node.ID,
						"nodeName":        node.Name,
						"exitCode":        record.ExitCode,
						"stdout":          record.Stdout,
						"stderr":          record.Stderr,
						"result":          record.Result,
					}
					if run.FinishedAt != nil {
						response["recordedAt"] = *run.FinishedAt
					}
					s.auditScript(r, "script.recorded_test", script.ID+" -> run "+run.ID)
					writeJSON(w, http.StatusOK, response)
					return
				}
			}
		}
	}
	message := "no recorded execution yet: run a published workflow that uses this script, then the recorded result appears here without re-running anything"
	if script.Language == store.ScriptLanguageJavaScript {
		message = "javascript runs in-process and is not taped: recorded results exist for python scripts, whose subprocess executions land on the effect tape"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"found":   false,
		"message": message,
	})
}
