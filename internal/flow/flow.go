// Package flow defines the runtime contracts shared by the engine, nodes and hosts.
package flow

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Item is the unit of data that travels along an edge.
type Item map[string]any

// Output maps an output port to the item produced on that port.
type Output map[string]Item

// DecisionInfo is the human decision recorded for an approval node. It travels
// into the node so the decision and its reason reach downstream steps.
type DecisionInfo struct {
	Decision string
	Note     string
	Actor    string
	At       time.Time
}

// Context is the run-scoped data available to expressions. Nodes holds the
// outputs of nodes that have completed so far.
type Context struct {
	Nodes    map[string]Output
	Run      RunContext
	Job      JobContext
	Vars     map[string]any
	Index    int
	HasIndex bool
}

type RunContext struct {
	ID        string
	Trigger   string
	Status    string
	Hops      int
	RootRunID string
}

type JobContext struct {
	ID   string
	Name string
}

// Credential is a decrypted credential made available to a node. Data is the
// parsed JSON object when the stored secret is JSON; Raw preserves plaintext.
type Credential struct {
	Type string
	Data map[string]any
	Raw  string
}

type Input struct {
	Item           Item
	Config         map[string]any
	Credentials    map[string]string
	CredentialData map[string]Credential
	Context        *Context
	RunID          string
	NodeID         string
	Attempt        int
	Decision       string // set when resuming an approval node
	DecisionNote   string
	DecisionActor  string
	DecisionAt     time.Time
	// DurableWait asks flow.wait to suspend the run instead of sleeping, so a
	// long wait survives a restart. WakeAt carries the deadline persisted by a
	// previous suspension; zero means "compute it now".
	DurableWait bool
	WakeAt      time.Time
	Host        Host
	Subflow     SubflowRunner
	Now         time.Time
	// Trace records small request/response metadata for the run inspector.
	// Nodes call it at most once per kind ("request" or "response").
	Trace func(kind string, data map[string]any)
}

// IdempotencyKey is stable across retries of the same node, so an effect can
// dedupe a request whose response was lost.
func (in Input) IdempotencyKey() string {
	if in.RunID == "" {
		return ""
	}
	key := in.RunID + ":" + in.NodeID
	if in.Context != nil && in.Context.HasIndex {
		key += fmt.Sprintf(":item-%d", in.Context.Index)
	}
	return key
}

type Node interface {
	Execute(ctx context.Context, in Input) (Output, error)
}

// SubflowRunner is implemented by the engine and handed to subflow nodes.
type SubflowRunner interface {
	RunSubflow(ctx context.Context, workflowID string, item Item) (Item, error)
	// RunSubflowAsync starts the child without waiting for it and returns the
	// input item immediately. Replay keeps this synchronous so effects are
	// recorded and replayed rather than racing the parent.
	RunSubflowAsync(ctx context.Context, workflowID string, item Item) (Item, error)
}

// Host is the only door to the outside world. A simulated host makes every
// example runnable offline; a live host talks to the real network.
type Host interface {
	HTTP(ctx context.Context, req HTTPRequest) (HTTPResponse, error)
	Query(ctx context.Context, credID, sql string) ([]Item, error)
	Slack(ctx context.Context, credID, channel, text string) error
	Sleep(ctx context.Context, d time.Duration) error
	Now() time.Time
}

// Event is a named signal published by a running workflow. Workflows with
// a matching trigger.event node start a run when it fires.
type Event struct {
	Name        string
	Item        Item
	Hops        int
	SourceRunID string
	// RootRunID is the run at the head of the event chain. It lets the server
	// budget every descendant of one trigger instead of only bounding hops.
	RootRunID string
}

// EventPublisher is an optional Host capability. Hosts that implement it
// forward published events to the event bus; hosts without it (simulation)
// execute flow.emit as a no-op publish.
type EventPublisher interface {
	Publish(ev Event)
}

