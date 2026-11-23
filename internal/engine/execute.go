// Package engine executes typed workflow DAGs and records every step.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/schemas"
	"loom/internal/store"
)

func (e *Engine) execute(ctx context.Context, st *execState, opts Options) (*Result, error) {
	if st.parked {
		return nil, ErrParkedExecution
	}
	if _, err := graph.BuildPlan(st.wf); err != nil {
		return e.fail(st, "", err)
	}

	preds, succ := predecessorGraph(st.wf)
	ready := make([]string, 0, len(st.wf.Nodes))
	for id, count := range preds {
		if count == 0 {
			ready = append(ready, id)
		}
	}
	sort.Strings(ready)

	type outcome struct {
		nodeID  string
		output  flow.Output
		skipped bool
		err     error
	}
	results := make(chan outcome, e.runParallelism(st.wf, opts))
	inFlight := 0
	var firstErr error
	var firstNode string

	settle := func(nodeID string) {
		for _, next := range succ[nodeID] {
			preds[next]--
			if preds[next] == 0 {
				ready = append(ready, next)
			}
		}
		sort.Strings(ready)
	}

	limit := e.runParallelism(st.wf, opts)
	for len(ready) > 0 || inFlight > 0 {
		for firstErr == nil && len(ready) > 0 && inFlight < limit {
			nodeID := ready[0]
			ready = ready[1:]

			if st.completed[nodeID] {
				settle(nodeID)
				continue
			}
			node := findNode(st.wf, nodeID)
			if node == nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("node %q not found", nodeID)
					firstNode = nodeID
				}
				continue
			}

			in, active := e.inputFor(st, node, opts)
			if !active {
				st.skipped[nodeID] = true
				settle(nodeID)
				continue
			}

			inFlight++
			go func(node *graph.Node, in flow.Input) {
				out, err := e.executeNode(ctx, st, node, in, opts)
				results <- outcome{nodeID: node.ID, output: out, err: err}
			}(node, in)
		}

		if inFlight == 0 {
			break
		}
		o := <-results
		inFlight--
		if o.err != nil {
			if firstErr == nil {
				firstErr = o.err
				firstNode = o.nodeID
			}
			continue
		}
		if o.skipped {
			settle(o.nodeID)
			continue
		}
		if o.output == nil {
			o.output = flow.Output{}
		}
		st.outputs[o.nodeID] = o.output
		st.completed[o.nodeID] = true
		settle(o.nodeID)
	}

	if firstErr != nil {
		if suspend, ok := flow.IsSuspend(firstErr); ok {
			return e.suspend(st, firstNode, suspend)
		}
		if errors.Is(firstErr, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return e.fail(st, firstNode, errors.New("workflow execution timed out"))
		}
		if errors.Is(firstErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return e.cancelled(st)
		}
		return e.fail(st, firstNode, firstErr)
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return e.fail(st, "", errors.New("workflow execution timed out"))
		}
		return e.cancelled(st)
	}
	return e.done(st)
}

// predecessorGraph counts distinct upstream nodes per node. Multiple edges
// between the same pair (for example main and error ports) still gate the
// target exactly once.
func predecessorGraph(wf *graph.Workflow) (map[string]int, map[string][]string) {
	preds := make(map[string]int, len(wf.Nodes))
	succ := make(map[string][]string, len(wf.Nodes))
	for _, n := range wf.Nodes {
		preds[n.ID] = 0
	}
	seen := make(map[string]map[string]bool, len(wf.Nodes))
	for _, edge := range wf.Edges {
		if _, ok := preds[edge.From.Node]; !ok {
			continue
		}
		if _, ok := preds[edge.To.Node]; !ok {
			continue
		}
		if seen[edge.To.Node] == nil {
			seen[edge.To.Node] = map[string]bool{}
		}
		if seen[edge.To.Node][edge.From.Node] {
			continue
		}
		seen[edge.To.Node][edge.From.Node] = true
		preds[edge.To.Node]++
		succ[edge.From.Node] = append(succ[edge.From.Node], edge.To.Node)
	}
	return preds, succ
}

