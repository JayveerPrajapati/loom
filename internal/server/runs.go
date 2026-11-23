package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// redactedResult masks node outputs at the API boundary. Execution and replay
// keep the exact bytes; every response surface uses the same display mask so a
// sync run cannot return what data-context/replay deliberately hide.
func redactedResult(res *engine.Result) *engine.Result {
	if res == nil {
		return nil
	}
	out := *res
	out.Outputs = engine.RedactForDisplay(res.Outputs)
	// Node events embed the recorded request/response, so they need the same
	// mask; otherwise the raw headers leak through the events field.
	out.Events = engine.RedactForDisplay(res.Events)
	return &out
}

func (s *api) startRun(w http.ResponseWriter, r *http.Request) {
	wf, err := s.deps.Store.GetWorkflow(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// Drafts are stored sparse: profile values, bundle refs and the other
	// profile-era assets resolve at publish. A manual run may start before
	// that publish, so resolve the same chain here.
	wf, err = workflowForRun(s.deps.Store, wf)
	if err != nil {
		writeRunPrepError(w, err)
		return
	}

	var req struct {
		Input flow.Item `json:"input"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	opts := engine.Options{Trigger: "manual", Input: req.Input, AutoApprove: false, Actor: actorFor(r)}
	if r.URL.Query().Get("async") == "1" {
		runID, err := s.deps.Engine.StartAsync(context.WithoutCancel(r.Context()), wf, opts)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"runId": runID, "status": "queued"})
		return
	}

	res, err := s.deps.Engine.Start(context.WithoutCancel(r.Context()), wf, opts)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, redactedResult(res))
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
	offset := intQuery(r.URL.Query().Get("offset"), 0)
	cursor := strings.TrimSpace(r.URL.Query().Get("cursor"))
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	statusFilter := r.URL.Query().Get("status")
	parent := r.URL.Query().Get("parent")
	job := r.URL.Query().Get("job")
	triggerFilter := strings.TrimSpace(r.URL.Query().Get("trigger"))
	roots := r.URL.Query().Get("roots") == "1"
	// A search, cursor, status, trigger, or roots filter switches to the
	// paged query, which reports the true total. Cursor paging is
	// keyset-based and stays constant-cost at any depth; offset remains for
	// compatibility with older clients. parent and job-only views keep
	// their dedicated paths, never page, and ignore a status filter —
	// matching the routing that predates cursors. The dashboard's job view
	// sends roots=1 so it can page through roots-only runs.
	usePaged := search != "" || cursor != "" || offset > 0 || triggerFilter != "" || roots ||
		(statusFilter != "" && parent == "" && job == "")
	if usePaged && statusFilter != "" && !validRunStatus(statusFilter) {
		writeError(w, http.StatusBadRequest, "status must be queued, running, waiting, done, failed, or cancelled")
		return
	}
	if usePaged {
		if offset > 0 {
			filtered, total, ferr := s.deps.Store.ListRunsFiltered(store.RunsFilter{
				WorkflowID: r.URL.Query().Get("workflow"),
				Status:     statusFilter,
				Query:      search,
				JobID:      job,
				Trigger:    triggerFilter,
				RootsOnly:  roots,
				Limit:      limit,
				Offset:     offset,
			})
			if ferr != nil {
				writeError(w, http.StatusInternalServerError, ferr.Error())
				return
			}
			if filtered == nil {
				filtered = []store.Run{}
			}
			hideRunTokens(r, filtered)
			writeJSON(w, http.StatusOK, map[string]any{
				"runs": filtered, "count": total, "limit": limit, "offset": offset,
			})
			return
		}
		page, ferr := s.deps.Store.ListRunsPage(store.RunsFilter{
			WorkflowID: r.URL.Query().Get("workflow"),
			Status:     statusFilter,
			Query:      search,
			JobID:      job,
			Trigger:    triggerFilter,
			RootsOnly:  roots,
			Limit:      limit,
			Cursor:     cursor,
		})
		if ferr != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor: "+ferr.Error())
			return
		}
		hideRunTokens(r, page.Runs)
		writeJSON(w, http.StatusOK, map[string]any{
			"runs": page.Runs, "count": page.Total, "limit": limit, "nextCursor": page.NextCursor,
		})
		return
	}
	var runs []store.Run
	var err error
	if parent != "" {
		runs, err = s.deps.Store.ListChildRuns(parent, limit)
	} else if job != "" {
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
	hideRunTokens(r, runs)
	count := len(runs)
	// A status filter reports the true total: callers that ask for one row
	// (the nav badge) still need the full backlog size.
	if status := r.URL.Query().Get("status"); status != "" {
		if total, countErr := s.deps.Store.CountRunsByStatus(status); countErr == nil {
			count = total
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "count": count})
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
	hideRunToken(r, run)
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
			writeJSON(w, http.StatusOK, workflowForResponse(r, &wf))
			return
		}
	}
	if run.TemplateVersion > 0 {
		if wf, err := s.deps.Store.GetWorkflowVersion(run.WorkflowID, run.TemplateVersion); err == nil {
			writeJSON(w, http.StatusOK, workflowForResponse(r, wf))
			return
		}
	}
	wf, err := s.deps.Store.GetWorkflow(run.WorkflowID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, workflowForResponse(r, wf))
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
	data, err := s.deps.Store.LoadBlobForDisplay(hash)
	if err != nil || !json.Valid(data) {
		return nil
	}
	return append(json.RawMessage(nil), data...)
}

func (s *api) getBlob(w http.ResponseWriter, r *http.Request) {
	data, err := s.deps.Store.LoadBlobForDisplay(r.PathValue("hash"))
	if err != nil {
		writeError(w, http.StatusNotFound, "blob not found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
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
	// A waiting/queued run did not own a slot in the engine's active map, so
	// nothing else will release this workflow's queue.
	s.deps.Engine.DrainQueued(context.Background())
}

func (s *api) resumeRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := readJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Decision != "approved" && req.Decision != "rejected" {
		writeError(w, http.StatusBadRequest, "decision must be approved or rejected")
		return
	}
	note := strings.TrimSpace(req.Note)
	if utf8.RuneCountInString(note) > 500 {
		writeError(w, http.StatusUnprocessableEntity, engine.ErrRejectionReasonTooLong.Error())
		return
	}
	run, err := s.deps.Store.GetRun(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	actorID, actorLabel := actorIdentity(r)
	res, err := s.deps.Engine.ResumeDecision(r.Context(), id, req.Decision,
		engine.DecisionMeta{ActorID: actorID, ActorLabel: actorLabel, Source: "api"}, note)
	if err != nil {
		if errors.Is(err, engine.ErrRejectionReasonRequired) ||
			errors.Is(err, engine.ErrRejectionReasonTooShort) ||
			errors.Is(err, engine.ErrRejectionReasonTooLong) {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if run.ParentRunID != "" {
		s.refreshBatchParent(run.ParentRunID)
	}
	writeJSON(w, http.StatusOK, redactedResult(res))
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
		// Mask again on read: rows written before the storage fix (or by an
		// older binary) may still carry raw response headers.
		events = engine.RedactForDisplay(events)
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
	// Wait for store notifications instead of polling: every run mutation
	// pokes this stream, so event delivery is immediate and idle streams
	// cost nothing. The fallback poll bounds notification loss (a missed
	// poke surfaces within its period), and notification coalescing is
	// safe because a wake re-lists everything after the last delivered seq.
	changes, stopWatching := s.deps.Store.SubscribeRunChanges(id)
	defer stopWatching()
	IncStreams()
	defer DecStreams()
	fallback := time.NewTicker(5 * time.Second)
	defer fallback.Stop()
	// A stream must notice a dead session even while idle: without run
	// notifications nothing would wake the auth recheck, so a dedicated
	// ticker drives it. Two seconds halves the old per-poll auth rate
	// while keeping revocation latency well inside the security bound.
	authTick := time.NewTicker(2 * time.Second)
	defer authTick.Stop()
	lastWrite := time.Now()
	lastAuthCheck := time.Now()
	for {
		// A stream outlives a single auth decision. Re-resolve the principal
		// (session row, user.Disabled, token revocation) so a logout or
		// revocation closes the stream instead of delivering more events.
		// Throttled to one cheap indexed read per second.
		if time.Since(lastAuthCheck) >= time.Second {
			if _, ok := s.resolvePrincipal(r); !ok {
				return
			}
			lastAuthCheck = time.Now()
		}
		// Read the run row BEFORE listing events. If the row is terminal, the
		// event read that follows starts strictly later, so it is guaranteed
		// to include the terminal event: the row flip and the event commit
		// share one transaction. Listing first raced — the row could report
		// terminal while the (earlier) event read had not yet seen
		// run.finished, closing the stream without ever delivering it and
		// leaving clients stuck on "Running" forever.
		run, err := s.deps.Store.GetRun(id)
		if err != nil {
			// The run row is gone (prune or delete): there is nothing
			// left to stream.
			return
		}
		events, err := s.deps.Store.ListEventsAfter(id, last)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
			flusher.Flush()
			return
		}
		for _, ev := range events {
			data, _ := json.Marshal(engine.RedactForDisplay(ev))
			fmt.Fprintf(w, "id: %d\ndata: %s\n\n", ev.Seq, data)
			last = ev.Seq
		}
		if len(events) > 0 {
			lastWrite = time.Now()
			flusher.Flush()
		} else if time.Since(lastWrite) >= 15*time.Second {
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
			lastWrite = time.Now()
		}
		if terminal(run.Status) {
			return
		}

		select {
		case <-r.Context().Done():
			return
		case <-changes:
			IncStreamWake(true)
		case <-fallback.C:
			IncStreamWake(false)
		case <-authTick.C:
		}
	}
}
