// Package engine executes typed workflow DAGs and records every step.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"loom/internal/expr"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/store"
)

const maxSubflowDepth = 5

type Engine struct {
	Store              *store.Store
	Host               flow.Host
	Registry           map[string]flow.Node
	ResolveWorkflow    func(string) (*graph.Workflow, error)
	CredentialResolver func(string) (flow.Credential, bool)
	Parallelism        int
	MaxAttempts        int
	Now                func() time.Time

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

type Options struct {
	Trigger         string
	Input           flow.Item
	AutoApprove     bool
	MaxAttempts     int
	MaxConcurrency  int
	Timeout         time.Duration
	ParentRunID     string
	ParentNodeID    string
	Snapshot        *graph.Workflow
	JobID           string
	TemplateVersion int
}

type Result struct {
	RunID       string                 `json:"runId"`
	Status      string                 `json:"status"`
	Outputs     map[string]flow.Output `json:"outputs,omitempty"`
	ResumeNode  string                 `json:"resumeNode,omitempty"`
	ResumeToken string                 `json:"resumeToken,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Events      []store.Event          `json:"events,omitempty"`
}

func New(st *store.Store, h flow.Host) *Engine {
	return &Engine{
		Store:           st,
		Host:            h,
		Registry:        nodes.Registry(),
		ResolveWorkflow: st.GetWorkflow,
		Parallelism:     4,
		MaxAttempts:     3,
		Now:             time.Now,
		active:          map[string]context.CancelFunc{},
	}
}

type execState struct {
	runID     string
	wf        *graph.Workflow
	outputs   map[string]flow.Output
	completed map[string]bool
	skipped   map[string]bool
	decision  map[string]string
	vars      map[string]any
	depth     int
}

func (e *Engine) Start(ctx context.Context, wf *graph.Workflow, opts Options) (*Result, error) {
	return e.start(ctx, wf, opts, 0)
}

// StartAsync creates and registers a run, then executes it in the background.
// The returned run id can be streamed from the event log while it works.
func (e *Engine) StartAsync(ctx context.Context, wf *graph.Workflow, opts Options) (string, error) {
	st, runCtx, cancel, err := e.begin(ctx, wf, opts, 0)
	if err != nil {
		return "", err
	}
	go func() {
		defer cancel()
		defer e.unregister(st.runID)
		_, _ = e.execute(runCtx, st, opts)
	}()
	return st.runID, nil
}

func (e *Engine) start(ctx context.Context, wf *graph.Workflow, opts Options, depth int) (*Result, error) {
	st, runCtx, cancel, err := e.begin(ctx, wf, opts, depth)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer e.unregister(st.runID)
	return e.execute(runCtx, st, opts)
}

func (e *Engine) begin(ctx context.Context, wf *graph.Workflow, opts Options, depth int) (*execState, context.Context, context.CancelFunc, error) {
	runWF := wf
	if opts.Snapshot != nil {
		runWF = opts.Snapshot
	}
	report := graph.Validate(runWF)
	if report.HasErrors() {
		return nil, nil, nil, fmt.Errorf("invalid workflow: %s", firstError(report))
	}
	if opts.Snapshot == nil {
		// Manual and webhook runs load the workflow from the store. Only
		// persist a file-backed workflow that is not stored yet; rewriting it
		// on every run churns updated_at and version state for no benefit.
		if _, err := e.Store.GetWorkflow(wf.ID); err != nil {
			if err := e.Store.SaveWorkflow(wf); err != nil {
				return nil, nil, nil, err
			}
		}
	}
	version, err := hashJSON(runWF)
	if err != nil {
		return nil, nil, nil, err
	}
	snapshot, err := json.Marshal(runWF)
	if err != nil {
		return nil, nil, nil, err
	}

	trigger := opts.Trigger
	if trigger == "" {
		trigger = "manual"
	}
	runID := flow.NewID("run")
	started := e.now()
	run := store.Run{
		ID: runID, WorkflowID: runWF.ID, WorkflowVersion: version,
		TemplateVersion: opts.TemplateVersion, JobID: opts.JobID,
		Status: "queued", Trigger: trigger, StartedAt: started,
		ParentRunID: opts.ParentRunID, ParentNodeID: opts.ParentNodeID,
		Snapshot: snapshot,
	}
	if err := e.Store.CreateRun(run); err != nil {
		return nil, nil, nil, err
	}
	if err := e.appendEvent(runID, store.Event{Type: "run.started", Data: map[string]any{"trigger": trigger}}); err != nil {
		return nil, nil, nil, err
	}

	timeout := opts.Timeout
	if timeout <= 0 && runWF.Settings != nil {
		if sec, ok := runWF.Settings["timeoutSeconds"].(float64); ok && sec > 0 {
			timeout = time.Duration(sec * float64(time.Second))
		} else if sec, ok := runWF.Settings["timeout"].(float64); ok && sec > 0 {
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

	st := &execState{
		runID: runID, wf: runWF,
		outputs: map[string]flow.Output{}, completed: map[string]bool{},
		skipped: map[string]bool{}, decision: map[string]string{},
		vars: varsFor(runWF), depth: depth,
	}
	return st, runCtx, cancel, nil
}

func varsFor(wf *graph.Workflow) map[string]any {
	vars := map[string]any{}
	if wf == nil {
		return vars
	}
	if raw, ok := wf.Settings["variables"].(map[string]any); ok {
		for key, value := range raw {
			vars[key] = value
		}
	}
	return vars
}

func (e *Engine) execute(ctx context.Context, st *execState, opts Options) (*Result, error) {
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
		Decision: st.decision[node.ID],
		Host:     e.Host,
		Subflow:  &subflowRunner{engine: e, depth: st.depth, parentRunID: st.runID, parentNodeID: node.ID},
		Now:      e.now(),
		Context: &flow.Context{
			Nodes: nodesCopy,
			Run:   flow.RunContext{ID: st.runID, Trigger: opts.Trigger, Status: "running"},
			Job:   flow.JobContext{ID: opts.JobID},
			Vars:  st.vars,
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
	in.Item = mergeItems(items)
	return in, true
}

func (e *Engine) executeNode(ctx context.Context, st *execState, node *graph.Node, in flow.Input, opts Options) (flow.Output, error) {
	impl, ok := e.Registry[node.Type]
	if !ok {
		return nil, fmt.Errorf("no implementation for %s", node.Type)
	}
	in.CredentialData = e.resolveCredentials(node)
	if opts.AutoApprove && node.Type == "flow.approval" && in.Decision == "" {
		in.Decision = "approved"
	}

	idempotencyKey := in.IdempotencyKey()
	inputHash, err := e.saveJSON(in.Item)
	if err != nil {
		return nil, err
	}
	if err := e.appendEvent(st.runID, store.Event{
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
	if err := e.appendEvent(st.runID, store.Event{
		Type: "node.finished", NodeID: node.ID, Attempt: succeededAttempt,
		InputHash: inputHash, OutputHash: outputHash,
		DurationMs: e.now().Sub(started).Milliseconds(),
		Data:       itemEventData(in, idempotencyKey, trace),
	}); err != nil {
		return nil, err
	}
	return out, nil
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
		out, execErr = impl.Execute(ctx, in)
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
		if err := e.appendEvent(st.runID, store.Event{
			Type: "node.failed", NodeID: node.ID, Attempt: attempt, InputHash: inputHash,
			Error: execErr.Error(), Data: itemEventData(in, idempotencyKey, trace),
		}); err != nil {
			return nil, err, attempt, trace
		}
		if attempt < maxAttempts {
			if err := e.appendEvent(st.runID, store.Event{
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

// executeBatch runs the node once per array element. Per-item failures are
// recorded as item.failed events; ContinueOnItemError decides whether the node
// fails or returns a partial result.
func (e *Engine) executeBatch(ctx context.Context, st *execState, node *graph.Node, in flow.Input, impl flow.Node, opts Options) (flow.Output, error) {
	path := strings.TrimSpace(node.Batch.ItemsPath)
	if path == "" {
		path = "items"
	}
	raw, ok := lookupPath(in.Item, path)
	if !ok {
		return nil, fmt.Errorf("batch: item has no %q array", path)
	}
	items, err := toItems(raw)
	if err != nil {
		return nil, fmt.Errorf("batch: %w", err)
	}

	concurrency := 1
	if node.Batch.Concurrency > 0 {
		concurrency = node.Batch.Concurrency
	}

	results := make([]any, len(items))
	var failed atomic.Int64
	var firstErr error
	var errMu sync.Mutex

	processItem := func(idx int, it flow.Item) bool {
		sub := in
		sub.Item = flow.CloneItem(it)
		if in.Context != nil {
			ctxCopy := *in.Context
			ctxCopy.Index = idx
			ctxCopy.HasIndex = true
			sub.Context = &ctxCopy
		}
		itemKey := sub.IdempotencyKey()
		itemHash, err := e.saveJSON(sub.Item)
		if err != nil {
			errMu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			errMu.Unlock()
			return false
		}
		if err := e.appendEvent(st.runID, store.Event{
			Type: "item.started", NodeID: node.ID, InputHash: itemHash,
			Data: map[string]any{"index": idx, "idempotencyKey": itemKey},
		}); err != nil {
			errMu.Lock()
			if firstErr == nil {
				firstErr = err
			}
			errMu.Unlock()
			return false
		}
		out, itemErr, _, _ := e.runAttempts(ctx, st, node, sub, impl, opts, itemHash, itemKey)
		if itemErr != nil {
			failed.Add(1)
			results[idx] = flow.Item{"index": idx, "error": itemErr.Error()}
			_ = e.appendEvent(st.runID, store.Event{
				Type: "item.failed", NodeID: node.ID,
				Error: itemErr.Error(), Data: map[string]any{"index": idx},
			})
			errMu.Lock()
			if firstErr == nil {
				firstErr = itemErr
			}
			errMu.Unlock()
			return false
		}
		result := flow.Item{}
		if out != nil {
			if main, ok := out["main"]; ok {
				result = main
			}
		}
		results[idx] = result
		_ = e.appendEvent(st.runID, store.Event{
			Type: "item.finished", NodeID: node.ID, Data: map[string]any{"index": idx},
		})
		return true
	}

	if concurrency <= 1 {
		for i, item := range items {
			if firstErr != nil && !node.Batch.ContinueOnItemError {
				break
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !processItem(i, item) && !node.Batch.ContinueOnItemError {
				break
			}
		}
	} else {
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		for i, item := range items {
			errMu.Lock()
			stop := firstErr != nil && !node.Batch.ContinueOnItemError
			errMu.Unlock()
			if stop {
				break
			}

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case sem <- struct{}{}:
			}

			wg.Add(1)
			go func(idx int, it flow.Item) {
				defer wg.Done()
				defer func() { <-sem }()
				processItem(idx, it)
			}(i, item)
		}
		wg.Wait()
	}

	if firstErr != nil && !node.Batch.ContinueOnItemError {
		return nil, firstErr
	}

	return flow.Output{"main": flow.Item{
		"items": results, "count": len(items), "failed": int(failed.Load()),
	}}, nil
}

func lookupPath(item flow.Item, path string) (any, bool) {
	var current any = map[string]any(item)
	for _, part := range strings.Split(path, ".") {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		case flow.Item:
			next, ok := node[part]
			if !ok {
				return nil, false
			}
			current = next
		default:
			return nil, false
		}
	}
	return current, true
}

func toItems(raw any) ([]flow.Item, error) {
	switch list := raw.(type) {
	case []any:
		out := make([]flow.Item, 0, len(list))
		for _, value := range list {
			item, ok := asItem(value)
			if !ok {
				return nil, fmt.Errorf("array elements must be objects")
			}
			out = append(out, item)
		}
		return out, nil
	case []flow.Item:
		return list, nil
	default:
		return nil, fmt.Errorf("value must be an array of objects")
	}
}

func asItem(value any) (flow.Item, bool) {
	switch item := value.(type) {
	case flow.Item:
		return item, true
	case map[string]any:
		return flow.Item(item), true
	default:
		return nil, false
	}
}

func itemEventData(in flow.Input, idempotencyKey string, trace map[string]map[string]any) map[string]any {
	data := nodeEventData(idempotencyKey, trace)
	if in.Context != nil && in.Context.HasIndex {
		data["itemIndex"] = in.Context.Index
	}
	return data
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

// handleNodeError converts a terminal node failure into a normal output when
// the node opts into continue or branch handling. The node.finished event keeps
// the error text, so the run can finish while the failure stays visible and is
// restorable after a crash.
func (e *Engine) handleNodeError(st *execState, node *graph.Node, nodeErr error, attempts int) (flow.Output, error) {
	item := flow.Item{
		"error":    nodeErr.Error(),
		"nodeId":   node.ID,
		"attempts": attempts,
	}
	out := flow.Output{}
	if node.OnError == "branch" {
		out["error"] = item
	} else {
		out["main"] = item
	}
	outputHash, err := e.saveJSON(out)
	if err != nil {
		return nil, err
	}
	if err := e.appendEvent(st.runID, store.Event{
		Type: "node.finished", NodeID: node.ID, Attempt: attempts,
		OutputHash: outputHash, Error: nodeErr.Error(),
		Data: map[string]any{"handled": node.OnError},
	}); err != nil {
		return nil, err
	}
	return out, nil
}

// retryDelay returns the wait before the next attempt. Without a policy it
// keeps the historical quadratic backoff; with one it uses capped exponential
// backoff so a single flaky node can retry cheaply without stalling the run.
func retryDelay(policy *graph.RetryPolicy, attempt int) time.Duration {
	if policy == nil {
		return time.Duration(attempt*attempt) * 100 * time.Millisecond
	}
	base := time.Duration(policy.BackoffMs) * time.Millisecond
	if base <= 0 {
		base = 100 * time.Millisecond
	}
	maxDelay := time.Duration(policy.MaxDelayMs) * time.Millisecond
	delay := base
	for i := 1; i < attempt; i++ {
		if maxDelay > 0 && delay >= maxDelay {
			return maxDelay
		}
		delay *= 2
	}
	if maxDelay > 0 && delay > maxDelay {
		delay = maxDelay
	}
	return delay
}

func nodeEventData(idempotencyKey string, trace map[string]map[string]any) map[string]any {
	data := map[string]any{"idempotencyKey": idempotencyKey}
	for kind, value := range trace {
		data[kind] = value
	}
	return data
}

func (e *Engine) suspend(st *execState, nodeID string, s *flow.Suspend) (*Result, error) {
	token := flow.NewToken()
	data, _ := json.Marshal(s.Data)
	if err := e.Store.SetRunWaiting(st.runID, nodeID, token, data); err != nil {
		return nil, err
	}
	eventData := map[string]any{"reason": s.Reason}
	for k, v := range s.Data {
		eventData[k] = v
	}
	if err := e.appendEvent(st.runID, store.Event{
		Type: "run.waiting", NodeID: nodeID, Data: eventData,
	}); err != nil {
		return nil, err
	}
	return e.result(st, "waiting", nodeID, token, ""), nil
}

func (e *Engine) fail(st *execState, nodeID string, runErr error) (*Result, error) {
	now := e.now()
	if err := e.appendEvent(st.runID, store.Event{Type: "run.failed", NodeID: nodeID, Error: runErr.Error()}); err != nil {
		return nil, err
	}
	if err := e.Store.UpdateRunStatus(st.runID, "failed", runErr.Error(), &now); err != nil {
		return nil, err
	}
	return e.result(st, "failed", "", "", runErr.Error()), nil
}

func (e *Engine) cancelled(st *execState) (*Result, error) {
	now := e.now()
	if err := e.appendEvent(st.runID, store.Event{Type: "run.cancelled"}); err != nil {
		return nil, err
	}
	if err := e.Store.UpdateRunStatus(st.runID, "cancelled", "", &now); err != nil {
		return nil, err
	}
	return e.result(st, "cancelled", "", "", ""), nil
}

func (e *Engine) done(st *execState) (*Result, error) {
	now := e.now()
	if err := e.appendEvent(st.runID, store.Event{Type: "run.finished"}); err != nil {
		return nil, err
	}
	if err := e.Store.UpdateRunStatus(st.runID, "done", "", &now); err != nil {
		return nil, err
	}
	return e.result(st, "done", "", "", ""), nil
}

func (e *Engine) result(st *execState, status, resumeNode, token, errMsg string) *Result {
	events, _ := e.Store.ListEvents(st.runID)
	return &Result{
		RunID: st.runID, Status: status, Outputs: st.outputs,
		ResumeNode: resumeNode, ResumeToken: token, Error: errMsg, Events: events,
	}
}

func (e *Engine) workflowForRun(run *store.Run) (*graph.Workflow, error) {
	if len(run.Snapshot) > 0 {
		var wf graph.Workflow
		if err := json.Unmarshal(run.Snapshot, &wf); err != nil {
			return nil, err
		}
		return &wf, nil
	}
	return e.Store.GetWorkflow(run.WorkflowID)
}

func (e *Engine) Resume(ctx context.Context, runID, decision string) (*Result, error) {
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

	if decision != "approved" && decision != "rejected" {
		return nil, fmt.Errorf("decision must be approved or rejected")
	}

	st := &execState{
		runID: runID, wf: wf, outputs: outputs, completed: completed,
		skipped: map[string]bool{}, decision: map[string]string{run.ResumeNode: decision},
		vars: varsFor(wf),
	}
	if err := e.Store.ClearRunWaiting(runID); err != nil {
		return nil, err
	}
	if err := e.appendEvent(runID, store.Event{
		Type: "run.resumed", NodeID: run.ResumeNode, Data: map[string]any{"decision": decision},
	}); err != nil {
		return nil, err
	}

	opts := Options{Trigger: run.Trigger, Input: flow.Item{}, MaxAttempts: e.MaxAttempts}
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

// ResumeInterrupted continues a run that was left in flight when the previous
// process stopped. Completed nodes are restored from the event log, so only
// unfinished work executes again. The run keeps its id and audit trail; a
// run.resumed event marks the restart boundary.
func (e *Engine) ResumeInterrupted(ctx context.Context, runID string) (*Result, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	if run.Status != "running" && run.Status != "queued" {
		return nil, fmt.Errorf("run %s is %s, not in flight", runID, run.Status)
	}
	failInterrupted := func(cause error) (*Result, error) {
		now := e.now()
		_ = e.appendEvent(runID, store.Event{Type: "run.failed", Error: cause.Error()})
		_ = e.Store.UpdateRunStatus(runID, "failed", cause.Error(), &now)
		return nil, cause
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return failInterrupted(fmt.Errorf("cannot resolve workflow: %w", err))
	}
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return failInterrupted(fmt.Errorf("cannot read events: %w", err))
	}
	if err := verifyEvents(events); err != nil {
		return failInterrupted(fmt.Errorf("invalid event log: %w", err))
	}
	outputs, completed, err := e.loadOutputs(runID)
	if err != nil {
		return failInterrupted(fmt.Errorf("cannot load outputs: %w", err))
	}

	st := &execState{
		runID: runID, wf: wf, outputs: outputs, completed: completed,
		skipped: map[string]bool{}, decision: map[string]string{},
		vars: varsFor(wf),
	}
	if err := e.Store.UpdateRunStatus(runID, "running", "", nil); err != nil {
		return nil, err
	}
	if err := e.appendEvent(runID, store.Event{
		Type: "run.resumed", Data: map[string]any{"reason": "restart"},
	}); err != nil {
		return nil, err
	}

	opts := Options{
		Trigger: run.Trigger, Input: flow.Item{}, MaxAttempts: e.MaxAttempts,
		JobID: run.JobID, TemplateVersion: run.TemplateVersion,
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

func (e *Engine) Cancel(runID string) bool {
	e.mu.Lock()
	cancel, ok := e.active[runID]
	e.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// CancelAll cancels all currently executing runs (used during graceful shutdown).
func (e *Engine) CancelAll() int {
	e.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(e.active))
	for _, cancel := range e.active {
		cancels = append(cancels, cancel)
	}
	e.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return len(cancels)
}

type ReplayMode string

const (
	ReplayFull ReplayMode = "full"
	ReplayStep ReplayMode = "step"
	ReplayFrom ReplayMode = "from"
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
	Events       []store.Event          `json:"events,omitempty"`
}

func (e *Engine) Replay(ctx context.Context, runID, nodeID string, mode ReplayMode) (*ReplayResult, error) {
	run, err := e.Store.GetRun(runID)
	if err != nil {
		return nil, err
	}
	wf, err := e.workflowForRun(run)
	if err != nil {
		return nil, err
	}
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return nil, err
	}

	switch mode {
	case ReplayStep:
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
		outputs, _, err := e.loadOutputs(runID)
		if err != nil {
			return nil, err
		}

		inputData, err := e.Store.LoadBlob(recorded.InputHash)
		if err != nil {
			return nil, err
		}
		var item flow.Item
		if err := json.Unmarshal(inputData, &item); err != nil {
			return nil, err
		}

		impl, ok := e.Registry[node.Type]
		if !ok {
			return nil, fmt.Errorf("no implementation for %s", node.Type)
		}
		out, err := impl.Execute(ctx, flow.Input{
			Item: item, Config: node.Config, Credentials: node.Credentials,
			CredentialData: e.resolveCredentials(node),
			Context: &flow.Context{
				Nodes: outputs,
				Run:   flow.RunContext{ID: runID, Trigger: run.Trigger, Status: run.Status},
				Job:   flow.JobContext{ID: run.JobID},
				Vars:  varsFor(wf),
			},
			RunID: runID, NodeID: nodeID, Attempt: 1,
			Host: e.Host, Subflow: &subflowRunner{engine: e, parentRunID: runID, parentNodeID: nodeID}, Now: e.now(),
		})
		if _, suspended := flow.IsSuspend(err); suspended {
			// The node paused on an external decision (for example an approval).
			// Verify the recorded output blob rather than re-prompting.
			data, loadErr := e.Store.LoadBlob(recorded.OutputHash)
			if loadErr != nil {
				return nil, err
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
			}, nil
		}
		if err != nil {
			return nil, err
		}
		actualHash, err := e.saveJSON(out)
		if err != nil {
			return nil, err
		}
		return &ReplayResult{
			RunID: runID, Mode: mode, NodeID: nodeID,
			Match:        actualHash == recorded.OutputHash,
			RecordedHash: recorded.OutputHash, ActualHash: actualHash,
			Status: "done", Events: events,
		}, nil

	case ReplayFrom:
		if nodeID == "" {
			return nil, errors.New("from replay needs a node id")
		}
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
			ParentRunID: runID, ParentNodeID: nodeID,
		}
		st, runCtx, cancel, err := e.begin(ctx, wf, opts, 0)
		if err != nil {
			return nil, err
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
		if err := verifyEvents(events); err != nil {
			return nil, err
		}
		outputs, _, err := e.loadOutputs(runID)
		if err != nil {
			return nil, err
		}
		return &ReplayResult{
			RunID: runID, Mode: ReplayFull, Status: run.Status,
			Match: true, Outputs: outputs, Events: events,
		}, nil
	}
}

type NodeData struct {
	NodeID  string      `json:"nodeId"`
	Name    string      `json:"name"`
	Type    string      `json:"type"`
	Status  string      `json:"status"`
	Error   string      `json:"error,omitempty"`
	Outputs flow.Output `json:"outputs,omitempty"`
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
	ctx := &flow.Context{
		Nodes: outputs,
		Run:   flow.RunContext{ID: runID, Trigger: run.Trigger, Status: run.Status},
		Job:   flow.JobContext{ID: run.JobID},
		Vars:  varsFor(wf),
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

type subflowRunner struct {
	engine       *Engine
	depth        int
	parentRunID  string
	parentNodeID string
}

func (r *subflowRunner) RunSubflow(ctx context.Context, workflowID string, item flow.Item) (flow.Item, error) {
	if r.depth >= maxSubflowDepth {
		return nil, fmt.Errorf("subflow depth limit (%d) exceeded", maxSubflowDepth)
	}
	if r.engine.ResolveWorkflow == nil {
		return nil, errors.New("subflow resolver is not configured")
	}
	wf, err := r.engine.ResolveWorkflow(workflowID)
	if err != nil {
		return nil, err
	}
	res, err := r.engine.start(ctx, wf, Options{
		Trigger: "subflow", Input: item, MaxAttempts: r.engine.MaxAttempts,
		ParentRunID: r.parentRunID, ParentNodeID: r.parentNodeID,
	}, r.depth+1)
	if err != nil {
		return nil, err
	}
	if res.Status != "done" {
		return nil, fmt.Errorf("subflow %s ended %s: %s", workflowID, res.Status, res.Error)
	}
	return finalItem(wf, res.Outputs), nil
}

func (e *Engine) register(runID string, cancel context.CancelFunc) {
	e.mu.Lock()
	e.active[runID] = cancel
	e.mu.Unlock()
}

func (e *Engine) unregister(runID string) {
	e.mu.Lock()
	delete(e.active, runID)
	e.mu.Unlock()
}

func (e *Engine) runParallelism(wf *graph.Workflow, opts Options) int {
	limit := e.parallelism()
	if wf != nil {
		if n := intValue(wf.Settings["concurrency"]); n > 0 && n < limit {
			limit = n
		}
	}
	if opts.MaxConcurrency > 0 && opts.MaxConcurrency < limit {
		limit = opts.MaxConcurrency
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	default:
		return 0
	}
}

func (e *Engine) parallelism() int {
	if e.Parallelism < 1 {
		return 1
	}
	return e.Parallelism
}

func (e *Engine) now() time.Time {
	if e.Now == nil {
		return time.Now()
	}
	return e.Now()
}

func (e *Engine) appendEvent(runID string, ev store.Event) error {
	if ev.At.IsZero() {
		ev.At = e.now()
	}
	_, err := e.Store.AppendEvent(runID, ev)
	return err
}

func (e *Engine) saveJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return e.Store.SaveBlob(data)
}

func hashJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func verifyEvents(events []store.Event) error {
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			return fmt.Errorf("event log gap: expected seq %d, got %d", i+1, ev.Seq)
		}
	}
	return nil
}

func findNode(wf *graph.Workflow, id string) *graph.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}

func mergeItems(items []flow.Item) flow.Item {
	if len(items) == 1 {
		return items[0]
	}
	out := flow.Item{}
	for _, item := range items {
		for k, v := range item {
			out[k] = v
		}
	}
	return out
}

// triggerInput recovers the trigger item recorded for a run so a re-run starts
// from the same input instead of an empty object.
func (e *Engine) triggerInput(runID string, wf *graph.Workflow) flow.Item {
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return flow.Item{}
	}
	for _, node := range wf.Nodes {
		def, ok := nodes.Get(node.Type)
		if !ok || def.Category != "trigger" {
			continue
		}
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			if ev.NodeID != node.ID || ev.Type != "node.started" || ev.InputHash == "" {
				continue
			}
			data, err := e.Store.LoadBlob(ev.InputHash)
			if err != nil {
				continue
			}
			var item flow.Item
			if json.Unmarshal(data, &item) == nil {
				return item
			}
		}
	}
	return flow.Item{}
}

func descendants(wf *graph.Workflow, root string) map[string]bool {
	adj := map[string][]string{}
	for _, e := range wf.Edges {
		adj[e.From.Node] = append(adj[e.From.Node], e.To.Node)
	}
	seen := map[string]bool{root: true}
	stack := []string{root}
	for len(stack) > 0 {
		u := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, v := range adj[u] {
			if !seen[v] {
				seen[v] = true
				stack = append(stack, v)
			}
		}
	}
	return seen
}

func finalItem(wf *graph.Workflow, outputs map[string]flow.Output) flow.Item {
	outgoing := map[string]bool{}
	for _, e := range wf.Edges {
		outgoing[e.From.Node] = true
	}
	sinks := make([]string, 0, 1)
	for _, n := range wf.Nodes {
		if !outgoing[n.ID] {
			sinks = append(sinks, n.ID)
		}
	}
	sort.Strings(sinks)

	items := make([]flow.Item, 0, len(sinks))
	for _, id := range sinks {
		out := outputs[id]
		ports := make([]string, 0, len(out))
		for port := range out {
			ports = append(ports, port)
		}
		sort.Strings(ports)
		if len(ports) == 0 {
			continue
		}
		items = append(items, out[ports[0]])
	}
	if len(items) == 0 {
		return flow.Item{}
	}
	return mergeItems(items)
}

func firstError(report graph.Report) string {
	for _, issue := range report.Issues {
		if issue.Severity == graph.SeverityError {
			return issue.String()
		}
	}
	return "unknown validation error"
}
