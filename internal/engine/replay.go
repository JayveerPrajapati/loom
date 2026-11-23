package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"loom/internal/cassette"
	"loom/internal/expr"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
	"sort"
)

// ErrReplayNoSlot is returned when a replay needs a concurrency slot that is
// currently occupied. The caller can retry once a slot frees.
var ErrReplayNoSlot = errors.New("replay needs a free run slot")

type ReplayMode string

const (
	ReplayFull ReplayMode = "full"
	ReplayStep ReplayMode = "step"
	// ReplayStepExecute re-runs the node on the live host. It is opt-in because
	// it can repeat external side effects; ReplayStep only verifies blobs.
	ReplayStepExecute ReplayMode = "step-execute"
	ReplayFrom        ReplayMode = "from"
	// ReplayStepHermetic re-runs the node against its recorded effects: the
	// same code executes, but no external call is made.
	ReplayStepHermetic ReplayMode = "step-hermetic"
	// ReplayFromHermetic re-runs a node and its descendants from recorded
	// effects, so an entire downstream branch can be reproduced offline.
	ReplayFromHermetic ReplayMode = "from-hermetic"
)

type ReplayResult struct {
	RunID        string                 `json:"runId"`
	Mode         ReplayMode             `json:"mode"`
	NodeID       string                 `json:"nodeId,omitempty"`
	Match        bool                   `json:"match"`
	RecordedHash string                 `json:"recordedHash,omitempty"`
	ActualHash   string                 `json:"actualHash,omitempty"`
	Status       string                 `json:"status"`
	Outputs      map[string]flow.Output `json:"outputs,omitempty"`
	Config       map[string]any         `json:"config,omitempty"`
	Credentials  map[string]string      `json:"credentials,omitempty"`
	Events       []store.Event          `json:"events,omitempty"`
	// Effects is the number of recorded effects used by a hermetic replay.
	Effects int             `json:"effects,omitempty"`
	Diffs   []cassette.Diff `json:"diffs,omitempty"`
	Misses  []cassette.Miss `json:"misses,omitempty"`
}

// Display returns a copy safe to send to the API/UI: recorded outputs and the
// node configuration are masked at the display boundary. Internal replay keeps
// the raw values for seeding a child run.
func (r *ReplayResult) Display() *ReplayResult {
	if r == nil {
		return nil
	}
	out := *r
	out.Outputs = RedactForDisplay(r.Outputs)
	out.Config = RedactForDisplay(r.Config)
	// Replay returns events straight from the log; mask them here too so the
	// replay endpoint cannot serve legacy raw rows.
	out.Events = RedactForDisplay(r.Events)
	return &out
}

