package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"loom/internal/egress"
	"loom/internal/flow"
	"loom/internal/graph"
)

// egressCapableHost implements every optional capability nodes assert at
// runtime, recording what was used.
type egressCapableHost struct {
	dialed    []string
	saved     int
	loaded    int
	executed  int
	published int
}

func (h *egressCapableHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.dialed = append(h.dialed, req.URL)
	return flow.HTTPResponse{Status: 200, Body: flow.Item{}}, nil
}

func (h *egressCapableHost) Query(context.Context, string, string) ([]flow.Item, error) {
	return nil, nil
}

func (h *egressCapableHost) Slack(context.Context, string, string, string) error { return nil }

func (h *egressCapableHost) Sleep(context.Context, time.Duration) error { return nil }

func (h *egressCapableHost) Now() time.Time { return time.Unix(0, 0) }

func (h *egressCapableHost) SaveBlob([]byte) (string, int, error) {
	h.saved++
	return "hash", 1, nil
}

func (h *egressCapableHost) LoadBlob(string) ([]byte, error) {
	h.loaded++
	return []byte("blob"), nil
}

func (h *egressCapableHost) Exec(context.Context, flow.ExecRequest) (flow.ExecResult, error) {
	h.executed++
	return flow.ExecResult{}, nil
}

func (h *egressCapableHost) Publish(flow.Event) { h.published++ }

func TestWorkflowEgressHostBlocksUnlistedDestination(t *testing.T) {
	base := &egressCapableHost{}
	wrapped := wrapWorkflowEgress(base, &egress.WorkflowPolicy{
		Allowlist: []string{"https://api.github.com"},
	})
	_, err := wrapped.HTTP(context.Background(), flow.HTTPRequest{URL: "https://evil.example.net/x"})
	if err == nil || !strings.Contains(err.Error(), "workflow's egress allowlist") {
		t.Fatalf("want workflow denial, got %v", err)
	}
	if len(base.dialed) != 0 {
		t.Fatal("denied request must not reach the base host")
	}
	if _, err := wrapped.HTTP(context.Background(), flow.HTTPRequest{URL: "https://api.github.com/legacy"}); err != nil {
		t.Fatalf("listed destination should pass: %v", err)
	}
}

func TestWorkflowEgressHostNilPolicyIsIdentity(t *testing.T) {
	base := &egressCapableHost{}
	if got := wrapWorkflowEgress(base, nil); got != flow.Host(base) {
		t.Fatal("nil policy must not wrap")
	}
	if got := wrapWorkflowEgress(base, &egress.WorkflowPolicy{}); got != flow.Host(base) {
		t.Fatal("empty allowlist must not wrap")
	}
}

// The wireEventBus lesson: a wrapper must forward optional capabilities,
// or code.python (Executor) and blob modes (BlobSaver/BlobLoader) silently
// break.
func TestWorkflowEgressHostForwardsCapabilities(t *testing.T) {
	base := &egressCapableHost{}
	wrapped := wrapWorkflowEgress(base, &egress.WorkflowPolicy{Allowlist: []string{"https://api.example.com"}})

	if _, ok := wrapped.(flow.Executor); !ok {
		t.Fatal("Executor capability must be forwarded")
	}
	if _, err := wrapped.(flow.Executor).Exec(context.Background(), flow.ExecRequest{}); err != nil {
		t.Fatalf("exec should forward: %v", err)
	}
	if base.executed != 1 {
		t.Fatal("exec must reach the base host")
	}

	if _, ok := wrapped.(flow.BlobSaver); !ok {
		t.Fatal("BlobSaver capability must be forwarded")
	}
	if _, _, err := wrapped.(flow.BlobSaver).SaveBlob([]byte("x")); err != nil {
		t.Fatalf("save should forward: %v", err)
	}
	if base.saved != 1 {
		t.Fatal("save must reach the base host")
	}

	if _, ok := wrapped.(flow.BlobLoader); !ok {
		t.Fatal("BlobLoader capability must be forwarded")
	}
	if _, err := wrapped.(flow.BlobLoader).LoadBlob("hash"); err != nil {
		t.Fatalf("load should forward: %v", err)
	}
	if base.loaded != 1 {
		t.Fatal("load must reach the base host")
	}

	if _, ok := wrapped.(flow.EventPublisher); !ok {
		t.Fatal("EventPublisher capability must be forwarded")
	}
	wrapped.(flow.EventPublisher).Publish(flow.Event{Name: "x"})
	if base.published != 1 {
		t.Fatal("publish must reach the base host")
	}
}

func TestWorkflowEgressValidatedByGraph(t *testing.T) {
	wf := &graph.Workflow{
		ID: "w", Name: "w", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start"},
			{ID: "n", Type: "http.request", TypeVersion: 1, Name: "N",
				Config: map[string]any{"url": "https://api.example.com", "method": "GET"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "n", Port: "main"}},
		},
	}
	wf.Egress = &egress.WorkflowPolicy{Allowlist: []string{"https://api.example.com"}}
	if report := graph.Validate(wf); report.HasErrors() {
		t.Fatalf("valid egress entry must pass: %v", report)
	}
	wf.Egress = &egress.WorkflowPolicy{Allowlist: []string{"api.example.com", "https://x/*/bad"}}
	report := graph.Validate(wf)
	if !report.HasErrors() {
		t.Fatal("invalid egress entries must fail validation")
	}
	joined := ""
	for _, issue := range report.Issues {
		joined += issue.Message
	}
	if !strings.Contains(joined, "egress") {
		t.Fatalf("validation must name the egress problem: %v", report)
	}
}