func (e *Engine) inputFor(st *execState, node *graph.Node, opts Options) (flow.Input, bool) {
	nodesCopy := make(map[string]flow.Output, len(st.outputs))
	for id, out := range st.outputs {
		nodesCopy[id] = out
	}
	in := flow.Input{
		Config: node.Config, Credentials: node.Credentials,
		RunID: st.runID, NodeID: node.ID,
		Decision:      st.decision[node.ID],
		DecisionNote:  st.decisionInfo[node.ID].Note,
		DecisionActor: st.decisionInfo[node.ID].Actor,
		DecisionAt:    st.decisionInfo[node.ID].At,
		Host:          st.host,
		Subflow:       &subflowRunner{engine: e, depth: st.depth, parentRunID: st.runID, parentNodeID: node.ID, actor: st.actor, rootRunID: st.rootRunID, effects: st.effects, cassetteRunID: st.cassetteRunID},
		Now:           e.now(),
		Context: &flow.Context{
			Nodes: nodesCopy,
			Run: flow.RunContext{
				ID: st.runID, Trigger: opts.Trigger, Status: "running",
				Hops: opts.EventHops, RootRunID: st.rootRunID,
			},
			Job:  flow.JobContext{ID: opts.JobID},
			Vars: st.vars,
		},
	}

	def, ok := nodes.Get(node.Type)
	if !ok {
		in.Item = flow.Item{}
		return in, true
	}
	if def.Category == "trigger" {
		in.Item = flow.CloneItem(opts.Input)
		return in, true
	}
	if item, ok := opts.ItemOverrides[node.ID]; ok {
		// Replay with an edited input: the override replaces the merged
		// upstream item for this node.
		in.Item = item
		return in, true
	}

	edges := make([]graph.Edge, 0, 2)
	for _, edge := range st.wf.Edges {
		if edge.To.Node == node.ID {
			edges = append(edges, edge)
		}
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].From.Node == edges[j].From.Node {
			return edges[i].From.Port < edges[j].From.Port
		}
		return edges[i].From.Node < edges[j].From.Node
	})

	items := make([]flow.Item, 0, len(edges))
	for _, edge := range edges {
		out, ok := st.outputs[edge.From.Node]
		if !ok {
			continue
		}
		item, ok := out[edge.From.Port]
		if !ok {
			continue
		}
		items = append(items, item)
	}
	if len(items) == 0 {
		return in, false
	}
	mode, _ := node.Config["mode"].(string)
	in.Item = mergeItems(items, mode)
	return in, true
}