func (e *Engine) Replay(ctx context.Context, runID, nodeID string, mode ReplayMode, itemOverride ...*flow.Item) (*ReplayResult, error) {
	var override *flow.Item
	if len(itemOverride) > 0 {
		override = itemOverride[0]
	}
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return nil, err
	}
	events, err := e.Store.VerifyChain(runID)
	if err != nil {
		return nil, err
	}

	switch mode {
	case ReplayStep, ReplayStepExecute, ReplayStepHermetic:
		if nodeID == "" {
			return nil, errors.New("step replay needs a node id")
		}
		var recorded *store.Event
		for i := range events {
			if events[i].NodeID == nodeID && events[i].Type == "node.finished" {
				recorded = &events[i]
			}
		}
		if recorded == nil {
			return nil, fmt.Errorf("node %q has no completed event", nodeID)
		}
		node := findNode(wf, nodeID)
		if node == nil {
			return nil, fmt.Errorf("node %q not in workflow", nodeID)
		}
		if mode == ReplayStep {
			// Verify-only: recompute the recorded input and output hashes from
			// the raw blobs. No host is called, so a replay can never repeat a
			// side effect. Use ReplayStepExecute for a live re-run.
			actualIn, err := e.verifyBlob(recorded.InputHash)
			if err != nil {
				return nil, err
			}
			actualOut, err := e.verifyBlob(recorded.OutputHash)
			if err != nil {
				return nil, err
			}
			match := (recorded.InputHash == "" || actualIn == recorded.InputHash) &&
				(recorded.OutputHash == "" || actualOut == recorded.OutputHash)
			return &ReplayResult{
				RunID: runID, Mode: mode, NodeID: nodeID,
				Match:        match,
				RecordedHash: recorded.OutputHash, ActualHash: actualOut,
				Status: "verified", Events: events,
				Config:      RedactForDisplay(node.Config),
				Credentials: node.Credentials,
			}, nil
		}
		outputs, _, err := e.loadOutputs(runID)
		if err != nil {
			return nil, err
		}

		var item flow.Item
		if override != nil {
			// Re-run with an edited input instead of the recorded blob.
			item = flow.CloneItem(*override)
		} else {
			inputData, err := e.Store.LoadBlob(recorded.InputHash)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(inputData, &item); err != nil {
				return nil, err
			}
		}

		// A hermetic re-run uses the tape instead of the live host. Missing
		// effects fail the node, so replay can never silently reach the network.
		// Live replay modes honor the workflow's egress policy too (D-P2:
		// the current stored workflow doc governs the replay).
		execHost := wrapWorkflowEgress(e.Host, wf.Egress)
		var replayer *cassette.Replayer
		if mode == ReplayStepHermetic {
			tape, err := e.loadTape(runID, nodeID, recorded.Attempt, 0)
			if err != nil {
				return nil, err
			}
			replayer = cassette.NewReplayer(tape)
			replayer.Blobs = e.Store
			execHost = replayer
		}

		impl, ok := e.Registry[node.Type]
		if !ok {
			return nil, fmt.Errorf("no implementation for %s", node.Type)
		}
		subflowEffects := cassette.ModeOff
		if mode == ReplayStepHermetic {
			subflowEffects = cassette.ModeReplay
		}
		out, err := impl.Execute(ctx, flow.Input{
			Item: item, Config: node.Config, Credentials: node.Credentials,
			CredentialData: e.resolveCredentials(node),
			Context: &flow.Context{
				Nodes: outputs,
				Run: flow.RunContext{
					ID: runID, Trigger: run.Trigger, Status: run.Status,
					RootRunID: rootRunIDFor(run),
				},
				Job:  flow.JobContext{ID: run.JobID},
				Vars: varsFor(wf),
			},
			RunID: runID, NodeID: nodeID, Attempt: 1,
			Host: execHost, Subflow: &subflowRunner{engine: e, parentRunID: runID, parentNodeID: nodeID, rootRunID: rootRunIDFor(run), effects: subflowEffects}, Now: e.now(),
		})
		if _, suspended := flow.IsSuspend(err); suspended {
			// The node paused on an external decision (for example an approval).
			// Verify the recorded output blob rather than re-prompting.
			data, loadErr := e.Store.LoadBlob(recorded.OutputHash)
			if loadErr != nil {
				return nil, loadErr
			}
			var recordedOut flow.Output
			if loadErr := json.Unmarshal(data, &recordedOut); loadErr != nil {
				return nil, loadErr
			}
			actualHash, hashErr := hashJSON(recordedOut)
			if hashErr != nil {
				return nil, hashErr
			}
			return &ReplayResult{
				RunID: runID, Mode: mode, NodeID: nodeID,
				Match:        actualHash == recorded.OutputHash,
				RecordedHash: recorded.OutputHash, ActualHash: actualHash,
				Status: "waiting", Events: events,
				Config:      RedactForDisplay(node.Config),
				Credentials: node.Credentials,
			}, nil
		}
		if err != nil {
			return nil, err
		}
		actualHash, err := e.saveJSON(out)
		if err != nil {
			return nil, err
		}
		result := &ReplayResult{
			RunID: runID, Mode: mode, NodeID: nodeID,
			Match:        actualHash == recorded.OutputHash,
			RecordedHash: recorded.OutputHash, ActualHash: actualHash,
			Status: "done", Events: events,
			Config:      RedactForDisplay(node.Config),
			Credentials: node.Credentials,
		}
		if replayer != nil {
			result.Effects = len(replayer.Tape.Entries)
			result.Diffs = replayer.Diffs()
			result.Misses = replayer.Misses()
		}
		return result, nil

	case ReplayFrom, ReplayFromHermetic:
		if nodeID == "" {
			return nil, errors.New("from replay needs a node id")
		}
		hermetic := mode == ReplayFromHermetic
		clear := descendants(wf, nodeID)
		outputs, completed, err := e.loadOutputs(runID)
		if err != nil {
			return nil, err
		}
		for id := range clear {
			delete(outputs, id)
			delete(completed, id)
		}
		// Re-execution writes new events and may repeat side effects. Record it
		// as a separate run so the original audit log stays append-only and
		// immutable.
		opts := Options{
			Trigger: "replay", Input: e.triggerInput(runID, wf), Snapshot: wf,
			MaxAttempts: e.MaxAttempts, JobID: run.JobID, TemplateVersion: run.TemplateVersion,
			ParentRunID: runID, ParentNodeID: nodeID, Actor: run.Actor,
			RootRunID: rootRunIDFor(run), Effects: cassette.ModeOff,
		}
		if hermetic {
			// Reproduce the whole downstream branch from the source run's
			// tapes; the child run records any drift it encounters.
			opts.Effects = cassette.ModeReplay
			opts.CassetteRunID = runID
		}
		if override != nil {
			// Re-execute the target node with the edited input; downstream
			// nodes consume the new outputs.
			opts.ItemOverrides = map[string]flow.Item{nodeID: *override}
		}
		st, runCtx, cancel, err := e.begin(ctx, wf, opts, 0)
		if err != nil {
			return nil, err
		}
		if st.parked {
			// The replay child was created and parked instead of admitted. It
			// must never execute while the cap is held, so finish it as
			// cancelled and report the capacity conflict to the caller.
			e.cancelParkedReplay(st, cancel)
			return nil, ErrReplayNoSlot
		}
		defer cancel()
		defer e.unregister(st.runID)
		st.outputs = outputs
		st.completed = completed
		res, err := e.execute(runCtx, st, opts)
		if err != nil {
			return nil, err
		}
		return &ReplayResult{
			RunID: st.runID, Mode: mode, NodeID: nodeID,
			Status: res.Status, Outputs: res.Outputs, Events: res.Events,
			Match: res.Status == "done",
		}, nil

	default: // full
		outputs, _, err := e.loadOutputs(runID)
		if err != nil {
			return nil, err
		}
		if err := e.verifyRunBlobs(events); err != nil {
			return nil, err
		}
		return &ReplayResult{
			RunID: runID, Mode: ReplayFull, Status: run.Status,
			Match: true, Outputs: outputs, Events: events,
		}, nil
	}
}

