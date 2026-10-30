package server

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"loom/examples"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/runbook"
	"loom/internal/schedule"
	"loom/internal/store"
)

type api struct {
	deps       Deps
	webhookSem chan struct{}
}

func (s *api) acquireWebhook(w http.ResponseWriter, r *http.Request) bool {
	if s.webhookSem == nil {
		return true
	}
	select {
	case s.webhookSem <- struct{}{}:
		return true
	case <-time.After(5 * time.Second):
		writeError(w, http.StatusTooManyRequests, "webhook ingestion queue full, retry later")
		return false
	case <-r.Context().Done():
		return false
	}
}

func (s *api) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.deps.Token == "" {
			next(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.deps.Token)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *api) reload() {
	if s.deps.Reload == nil {
		return
	}
	if err := s.deps.Reload(); err != nil {
		log.Printf("scheduler reload: %v", err)
	}
}

func (s *api) nodeTypes(w http.ResponseWriter, _ *http.Request) {
	list := nodes.All()
	for index := range list {
		if list[index].Inputs == nil {
			list[index].Inputs = []string{}
		}
		if list[index].Outputs == nil {
			list[index].Outputs = []string{}
		}
		if list[index].Credentials == nil {
			list[index].Credentials = []string{}
		}
		if list[index].OptionalCredentials == nil {
			list[index].OptionalCredentials = []string{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"nodeTypes": list})
}

func (s *api) listCredentials(w http.ResponseWriter, _ *http.Request) {
	list, err := s.deps.Store.ListCredentials()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if list == nil {
		list = []store.CredentialInfo{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"credentials": list, "count": len(list)})
}

func (s *api) createCredential(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string          `json:"id"`
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.Type = strings.TrimSpace(req.Type)
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if len(req.Data) == 0 {
		req.Data = json.RawMessage(`{}`)
	}
	if !json.Valid(req.Data) {
		writeError(w, http.StatusBadRequest, "data must be valid JSON")
		return
	}
	if req.ID == "" {
		req.ID = flow.NewID("cred")
	}
	if err := s.deps.Store.SaveCredential(req.ID, req.Type, req.Data); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, store.CredentialInfo{
		ID: req.ID, Type: req.Type, CreatedAt: time.Now().UTC(),
	})
}

