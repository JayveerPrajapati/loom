// Package engine executes typed workflow DAGs and records every step.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"loom/internal/cassette"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/store"
)

type Engine struct {
	Store              *store.Store
	Host               flow.Host
	Registry           map[string]flow.Node
	ResolveWorkflow    func(string) (*graph.Workflow, error)
	CredentialResolver func(string) (flow.Credential, bool)
	Parallelism        int
	MaxAttempts        int
	Now                func() time.Time
	// DefaultEffects applies when a caller does not set Options.Effects. Serve
	// sets this to record for live hosts so history is replayable.
	DefaultEffects cassette.Mode
	// DurableWaits makes flow.wait suspend the run and persist a wakeup time
	// instead of sleeping in a goroutine.
	DurableWaits bool
	// OnRunFinished is called in a goroutine after a top-level run reaches a
	// terminal state. It must not block; serve uses it for shadow verification.
	OnRunFinished func(ctx context.Context, runID string)
	// OnApprovalWaiting is called in a goroutine when a run parks on an
	// approval, so notifications never block execution.
	OnApprovalWaiting func(ctx context.Context, runID, nodeID string, data map[string]any)
	// OnRunParked is called synchronously when a run parks with a durable
	// wakeup deadline, so an exact-deadline scheduler can arm its timer. It
	// must not block; it receives the persisted wake time.
	OnRunParked func(wakeAt time.Time)

	mu          sync.Mutex
	active      map[string]context.CancelFunc
	rateWindows map[string][]time.Time
	stopped     bool
	// onRateRecord is a test hook called with the timestamp a rate slot was
	// actually reserved. It is nil in production.
	onRateRecord func(key string, at time.Time)
}

type Options struct {
	Trigger        string
	Input          flow.Item
	AutoApprove    bool
	MaxAttempts    int
	MaxConcurrency int
	Timeout        time.Duration
	ParentRunID    string
	ParentNodeID   string
	// RootRunID is the first run in an event chain. It defaults to the run's
	// own id; descendants inherit it so the server can budget the whole tree.
	RootRunID       string
	Snapshot        *graph.Workflow
	JobID           string
	TemplateVersion int
	Actor           string

	// Effects selects external-effect handling for this run. ModeRecord
	// persists every host effect as a per-node tape; ModeReplay serves those
	// tapes and fails closed instead of touching the network.
	Effects cassette.Mode

	// CassetteRunID is the run whose tapes a replay consumes. It defaults to
	// the run being executed (step replay of the same run).
	CassetteRunID string

	// ItemOverrides replaces the merged upstream input for specific nodes.
	// Used by replay to re-execute a node with an edited input; nodes not in
	// the map keep normal edge-merge behavior.
	ItemOverrides map[string]flow.Item

	// EventHops records how many event-bus hops started this run. flow.emit
	// reads it (via RunContext.Hops) to bound event chains.
	EventHops int
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
		rateWindows:     map[string][]time.Time{},
	}
}

type execState struct {
	runID string
	wf    *graph.Workflow
	// host is the run's host: the engine host, wrapped with the
	// workflow's egress policy when its doc declares one.
	host         flow.Host
	outputs      map[string]flow.Output
	completed    map[string]bool
	skipped      map[string]bool
	decision     map[string]string
	decisionInfo map[string]flow.DecisionInfo
	vars         map[string]any
	depth        int
	actor        string
	rootRunID    string
	parked       bool
	// effects and cassetteRunID are copied from Options so every execution
	// path (manual, webhook, scheduler, batch, resume) honours them.
	effects       cassette.Mode
	cassetteRunID string
	// wakeAt is the persisted deadline for a durable wait being resumed.
	wakeAt time.Time
}

// recoverBackground keeps a panic in a worker goroutine (for example a
// crypto/rand failure in NewID/NewToken) from crashing the whole server.
func (e *Engine) recoverBackground(runID string) {
	if r := recover(); r != nil {
		log.Printf("engine: run %s goroutine panicked: %v", runID, r)
	}
}

