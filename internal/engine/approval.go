package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"loom/internal/cassette"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrRejectionReasonRequired is returned when an approval is rejected without
// a usable explanation. A rejection with no recorded reason is not useful
// history, and a one-word answer is not a reason.
var ErrRejectionReasonRequired = errors.New("a rejection reason is required")

// ErrRejectionReasonTooShort is returned when a rejection note exists but is
// too short to be a useful reason.
var ErrRejectionReasonTooShort = errors.New("a rejection reason must be at least 3 characters")

// ErrRejectionReasonTooLong is returned when a decision note exceeds the
// stored limit, so the API refuses it instead of silently truncating.
var ErrRejectionReasonTooLong = errors.New("a rejection reason must be 500 characters or fewer")

// minReasonRunes is the shortest rejection note the engine accepts.
const minReasonRunes = 3

// maxNoteRunes bounds every decision note (approval notes included).
const maxNoteRunes = 500

// approvalLinkTTL bounds an approval link's life when the node does not set
// linkExpiresMinutes.
const approvalLinkTTL = 7 * 24 * time.Hour

func (e *Engine) suspend(st *execState, nodeID string, s *flow.Suspend) (*Result, error) {
	token := flow.NewToken()
	payload := map[string]any{}
	for k, v := range s.Data {
		payload[k] = v
	}
	var wakeAt, expiresAt time.Time
	if s.Reason == "wait" {
		if raw, ok := payload["wakeAt"].(string); ok {
			wakeAt, _ = time.Parse(time.RFC3339Nano, raw)
		}
	}
	if s.Reason == "approval" {
		now := e.now().UTC()
		payload["requestedAt"] = now.Format(time.RFC3339)
		ttl := approvalLinkTTL
		var timeout time.Duration
		if node := nodeByID(st.wf, nodeID); node != nil {
			if mins := numberField(node.Config["linkExpiresMinutes"]); mins > 0 {
				ttl = time.Duration(mins * float64(time.Minute))
			}
			if sec := numberField(node.Config["timeoutSeconds"]); sec > 0 {
				timeout = time.Duration(sec * float64(time.Second))
				payload["onTimeout"] = approvalTimeoutChoice(node.Config["onTimeout"])
			}
			if mins := numberField(node.Config["reminderMinutes"]); mins > 0 {
				payload["reminderMinutes"] = mins
			}
		}
		expiresAt = now.Add(ttl)
		if timeout > 0 {
			wakeAt = now.Add(timeout)
			payload["wakeAt"] = wakeAt.Format(time.RFC3339Nano)
		}
	}
	data, _ := json.Marshal(payload)
	if err := e.Store.SetRunWaitingUntil(st.runID, nodeID, token, data, wakeAt, expiresAt); err != nil {
		return nil, err
	}
	eventData := map[string]any{"reason": s.Reason}
	for k, v := range payload {
		eventData[k] = v
	}
	eventData["linkExpiresAt"] = expiresAt.UTC().Format(time.RFC3339)
	if err := e.emit(st, store.Event{
		Type: "run.waiting", NodeID: nodeID, Data: eventData,
	}); err != nil {
		return nil, err
	}
	// The run released its concurrency slot by parking; admit the next queued
	// run of this workflow instead of leaving capacity idle until a restart.
	e.dequeueNext(st.wf.ID)

	// Tell the exact-deadline scheduler (when wired) to re-arm its timer for
	// the earliest pending wakeup.
	if !wakeAt.IsZero() && e.OnRunParked != nil {
		e.OnRunParked(wakeAt)
	}

	if s.Reason == "approval" {
		e.notifyApproval(st, nodeID, payload)
	}
	if s.Reason == "approval" && e.OnApprovalWaiting != nil && st.effects != cassette.ModeReplay {
		notify := e.OnApprovalWaiting
		payloadCopy := payload
		runID := st.runID
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			notify(ctx, runID, nodeID, payloadCopy)
		}()
	}
	return e.result(st, "waiting", nodeID, token, ""), nil
}

// notifyApproval delivers an approval request to the configured Slack
// credential/channel or webhook. Delivery failures are recorded on the run and
// never fail execution: the run is already waiting either way.
func (e *Engine) notifyApproval(st *execState, nodeID string, payload map[string]any) {
	// A replay must never notify: the notification is an external effect and
	// hermetic replay promises zero network I/O.
	if st.effects == cassette.ModeReplay {
		return
	}
	node := nodeByID(st.wf, nodeID)
	if node == nil {
		return
	}
	credID := node.Credentials["slack"]
	if credID == "" {
		credID = stringField(node.Config["credential"])
	}
	channel := stringField(payload["channel"])
	message := stringField(payload["message"])
	webhook := stringField(node.Config["notifyUrl"])
	if (credID == "" || channel == "") && webhook == "" {
		return
	}
	runID := st.runID
	go func() {
		defer e.recoverBackground(runID)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		data := map[string]any{"channel": channel}
		if credID != "" && channel != "" {
			if err := st.host.Slack(ctx, credID, channel, message); err != nil {
				data["slackError"] = err.Error()
			} else {
				data["slack"] = true
			}
		}
		if webhook != "" {
			resp, err := st.host.HTTP(ctx, flow.HTTPRequest{
				Method: "POST", URL: webhook, ContentType: "application/json",
				Body: map[string]any{
					"event": "approval.waiting", "runId": runID, "nodeId": nodeID,
					"message": message, "channel": channel,
				},
			})
			if err != nil {
				data["webhookError"] = err.Error()
			} else {
				data["webhook"] = resp.Status
			}
		}
		_, _ = e.Store.AppendEvent(runID, store.Event{
			Type: "approval.notified", NodeID: nodeID, Actor: "system:engine", Data: data,
		})
	}()
}