func (s *api) deleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.DeleteCredential(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) runDataContext(w http.ResponseWriter, r *http.Request) {
	ctx, err := s.deps.Engine.DataContext(r.PathValue("id"), r.URL.Query().Get("nodeId"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ctx)
}

func (s *api) previewExpression(w http.ResponseWriter, r *http.Request) {
	workflowID := r.PathValue("id")
	if _, err := s.deps.Store.GetWorkflow(workflowID); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		RunID      string `json:"runId"`
		NodeID     string `json:"nodeId"`
		Expression string `json:"expression"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Expression) == "" {
		writeError(w, http.StatusBadRequest, "expression is required")
		return
	}
	if req.RunID == "" {
		runs, err := s.deps.Store.ListRuns(workflowID, 1)
		if err != nil || len(runs) == 0 {
			writeError(w, http.StatusBadRequest, "no run available for preview")
			return
		}
		req.RunID = runs[0].ID
	}
	value, err := s.deps.Engine.Preview(req.RunID, req.NodeID, req.Expression)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runId": req.RunID, "nodeId": req.NodeID, "expression": req.Expression, "value": value,
	})
}

func (s *api) listExamples(w http.ResponseWriter, _ *http.Request) {
	list, err := examples.All()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"examples": list})
}

func (s *api) stats(w http.ResponseWriter, _ *http.Request) {
	stats, err := s.deps.Store.RunStats()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *api) listWorkflows(w http.ResponseWriter, _ *http.Request) {
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
	report := graph.Validate(&wf)
	if report.HasErrors() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": report.Issues})
		return
	}
	if err := s.validateWebhookPaths(&wf); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.deps.Store.SaveWorkflow(&wf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	writeJSON(w, http.StatusCreated, wf)
}

func (s *api) getWorkflow(w http.ResponseWriter, r *http.Request) {
	wf, err := s.deps.Store.GetWorkflow(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wf)
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
	report := graph.Validate(wf)
	if report.HasErrors() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": report.Issues})
		return
	}
	if err := s.validateWebhookPaths(wf); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	version, err := s.deps.Store.PublishWorkflow(wf)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	writeJSON(w, http.StatusCreated, map[string]any{"workflow": wf, "version": version})
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
	writeJSON(w, http.StatusOK, wf)
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
	report := graph.Validate(&wf)
	if report.HasErrors() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "invalid workflow", "issues": report.Issues})
		return
	}
	if err := s.validateWebhookPaths(&wf); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err := s.deps.Store.SaveWorkflow(&wf); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) startRun(w http.ResponseWriter, r *http.Request) {
	wf, err := s.deps.Store.GetWorkflow(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	var req struct {
		Input flow.Item `json:"input"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	opts := engine.Options{Trigger: "manual", Input: req.Input, AutoApprove: false}
	if r.URL.Query().Get("async") == "1" {
		runID, err := s.deps.Engine.StartAsync(context.WithoutCancel(r.Context()), wf, opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"runId": runID, "status": "queued"})
		return
	}

	res, err := s.deps.Engine.Start(r.Context(), wf, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *api) listJobs(w http.ResponseWriter, _ *http.Request) {
	jobs, err := s.deps.Store.ListJobs()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if jobs == nil {
		jobs = []store.Job{}
	}
	for i := range jobs {
		s.decorateJob(&jobs[i])
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": jobs, "count": len(jobs)})
}

func (s *api) decorateJob(job *store.Job) {
	job.NextRunAt = nil
	if !job.Enabled {
		return
	}
	next, ok, err := schedule.Next(job.TriggerType, job.TriggerConfig, time.Now())
	if err == nil && ok {
		job.NextRunAt = &next
	}
}

func (s *api) previewSchedule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TriggerType   string         `json:"triggerType"`
		TriggerConfig map[string]any `json:"triggerConfig"`
		Count         int            `json:"count"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	triggerType := strings.TrimSpace(req.TriggerType)
	if triggerType == "" {
		triggerType = "manual"
	}
	count := req.Count
	if count <= 0 {
		count = 3
	}
	if count > 10 {
		count = 10
	}
	if err := schedule.Validate(triggerType, req.TriggerConfig); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false,
			"next":  []string{},
			"error": err.Error(),
		})
		return
	}
	next := make([]string, 0, count)
	now := time.Now()
	for i := 0; i < count; i++ {
		at, ok, err := schedule.Next(triggerType, req.TriggerConfig, now)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"valid": false,
				"next":  []string{},
				"error": err.Error(),
			})
			return
		}
		if !ok {
			break
		}
		next = append(next, at.UTC().Format(time.RFC3339))
		if triggerType != "cron" {
			break
		}
		now = at
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "next": next})
}

func (s *api) createJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            string         `json:"name"`
		TemplateID      string         `json:"templateId"`
		TemplateVersion int            `json:"templateVersion"`
		InputDefaults   map[string]any `json:"inputDefaults"`
		TriggerType     string         `json:"triggerType"`
		TriggerConfig   map[string]any `json:"triggerConfig"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TemplateID == "" {
		writeError(w, http.StatusBadRequest, "templateId is required")
		return
	}
	if req.TemplateVersion <= 0 {
		writeError(w, http.StatusBadRequest, "templateVersion is required")
		return
	}
	if _, err := s.deps.Store.GetWorkflowVersion(req.TemplateID, req.TemplateVersion); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Name == "" {
		req.Name = "Untitled job"
	}
	triggerType := strings.TrimSpace(req.TriggerType)
	if triggerType == "" {
		triggerType = "manual"
	}
	if err := schedule.Validate(triggerType, req.TriggerConfig); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if triggerType == "manual" {
		req.TriggerConfig = nil
	}
	job := store.Job{
		ID: flow.NewID("job"), Name: req.Name, TemplateID: req.TemplateID,
		TemplateVersion: req.TemplateVersion, Enabled: true, TriggerType: triggerType,
		InputDefaults: req.InputDefaults, TriggerConfig: req.TriggerConfig,
	}
	if err := s.deps.Store.CreateJob(&job); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	s.decorateJob(&job)
	writeJSON(w, http.StatusCreated, job)
}

func (s *api) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.deps.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.decorateJob(job)
	writeJSON(w, http.StatusOK, job)
}