func (e *Engine) Start(ctx context.Context, wf *graph.Workflow, opts Options) (*Result, error) {
	return e.start(ctx, wf, opts, 0)
}

// StartAsync creates and registers a run, then executes it in the background.
// The returned run id can be streamed from the event log while it works.
func (e *Engine) StartAsync(ctx context.Context, wf *graph.Workflow, opts Options) (string, error) {
	return e.startAsync(ctx, wf, opts, 0)
}

// startAsync is StartAsync with an explicit nesting depth, so an asynchronous
// subflow still counts against maxSubflowDepth.
func (e *Engine) startAsync(ctx context.Context, wf *graph.Workflow, opts Options, depth int) (string, error) {
	st, runCtx, cancel, err := e.begin(ctx, wf, opts, depth)
	if err != nil {
		return "", err
	}
	if st.parked {
		return st.runID, nil
	}
	go func() {
		defer cancel()
		defer e.unregister(st.runID)
		defer e.recoverBackground(st.runID)
		_, _ = e.execute(runCtx, st, opts)
	}()
	return st.runID, nil
}

func (e *Engine) start(ctx context.Context, wf *graph.Workflow, opts Options, depth int) (*Result, error) {
	st, runCtx, cancel, err := e.begin(ctx, wf, opts, depth)
	if err != nil {
		return nil, err
	}
	if st.parked {
		return &Result{RunID: st.runID, Status: "queued"}, nil
	}
	defer cancel()
	defer e.unregister(st.runID)
	return e.execute(runCtx, st, opts)
}

// connectorDefs resolves connector-authored node definitions from the
// store (Campaign 4): current versions, cached per call chain.
func (e *Engine) connectorDefs(nodeType string) (nodes.Definition, bool) {
	if !strings.HasPrefix(nodeType, nodes.ConnectorTypePrefix) {
		return nodes.Definition{}, false
	}
	rest := strings.TrimPrefix(nodeType, nodes.ConnectorTypePrefix)
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 {
		return nodes.Definition{}, false
	}
	c, err := e.Store.GetConnector(parts[0])
	if err != nil {
		return nodes.Definition{}, false
	}
	for _, def := range store.ConnectorNodeTypes(c) {
		if def.Type == nodeType {
			return def, true
		}
	}
	return nodes.Definition{}, false
}