func (e *Engine) executeNode(ctx context.Context, st *execState, node *graph.Node, in flow.Input, opts Options) (flow.Output, error) {
	if st.parked {
		return nil, ErrParkedExecution
	}
	impl, ok := e.Registry[node.Type]
	if !ok && strings.HasPrefix(node.Type, nodes.ConnectorTypePrefix) {
		// Connector-authored node types all dispatch to the one
		// connectorNode implementation; the request template in the
		// node's config carries the specifics.
		impl, ok = e.Registry[nodes.ConnectorTypePrefix]
	}
	if !ok {
		return nil, fmt.Errorf("no implementation for %s", node.Type)
	}
	in.CredentialData = e.resolveCredentials(node)
	if opts.AutoApprove && node.Type == "flow.approval" && in.Decision == "" {
		in.Decision = "approved"
		in.DecisionNote = "auto-approved"
		in.DecisionActor = "system:auto-approve"
		in.DecisionAt = e.now()
	}

	// Per-node rate limit: at most rateLimitPerMinute calls per minute to
	// this node, across every run of the workflow. The wait is computed
	// without reserving a slot; the slot is recorded only once the caller can
	// actually proceed and Sleep has moved past the window. This prevents a
	// herd of blocked callers from waking together and all proceeding.
	if rpm := intValue(node.Config["rateLimitPerMinute"]); rpm > 0 {
		if err := e.acquireRateSlot(ctx, st.wf.ID+"/"+node.ID, rpm); err != nil {
			return nil, err
		}
	}

	idempotencyKey := in.IdempotencyKey()
	inputHash, err := e.saveJSON(in.Item)
	if err != nil {
		return nil, err
	}
	if err := e.emit(st, store.Event{
		Type: "node.started", NodeID: node.ID, Attempt: 1, InputHash: inputHash,
		Data: map[string]any{"idempotencyKey": idempotencyKey},
	}); err != nil {
		return nil, err
	}

	maxAttempts := e.attemptLimit(node, opts)
	started := e.now()
	var out flow.Output
	var execErr error
	succeededAttempt := 1
	var trace map[string]map[string]any
	if node.Batch != nil {
		out, execErr = e.executeBatch(ctx, st, node, in, impl, opts)
	} else {
		out, execErr, succeededAttempt, trace = e.runAttempts(ctx, st, node, in, impl, opts, inputHash, idempotencyKey)
	}
	if execErr != nil {
		if node.OnError == "continue" || node.OnError == "branch" {
			return e.handleNodeError(st, node, execErr, maxAttempts)
		}
		return nil, execErr
	}
	if out == nil {
		out = flow.Output{}
	}

	outputHash, err := e.saveJSON(out)
	if err != nil {
		return nil, err
	}
	data := itemEventData(in, idempotencyKey, trace)
	// Output contract (v2 plan D16): the main output is checked against the
	// node's resolved contract and the result is recorded on the finished
	// event. Data always flows onward — this is observability, never a
	// gate — so one drifting upstream field cannot break a production run.
	if len(node.OutputSchemaFields) > 0 {
		data["contract"] = contractResult(out["main"], node.OutputSchemaFields)
	}
	if err := e.emit(st, store.Event{
		Type: "node.finished", NodeID: node.ID, Attempt: succeededAttempt,
		InputHash: inputHash, OutputHash: outputHash,
		DurationMs: e.now().Sub(started).Milliseconds(),
		Data:       data,
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// contractResult checks one item against a contract and renders the result
// for the event log: {"valid": bool, "violations": [{path, message}]}.
func contractResult(item flow.Item, fields []schemas.Field) map[string]any {
	violations := schemas.Check(item, fields)
	result := map[string]any{"valid": len(violations) == 0}
	if len(violations) > 0 {
		rows := make([]map[string]any, 0, len(violations))
		for _, v := range violations {
			rows = append(rows, map[string]any{"path": v.Path, "message": v.Message})
		}
		result["violations"] = rows
	}
	return result
}

func (e *Engine) attemptLimit(node *graph.Node, opts Options) int {
	maxAttempts := e.MaxAttempts
	if opts.MaxAttempts > 0 {
		maxAttempts = opts.MaxAttempts
	}
	if node.Retry != nil && node.Retry.MaxAttempts > 0 {
		maxAttempts = node.Retry.MaxAttempts
	}
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	return maxAttempts
}

// runAttempts executes one item with retries and records node-level attempt
// events. It does not append node.started/node.finished; callers own those so
// batched nodes can emit one finished event with the aggregate output.
func (e *Engine) runAttempts(ctx context.Context, st *execState, node *graph.Node, in flow.Input, impl flow.Node, opts Options, inputHash, idempotencyKey string) (flow.Output, error, int, map[string]map[string]any) {
	// Durable waits are advisory: only flow.wait reads these fields.
	in.DurableWait = e.DurableWaits
	in.WakeAt = st.wakeAt
	maxAttempts := e.attemptLimit(node, opts)
	var out flow.Output
	var execErr error
	succeededAttempt := 1
	trace := map[string]map[string]any{}
	in.Trace = func(kind string, data map[string]any) {
		if kind != "" && data != nil {
			trace[kind] = data
		}
	}
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		trace = map[string]map[string]any{}
		in.Attempt = attempt
		scope, scopeErr := e.effectScope(st, node, in, attempt)
		if scopeErr != nil {
			return nil, scopeErr, attempt, trace
		}
		in.Host = scope.host
		out, execErr = impl.Execute(ctx, in)
		// Persist the tape (or record drift) before deciding the attempt
		// outcome, so a failed attempt still leaves an inspectable recording.
		if flushErr := e.finishEffects(st, node, scope, in, attempt); flushErr != nil && execErr == nil {
			execErr = flushErr
		}
		if execErr == nil {
			succeededAttempt = attempt
			break
		}
		if _, ok := flow.IsSuspend(execErr); ok {
			return nil, execErr, attempt, trace
		}
		if errors.Is(execErr, context.Canceled) {
			return nil, execErr, attempt, trace
		}
		if err := e.emit(st, store.Event{
			Type: "node.failed", NodeID: node.ID, Attempt: attempt, InputHash: inputHash,
			Error: execErr.Error(), Data: itemEventData(in, idempotencyKey, trace),
		}); err != nil {
			return nil, err, attempt, trace
		}
		if attempt < maxAttempts {
			if err := e.emit(st, store.Event{
				Type: "node.retried", NodeID: node.ID, Attempt: attempt + 1,
				Data: itemEventData(in, idempotencyKey, nil),
			}); err != nil {
				return nil, err, attempt, trace
			}
			backoff := retryDelay(node.Retry, attempt)
			select {
			case <-ctx.Done():
				return nil, ctx.Err(), attempt, trace
			case <-time.After(backoff):
			}
		}
	}
	return out, execErr, succeededAttempt, trace
}

func (e *Engine) resolveCredentials(node *graph.Node) map[string]flow.Credential {
	out := map[string]flow.Credential{}
	for key, id := range node.Credentials {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if e.Store != nil {
			typ, raw, err := e.Store.GetCredential(id)
			if err == nil {
				cred := flow.Credential{Type: typ, Raw: string(raw)}
				var data map[string]any
				if json.Unmarshal(raw, &data) == nil {
					cred.Data = data
				}
				out[key] = cred
				continue
			}
		}
		if e.CredentialResolver != nil {
			if cred, ok := e.CredentialResolver(id); ok {
				out[key] = cred
			}
		}
	}
	return out
}