func (s *api) setJobEnabled(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Store.SetJobEnabled(r.PathValue("id"), req.Enabled); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	job, err := s.deps.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.reload()
	s.decorateJob(job)
	writeJSON(w, http.StatusOK, job)
}

func (s *api) upgradeJob(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TemplateVersion int `json:"templateVersion"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	job, err := s.deps.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if _, err := s.deps.Store.GetWorkflowVersion(job.TemplateID, req.TemplateVersion); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Store.UpdateJobVersion(job.ID, req.TemplateVersion); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	job, _ = s.deps.Store.GetJob(job.ID)
	s.reload()
	s.decorateJob(job)
	writeJSON(w, http.StatusOK, job)
}

func (s *api) updateJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.deps.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		Name          string         `json:"name"`
		TriggerType   string         `json:"triggerType"`
		InputDefaults map[string]any `json:"inputDefaults"`
		TriggerConfig map[string]any `json:"triggerConfig"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if name := strings.TrimSpace(req.Name); name != "" {
		job.Name = name
	}
	if req.TriggerType != "" {
		job.TriggerType = strings.TrimSpace(req.TriggerType)
	}
	if req.InputDefaults != nil {
		job.InputDefaults = req.InputDefaults
	}
	if req.TriggerConfig != nil {
		job.TriggerConfig = req.TriggerConfig
	}
	if job.TriggerType == "manual" {
		job.TriggerConfig = nil
	}
	if err := schedule.Validate(job.TriggerType, job.TriggerConfig); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Store.UpdateJob(job); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	s.decorateJob(job)
	writeJSON(w, http.StatusOK, job)
}

func (s *api) duplicateJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.deps.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	duplicate := *job
	duplicate.ID = flow.NewID("job")
	duplicate.Name = job.Name + " copy"
	duplicate.Enabled = false
	duplicate.LastRunAt = nil
	if err := s.deps.Store.CreateJob(&duplicate); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reload()
	s.decorateJob(&duplicate)
	writeJSON(w, http.StatusCreated, duplicate)
}