func (e *Engine) begin(ctx context.Context, wf *graph.Workflow, opts Options, depth int) (*execState, context.Context, context.CancelFunc, error) {
	if opts.Effects == "" {
		opts.Effects = e.DefaultEffects
	}
	runWF := wf
	if opts.Snapshot != nil {
		runWF = opts.Snapshot
	}
	// Connector node types (Campaign 4): a published doc carries the
	// inlined request template; a never-published draft does not, so it
	// resolves against the connector's current version here (subflow
	// precedent: the engine may read the store at run start). Validation
	// then accepts the connector types through the same resolver.
	var extra graph.ExtraDefs
	hasConnectors := false
	for i := range runWF.Nodes {
		if strings.HasPrefix(runWF.Nodes[i].Type, nodes.ConnectorTypePrefix) {
			hasConnectors = true
			break
		}
	}
	if hasConnectors && e.Store != nil {
		if _, err := e.Store.ResolveConnectorRefs(runWF); err != nil {
			return nil, nil, nil, err
		}
		extra = e.connectorDefs
	}
	// Size caps are an ingest guard; a workflow already stored must still run.
	var report graph.Report
	if extra != nil {
		report = graph.ValidateStoredWithExtra(runWF, extra)
	} else {
		report = graph.ValidateStored(runWF)
	}
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
	rootRunID := opts.RootRunID
	if rootRunID == "" {
		rootRunID = runID
	}
	started := e.now()
	actor := actorOrDefault(opts.Actor)
	var inputJSON []byte
	if len(opts.Input) > 0 {
		inputJSON, _ = json.Marshal(opts.Input)
	}
	// Initial status: a run that will be admitted immediately is created
	// as running in the same INSERT that publishes it. Creating it queued
	// and flipping it in a second statement left a window where the queue
	// reconciler (dequeueNext, reading on a concurrent connection) could
	// admit and execute the run a second time. Only genuinely parked runs
	// — capped workflows that must wait for a slot — are created queued.
	status := "running"
	capped := depth == 0 &&
		!isNonRootParented(trigger, opts.ParentRunID) &&
		intValue(runWF.Settings["maxParallelRuns"]) > 0
	if capped {
		status = "queued"
	}
	run := store.Run{
		ID: runID, WorkflowID: runWF.ID, WorkflowVersion: version,
		TemplateVersion: opts.TemplateVersion, JobID: opts.JobID,
		Status: status, Trigger: trigger, StartedAt: started,
		ParentRunID: opts.ParentRunID, ParentNodeID: opts.ParentNodeID,
		RootRunID: rootRunID,
		Snapshot:  snapshot, Actor: actor, Input: inputJSON,
		Effects: string(opts.Effects), CassetteRunID: opts.CassetteRunID,
	}
	if err := e.Store.CreateRun(run); err != nil {
		return nil, nil, nil, err
	}
	startedData := map[string]any{"trigger": trigger}
	if len(opts.Input) > 0 {
		startedData["input"] = opts.Input
	}
	if err := e.appendEvent(runID, store.Event{Type: "run.started", Actor: actor, Data: startedData}); err != nil {
		return nil, nil, nil, err
	}

	// Concurrency admission (top-level runs only): atomically flip the
	// queued row to running unless the workflow's parallel-run cap is met,
	// in which case the run parks until a slot frees (dequeueNext). Every
	// other path was created running by the INSERT itself.
	if capped {
		ok, err := e.Store.AdmitRun(runID, runWF.ID, intValue(runWF.Settings["maxParallelRuns"]))
		if err != nil {
			return nil, nil, nil, err
		}
		if !ok {
			parkCtx, parkCancel := context.WithCancel(ctx)
			e.register(runID, parkCancel)
			parked := &execState{
				runID: runID, wf: runWF,
				host:    wrapWorkflowEgress(e.Host, runWF.Egress),
				outputs: map[string]flow.Output{}, completed: map[string]bool{},
				skipped: map[string]bool{}, decision: map[string]string{},
				decisionInfo: map[string]flow.DecisionInfo{},
				vars:         varsFor(runWF), depth: depth, actor: actor,
				rootRunID: rootRunID, parked: true,
				effects: opts.Effects, cassetteRunID: opts.CassetteRunID,
			}
			return parked, parkCtx, parkCancel, nil
		}
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
		decisionInfo: map[string]flow.DecisionInfo{},
		vars:         varsFor(runWF), depth: depth, actor: actor,
		rootRunID: rootRunID,
		effects:   opts.Effects, cassetteRunID: opts.CassetteRunID,
	}
	// The workflow's own egress allowlist (doc-level `egress` block)
	// wraps the engine host for this run — subflow runs get their own
	// wrap when their subworkflow declares its own policy.
	st.host = wrapWorkflowEgress(e.Host, runWF.Egress)
	return st, runCtx, cancel, nil
}

// isNonRootParented reports whether a run inherits its parent's concurrency
// slot instead of taking a root slot of its own. Replay children are excluded
// on purpose: a replay that cannot get a slot must fail closed with
// ErrReplayNoSlot rather than run alongside the cap holder.
func isNonRootParented(trigger, parentRunID string) bool {
	if strings.TrimSpace(parentRunID) == "" {
		return false
	}
	switch trigger {
	case "batch", "subflow", "ci":
		return true
	default:
		return false
	}
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
