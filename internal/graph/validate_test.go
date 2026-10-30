package graph

import (
	"path/filepath"
	"testing"

	"loom/internal/nodes"
)

func TestExamplesAreClean(t *testing.T) {
	files, err := filepath.Glob("../../examples/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no example workflows found")
	}

	for _, file := range files {
		wf, err := Load(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		report := Validate(wf)
		for _, issue := range report.Issues {
			t.Errorf("%s: %s", file, issue.String())
		}
	}
}

func TestRejectsTypeMismatch(t *testing.T) {
	wf := &Workflow{
		ID: "type-mismatch", Name: "Type mismatch", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("query", "db.postgres", map[string]any{"operation": "query", "query": "select 1"}, map[string]string{"postgres": "cred_pg"}),
			mkNode("post", "chat.slack", map[string]any{"channel": "#ops", "text": "hi"}, map[string]string{"slack": "cred_slack"}),
		},
		Edges: []Edge{
			mkEdge("start", "main", "query", "main"),
			mkEdge("query", "main", "post", "main"),
		},
	}

	report := Validate(wf)
	if !hasCode(report, "type_mismatch") {
		t.Fatalf("expected type_mismatch, got: %v", report.Issues)
	}
}

func TestRejectsDuplicateInput(t *testing.T) {
	http := func(id string) Node {
		return mkNode(id, "http.request",
			map[string]any{"method": "GET", "url": "https://example.com"},
			map[string]string{"http": "cred_http"})
	}

	wf := &Workflow{
		ID: "duplicate-input", Name: "Duplicate input", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			http("a"), http("b"), http("c"),
		},
		Edges: []Edge{
			mkEdge("start", "main", "a", "main"),
			mkEdge("start", "main", "b", "main"),
			mkEdge("a", "main", "c", "main"),
			mkEdge("b", "main", "c", "main"),
		},
	}

	report := Validate(wf)
	if !hasCode(report, "duplicate_input") {
		t.Fatalf("expected duplicate_input, got: %v", report.Issues)
	}
}

func TestRejectsCycle(t *testing.T) {
	http := func(id string) Node {
		return mkNode(id, "http.request",
			map[string]any{"method": "GET", "url": "https://example.com"},
			map[string]string{"http": "cred_http"})
	}

	wf := &Workflow{
		ID: "cycle", Name: "Cycle", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			http("a"),
			mkNode("join", "flow.merge", map[string]any{}, nil),
			http("b"),
		},
		Edges: []Edge{
			mkEdge("start", "main", "a", "main"),
			mkEdge("a", "main", "join", "a"),
			mkEdge("join", "main", "b", "main"),
			mkEdge("b", "main", "join", "b"),
		},
	}

	report := Validate(wf)
	if !hasCode(report, "cycle") {
		t.Fatalf("expected cycle, got: %v", report.Issues)
	}
}

func TestRejectsUnknownConfig(t *testing.T) {
	wf := &Workflow{
		ID: "typo", Name: "Typo", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("code", "code.run", map[string]any{
				"language": "javascript",
				"source":   "return item;",
				"soruce":   "typo",
			}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "code", "main")},
	}

	report := Validate(wf)
	if !hasCode(report, "unknown_config") {
		t.Fatalf("expected unknown_config, got: %v", report.Issues)
	}
}

func TestHTTPCredentialIsOptional(t *testing.T) {
	wf := &Workflow{
		ID: "optional-cred", Name: "Optional credential", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("http", "http.request", map[string]any{
				"method": "GET", "url": "https://example.com",
			}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "http", "main")},
	}
	report := Validate(wf)
	if hasCode(report, "missing_credential") {
		t.Fatalf("HTTP credential should be optional: %v", report.Issues)
	}
}

func TestRejectsNodeReferenceNotUpstream(t *testing.T) {
	wf := &Workflow{
		ID: "bad-ref", Name: "Bad ref", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("a", "data.set", map[string]any{"values": map[string]any{"a": 1}}, nil),
			mkNode("b", "data.set", map[string]any{"values": map[string]any{"b": 2}}, nil),
			mkNode("out", "data.set", map[string]any{
				"values": map[string]any{"leak": `{{ $node["b"].json.b }}`},
			}, nil),
		},
		Edges: []Edge{
			mkEdge("start", "main", "a", "main"),
			mkEdge("start", "main", "b", "main"),
			mkEdge("a", "main", "out", "main"),
		},
	}
	report := Validate(wf)
	if !hasCode(report, "node_reference_not_upstream") {
		t.Fatalf("expected node_reference_not_upstream, got: %v", report.Issues)
	}
}

