package nodes

import (
	"context"
	"strings"
	"testing"

	"loom/internal/flow"
)

// Connector executor (Campaign 4): template rendering phases and the
// delegation into httpNode's trusted pipeline.

func connectorTestInput() (flow.Input, *fakeHost) {
	host := &fakeHost{resp: flow.HTTPResponse{Status: 200, Body: flow.Item{"ok": true}}}
	credData := map[string]any{"baseUrl": "https://api.example.test", "token": "tok-123"}
	in := flow.Input{
		Item: flow.Item{"title": "hello world"},
		Config: map[string]any{
			"owner": "acme",
			// Publish-inlined template (store.ResolveConnectorRefs).
			"request": map[string]any{
				"method": "GET",
				"url":    "{{cred.baseUrl}}/orgs/{{config.owner}}/things",
				"query":  map[string]any{"limit": "10", "q": "{{$json.title}}"},
				"headers": map[string]any{
					"Authorization": "Bearer {{cred.token}}",
				},
				"successCodes": "200,2xx",
			},
			"_connector": map[string]any{"id": "conn-1", "version": float64(1)},
		},
		CredentialData: map[string]flow.Credential{
			"http": {Type: "http", Raw: `{}`, Data: credData},
		},
		Host: host,
	}
	return in, host
}

func TestConnectorNodeRendersConfigAndCredPlaceholders(t *testing.T) {
	in, host := connectorTestInput()
	out, err := (connectorNode{}).Execute(context.Background(), in)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// The request reached the host with placeholders resolved and item
	// expressions ({{$json.title}}) left for httpNode's scope.
	if got := host.req.URL; !strings.HasPrefix(got, "https://api.example.test/orgs/acme/things?") {
		t.Fatalf("url = %q", got)
	}
	if host.req.Method != "GET" {
		t.Fatalf("method = %q", host.req.Method)
	}
	if !strings.Contains(host.req.URL, "q=hello+world") || !strings.Contains(host.req.URL, "limit=10") {
		t.Fatalf("query missing from url: %q", host.req.URL)
	}
	if host.req.Headers["Authorization"] != "Bearer tok-123" {
		t.Fatalf("auth header = %q", host.req.Headers["Authorization"])
	}
	if out["main"]["statusCode"] != 200 || out["main"]["ok"] != true {
		t.Fatalf("output item = %v", out["main"])
	}
}

func TestConnectorNodeMissingTemplateFails(t *testing.T) {
	in, _ := connectorTestInput()
	delete(in.Config, "request")
	if _, err := (connectorNode{}).Execute(context.Background(), in); err == nil {
		t.Fatal("missing template must fail, not run a bare request")
	}
}

func TestConnectorPlaceholderPhasesDoNotCollide(t *testing.T) {
	in, _ := connectorTestInput()
	// An item-scope expression must survive phase one verbatim.
	rendered := renderConnectorTemplate("x={{$json.title}} c={{config.owner}} u={{unknown.ref}}", in)
	if rendered != "x={{$json.title}} c=acme u={{unknown.ref}}" {
		t.Fatalf("phased rendering: %q", rendered)
	}
}
func TestConnectorNodeBodyTemplate(t *testing.T) {
	in, host := connectorTestInput()
	req := in.Config["request"].(map[string]any)
	req["method"] = "POST"
	req["url"] = "{{cred.baseUrl}}/things"
	req["body"] = `{"name": "{{config.owner}}"}`
	if _, err := (connectorNode{}).Execute(context.Background(), in); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if host.req.Body != `{"name": "acme"}` {
		t.Fatalf("body = %q", host.req.Body)
	}
}
