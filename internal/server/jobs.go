package server

import (
	"context"
	"errors"
	"io"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/schedule"
	"loom/internal/store"
	"net/http"
	"strings"
	"time"
)

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
		MaxConcurrency: jobConcurrency(*job), Actor: actorFor(r),
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
	res, err := s.deps.Engine.Start(context.WithoutCancel(r.Context()), wf, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactedResult(res))
}
