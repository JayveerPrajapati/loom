package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"loom/internal/bus"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
)

// eventRunBudget caps how many runs one event tree may create in total. Hop
// depth alone bounds a chain at N^hops; this budget bounds the whole fan-out.
const eventRunBudget = 100

// eventBudgetAllows reports whether a tree that already has count runs
// (including the root) may start another descendant.
func eventBudgetAllows(count int) bool {
	return count < eventRunBudget
}

// eventBudgetMu makes the count-and-start sequence atomic across concurrent
// emitters so the budget cannot be overshot by a race.
var eventBudgetMu sync.Mutex

var (
	eventBudgetLogMu sync.Mutex
	eventBudgetLog   = map[string]time.Time{}
)

// logBudgetOnce keeps a saturated event tree from flooding the log: at most
// one line per root per minute, and the map is reset if it ever grows past a
// bound (a different tree per minute for days).
func logBudgetOnce(root string) {
	now := time.Now()
	eventBudgetLogMu.Lock()
	if last, ok := eventBudgetLog[root]; ok && now.Sub(last) < time.Minute {
		eventBudgetLogMu.Unlock()
		return
	}
	if len(eventBudgetLog) > 4096 {
		eventBudgetLog = map[string]time.Time{}
	}
	eventBudgetLog[root] = now
	eventBudgetLogMu.Unlock()
	log.Printf("engine: event budget exhausted for run %s", root)
}

// publishHost adds the event-bus publishing capability to any host.
type publishHost struct {
	flow.Host
	B *bus.Bus
}

func (h *publishHost) Publish(ev flow.Event) { h.B.Publish(ev) }

// publishExecHost is the publishHost variant used when the wrapped host
// implements the subprocess capability. Go interface embedding only
// promotes the flow.Host method set, so a wrapper that unconditionally
// implemented Exec would (a) leak the capability to hosts without it —
// turning the actionable "start with --enable-subprocess-nodes" error
// into a generic one on simulated servers — and (b) drop it for live
// hosts, breaking every server-side code.python run. The wrapper is
// therefore chosen by the base host's actual capabilities: it forwards
// exactly what the base implements, nothing more.
type publishExecHost struct {
	publishHost
}

func (h *publishExecHost) Exec(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	executor, ok := h.Host.(flow.Executor)
	if !ok {
		return flow.ExecResult{}, fmt.Errorf("this host cannot execute subprocesses (live runs need --enable-subprocess-nodes; replays verify from the recorded tape)")
	}
	return executor.Exec(ctx, req)
}

func (h *publishExecHost) SaveBlob(data []byte) (string, int, error) {
	saver, ok := h.Host.(flow.BlobSaver)
	if !ok {
		return "", 0, fmt.Errorf("host cannot save blobs")
	}
	return saver.SaveBlob(data)
}

func (h *publishExecHost) LoadBlob(hash string) ([]byte, error) {
	loader, ok := h.Host.(flow.BlobLoader)
	if !ok {
		return nil, fmt.Errorf("host cannot load blobs")
	}
	return loader.LoadBlob(hash)
}

// wireEventBus connects the engine's host to the event bus and subscribes
// the API's event handler. Called once from Handler.
func wireEventBus(deps Deps, s *api) {
	b := &bus.Bus{}
	if deps.Engine == nil || deps.Engine.Host == nil {
		return
	}
	base := deps.Engine.Host
	var wrapped flow.Host = &publishHost{Host: base, B: b}
	if _, ok := base.(flow.Executor); ok {
		wrapped = &publishExecHost{publishHost{Host: base, B: b}}
	}
	deps.Engine.Host = wrapped
	b.Subscribe(s.onEvent)
}

