package server

import (
	"encoding/json"
	"errors"
	"loom/internal/engine"
	"loom/internal/flow"
	"net/http"
	"sort"
)

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
	baseEvents, err := s.deps.Store.VerifyChain(baseID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "base run: "+err.Error())
		return
	}
	againstEvents, err := s.deps.Store.VerifyChain(againstID)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "against run: "+err.Error())
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

func (s *api) replayRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Mode     string     `json:"mode"`
		NodeID   string     `json:"nodeId"`
		Execute  bool       `json:"execute"`
		Hermetic bool       `json:"hermetic"`
		Input    *flow.Item `json:"input"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode := engine.ReplayMode(req.Mode)
	switch mode {
	case engine.ReplayFull:
	case engine.ReplayStep:
		if req.NodeID == "" {
			writeError(w, http.StatusBadRequest, "nodeId is required for step and from replays")
			return
		}
		switch {
		case req.Hermetic:
			mode = engine.ReplayStepHermetic
		case req.Execute:
			mode = engine.ReplayStepExecute
		}
	case engine.ReplayStepExecute, engine.ReplayFrom:
		if req.NodeID == "" {
			writeError(w, http.StatusBadRequest, "nodeId is required for step and from replays")
			return
		}
		if req.Hermetic {
			if mode == engine.ReplayFrom {
				mode = engine.ReplayFromHermetic
			} else {
				mode = engine.ReplayStepHermetic
			}
		}
	case engine.ReplayStepHermetic, engine.ReplayFromHermetic:
		if req.NodeID == "" {
			writeError(w, http.StatusBadRequest, "nodeId is required for step and from replays")
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode must be full, step, step-execute, from, step-hermetic, or from-hermetic")
		return
	}
	var override *flow.Item
	if req.Input != nil {
		if !allowsInputOverride(mode) {
			writeError(w, http.StatusBadRequest, "input override is only valid for executed replays")
			return
		}
		override = req.Input
	}
	if _, err := s.deps.Store.GetRun(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	res, err := s.deps.Engine.Replay(r.Context(), id, req.NodeID, mode, override)
	if err != nil {
		if errors.Is(err, engine.ErrReplayNoSlot) {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res.Display())
}

// verifyWorkflow replays a candidate workflow version against a recorded run
// with no network access and returns a per-node verdict.
func (s *api) verifyWorkflow(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		RunID   string `json:"runId"`
		Version int    `json:"version"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.RunID == "" {
		writeError(w, http.StatusBadRequest, "runId is required")
		return
	}
	run, err := s.deps.Store.GetRun(req.RunID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if run.WorkflowID != id {
		writeError(w, http.StatusBadRequest, "run does not belong to this workflow")
		return
	}
	candidate, err := s.deps.Engine.CandidateWorkflow(req.RunID, req.Version)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	verdict, err := s.deps.Engine.VerifyAgainstRun(r.Context(), req.RunID, candidate)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, verdict)
}

// allowsInputOverride reports which replay modes accept an edited input item.
func allowsInputOverride(mode engine.ReplayMode) bool {
	switch mode {
	case engine.ReplayStepExecute, engine.ReplayFrom,
		engine.ReplayStepHermetic, engine.ReplayFromHermetic:
		return true
	default:
		return false
	}
}
