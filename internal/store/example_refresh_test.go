package store

import (
	"testing"

	"loom/internal/graph"
)

// oldExample is the shape of a workflow stored before chat.slack was retired:
// same ids as the bundled daily-digest example, with the Slack node last.
func oldExample() *graph.Workflow {
	return &graph.Workflow{
		ID: "daily-digest", Name: "Daily news digest", Version: 1,
		Nodes: []graph.Node{
			{ID: "schedule", Type: "trigger.cron", TypeVersion: 1, Name: "Every morning",
				Position: graph.Position{X: -41, Y: -105},
				Config:   map[string]any{"schedule": "0 8 * * *", "timezone": "UTC"}},
			{ID: "news", Type: "http.request", TypeVersion: 1, Name: "Fetch headlines",
				Position:    graph.Position{X: 220, Y: 125},
				Config:      map[string]any{"method": "GET", "url": "https://news.example.com/api/headlines"},
				Credentials: map[string]string{"http": "cred_http"}},
			{ID: "summarize", Type: "code.run", TypeVersion: 1, Name: "Write the digest",
				Position: graph.Position{X: 488, Y: -112},
				Config:   map[string]any{"language": "javascript", "outputType": "message", "source": "return { text: 'digest' };"}},
			{ID: "post", Type: "chat.slack", TypeVersion: 1, Name: "Post to Slack",
				Position:    graph.Position{X: 705, Y: 64},
				Config:      map[string]any{"channel": "#digest", "text": "{{ $json.text }}"},
				Credentials: map[string]string{"slack": "cred_slack"}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "schedule", Port: "main"}, To: graph.Endpoint{Node: "news", Port: "main"}},
			{From: graph.Endpoint{Node: "news", Port: "main"}, To: graph.Endpoint{Node: "summarize", Port: "main"}},
			{From: graph.Endpoint{Node: "summarize", Port: "main"}, To: graph.Endpoint{Node: "post", Port: "main"}},
		},
	}
}

func TestOpenRefreshesBundledExampleNodes(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	old := oldExample()
	if err := s.SaveWorkflow(old); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if _, err := s.PublishWorkflow(old); err != nil {
		t.Fatalf("publish workflow: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	// Reopening runs migrate, which refreshes stored copies of bundled examples.
	s, err = Open(dir)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer s.Close()

	wf, err := s.GetWorkflow("daily-digest")
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	post := nodeByID(t, wf, "post")
	if post.Type != "http.request" {
		t.Fatalf("post type = %q, want http.request", post.Type)
	}
	if post.Name != "Post to Slack" || post.Position != old.Nodes[3].Position {
		t.Fatalf("replacement changed label/placement: %#v", post)
	}
	if post.Credentials["http"] == "" {
		t.Fatalf("replacement has no http credential: %#v", post.Credentials)
	}
	if len(wf.Edges) != 3 {
		t.Fatalf("edges = %d, want 3", len(wf.Edges))
	}
	for _, node := range wf.Nodes {
		if node.Type == "chat.slack" || node.Type == "data.csv.parse" {
			t.Fatalf("retired node %q survived the migration", node.Type)
		}
	}

	// The published snapshot must be rewritten too, or opening version 1 from
	// history brings the retired node back.
	v1, err := s.GetWorkflowVersion("daily-digest", 1)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if got := nodeByID(t, v1, "post").Type; got != "http.request" {
		t.Fatalf("version 1 post type = %q, want http.request", got)
	}
}

func TestRefreshLeavesUserWorkflowsAlone(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer s.Close()

	custom := &graph.Workflow{
		ID: "my-slack-workflow", Name: "My Slack workflow", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", Config: map[string]any{}},
			{ID: "post", Type: "chat.slack", Config: map[string]any{"channel": "#mine", "text": "hi"}},
		},
	}
	if err := s.SaveWorkflow(custom); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := s.refreshBundledExamples(); err != nil {
		t.Fatalf("refresh examples: %v", err)
	}
	wf, err := s.GetWorkflow("my-slack-workflow")
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if got := nodeByID(t, wf, "post").Type; got != "chat.slack" {
		t.Fatalf("user workflow was rewritten to %q", got)
	}
}

func nodeByID(t *testing.T, wf *graph.Workflow, id string) graph.Node {
	t.Helper()
	for _, node := range wf.Nodes {
		if node.ID == id {
			return node
		}
	}
	t.Fatalf("node %q not found", id)
	return graph.Node{}
}
