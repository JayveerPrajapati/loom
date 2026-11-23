package engine

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"loom/internal/cassette"
	"loom/internal/graph"
)

// CINode is one node's verdict in a workflow regression replay.
type CINode struct {
	NodeID       string          `json:"nodeId"`
	Name         string          `json:"name,omitempty"`
	Status       string          `json:"status"` // match | drift | added | removed | blocked
	RecordedHash string          `json:"recordedHash,omitempty"`
	ActualHash   string          `json:"actualHash,omitempty"`
	Diffs        []cassette.Diff `json:"diffs,omitempty"`
	Misses       []cassette.Miss `json:"misses,omitempty"`
}

// CIVerdict is the result of replaying a candidate workflow version against a
// historical run. Passed is true only when every recorded node reproduced
// without a cassette miss or a request drift.
type CIVerdict struct {
	SourceRunID string   `json:"sourceRunId"`
	RunID       string   `json:"runId"`
	WorkflowID  string   `json:"workflowId"`
	Status      string   `json:"status"`
	Passed      bool     `json:"passed"`
	Error       string   `json:"error,omitempty"`
	Nodes       []CINode `json:"nodes"`
}

// CandidateWorkflow loads the workflow a verification should replay. Version 0
// reuses the snapshot recorded on the source run; a positive version loads that
// published version instead.
func (e *Engine) CandidateWorkflow(sourceRunID string, version int) (*graph.Workflow, error) {
	run, err := e.Store.GetRun(sourceRunID)
	if err != nil {
		return nil, err
	}
	if version <= 0 {
		return e.workflowForRun(run)
	}
	return e.Store.GetWorkflowVersion(run.WorkflowID, version)
}

// VerifyAgainstRun replays the candidate workflow with the source run's
// recorded trigger input and effects. It never touches the network: a node
// whose effect was not recorded fails closed and the verdict reports it.
func (e *Engine) VerifyAgainstRun(ctx context.Context, sourceRunID string, candidate *graph.Workflow) (*CIVerdict, error) {
	source, err := e.Store.GetRun(sourceRunID)
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		candidate, err = e.workflowForRun(source)
		if err != nil {
			return nil, err
		}
	}
	if candidate.ID == "" {
		candidate.ID = source.WorkflowID
	}

	rootRunID := source.RootRunID
	if rootRunID == "" {
		rootRunID = sourceRunID
	}
	res, err := e.Start(ctx, candidate, Options{
		Trigger: "ci", Input: e.triggerInput(sourceRunID, candidate), Snapshot: candidate,
		ParentRunID: sourceRunID, RootRunID: rootRunID,
		AutoApprove: true,
		Effects:     cassette.ModeReplay, CassetteRunID: sourceRunID, Actor: "system:ci",
	})
	if err != nil {
		return nil, err
	}

	recorded, _, err := e.loadOutputs(sourceRunID)
	if err != nil {
		return nil, err
	}

	verdict := &CIVerdict{
		SourceRunID: sourceRunID, RunID: res.RunID, WorkflowID: candidate.ID,
		Status: res.Status, Error: res.Error,
	}

	// Collect replay drift reported by the child run.
	drift := map[string]CINode{}
	events, err := e.Store.ListEvents(res.RunID)
	if err != nil {
		return nil, err
	}
	for _, ev := range events {
		if ev.Type != nodeEffectsDriftEvent {
			continue
		}
		node := drift[ev.NodeID]
		node.NodeID = ev.NodeID
		node.Diffs = append(node.Diffs, decodeDiffs(ev.Data["diffs"])...)
		node.Misses = append(node.Misses, decodeMisses(ev.Data["misses"])...)
		drift[ev.NodeID] = node
	}
	failed := map[string]bool{}
	for _, ev := range events {
		if ev.Type == "node.failed" {
			failed[ev.NodeID] = true
		}
	}

	nodes := map[string]CINode{}
	for _, node := range candidate.Nodes {
		nodes[node.ID] = CINode{NodeID: node.ID, Name: node.Name}
	}

	ids := make([]string, 0, len(nodes)+len(recorded))
	for id := range nodes {
		ids = append(ids, id)
	}
	for id := range recorded {
		if _, ok := nodes[id]; !ok {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)

	passed := res.Status == "done" && res.Error == ""
	for _, id := range ids {
		entry := nodes[id]
		recordedOut, hadRecorded := recorded[id]
		actualOut, hadActual := res.Outputs[id]

		if _, exists := nodes[id]; !exists {
			entry.Status = "removed"
			entry.RecordedHash, _ = hashJSON(recordedOut)
			passed = false
			verdict.Nodes = append(verdict.Nodes, entry)
			continue
		}
		if !hadRecorded {
			entry.Status = "added"
			entry.ActualHash, _ = hashJSON(actualOut)
			// A newly added node is not a regression by itself, but it has no
			// recorded effect to replay, so the run cannot be called verified.
			if hadActual {
				passed = false
			}
			verdict.Nodes = append(verdict.Nodes, entry)
			continue
		}
		entry.RecordedHash, _ = hashJSON(recordedOut)
		if d, ok := drift[id]; ok {
			entry.Diffs = d.Diffs
			entry.Misses = d.Misses
		}
		if failed[id] {
			entry.Status = "blocked"
			entry.ActualHash, _ = hashJSON(actualOut)
			passed = false
			verdict.Nodes = append(verdict.Nodes, entry)
			continue
		}
		if !hadActual {
			entry.Status = "blocked"
			passed = false
			verdict.Nodes = append(verdict.Nodes, entry)
			continue
		}
		entry.ActualHash, _ = hashJSON(actualOut)
		if entry.ActualHash == entry.RecordedHash && len(entry.Diffs) == 0 && len(entry.Misses) == 0 {
			entry.Status = "match"
		} else {
			entry.Status = "drift"
			passed = false
		}
		verdict.Nodes = append(verdict.Nodes, entry)
	}
	verdict.Passed = passed
	return verdict, nil
}

func decodeDiffs(raw any) []cassette.Diff {
	out := make([]cassette.Diff, 0)
	for _, item := range asAnySlice(raw) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, cassette.Diff{
			Kind:         cassette.Kind(stringValue(entry["kind"])),
			Index:        intValue(entry["index"]),
			RecordedHash: stringValue(entry["recordedHash"]),
			ActualHash:   stringValue(entry["actualHash"]),
		})
	}
	return out
}

func decodeMisses(raw any) []cassette.Miss {
	out := make([]cassette.Miss, 0)
	for _, item := range asAnySlice(raw) {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, cassette.Miss{
			Kind:  cassette.Kind(stringValue(entry["kind"])),
			Index: intValue(entry["index"]),
		})
	}
	return out
}

func asAnySlice(raw any) []any {
	switch value := raw.(type) {
	case []any:
		return value
	case nil:
		return nil
	default:
		return []any{value}
	}
}

func stringValue(raw any) string {
	value, _ := raw.(string)
	return value
}

func (v *CIVerdict) Summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "verified %s against %s: %s", v.RunID, v.SourceRunID, v.Status)
	if v.Passed {
		b.WriteString(" (pass)")
	} else {
		b.WriteString(" (fail)")
	}
	return b.String()
}
