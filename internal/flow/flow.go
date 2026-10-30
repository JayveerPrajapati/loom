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
	ID      string
	Trigger string
	Status  string
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
	Host           Host
	Subflow        SubflowRunner
	Now            time.Time
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

type HTTPRequest struct {
	Method         string
	URL            string
	Headers        map[string]string
	Body           any
	BodyRaw        []byte
	ContentType    string
	Timeout        time.Duration
	IdempotencyKey string
}

type HTTPResponse struct {
	Status     int
	StatusText string
	Headers    map[string]string
	Body       Item
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
