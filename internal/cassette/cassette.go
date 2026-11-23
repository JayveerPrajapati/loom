// Package cassette records the external effects a node performs so a later
// replay can reproduce the run without touching the network.
//
// A tape is positional: every call a node makes through flow.Host is appended
// in order. Replay serves the same ordinals and fails closed when an effect was
// never recorded, so a node can never silently hit the outside world during a
// hermetic replay.
package cassette

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"loom/internal/flow"
)

// Mode selects how the engine treats external effects for a run.
type Mode string

const (
	ModeOff    Mode = ""
	ModeRecord Mode = "record"
	ModeReplay Mode = "replay"
)

// Kind identifies one host capability.
type Kind string

const (
	KindHTTP  Kind = "http"
	KindQuery Kind = "query"
	KindSlack Kind = "slack"
	KindBlob  Kind = "blob"
	KindExec  Kind = "exec"
)

// ErrMiss reports an effect a replay could not find on its tape. It is wrapped
// so callers can match with errors.Is.
var ErrMiss = errors.New("cassette miss")

// Entry is one recorded external effect.
type Entry struct {
	Kind        Kind            `json:"kind"`
	Index       int             `json:"index"`
	RequestHash string          `json:"requestHash"`
	Request     json.RawMessage `json:"request"`
	Response    json.RawMessage `json:"response,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// Tape is the effects recorded for one node execution (one attempt and, for a
// batched node, one item index).
type Tape struct {
	RunID     string  `json:"runId"`
	NodeID    string  `json:"nodeId"`
	Attempt   int     `json:"attempt"`
	ItemIndex int     `json:"itemIndex"`
	Entries   []Entry `json:"entries"`
}

// Blobs is the subset of the store a recorder needs.
type Blobs interface {
	SaveBlob(data []byte) (string, error)
	LoadBlob(hash string) ([]byte, error)
}

// Diff records a replay whose request differs from the recording. The replay is
// allowed to continue so downstream drift is visible, but the verdict is not a
// clean pass.
type Diff struct {
	Kind         Kind   `json:"kind"`
	Index        int    `json:"index"`
	RecordedHash string `json:"recordedHash"`
	ActualHash   string `json:"actualHash"`
}

// Miss records an effect that had no recorded counterpart.
type Miss struct {
	Kind  Kind `json:"kind"`
	Index int  `json:"index"`
}

// ---------------------------------------------------------------------------
// Recorder
// ---------------------------------------------------------------------------

// Recorder wraps a live host and records every external effect.
type Recorder struct {
	Base   flow.Host
	Tape   *Tape
	mu     sync.Mutex
	index  int
	failed error
}

func NewRecorder(base flow.Host, runID, nodeID string, attempt, itemIndex int) *Recorder {
	return &Recorder{
		Base: base,
		Tape: &Tape{RunID: runID, NodeID: nodeID, Attempt: attempt, ItemIndex: itemIndex},
	}
}

func (r *Recorder) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	resp, err := r.Base.HTTP(ctx, req)
	r.record(KindHTTP, requestPayload(req), resp, err)
	return resp, err
}

// Exec records subprocess executions. The request identity is the source,
// the incoming item and the resolved environment; the response carries the
// exit status, captured output and parsed result, so hermetic replay serves
// the recorded outcome without re-running anything.
func (r *Recorder) Exec(ctx context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	executor, ok := r.Base.(flow.Executor)
	if !ok {
		return flow.ExecResult{}, fmt.Errorf("this host cannot execute subprocesses (live runs need --enable-subprocess-nodes; replays verify from the recorded tape)")
	}
	res, err := executor.Exec(ctx, req)
	r.record(KindExec, execPayload(req), res, err)
	return res, err
}

func execPayload(req flow.ExecRequest) map[string]any {
	return map[string]any{
		"source": req.Source,
		"stdin":  req.Stdin,
		"env":    req.EnvKey,
	}
}

func (r *Recorder) Query(ctx context.Context, credID, sql string) ([]flow.Item, error) {
	rows, err := r.Base.Query(ctx, credID, sql)
	r.record(KindQuery, map[string]any{"credential": credID, "sql": sql}, rows, err)
	return rows, err
}

func (r *Recorder) Slack(ctx context.Context, credID, channel, text string) error {
	err := r.Base.Slack(ctx, credID, channel, text)
	r.record(KindSlack, map[string]any{"credential": credID, "channel": channel, "text": text}, nil, err)
	return err
}

// Sleep and Now are not external effects: a wait must still wait (durable
// timers move the wait out of the run; a cassette does not).
func (r *Recorder) Sleep(ctx context.Context, d time.Duration) error {
	return r.Base.Sleep(ctx, d)
}

func (r *Recorder) Now() time.Time { return r.Base.Now() }

// Publish forwards the optional event capability. Wrapping a host must not
// silently disable flow.emit: the wrapper is a recording layer, not a filter.
// A Replayer deliberately does not implement this interface, so a hermetic
// replay never starts subscriber workflows.
func (r *Recorder) Publish(ev flow.Event) {
	if publisher, ok := r.Base.(flow.EventPublisher); ok {
		publisher.Publish(ev)
	}
}

// SaveBlob forwards the optional blob capability and records it as an
// effect. The payload carries no request data to match against: the hash is
// content-addressed, so the taped response is the verification. Loads are
// not recorded — the effect is the request built from the bytes.
func (r *Recorder) SaveBlob(data []byte) (string, int, error) {
	saver, ok := r.Base.(flow.BlobSaver)
	if !ok {
		return "", 0, fmt.Errorf("cassette: host cannot save blobs")
	}
	hash, size, err := saver.SaveBlob(data)
	r.record(KindBlob, map[string]any{}, map[string]any{"hash": hash, "size": size}, err)
	return hash, size, err
}

// LoadBlob forwards the optional blob read capability.
func (r *Recorder) LoadBlob(hash string) ([]byte, error) {
	loader, ok := r.Base.(flow.BlobLoader)
	if !ok {
		return nil, fmt.Errorf("cassette: host cannot load blobs")
	}
	return loader.LoadBlob(hash)
}

func (r *Recorder) record(kind Kind, payload any, response any, callErr error) {
	req, err := json.Marshal(payload)
	if err != nil {
		r.setFailed(err)
		return
	}
	entry := Entry{
		Kind:        kind,
		Index:       r.index,
		RequestHash: hashJSON(req),
		Request:     req,
	}
	r.index++
	if callErr != nil {
		entry.Error = callErr.Error()
	} else if response != nil {
		data, err := json.Marshal(response)
		if err != nil {
			r.setFailed(err)
			return
		}
		entry.Response = data
	}
	r.mu.Lock()
	r.Tape.Entries = append(r.Tape.Entries, entry)
	r.mu.Unlock()
}

func (r *Recorder) setFailed(err error) {
	r.mu.Lock()
	if r.failed == nil {
		r.failed = err
	}
	r.mu.Unlock()
}

// Flush persists the tape and returns its blob hash and effect count.
func (r *Recorder) Flush(blobs Blobs) (string, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed != nil {
		return "", len(r.Tape.Entries), r.failed
	}
	data, err := json.Marshal(r.Tape)
	if err != nil {
		return "", len(r.Tape.Entries), err
	}
	hash, err := blobs.SaveBlob(data)
	if err != nil {
		return "", len(r.Tape.Entries), err
	}
	return hash, len(r.Tape.Entries), nil
}

// ---------------------------------------------------------------------------
// Replayer
// ---------------------------------------------------------------------------

// Replayer serves recorded effects and never touches the outside world.
type Replayer struct {
	Tape *Tape
	// Blobs, when set, serves LoadBlob reads from the content-addressed
	// store so nodes can rebuild requests that embed blob content. SaveBlob
	// never reads it: saving on replay returns the taped hash.
	Blobs Blobs

	mu      sync.Mutex
	index   int
	diffs   []Diff
	misses  []Miss
	byIndex map[int]Entry
}

func NewReplayer(tape *Tape) *Replayer {
	if tape == nil {
		tape = &Tape{}
	}
	index := make(map[int]Entry, len(tape.Entries))
	for _, entry := range tape.Entries {
		index[entry.Index] = entry
	}
	return &Replayer{Tape: tape, byIndex: index}
}

func (r *Replayer) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	entry, err := r.next(KindHTTP, requestPayload(req))
	if err != nil {
		return flow.HTTPResponse{}, err
	}
	if entry.Error != "" {
		return flow.HTTPResponse{}, errors.New(entry.Error)
	}
	var resp flow.HTTPResponse
	if len(entry.Response) > 0 {
		if err := json.Unmarshal(entry.Response, &resp); err != nil {
			return flow.HTTPResponse{}, fmt.Errorf("cassette: decode http response: %w", err)
		}
	}
	return resp, nil
}

// Exec serves the recorded subprocess outcome. Hermetic replay never
// re-runs user code and never resolves environments: the tape is the truth.
func (r *Replayer) Exec(_ context.Context, req flow.ExecRequest) (flow.ExecResult, error) {
	entry, err := r.next(KindExec, execPayload(req))
	if err != nil {
		return flow.ExecResult{}, err
	}
	if entry.Error != "" {
		return flow.ExecResult{}, errors.New(entry.Error)
	}
	var res flow.ExecResult
	if len(entry.Response) > 0 {
		if err := json.Unmarshal(entry.Response, &res); err != nil {
			return flow.ExecResult{}, fmt.Errorf("cassette: decode exec response: %w", err)
		}
	}
	return res, nil
}

func (r *Replayer) Query(_ context.Context, credID, sql string) ([]flow.Item, error) {
	entry, err := r.next(KindQuery, map[string]any{"credential": credID, "sql": sql})
	if err != nil {
		return nil, err
	}
	if entry.Error != "" {
		return nil, errors.New(entry.Error)
	}
	var rows []flow.Item
	if len(entry.Response) > 0 {
		if err := json.Unmarshal(entry.Response, &rows); err != nil {
			return nil, fmt.Errorf("cassette: decode query response: %w", err)
		}
	}
	return rows, nil
}

func (r *Replayer) Slack(_ context.Context, credID, channel, text string) error {
	entry, err := r.next(KindSlack, map[string]any{"credential": credID, "channel": channel, "text": text})
	if err != nil {
		return err
	}
	if entry.Error != "" {
		return errors.New(entry.Error)
	}
	return nil
}

func (r *Replayer) Sleep(_ context.Context, _ time.Duration) error { return nil }

// SaveBlob returns the taped hash. The bytes the node holds on replay are a
// reconstruction (Raw is never serialized), so re-hashing them would report
// drift that is not real: the HTTP tape entry already verified the response
// the blob came from, and the store verifies the content address on read.
func (r *Replayer) SaveBlob(_ []byte) (string, int, error) {
	entry, err := r.next(KindBlob, map[string]any{})
	if err != nil {
		return "", 0, err
	}
	if entry.Error != "" {
		return "", 0, errors.New(entry.Error)
	}
	var saved struct {
		Hash string `json:"hash"`
		Size int    `json:"size"`
	}
	if len(entry.Response) > 0 {
		if err := json.Unmarshal(entry.Response, &saved); err != nil {
			return "", 0, fmt.Errorf("cassette: decode blob entry: %w", err)
		}
	}
	return saved.Hash, saved.Size, nil
}

// LoadBlob reads from the injected blob store. The bytes feed a request the
// tape then verifies by hash, so the read itself needs no tape entry.
func (r *Replayer) LoadBlob(hash string) ([]byte, error) {
	if r.Blobs == nil {
		return nil, fmt.Errorf("cassette: replay host has no blob store")
	}
	return r.Blobs.LoadBlob(hash)
}

func (r *Replayer) Now() time.Time {
	// Replayed nodes must not see wall-clock time drift. Use the recording's
	// node timestamp when the engine supplied one; otherwise return the zero
	// time so expression previews are stable.
	return time.Time{}
}

func (r *Replayer) next(kind Kind, payload any) (Entry, error) {
	req, err := json.Marshal(payload)
	if err != nil {
		return Entry{}, err
	}
	actual := hashJSON(req)

	r.mu.Lock()
	defer r.mu.Unlock()
	index := r.index
	r.index++
	entry, ok := r.byIndex[index]
	if !ok || entry.Kind != kind {
		r.misses = append(r.misses, Miss{Kind: kind, Index: index})
		return Entry{}, fmt.Errorf("%w: node %s effect #%d (%s) was not recorded",
			ErrMiss, r.Tape.NodeID, index, kind)
	}
	if entry.RequestHash != actual {
		// Fail closed: a tape recorded for a different request can produce a
		// plausible but wrong result. Serving it with only an advisory diff
		// would let a mismatched (for example, sibling subflow) tape pass.
		r.diffs = append(r.diffs, Diff{Kind: kind, Index: index, RecordedHash: entry.RequestHash, ActualHash: actual})
		r.misses = append(r.misses, Miss{Kind: kind, Index: index})
		return Entry{}, fmt.Errorf("%w: node %s effect #%d (%s) request drifted", ErrMiss, r.Tape.NodeID, index, kind)
	}
	return entry, nil
}

func (r *Replayer) Diffs() []Diff {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Diff(nil), r.diffs...)
}

func (r *Replayer) Misses() []Miss {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Miss(nil), r.misses...)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// requestPayload is the canonical, redacted representation of an HTTP call.
// IdempotencyKey contains the run id, so it is deliberately excluded: including
// it would make every replay look like a request drift. Timeout is a local
// policy, not part of the effect.
func requestPayload(req flow.HTTPRequest) map[string]any {
	return map[string]any{
		"method":      strings.ToUpper(req.Method),
		"url":         req.URL,
		"headers":     redactHeaders(req.Headers),
		"body":        req.Body,
		"rawBody":     string(req.BodyRaw),
		"contentType": req.ContentType,
	}
}

var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"cookie":              true,
	"set-cookie":          true,
	"proxy-authorization": true,
	"x-api-key":           true,
}

func redactHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		if sensitiveHeaders[strings.ToLower(key)] {
			out[key] = "[redacted]"
			continue
		}
		out[key] = value
	}
	return out
}

func hashJSON(data []byte) string {
	// encoding/json emits map keys in sorted order, so hashing the payload is
	// stable for the map-shaped requests used here.
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
