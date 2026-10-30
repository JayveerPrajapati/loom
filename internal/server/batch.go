package server

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
)

const (
	maxBatchItems       = 1000
	defaultConcurrency  = 3
	maxBatchConcurrency = 20
)

type batchState struct {
	Items       []any `json:"items"`
	Indices     []int `json:"indices,omitempty"`
	Concurrency int   `json:"concurrency"`
}

func (s *api) startBatch(w http.ResponseWriter, r *http.Request) {
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
		Items       []any  `json:"items"`
		CSV         string `json:"csv"`
		Concurrency int    `json:"concurrency"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	items := req.Items
	if len(items) == 0 && strings.TrimSpace(req.CSV) != "" {
		items, err = parseBatchCSV(req.CSV)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if len(items) == 0 {
		writeError(w, http.StatusBadRequest, "batch needs at least one item")
		return
	}
	if len(items) > maxBatchItems {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("batch is limited to %d items", maxBatchItems))
		return
	}
	concurrency := normalizeConcurrency(req.Concurrency)
	indices := make([]int, len(items))
	for i := range indices {
		indices[i] = i
	}
	parent, err := s.createBatchRun(job, wf, items, indices, concurrency)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go s.executeBatch(parent.ID, *job, wf, items, indices, concurrency)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"runId": parent.ID, "status": "queued", "jobId": job.ID,
		"total": len(items), "concurrency": concurrency,
	})
}

func (s *api) retryRun(w http.ResponseWriter, r *http.Request) {
	run, err := s.deps.Store.GetRun(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if run.Trigger != "batch" {
		writeError(w, http.StatusBadRequest, "only batch runs can be retried")
		return
	}
	if run.Status == "running" {
		writeError(w, http.StatusConflict, "batch is still running")
		return
	}
	var state batchState
	if len(run.ResumeData) > 0 {
		if err := json.Unmarshal(run.ResumeData, &state); err != nil {
			writeError(w, http.StatusInternalServerError, "batch state is unreadable")
			return
		}
	}
	if len(state.Items) == 0 {
		writeError(w, http.StatusBadRequest, "batch has no stored items")
		return
	}
	job, err := s.deps.Store.GetJob(run.JobID)
	if err != nil {
		writeError(w, http.StatusConflict, "job no longer exists")
		return
	}
	wf, err := s.deps.Store.GetWorkflowVersion(job.TemplateID, job.TemplateVersion)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	children, err := s.deps.Store.ListChildRuns(run.ID, maxBatchItems)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	statuses := map[int]string{}
	for _, child := range children {
		index, ok := batchItemIndex(child.ParentNodeID)
		if ok {
			statuses[index] = child.Status
		}
	}
	failed := make([]any, 0)
	failedIndices := make([]int, 0)
	for position, item := range state.Items {
		original := position
		if position < len(state.Indices) {
			original = state.Indices[position]
		}
		status := statuses[original]
		if status == "failed" || status == "" {
			failed = append(failed, item)
			failedIndices = append(failedIndices, original)
		}
	}
	if len(failed) == 0 {
		writeError(w, http.StatusBadRequest, "batch has no failed items to retry")
		return
	}
	concurrency := normalizeConcurrency(state.Concurrency)
	parent, err := s.createBatchRun(job, wf, failed, failedIndices, concurrency)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	go s.executeBatch(parent.ID, *job, wf, failed, failedIndices, concurrency)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"runId": parent.ID, "status": "queued", "jobId": job.ID,
		"total": len(failed), "concurrency": concurrency, "retryOf": run.ID,
	})
}

func (s *api) createBatchRun(job *store.Job, wf *graph.Workflow, items []any, indices []int, concurrency int) (*store.Run, error) {
	snapshot, err := json.Marshal(wf)
	if err != nil {
		return nil, err
	}
	state, err := json.Marshal(batchState{Items: items, Indices: indices, Concurrency: concurrency})
	if err != nil {
		return nil, err
	}
	versionHash := s.workflowVersionHash(job.TemplateID, job.TemplateVersion)
	run := store.Run{
		ID: flow.NewID("run"), WorkflowID: job.TemplateID, WorkflowVersion: versionHash,
		TemplateVersion: job.TemplateVersion, JobID: job.ID, Status: "running",
		Trigger: "batch", StartedAt: time.Now().UTC(), Snapshot: snapshot, ResumeData: state,
	}
	if err := s.deps.Store.CreateRun(run); err != nil {
		return nil, err
	}
	return &run, nil
}

func (s *api) workflowVersionHash(workflowID string, version int) string {
	versions, err := s.deps.Store.ListWorkflowVersions(workflowID)
	if err == nil {
		for _, item := range versions {
			if item.Version == version {
				return item.ContentHash
			}
		}
	}
	return fmt.Sprintf("template-v%d", version)
}

func (s *api) executeBatch(parentID string, job store.Job, wf *graph.Workflow, items []any, indices []int, concurrency int) {
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for position, item := range items {
		original := position
		if position < len(indices) {
			original = indices[position]
		}
		sem <- struct{}{}
		wg.Add(1)
		go func(index int, item any) {
			defer wg.Done()
			defer func() { <-sem }()
			opts := engine.Options{
				Trigger: "batch", Input: batchInput(job.InputDefaults, item),
				Snapshot: wf, JobID: job.ID, TemplateVersion: job.TemplateVersion,
				ParentRunID: parentID, ParentNodeID: fmt.Sprintf("batch:%d", index),
				MaxConcurrency: jobConcurrency(job),
			}
			_, _ = s.deps.Engine.Start(context.Background(), wf, opts)
		}(original, item)
	}
	wg.Wait()
	s.refreshBatchParent(parentID)
}

func batchInput(defaults map[string]any, item any) flow.Item {
	input := flow.Item{}
	for key, value := range defaults {
		input[key] = value
	}
	if object, ok := item.(map[string]any); ok {
		for key, value := range object {
			input[key] = value
		}
		return input
	}
	input["item"] = item
	return input
}

func parseBatchCSV(text string) ([]any, error) {
	reader := csv.NewReader(strings.NewReader(text))
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("invalid CSV: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("CSV needs a header row and at least one data row")
	}
	headers := records[0]
	items := make([]any, 0, len(records)-1)
	for _, record := range records[1:] {
		if len(strings.TrimSpace(strings.Join(record, ""))) == 0 {
			continue
		}
		item := map[string]any{}
		for index, header := range headers {
			name := strings.TrimSpace(header)
			if name == "" || index >= len(record) {
				continue
			}
			item[name] = record[index]
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("CSV has no data rows")
	}
	return items, nil
}

func normalizeConcurrency(value int) int {
	if value <= 0 {
		return defaultConcurrency
	}
	if value > maxBatchConcurrency {
		return maxBatchConcurrency
	}
	return value
}

func batchItemIndex(nodeID string) (int, bool) {
	const prefix = "batch:"
	if !strings.HasPrefix(nodeID, prefix) {
		return 0, false
	}
	var index int
	if _, err := fmt.Sscanf(nodeID[len(prefix):], "%d", &index); err != nil {
		return 0, false
	}
	return index, true
}
