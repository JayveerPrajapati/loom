package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"loom/internal/store"
)

// revokeRunApproval invalidates the approval link of a waiting run without
// deciding it. The run stays waiting so the inbox can still decide it.
func (s *api) revokeRunApproval(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := s.deps.Store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if run.Status != "waiting" {
		writeError(w, http.StatusConflict, "run is not waiting for an approval")
		return
	}
	ok, err := s.deps.Store.ClearResumeToken(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		writeError(w, http.StatusConflict, "approval link was already revoked or decided")
		return
	}
	_, actorLabel := actorIdentity(r)
	if _, err := s.deps.Store.AppendEvent(id, store.Event{
		Type: "approval.revoked", NodeID: run.ResumeNode, Actor: actorLabel,
		Data: map[string]any{"nodeId": run.ResumeNode},
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"runId": id, "revoked": true, "status": run.Status,
	})
}

// listDecisions returns the recorded approval decisions, newest first. It is
// the queryable counterpart to the run.resumed events.
func (s *api) listDecisions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	limit := intQuery(query.Get("limit"), 50)
	offset := intQuery(query.Get("offset"), 0)
	items, total, err := s.deps.Store.ListDecisions(
		query.Get("workflowId"), query.Get("source"), query.Get("decision"), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"decisions": items, "count": total, "limit": limit, "offset": offset,
	})
}

// decisionMetrics reports decision volume, wait percentiles and the current
// backlog so the inbox has a number instead of a feeling.
func (s *api) decisionMetrics(w http.ResponseWriter, r *http.Request) {
	window := 24 * time.Hour
	if raw := r.URL.Query().Get("windowHours"); raw != "" {
		if hours, err := strconv.Atoi(raw); err == nil && hours > 0 && hours <= 24*30 {
			window = time.Duration(hours) * time.Hour
		}
	}
	stats, err := s.deps.Store.DecisionMetrics(window)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	waiting, err := s.deps.Store.ListRunsByStatus("waiting", 200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	now := time.Now().UTC()
	for i := range waiting {
		var payload map[string]any
		if len(waiting[i].ResumeData) > 0 {
			_ = json.Unmarshal(waiting[i].ResumeData, &payload)
		}
		raw, _ := payload["requestedAt"].(string)
		requested, err := time.Parse(time.RFC3339, raw)
		if err != nil || requested.IsZero() {
			continue
		}
		if waited := now.Sub(requested).Milliseconds(); waited > stats.OldestWaitMs {
			stats.OldestWaitMs = waited
		}
	}
	writeJSON(w, http.StatusOK, stats)
}

func intQuery(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
