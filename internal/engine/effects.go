package engine

import (
	"encoding/json"
	"fmt"
	"loom/internal/cassette"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/store"
)

const (
	nodeEffectsEvent      = "node.effects"
	nodeEffectsDriftEvent = "node.effects.drift"
)

// effectItemIndex identifies the batch item a tape belongs to. A non-batched
// execution and the first batch item both use 0; a node is never both.
func effectItemIndex(in flow.Input) int {
	if in.Context != nil && in.Context.HasIndex {
		return in.Context.Index
	}
	return 0
}

type effectScope struct {
	host     flow.Host
	recorder *cassette.Recorder
	replayer *cassette.Replayer
}

// persistedEffects reloads the execution mode stored on the run row. Every
// path that rebuilds an execState (resume, restart recovery, queue admission)
// must use this instead of DefaultEffects, or a hermetic run turns live.
func (e *Engine) persistedEffects(runID string) (cassette.Mode, string) {
	mode, source, err := e.Store.RunEffects(runID)
	if err != nil {
		// A read error or a failed integrity check must not turn a replay live.
		// Replay with no cassette source fails closed when the first node runs.
		return cassette.ModeReplay, ""
	}
	if mode == "" {
		return e.DefaultEffects, ""
	}
	return cassette.Mode(mode), source
}

func (e *Engine) effectScope(st *execState, node *graph.Node, in flow.Input, attempt int) (effectScope, error) {
	switch st.effects {
	case cassette.ModeRecord:
		// Wrap st.host, never e.Host: st.host carries the workflow
		// egress policy (engine begin/resume wrap it), and in.Host is
		// reassigned to the previous attempt's scope host before a
		// retry, so it cannot be reused here.
		rec := cassette.NewRecorder(st.host, st.runID, node.ID, attempt, effectItemIndex(in))
		return effectScope{host: rec, recorder: rec}, nil
	case cassette.ModeReplay:
		source := st.cassetteRunID
		if source == "" {
			source = st.runID
		}
		tape, err := e.loadTape(source, node.ID, attempt, effectItemIndex(in))
		if err != nil {
			return effectScope{}, err
		}
		rep := cassette.NewReplayer(tape)
		// Blob loads on replay read the same content-addressed store the
		// recording wrote to, so requests embedding blob content rebuild
		// identically and the tape verifies them by hash.
		rep.Blobs = e.Store
		return effectScope{host: rep, replayer: rep}, nil
	default:
		return effectScope{host: st.host}, nil
	}
}

// finishEffects persists a recorded tape and reports replay drift. It runs
// after every attempt, including failed and suspended ones, so the tape always
// matches what the node actually did.
func (e *Engine) finishEffects(st *execState, node *graph.Node, scope effectScope, in flow.Input, attempt int) error {
	if scope.recorder != nil {
		hash, count, err := scope.recorder.Flush(e.Store)
		if err != nil {
			return err
		}
		return e.emit(st, store.Event{
			Type: nodeEffectsEvent, NodeID: node.ID, Attempt: attempt,
			Data: map[string]any{"blob": hash, "count": count, "itemIndex": effectItemIndex(in)},
		})
	}
	if scope.replayer != nil {
		diffs := scope.replayer.Diffs()
		misses := scope.replayer.Misses()
		if len(diffs) == 0 && len(misses) == 0 {
			return nil
		}
		return e.emit(st, store.Event{
			Type: nodeEffectsDriftEvent, NodeID: node.ID, Attempt: attempt,
			Data: map[string]any{"diffs": diffs, "misses": misses, "itemIndex": effectItemIndex(in)},
		})
	}
	return nil
}

// loadTape finds the tape recorded for one node execution. Missing tapes are
// an error: a replay must not guess, and a run recorded before this feature
// existed is not replayable hermetically.
func (e *Engine) loadTape(runID, nodeID string, attempt, itemIndex int) (*cassette.Tape, error) {
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return nil, err
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		if ev.Type != nodeEffectsEvent || ev.NodeID != nodeID {
			continue
		}
		if ev.Attempt != attempt || eventInt(ev.Data, "itemIndex") != itemIndex {
			continue
		}
		hash, _ := ev.Data["blob"].(string)
		if hash == "" {
			return &cassette.Tape{RunID: runID, NodeID: nodeID, Attempt: attempt, ItemIndex: itemIndex}, nil
		}
		raw, err := e.Store.LoadBlob(hash)
		if err != nil {
			return nil, err
		}
		var tape cassette.Tape
		if err := json.Unmarshal(raw, &tape); err != nil {
			return nil, fmt.Errorf("cassette: decode tape for node %q: %w", nodeID, err)
		}
		return &tape, nil
	}
	return nil, fmt.Errorf("cassette: node %q attempt %d item %d has no recorded effects in run %s",
		nodeID, attempt, itemIndex, runID)
}

func eventInt(data map[string]any, key string) int {
	switch value := data[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		n, _ := value.Int64()
		return int(n)
	default:
		return 0
	}
}

// ExecRecord is one recorded subprocess execution from a finished run's
// effect tape (Phase 5): the evidence behind recorded tests.
type ExecRecord struct {
	Attempt   int    `json:"attempt"`
	ItemIndex int    `json:"itemIndex"`
	Source    string `json:"source"`
	EnvKey    string `json:"envKey"`
	ExitCode  int    `json:"exitCode"`
	Stdout    string `json:"stdout"`
	Stderr    string `json:"stderr"`
	Result    any    `json:"result,omitempty"`
}

// RecordedExecs returns every recorded subprocess execution of one node
// across a run's effect tapes, oldest first. It reads the tapes only —
// nothing executes, so it works on any server including simulated ones.
func (e *Engine) RecordedExecs(runID, nodeID string) ([]ExecRecord, error) {
	events, err := e.Store.ListEvents(runID)
	if err != nil {
		return nil, err
	}
	var records []ExecRecord
	for _, ev := range events {
		if ev.Type != nodeEffectsEvent || ev.NodeID != nodeID {
			continue
		}
		var tape *cassette.Tape
		if hash, _ := ev.Data["blob"].(string); hash != "" {
			raw, err := e.Store.LoadBlob(hash)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, &tape); err != nil {
				return nil, fmt.Errorf("cassette: decode tape for node %q: %w", nodeID, err)
			}
		} else {
			tape = &cassette.Tape{}
		}
		for _, entry := range tape.Entries {
			if entry.Kind != cassette.KindExec {
				continue
			}
			record := ExecRecord{Attempt: ev.Attempt, ItemIndex: eventInt(ev.Data, "itemIndex")}
			var request struct {
				Source string `json:"source"`
				Env    string `json:"env"`
			}
			_ = json.Unmarshal(entry.Request, &request)
			record.Source, record.EnvKey = request.Source, request.Env
			var response flow.ExecResult
			_ = json.Unmarshal(entry.Response, &response)
			record.ExitCode = response.ExitCode
			record.Stdout = response.Stdout
			record.Stderr = response.Stderr
			record.Result = response.Result
			records = append(records, record)
		}
	}
	return records, nil
}