// stringField reads a trimmed string config value.
func stringField(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// nodeByID finds a node in the workflow snapshot.
func nodeByID(wf *graph.Workflow, id string) *graph.Node {
	if wf == nil {
		return nil
	}
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}

// numberField reads a numeric node config value, which JSON decodes as float64.
func numberField(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case int:
		return float64(n)
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

// approvalTimeoutChoice maps the onTimeout config to a decision.
func approvalTimeoutChoice(v any) string {
	if s, _ := v.(string); s == "approved" {
		return "approved"
	}
	return "rejected"
}

// formatTimeout renders a timeout for the recorded reason.
func formatTimeout(v any) string {
	sec := numberField(v)
	if sec <= 0 {
		return "the configured timeout"
	}
	return time.Duration(sec * float64(time.Second)).String()
}

// DecisionMeta identifies who decided and through which surface.
type DecisionMeta struct {
	ActorID    string
	ActorLabel string
	Source     string
}

// ResumeDecision resumes a waiting run, recording the decision, its actor and
// the surface it came through.
func (e *Engine) ResumeDecision(ctx context.Context, runID, decision string, meta DecisionMeta, note string) (*Result, error) {
	actor := meta.ActorLabel
	if strings.TrimSpace(actor) == "" {
		actor = meta.ActorID
	}
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if run.Status != "waiting" {
		return nil, fmt.Errorf("run %s is %s, not waiting", runID, run.Status)
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return nil, err
	}
	outputs, completed, err := e.loadOutputs(runID)
	if err != nil {
		return nil, err
	}

	resumeData := map[string]any{}
	if len(run.ResumeData) > 0 {
		_ = json.Unmarshal(run.ResumeData, &resumeData)
	}
	// A durable timer resumes with no decision; approvals still require one.
	timerWait := decision == "" && resumeData["wakeAt"] != nil
	source := meta.Source
	if source == "" {
		source = "api"
	}
	// An approval parked with timeoutSeconds wakes with no decision. Resolve
	// that wake into the configured decision so the run moves on instead of
	// parking again, and record what decided it.
	if timerWait {
		if node := nodeByID(wf, run.ResumeNode); node != nil && node.Type == "flow.approval" {
			decision = approvalTimeoutChoice(node.Config["onTimeout"])
			note = fmt.Sprintf("Timed out after %s with no decision", formatTimeout(node.Config["timeoutSeconds"]))
			actor = "system:timeout"
			meta = DecisionMeta{ActorID: "system:timeout", ActorLabel: "system:timeout", Source: "timeout"}
			source = "timeout"
			timerWait = false
		}
	}
	note = strings.TrimSpace(note)
	if !timerWait && decision != "approved" && decision != "rejected" {
		return nil, fmt.Errorf("decision must be approved or rejected")
	}
	if note != "" && utf8.RuneCountInString(note) > maxNoteRunes {
		return nil, ErrRejectionReasonTooLong
	}
	if decision == "rejected" {
		if note == "" {
			return nil, ErrRejectionReasonRequired
		}
		if utf8.RuneCountInString(note) < minReasonRunes {
			return nil, ErrRejectionReasonTooShort
		}
	}
	var wakeAt time.Time
	if raw, ok := resumeData["wakeAt"].(string); ok {
		wakeAt, _ = time.Parse(time.RFC3339Nano, raw)
	}
	resumeActor := strings.TrimSpace(actor)
	if resumeActor == "" {
		resumeActor = actorOrDefault(run.Actor)
	}

	effects, cassetteRunID := e.persistedEffects(runID)
	st := &execState{
		runID: runID, wf: wf, outputs: outputs, completed: completed,
		skipped: map[string]bool{}, decision: map[string]string{},
		decisionInfo: map[string]flow.DecisionInfo{},
		vars:         varsFor(wf), actor: actorOrDefault(run.Actor),
		rootRunID: rootRunIDFor(run),
		effects:   effects, cassetteRunID: cassetteRunID, wakeAt: wakeAt,
	}
	st.host = wrapWorkflowEgress(e.Host, wf.Egress)
	if !timerWait {
		st.decision[run.ResumeNode] = decision
		st.decisionInfo[run.ResumeNode] = flow.DecisionInfo{
			Decision: decision, Note: note, Actor: resumeActor, At: e.now(),
		}
	}
	decidedAt := e.now().UTC()
	var requestedAt time.Time
	if raw, ok := resumeData["requestedAt"].(string); ok {
		requestedAt, _ = time.Parse(time.RFC3339, raw)
	}
	var waitedMs int64
	if !requestedAt.IsZero() {
		waitedMs = decidedAt.Sub(requestedAt).Milliseconds()
	}
	var claimed bool
	if decision == "" {
		claimed, err = e.Store.ResumeWaiting(runID)
	} else {
		claimed, err = e.Store.ResumeWaitingWithDecision(runID, store.Decision{
			RunID: runID, WorkflowID: wf.ID, NodeID: run.ResumeNode,
			Decision: decision, Note: note,
			ActorID: meta.ActorID, ActorLabel: resumeActor,
			Source: source, RequestedAt: requestedAt, DecidedAt: decidedAt, WaitedMs: waitedMs,
		})
	}
	if err != nil {
		return nil, err
	}
	if !claimed {
		return nil, fmt.Errorf("run %s was already resumed", runID)
	}
	data := map[string]any{}
	if decision != "" {
		data["decision"] = decision
	} else {
		data["reason"] = "timer"
	}
	if note = strings.TrimSpace(note); note != "" {
		data["note"] = note
	}
	if !timerWait {
		// The event carries Actor at the top level for the audit trail; the
		// data copy lets the run page show who decided without resolving the
		// event envelope, and keeps the actor with exported event data.
		data["actor"] = resumeActor
		data["actorId"] = meta.ActorID
		data["source"] = source
		data["decidedAt"] = decidedAt.Format(time.RFC3339)
	}
	if err := e.appendEvent(runID, store.Event{
		Type: "run.resumed", NodeID: run.ResumeNode, Actor: resumeActor,
		Data: data,
	}); err != nil {
		return nil, err
	}

	opts := Options{
		Trigger: run.Trigger, Input: flow.Item{}, MaxAttempts: e.MaxAttempts,
		RootRunID: rootRunIDFor(run), Actor: run.Actor,
	}
	timeout := opts.Timeout
	if timeout <= 0 && wf.Settings != nil {
		if sec, ok := wf.Settings["timeoutSeconds"].(float64); ok && sec > 0 {
			timeout = time.Duration(sec * float64(time.Second))
		} else if sec, ok := wf.Settings["timeout"].(float64); ok && sec > 0 {
			timeout = time.Duration(sec * float64(time.Second))
		}
	}

	var runCtx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		runCtx, cancel = context.WithCancel(ctx)
	}
	e.register(runID, cancel)
	defer e.unregister(runID)

	return e.execute(runCtx, st, opts)
}

// SweepApprovalReminders re-sends the approval request once for waiting runs
// whose reminderMinutes have elapsed. It returns how many were reminded. The
// reminder is recorded so a restart cannot send it twice.
func (e *Engine) SweepApprovalReminders(ctx context.Context) int {
	runs, err := e.Store.ListRunsByStatus("waiting", 500)
	if err != nil {
		return 0
	}
	reminded := 0
	now := e.now().UTC()
	for i := range runs {
		run := runs[i]
		// An expired approval link can never be resolved. Cancel the waiting
		// run with a CAS so exactly one sweep wins, then record the reason.
		if run.ResumeExpiresAt != nil && !now.Before(run.ResumeExpiresAt.UTC()) {
			expired, err := e.Store.ExpireWaitingRun(run.ID, now)
			if err != nil || !expired {
				continue
			}
			_ = e.appendEvent(run.ID, store.Event{
				Type: "run.cancelled", Actor: "system:engine",
				Error: "approval link expired",
				Data:  map[string]any{"reason": "approval link expired"},
			})
			continue
		}
		var payload map[string]any
		if len(run.ResumeData) == 0 || json.Unmarshal(run.ResumeData, &payload) != nil {
			continue
		}
		if _, done := payload["remindedAt"]; done {
			continue
		}
		mins := numberField(payload["reminderMinutes"])
		if mins <= 0 {
			continue
		}
		raw, _ := payload["requestedAt"].(string)
		requested, err := time.Parse(time.RFC3339, raw)
		if err != nil || now.Before(requested.Add(time.Duration(mins*float64(time.Minute)))) {
			continue
		}
		wf, err := e.workflowForRun(&run)
		if err != nil {
			continue
		}
		effects, cassetteRunID := e.persistedEffects(run.ID)
		notifyState := &execState{runID: run.ID, wf: wf, rootRunID: rootRunIDFor(&run), effects: effects, cassetteRunID: cassetteRunID}
		notifyState.host = wrapWorkflowEgress(e.Host, wf.Egress)
		e.notifyApproval(notifyState, run.ResumeNode, payload)
		payload["remindedAt"] = now.Format(time.RFC3339)
		if data, err := json.Marshal(payload); err == nil {
			_ = e.Store.UpdateResumeData(run.ID, data)
		}
		_, _ = e.Store.AppendEvent(run.ID, store.Event{
			Type: "approval.reminded", NodeID: run.ResumeNode, Actor: "system:engine",
			Data: map[string]any{"afterMinutes": mins},
		})
		reminded++
	}
	return reminded
}