type NodeData struct {
	NodeID      string            `json:"nodeId"`
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	Status      string            `json:"status"`
	Error       string            `json:"error,omitempty"`
	Outputs     flow.Output       `json:"outputs,omitempty"`
	Config      map[string]any    `json:"config,omitempty"`
	Credentials map[string]string `json:"credentials,omitempty"`
}

type DataContext struct {
	RunID      string     `json:"runId"`
	WorkflowID string     `json:"workflowId"`
	NodeID     string     `json:"nodeId,omitempty"`
	Upstream   []string   `json:"upstream"`
	Nodes      []NodeData `json:"nodes"`
}

// DataContext returns the recorded outputs of a run so the editor can offer
// upstream fields and previews. When nodeID is set, Upstream lists its
// transitive ancestors.
func (e *Engine) DataContext(runID, nodeID string) (*DataContext, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return nil, err
	}
	outputs, _, err := e.loadOutputs(runID)
	if err != nil {
		return nil, err
	}
	for id, out := range outputs {
		outputs[id] = RedactForDisplay(out)
	}
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return nil, err
	}
	status := map[string]string{}
	failures := map[string]string{}
	for _, ev := range events {
		switch ev.Type {
		case "node.started":
			status[ev.NodeID] = "running"
		case "node.finished":
			status[ev.NodeID] = "done"
		case "node.failed":
			status[ev.NodeID] = "failed"
			failures[ev.NodeID] = ev.Error
		}
	}
	result := &DataContext{RunID: runID, WorkflowID: wf.ID, NodeID: nodeID, Upstream: []string{}}
	if nodeID != "" {
		result.Upstream = ancestorIDs(wf, nodeID)
	}
	for _, node := range wf.Nodes {
		state := status[node.ID]
		if state == "" {
			state = "pending"
		}
		result.Nodes = append(result.Nodes, NodeData{
			NodeID: node.ID, Name: node.Name, Type: node.Type, Status: state,
			Error: failures[node.ID], Outputs: outputs[node.ID],
			Config: RedactForDisplay(node.Config), Credentials: node.Credentials,
		})
	}
	return result, nil
}

// Preview renders an expression against a recorded run without executing the
// workflow. It is used by the editor's live expression preview.
func (e *Engine) Preview(runID, nodeID, expression string) (string, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return "", err
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return "", err
	}
	outputs, _, err := e.loadOutputs(runID)
	if err != nil {
		return "", err
	}
	outputs = RedactForDisplay(outputs)
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return "", err
	}
	item := flow.Item{}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.NodeID != nodeID || ev.Type != "node.started" || ev.InputHash == "" {
			continue
		}
		data, loadErr := e.Store.LoadBlob(ev.InputHash)
		if loadErr == nil && json.Unmarshal(data, &item) == nil {
			break
		}
	}
	if item == nil {
		item = flow.Item{}
	}
	item = RedactForDisplay(item)
	ctx := &flow.Context{
		Nodes: outputs,
		Run: flow.RunContext{
			ID: runID, Trigger: run.Trigger, Status: run.Status,
			RootRunID: rootRunIDFor(run),
		},
		Job:  flow.JobContext{ID: run.JobID},
		Vars: varsFor(wf),
	}
	scope := expr.FromInput(flow.Input{
		Item: item, Context: ctx, RunID: runID, NodeID: nodeID,
	})
	return expr.RenderWithScope(expression, scope), nil
}

func ancestorIDs(wf *graph.Workflow, nodeID string) []string {
	parents := map[string][]string{}
	for _, edge := range wf.Edges {
		parents[edge.To.Node] = append(parents[edge.To.Node], edge.From.Node)
	}
	seen := map[string]bool{}
	stack := append([]string{}, parents[nodeID]...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] {
			continue
		}
		seen[id] = true
		stack = append(stack, parents[id]...)
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (e *Engine) loadOutputs(runID string) (map[string]flow.Output, map[string]bool, error) {
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return nil, nil, err
	}
	outputs := map[string]flow.Output{}
	completed := map[string]bool{}
	for _, ev := range events {
		if ev.Type != "node.finished" || ev.OutputHash == "" {
			continue
		}
		data, err := e.Store.LoadBlob(ev.OutputHash)
		if err != nil {
			return nil, nil, err
		}
		var out flow.Output
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, nil, err
		}
		outputs[ev.NodeID] = out
		completed[ev.NodeID] = true
	}
	return outputs, completed, nil
}