type HTTPRequest struct {
	Method         string
	URL            string
	Headers        map[string]string
	Body           any
	BodyRaw        []byte
	ContentType    string
	Timeout        time.Duration
	IdempotencyKey string
	// OAuth2 requests a client-credentials bearer token from the host.
	// It is deliberately not part of the recorded effect payload (it
	// carries the client secret); hermetic replay serves the recorded
	// response and never fetches tokens.
	OAuth2 *OAuth2Params
	// Proxy routes the request through an HTTP proxy. Trusted operator
	// or credential configuration; the target guard does not apply beyond
	// the proxy itself.
	Proxy string
}

// OAuth2Params is a client-credentials token request.
type OAuth2Params struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scope        string
}

type HTTPResponse struct {
	Status     int
	StatusText string
	Headers    map[string]string
	Body       Item
	// Raw is the response body exactly as received. It is never serialized
	// into cassettes (the parsed Body is the recorded form); nodes that need
	// faithful bytes, such as responseMode blob, reconstruct it on replay.
	Raw []byte `json:"-"`
}

// BlobSaver is an optional Host capability. Hosts that implement it hand
// bytes to the content-addressed blob store replay already verifies. Saving
// is deterministic: the same bytes always produce the same hash, so hermetic
// hosts can implement it without persistence by returning the hash alone.
type BlobSaver interface {
	// SaveBlob persists data (or, in hermetic contexts, content-addresses it)
	// and returns its hash and size.
	SaveBlob(data []byte) (hash string, size int, err error)
}

// BlobLoader is an optional Host capability for reading stored blobs back,
// used by nodes that turn blob references into request content.
type BlobLoader interface {
	// LoadBlob returns the stored content for a blob hash.
	LoadBlob(hash string) ([]byte, error)
}

// Executor is an optional Host capability for subprocess code execution
// (code.python). Recording hosts tape the request/response pair, so replays
// verify subprocess output without re-running anything.
type Executor interface {
	// Exec runs source against stdin (the incoming item) and returns the
	// exit status, captured output and parsed result.
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)
}

// ExecRequest is one subprocess execution. EnvKey names a resolved
// dependency environment ("" = interpreter without third-party packages);
// runs never install dependencies — environments are built at publish time.
type ExecRequest struct {
	Source  string
	Stdin   Item
	EnvKey  string
	Timeout time.Duration
	// MaxMemoryMB caps the subprocess's address space where the kernel
	// supports it (Linux). Platforms that refuse RLIMIT_AS (macOS reserves
	// a huge fixed VA) fall back to time and output caps and say so.
	MaxMemoryMB int
}

// ExecResult carries what the tape needs to verify a subprocess run.
// Result is the JSON the script returned (object, array, scalar or null).
type ExecResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
	Result   any
}

// Suspend is returned by a node that needs to pause the run (approval, long wait).
type Suspend struct {
	Reason string
	Data   map[string]any
}

func (s *Suspend) Error() string { return "suspended: " + s.Reason }

func IsSuspend(err error) (*Suspend, bool) {
	var s *Suspend
	if errors.As(err, &s) {
		return s, true
	}
	return nil, false
}

// LookupPath resolves "a.b.0" against an item. "$json." and "json." prefixes are
// accepted so node config reads naturally.
func LookupPath(item Item, path string) (any, bool) {
	path = strings.TrimSpace(path)
	path = strings.TrimPrefix(path, "$json.")
	path = strings.TrimPrefix(path, "$.")
	path = strings.TrimPrefix(path, "json.")
	if path == "" || path == "$json" || path == "json" {
		return item, true
	}

	var cur any = map[string]any(item)
	for _, part := range strings.Split(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case Item:
			v, ok := node[part]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			idx := 0
			if _, err := fmt.Sscanf(part, "%d", &idx); err != nil {
				return nil, false
			}
			if idx < 0 || idx >= len(node) {
				return nil, false
			}
			cur = node[idx]
		default:
			return nil, false
		}
	}
	return cur, true
}
