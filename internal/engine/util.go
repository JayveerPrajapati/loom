// Package engine executes typed workflow DAGs and records every step.
package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/nodes"
	"loom/internal/store"
)

func (e *Engine) register(runID string, cancel context.CancelFunc) {
	e.mu.Lock()
	e.active[runID] = cancel
	e.mu.Unlock()
}

// releaseRegistered cancels and removes any cancel handle previously
// registered for runID. It is used when a parked run is claimed: the parked
// handle would otherwise be dropped when executeClaimed registers its own,
// leaking the handle. context cancel funcs are idempotent, so cancelling a
// handle that is still live is safe.
func (e *Engine) releaseRegistered(runID string) {
	e.mu.Lock()
	cancel := e.active[runID]
	delete(e.active, runID)
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
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

// emit stamps the run's actor onto an event before appending it, so the actor
// is covered by the event chain.
func (e *Engine) emit(st *execState, ev store.Event) error {
	ev.Actor = st.actor
	return e.appendEvent(st.runID, ev)
}

func actorOrDefault(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "system:engine"
	}
	return actor
}

func (e *Engine) saveJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return e.Store.SaveBlob(data)
}

// rootRunIDFor returns the event-tree root recorded on a run, defaulting to
// the run itself for rows written before root_run_id existed.
func rootRunIDFor(run *store.Run) string {
	if run.RootRunID != "" {
		return run.RootRunID
	}
	return run.ID
}

func findNode(wf *graph.Workflow, id string) *graph.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
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

func firstError(report graph.Report) string {
	for _, issue := range report.Issues {
		if issue.Severity == graph.SeverityError {
			return issue.String()
		}
	}
	return "unknown validation error"
}