// rebuildEventTriggers rescans the stored workflows and swaps the
// event-name index. It runs at startup and after every workflow mutation
// (create, save, publish, delete — including the version a publish freezes),
// so a full rebuild cannot drift the way an incremental update can.
func (s *api) rebuildEventTriggers() {
	list, err := s.deps.Store.ListWorkflows()
	if err != nil {
		// Keep the previous index: a transient read failure must not
		// silently stop every event-triggered workflow.
		log.Printf("event trigger index: rebuild failed: %v", err)
		return
	}
	next := map[string]map[string]bool{}
	for _, summary := range list {
		wf, err := s.deps.Store.GetWorkflow(summary.ID)
		if err != nil {
			continue
		}
		for _, node := range wf.Nodes {
			if node.Type != "trigger.event" {
				continue
			}
			name, _ := node.Config["event"].(string)
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if next[name] == nil {
				next[name] = map[string]bool{}
			}
			next[name][wf.ID] = true
		}
	}
	s.eventTriggersMu.Lock()
	s.eventTriggers = next
	s.eventTriggersMu.Unlock()
}

// workflowsForEvent returns the IDs indexed for one event name, in no
// particular order. Callers re-read each workflow before starting a run, so
// a beat-stale entry just fails the load or the trigger re-check.
func (s *api) workflowsForEvent(name string) []string {
	s.eventTriggersMu.RLock()
	defer s.eventTriggersMu.RUnlock()
	ids := make([]string, 0, len(s.eventTriggers[name]))
	for id := range s.eventTriggers[name] {
		ids = append(ids, id)
	}
	return ids
}

// listensFor reports whether the workflow still carries a trigger.event
// node matching the name. onEvent re-verifies the loaded document so an
// index entry that outlived its workflow (or was edited between the index
// rebuild and now) cannot start a stale run.
func listensFor(wf *graph.Workflow, name string) bool {
	for _, node := range wf.Nodes {
		if node.Type != "trigger.event" {
			continue
		}
		configured, _ := node.Config["event"].(string)
		if strings.TrimSpace(configured) == name {
			return true
		}
	}
	return false
}

// onEvent starts a run of every workflow whose trigger.event node matches
// the published event. Runs get the event payload as their input, the
// emitting run as their parent, and the event's hop count so cycles
// terminate (flow.emit fails past MaxEventHops). Matching workflows come
// from the in-memory index; the budget check-and-start stays atomic under
// the event mutex, which is no longer held across workflow loads.
func (s *api) onEvent(ev flow.Event) {
	root := ev.RootRunID
	if root == "" {
		root = ev.SourceRunID
	}
	for _, id := range s.workflowsForEvent(ev.Name) {
		wf, err := s.deps.Store.GetWorkflow(id)
		if err != nil {
			continue
		}
		if !listensFor(wf, ev.Name) {
			continue
		}
		// Event runs start from the stored row too; resolve sparse drafts
		// before the run snapshot is taken.
		runWF, err := workflowForRun(s.deps.Store, wf)
		if err != nil {
			log.Printf("engine: cannot prepare workflow %s for event %s: %v", wf.ID, ev.Name, err)
			continue
		}
		if root != "" {
			eventBudgetMu.Lock()
			count, err := s.deps.Store.CountRunsByRoot(root)
			if err != nil {
				eventBudgetMu.Unlock()
				log.Printf("engine: cannot count event descendants for run %s: %v", root, err)
				return
			}
			if !eventBudgetAllows(count) {
				eventBudgetMu.Unlock()
				logBudgetOnce(root)
				return
			}
			_, _ = s.deps.Engine.StartAsync(context.WithoutCancel(context.Background()), runWF, engine.Options{
				Trigger: "event", Input: flow.CloneItem(ev.Item),
				Snapshot: runWF, ParentRunID: ev.SourceRunID, EventHops: ev.Hops,
				RootRunID:   root,
				MaxAttempts: s.deps.Engine.MaxAttempts, Actor: "system",
			})
			eventBudgetMu.Unlock()
			continue
		}
		_, _ = s.deps.Engine.StartAsync(context.WithoutCancel(context.Background()), runWF, engine.Options{
			Trigger: "event", Input: flow.CloneItem(ev.Item),
			Snapshot: runWF, ParentRunID: ev.SourceRunID, EventHops: ev.Hops,
			RootRunID:   root,
			MaxAttempts: s.deps.Engine.MaxAttempts, Actor: "system",
		})
	}
}