func (s *api) deleteJob(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.Store.DeleteJob(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.reload()
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) runJob(w http.ResponseWriter, r *http.Request) {
	job, err := s.deps.Store.GetJob(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if !job.Enabled {
		writeError(w, http.StatusConflict, "job is paused")
		return
	}
	wf, err := s.deps.Store.GetWorkflowVersion(job.TemplateID, job.TemplateVersion)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	var req struct {
		Input flow.Item `json:"input"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	input := flow.Item{}
	for key, value := range job.InputDefaults {
		input[key] = value
	}
	for key, value := range req.Input {
		input[key] = value
	}
	opts := engine.Options{
		Trigger: "job", Input: input, AutoApprove: false,
		Snapshot: wf, JobID: job.ID, TemplateVersion: job.TemplateVersion,
		MaxConcurrency: jobConcurrency(*job),
	}
	if r.URL.Query().Get("async") == "1" {
		runID, err := s.deps.Engine.StartAsync(context.WithoutCancel(r.Context()), wf, opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"runId": runID, "status": "queued", "jobId": job.ID})
		return
	}
	res, err := s.deps.Engine.Start(r.Context(), wf, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *api) listRuns(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	var runs []store.Run
	var err error
	if parent := r.URL.Query().Get("parent"); parent != "" {
		runs, err = s.deps.Store.ListChildRuns(parent, limit)
	} else if job := r.URL.Query().Get("job"); job != "" {
		runs, err = s.deps.Store.ListRunsByJob(job, limit)
	} else {
		runs, err = s.deps.Store.ListRuns(r.URL.Query().Get("workflow"), limit)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if runs == nil {
		runs = []store.Run{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "count": len(runs)})
}

func (s *api) getRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.deps.Store.GetRun(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if run.Trigger == "batch" && !terminal(run.Status) {
		s.refreshBatchParent(run.ID)
		if refreshed, err := s.deps.Store.GetRun(run.ID); err == nil {
			run = refreshed
		}
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *api) runWorkflow(w http.ResponseWriter, r *http.Request) {
	run, err := s.deps.Store.GetRun(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if len(run.Snapshot) > 0 {
		var wf graph.Workflow
		if err := json.Unmarshal(run.Snapshot, &wf); err == nil {
			writeJSON(w, http.StatusOK, wf)
			return
		}
	}
	if run.TemplateVersion > 0 {
		if wf, err := s.deps.Store.GetWorkflowVersion(run.WorkflowID, run.TemplateVersion); err == nil {
			writeJSON(w, http.StatusOK, wf)
			return
		}
	}
	wf, err := s.deps.Store.GetWorkflow(run.WorkflowID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, wf)
}

type nodeState struct {
	status     string
	inputHash  string
	outputHash string
	duration   int64
}

func collectNodeStates(events []store.Event) map[string]nodeState {
	states := map[string]nodeState{}
	for _, ev := range events {
		if ev.NodeID == "" {
			continue
		}
		state := states[ev.NodeID]
		switch ev.Type {
		case "node.started":
			if state.status == "" {
				state.status = "running"
			}
		case "node.finished":
			state.status = "done"
			state.duration = ev.DurationMs
		case "node.failed":
			state.status = "failed"
			state.duration = ev.DurationMs
		}
		if ev.InputHash != "" {
			state.inputHash = ev.InputHash
		}
		if ev.OutputHash != "" {
			state.outputHash = ev.OutputHash
		}
		states[ev.NodeID] = state
	}
	return states
}

func (s *api) blobRaw(hash string) json.RawMessage {
	if hash == "" {
		return nil
	}
	data, err := s.deps.Store.LoadBlob(hash)
	if err != nil || !json.Valid(data) {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func (s *api) diffRuns(w http.ResponseWriter, r *http.Request) {
	baseID := r.PathValue("id")
	againstID := r.URL.Query().Get("against")
	if againstID == "" {
		writeError(w, http.StatusBadRequest, "against query parameter is required")
		return
	}
	base, err := s.deps.Store.GetRun(baseID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	against, err := s.deps.Store.GetRun(againstID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if base.WorkflowID != against.WorkflowID {
		writeError(w, http.StatusBadRequest, "runs belong to different workflows")
		return
	}
	baseEvents, err := s.deps.Store.ListEvents(baseID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	againstEvents, err := s.deps.Store.ListEvents(againstID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	baseStates := collectNodeStates(baseEvents)
	againstStates := collectNodeStates(againstEvents)

	names := map[string]string{}
	var order []string
	if wf, err := s.deps.Store.GetWorkflow(base.WorkflowID); err == nil {
		for _, node := range wf.Nodes {
			order = append(order, node.ID)
			names[node.ID] = node.Name
		}
	}
	seen := map[string]bool{}
	for _, id := range order {
		seen[id] = true
	}
	var extra []string
	for id := range baseStates {
		if !seen[id] {
			extra = append(extra, id)
			seen[id] = true
		}
	}
	for id := range againstStates {
		if !seen[id] {
			extra = append(extra, id)
			seen[id] = true
		}
	}
	sort.Strings(extra)
	order = append(order, extra...)

	type nodeDiff struct {
		NodeID            string          `json:"nodeId"`
		Name              string          `json:"name,omitempty"`
		BaseStatus        string          `json:"baseStatus"`
		AgainstStatus     string          `json:"againstStatus"`
		BaseDurationMs    int64           `json:"baseDurationMs,omitempty"`
		AgainstDurationMs int64           `json:"againstDurationMs,omitempty"`
		InputMatch        bool            `json:"inputMatch"`
		OutputMatch       bool            `json:"outputMatch"`
		BaseInputHash     string          `json:"baseInputHash,omitempty"`
		AgainstInputHash  string          `json:"againstInputHash,omitempty"`
		BaseOutputHash    string          `json:"baseOutputHash,omitempty"`
		AgainstOutputHash string          `json:"againstOutputHash,omitempty"`
		BaseOutput        json.RawMessage `json:"baseOutput,omitempty"`
		AgainstOutput     json.RawMessage `json:"againstOutput,omitempty"`
	}
	nodes := make([]nodeDiff, 0, len(order))
	changed := 0
	for _, id := range order {
		a, b := baseStates[id], againstStates[id]
		node := nodeDiff{
			NodeID: id, Name: names[id],
			BaseStatus: a.status, AgainstStatus: b.status,
			BaseDurationMs: a.duration, AgainstDurationMs: b.duration,
			InputMatch: a.inputHash == b.inputHash, OutputMatch: a.outputHash == b.outputHash,
			BaseInputHash: a.inputHash, AgainstInputHash: b.inputHash,
			BaseOutputHash: a.outputHash, AgainstOutputHash: b.outputHash,
			BaseOutput: s.blobRaw(a.outputHash), AgainstOutput: s.blobRaw(b.outputHash),
		}
		if !node.InputMatch || !node.OutputMatch || node.BaseStatus != node.AgainstStatus {
			changed++
		}
		nodes = append(nodes, node)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"base": baseID, "against": againstID, "workflowId": base.WorkflowID,
		"changed": changed, "nodes": nodes,
	})
}

func (s *api) getBlob(w http.ResponseWriter, r *http.Request) {
	data, err := s.deps.Store.LoadBlob(r.PathValue("hash"))
	if err != nil {
		writeError(w, http.StatusNotFound, "blob not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

func (s *api) replayRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Mode   string `json:"mode"`
		NodeID string `json:"nodeId"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := engine.ReplayMode(req.Mode)
	switch mode {
	case engine.ReplayFull:
	case engine.ReplayStep, engine.ReplayFrom:
		if req.NodeID == "" {
			writeError(w, http.StatusBadRequest, "nodeId is required for step and from replays")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode must be full, step, or from")
		return
	}
	if _, err := s.deps.Store.GetRun(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	res, err := s.deps.Engine.Replay(r.Context(), id, req.NodeID, mode)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *api) cancelRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.deps.Store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if run.Trigger == "batch" {
		children, _ := s.deps.Store.ListChildRuns(id, maxBatchItems)
		for _, child := range children {
			if terminal(child.Status) || child.Status == "cancelled" {
				continue
			}
			if !s.deps.Engine.Cancel(child.ID) {
				s.markCancelled(child.ID)
			}
		}
		s.markCancelled(id)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelled", "runId": id})
		return
	}
	if s.deps.Engine.Cancel(id) {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling", "runId": id})
		return
	}
	if run.Status == "waiting" {
		s.markCancelled(id)
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelled", "runId": id})
		return
	}
	writeError(w, http.StatusNotFound, "run is not active")
}

func (s *api) markCancelled(runID string) {
	now := time.Now().UTC()
	_, _ = s.deps.Store.AppendEvent(runID, store.Event{Type: "run.cancelled"})
	_ = s.deps.Store.UpdateRunStatus(runID, "cancelled", "", &now)
}

func (s *api) refreshBatchParent(parentID string) {
	parent, err := s.deps.Store.GetRun(parentID)
	if err != nil || parent.Trigger != "batch" || parent.Status == "cancelled" {
		return
	}
	var state batchState
	totalExpected := 0
	if len(parent.ResumeData) > 0 {
		if err := json.Unmarshal(parent.ResumeData, &state); err == nil {
			totalExpected = len(state.Items)
		}
	}

	children, err := s.deps.Store.ListChildRuns(parentID, maxBatchItems)
	if err != nil {
		return
	}
	total := len(children)
	if totalExpected > total {
		total = totalExpected
	}
	if total == 0 {
		return
	}

	running, waiting, failed := 0, 0, 0
	for _, child := range children {
		switch child.Status {
		case "queued", "running":
			running++
		case "waiting":
			waiting++
		case "failed":
			failed++
		}
	}
	if totalExpected > len(children) {
		running += (totalExpected - len(children))
	}
	status := "done"
	message := ""
	var finished *time.Time
	now := time.Now().UTC()
	switch {
	case running > 0:
		status = "running"
	case waiting > 0:
		status = "waiting"
		message = fmt.Sprintf("%d of %d items are waiting for approval", waiting, total)
	case failed > 0:
		status = "failed"
		message = fmt.Sprintf("%d of %d items failed", failed, total)
		finished = &now
	default:
		finished = &now
	}
	if status == parent.Status && message == parent.Error {
		return
	}
	_ = s.deps.Store.UpdateRunStatus(parentID, status, message, finished)
}

func (s *api) resumeRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Decision string `json:"decision"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Decision != "approved" && req.Decision != "rejected" {
		writeError(w, http.StatusBadRequest, "decision must be approved or rejected")
		return
	}
	run, err := s.deps.Store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	res, err := s.deps.Engine.Resume(r.Context(), id, req.Decision)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if run.ParentRunID != "" {
		s.refreshBatchParent(run.ParentRunID)
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *api) streamEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.deps.Store.GetRun(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	if r.URL.Query().Get("format") == "json" {
		events, err := s.deps.Store.ListEvents(id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if events == nil {
			events = []store.Event{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"events": events})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	last := int64(0)
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		last, _ = strconv.ParseInt(raw, 10, 64)
	}
	if raw := r.URL.Query().Get("after"); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > last {
			last = parsed
		}
	}
	quiet := 0
	for {
		events, err := s.deps.Store.ListEventsAfter(id, last)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
			flusher.Flush()
			return
		}
		for _, ev := range events {
			data, _ := json.Marshal(ev)
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, data)
			last = ev.Seq
		}
		if len(events) > 0 {
			quiet = 0
			flusher.Flush()
		} else {
			quiet++
			if quiet >= 60 {
				fmt.Fprint(w, ": keepalive\n\n")
				flusher.Flush()
				quiet = 0
			}
		}

		run, err := s.deps.Store.GetRun(id)
		if err == nil && terminal(run.Status) {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (s *api) approvalCallback(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	run, err := s.deps.Store.GetRunByResumeToken(token)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	wantsJSON := strings.Contains(r.Header.Get("Content-Type"), "application/json") ||
		strings.Contains(r.Header.Get("Accept"), "application/json")
	if r.Method == http.MethodGet {
		// Opening the link only renders the decision page. A GET must never
		// approve a run: link prefetchers and scanners issue GETs.
		approvalPage(w, http.StatusOK, token, run, "")
		return
	}
	decision := ""
	if wantsJSON {
		var req struct {
			Decision string `json:"decision"`
		}
		if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		decision = req.Decision
	} else {
		if err := r.ParseForm(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		decision = r.FormValue("decision")
		if decision == "" {
			decision = slackDecision(r.FormValue("payload"))
		}
	}
	if decision == "" {
		approvalPage(w, http.StatusOK, token, run, "")
		return
	}
	if decision != "approved" && decision != "rejected" {
		writeError(w, http.StatusBadRequest, "decision must be approved or rejected")
		return
	}
	res, err := s.deps.Engine.Resume(r.Context(), run.ID, decision)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if wantsJSON {
		writeJSON(w, http.StatusOK, map[string]any{"runId": res.RunID, "status": res.Status})
		return
	}
	approvalPage(w, http.StatusOK, token, run, decision)
}

func slackDecision(payload string) string {
	if payload == "" {
		return ""
	}
	var decoded struct {
		Actions []struct {
			ActionID string `json:"action_id"`
			Value    string `json:"value"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil || len(decoded.Actions) == 0 {
		return ""
	}
	if value := strings.TrimSpace(decoded.Actions[0].Value); value != "" {
		return value
	}
	return strings.TrimSpace(decoded.Actions[0].ActionID)
}

func approvalPage(w http.ResponseWriter, status int, token string, run *store.Run, decision string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'")
	w.WriteHeader(status)
	workflow := html.EscapeString(run.WorkflowID)
	message := "This run is waiting for a decision."
	if len(run.ResumeData) > 0 {
		var data map[string]any
		if json.Unmarshal(run.ResumeData, &data) == nil {
			if text, ok := data["message"].(string); ok && strings.TrimSpace(text) != "" {
				message = text
			}
		}
	}
	if decision == "approved" || decision == "rejected" {
		fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Loom approval</title>
<body style="font-family:system-ui;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1>Run %s</h1><p>Run <code>%s</code> was %s.</p><p><a href="/">Open Loom</a></p>`,
			decision, html.EscapeString(run.ID), decision)
		return
	}
	action := "/hook/approval/" + token
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Loom approval</title>
<body style="font-family:system-ui;max-width:32rem;margin:4rem auto;padding:0 1rem">
<h1>Approve this run?</h1><p><strong>Workflow:</strong> %s<br><strong>Run:</strong> <code>%s</code></p><p>%s</p>
<form method="post" action="%s">
<button name="decision" value="approved" style="padding:.6rem 1rem;margin-right:.5rem">Approve</button>
<button name="decision" value="rejected" style="padding:.6rem 1rem">Reject</button>
</form></body>`, workflow, html.EscapeString(run.ID), html.EscapeString(message), html.EscapeString(action))
}

func (s *api) webhook(w http.ResponseWriter, r *http.Request) {
	if !s.acquireWebhook(w, r) {
		return
	}
	if s.webhookSem != nil {
		defer func() { <-s.webhookSem }()
	}
	path := strings.Trim(r.PathValue("path"), "/")
	wf, nodeID, err := s.workflowByWebhook(path)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	node := findNode(wf, nodeID)
	if node != nil {
		if method, _ := node.Config["method"].(string); method != "" && method != r.Method {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		if secret, _ := node.Config["secret"].(string); strings.TrimSpace(secret) != "" {
			secret = strings.TrimSpace(secret)
			headerName, _ := node.Config["secretHeader"].(string)
			if strings.TrimSpace(headerName) == "" {
				headerName = "X-Webhook-Secret"
			}
			gotSecret := r.Header.Get(headerName)
			if gotSecret == "" {
				gotSecret = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			}
			if gotSecret == "" {
				gotSecret = r.URL.Query().Get("secret")
			}
			if subtle.ConstantTimeCompare([]byte(gotSecret), []byte(secret)) != 1 {
				writeError(w, http.StatusUnauthorized, "invalid webhook secret")
				return
			}
		}
	}

	body := flow.Item{}
	data, err := io.ReadAll(io.LimitReader(r.Body, (1<<20)+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read request body")
		return
	}
	if len(data) > 1<<20 {
		writeError(w, http.StatusRequestEntityTooLarge, "request body exceeds 1 MiB")
		return
	}

	if node != nil {
		if hmacSecret, _ := node.Config["hmacSecret"].(string); strings.TrimSpace(hmacSecret) != "" {
			hmacSecret = strings.TrimSpace(hmacSecret)
			headerName, _ := node.Config["hmacHeader"].(string)
			if strings.TrimSpace(headerName) == "" {
				headerName = "X-Hub-Signature-256"
			}
			gotSig := strings.TrimSpace(r.Header.Get(headerName))
			gotSig = strings.TrimPrefix(gotSig, "sha256=")

			mac := hmac.New(sha256.New, []byte(hmacSecret))
			mac.Write(data)
			expectedSig := hex.EncodeToString(mac.Sum(nil))

			if subtle.ConstantTimeCompare([]byte(gotSig), []byte(expectedSig)) != 1 {
				writeError(w, http.StatusUnauthorized, "invalid webhook signature")
				return
			}
		}
	}
	if len(strings.TrimSpace(string(data))) > 0 {
		var decoded any
		if err := json.Unmarshal(data, &decoded); err == nil {
			if m, ok := decoded.(map[string]any); ok {
				body = flow.Item(m)
			} else {
				body = flow.Item{"body": decoded}
			}
		} else if strings.Contains(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
			if form, err := url.ParseQuery(string(data)); err == nil && len(form) > 0 {
				for k, v := range form {
					if len(v) == 1 {
						body[k] = v[0]
					} else {
						body[k] = v
					}
				}
			} else {
				body = flow.Item{"body": string(data)}
			}
		} else {
			body = flow.Item{"body": string(data)}
		}
	}

	if len(r.URL.Query()) > 0 {
		queryParams := map[string]any{}
		for k, v := range r.URL.Query() {
			if k == "async" {
				continue
			}
			if len(v) == 1 {
				queryParams[k] = v[0]
			} else {
				queryParams[k] = v
			}
		}
		if len(queryParams) > 0 {
			if len(body) == 0 {
				for k, v := range queryParams {
					body[k] = v
				}
			}
			body["query"] = queryParams
		}
	}

	reqHeaders := map[string]any{}
	for k, v := range r.Header {
		if len(v) == 1 {
			reqHeaders[k] = v[0]
		} else {
			reqHeaders[k] = v
		}
	}
	body["headers"] = reqHeaders

	opts := engine.Options{Trigger: "webhook", Input: body}
	if r.URL.Query().Get("async") == "1" {
		runID, err := s.deps.Engine.StartAsync(context.WithoutCancel(r.Context()), wf, opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"runId": runID, "status": "queued"})
		return
	}
	res, err := s.deps.Engine.Start(r.Context(), wf, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Public callers only learn that the run was accepted. Node inputs,
	// outputs, events and approval tokens stay behind the authenticated API.
	writeJSON(w, http.StatusOK, map[string]any{"runId": res.RunID, "status": res.Status})
}

// validateWebhookPaths rejects duplicate /hook paths across the workspace.
// Without this, the first workflow returned by the store silently shadows the
// other and requests land on the wrong graph.
func (s *api) validateWebhookPaths(wf *graph.Workflow) error {
	paths := map[string]string{}
	for _, node := range wf.Nodes {
		if node.Type != "trigger.webhook" {
			continue
		}
		path, _ := node.Config["path"].(string)
		path = strings.Trim(path, "/")
		if path == "" {
			continue
		}
		if previous, ok := paths[path]; ok {
			return fmt.Errorf("webhook path %q is used by both %q and %q", "/hook/"+path, previous, node.ID)
		}
		paths[path] = node.ID
	}
	if len(paths) == 0 {
		return nil
	}
	summaries, err := s.deps.Store.ListWorkflows()
	if err != nil {
		return err
	}
	for _, summary := range summaries {
		if summary.ID == wf.ID {
			continue
		}
		other, err := s.deps.Store.GetWorkflow(summary.ID)
		if err != nil {
			continue
		}
		for _, node := range other.Nodes {
			if node.Type != "trigger.webhook" {
				continue
			}
			path, _ := node.Config["path"].(string)
			path = strings.Trim(path, "/")
			if _, ok := paths[path]; ok {
				return fmt.Errorf("webhook path %q is already used by %q", "/hook/"+path, other.Name)
			}
		}
	}
	return nil
}

func (s *api) workflowByWebhook(path string) (*graph.Workflow, string, error) {
	list, err := s.deps.Store.ListWorkflows()
	if err != nil {
		return nil, "", err
	}
	for _, summary := range list {
		wf, err := s.deps.Store.GetWorkflow(summary.ID)
		if err != nil {
			continue
		}
		for _, node := range wf.Nodes {
			if node.Type != "trigger.webhook" {
				continue
			}
			configured, _ := node.Config["path"].(string)
			if strings.Trim(configured, "/") == path {
				return wf, node.ID, nil
			}
		}
	}
	return nil, "", fmt.Errorf("no workflow listens on /hook/%s", path)
}

func readJSON(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, (5<<20)+1))
	if err != nil {
		return err
	}
	if len(data) > 5<<20 {
		return errors.New("request body exceeds limit of 5 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func terminal(status string) bool {
	return status == "done" || status == "failed" || status == "cancelled"
}

func findNode(wf *graph.Workflow, id string) *graph.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}