func TestAcceptsUpstreamNodeReference(t *testing.T) {
	wf := &Workflow{
		ID: "good-ref", Name: "Good ref", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("a", "data.set", map[string]any{"values": map[string]any{"a": 1}}, nil),
			mkNode("out", "data.set", map[string]any{
				"values": map[string]any{"copy": `{{ $node["a"].json.a }}`},
			}, nil),
		},
		Edges: []Edge{
			mkEdge("start", "main", "a", "main"),
			mkEdge("a", "main", "out", "main"),
		},
	}
	report := Validate(wf)
	for _, issue := range report.Issues {
		if issue.Code == "node_reference_not_upstream" || issue.Code == "unknown_node_reference" {
			t.Fatalf("unexpected reference issue: %v", report.Issues)
		}
	}
}

func TestBuildPlanLevels(t *testing.T) {
	http := func(id string) Node {
		return mkNode(id, "http.request",
			map[string]any{"method": "GET", "url": "https://example.com"},
			map[string]string{"http": "cred_http"})
	}

	wf := &Workflow{
		ID: "plan", Name: "Plan", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			http("a"), http("b"),
		},
		Edges: []Edge{
			mkEdge("start", "main", "a", "main"),
			mkEdge("a", "main", "b", "main"),
		},
	}

	plan, err := BuildPlan(wf)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"start"}, {"a"}, {"b"}}
	if len(plan.Levels) != len(want) {
		t.Fatalf("levels = %v, want %v", plan.Levels, want)
	}
	for i := range want {
		if len(plan.Levels[i]) != len(want[i]) || plan.Levels[i][0] != want[i][0] {
			t.Fatalf("levels = %v, want %v", plan.Levels, want)
		}
	}
}

func TestCompatible(t *testing.T) {
	cases := []struct {
		src, dst nodes.PortType
		ok, warn bool
	}{
		{nodes.TypeJSON, nodes.TypeOrder, true, true},
		{nodes.TypeOrder, nodes.TypeJSON, true, false},
		{nodes.TypeOrder, nodes.TypeMessage, false, false},
		{nodes.TypeMessage, nodes.TypeMessage, true, false},
		{nodes.TypeAny, nodes.TypeMessage, true, false},
		{nodes.TypeRows, nodes.TypeJSON, true, false},
	}
	for _, c := range cases {
		ok, warn := Compatible(c.src, c.dst)
		if ok != c.ok || warn != c.warn {
			t.Errorf("Compatible(%q, %q) = (%v, %v), want (%v, %v)", c.src, c.dst, ok, warn, c.ok, c.warn)
		}
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	_, err := Parse([]byte(`{"id":"x","name":"x","version":1,"nodes":[],"edges":[],"bogus":true}`))
	if err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestRetryPolicyValidation(t *testing.T) {
	wf := &Workflow{
		ID: "retry", Name: "Retry", Version: 1,
		Nodes: []Node{mkNode("start", "trigger.manual", map[string]any{}, nil)},
	}
	wf.Nodes[0].Retry = &RetryPolicy{MaxAttempts: 21, BackoffMs: 10, MaxDelayMs: 5}

	report := Validate(wf)
	if !report.HasErrors() {
		t.Fatal("expected retry policy validation errors")
	}
	for _, code := range []string{"invalid_retry_attempts", "retry_backoff_exceeds_max"} {
		if !hasCode(report, code) {
			t.Errorf("missing validation code %q", code)
		}
	}
}

func TestOnErrorValidation(t *testing.T) {
	base := func() *Workflow {
		return &Workflow{
			ID: "on-error", Name: "On error", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				mkNode("call", "http.request", map[string]any{"method": "GET", "url": "https://example.com"}, map[string]string{"http": "cred_http"}),
				mkNode("recover", "data.set", map[string]any{"values": map[string]any{"ok": true}}, nil),
			},
			Edges: []Edge{
				mkEdge("start", "main", "call", "main"),
				mkEdge("call", "error", "recover", "main"),
			},
		}
	}

	wf := base()
	if report := Validate(wf); !hasCode(report, "unknown_port") {
		t.Fatal("expected unknown_port without onError=branch")
	}

	wf = base()
	wf.Nodes[1].OnError = "branch"
	if report := Validate(wf); report.HasErrors() {
		t.Fatalf("expected valid error branch, got %#v", report.Issues)
	}

	wf = base()
	wf.Nodes[1].OnError = "retry"
	if report := Validate(wf); !hasCode(report, "invalid_on_error") {
		t.Fatal("expected invalid_on_error")
	}
}

func mkNode(id, typ string, config map[string]any, creds map[string]string) Node {
	return Node{
		ID: id, Type: typ, TypeVersion: 1, Name: id,
		Position: Position{}, Config: config, Credentials: creds,
	}
}

func mkEdge(from, fromPort, to, toPort string) Edge {
	return Edge{
		From: Endpoint{Node: from, Port: fromPort},
		To:   Endpoint{Node: to, Port: toPort},
	}
}

func hasCode(report Report, code string) bool {
	for _, issue := range report.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}
