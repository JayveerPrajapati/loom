package nodes

import (
	"context"
	"strings"
	"testing"

	"loom/internal/flow"
)

func TestHTTPGraphQLBody(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Body: flow.Item{"data": map[string]any{"viewer": "loom"}},
	}}
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method":           "POST",
			"url":              "https://api.example.com/graphql",
			"bodyType":         "graphql",
			"graphqlQuery":     "query { viewer }",
			"graphqlVariables": map[string]any{"id": "42"},
			"auth":             "none",
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h.req.Method != "POST" || h.req.ContentType != "application/json" {
		t.Fatalf("req = %s %s", h.req.Method, h.req.ContentType)
	}
	body, ok := h.req.Body.(flow.Item)
	if !ok {
		body, ok = h.req.Body.(map[string]any)
	}
	if !ok || body["query"] != "query { viewer }" {
		t.Fatalf("body = %#v", h.req.Body)
	}
	if vars, _ := body["variables"].(map[string]any); vars["id"] != "42" {
		t.Fatalf("variables = %#v", body["variables"])
	}
	if out["main"]["data"].(map[string]any)["viewer"] != "loom" {
		t.Fatalf("out = %#v", out["main"])
	}
}

func TestHTTPGraphQLErrorExtraction(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{
		Status: 200, StatusText: "OK",
		Body: flow.Item{"data": nil, "errors": []any{
			map[string]any{"message": "field 'viewer' not found"},
		}},
	}}
	// Without failOnError the errors array stays in the item.
	out, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "POST", "url": "https://api.example.com/graphql",
			"bodyType": "graphql", "graphqlQuery": "query { viewer }",
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	errs, ok := out["main"]["errors"].([]any)
	if !ok || len(errs) != 1 {
		t.Fatalf("errors = %#v", out["main"]["errors"])
	}

	// With failOnError a 200-with-errors response must fail the node.
	_, err = httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "POST", "url": "https://api.example.com/graphql",
			"bodyType": "graphql", "graphqlQuery": "query { viewer }",
			"failOnError": true,
		},
		Host: h,
	})
	if err == nil || !strings.Contains(err.Error(), "field 'viewer' not found") {
		t.Fatalf("err = %v, want graphql error surfaced", err)
	}
}

func TestHTTPGraphQLQueryRequired(t *testing.T) {
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "POST", "url": "https://api.example.com/graphql",
			"bodyType": "graphql",
		},
		Host: &fakeHost{resp: flow.HTTPResponse{Status: 200, Body: flow.Item{}}},
	})
	if err == nil || !strings.Contains(err.Error(), "graphqlQuery") {
		t.Fatalf("err = %v, want graphqlQuery required", err)
	}
}

func TestHTTPOAuth2Wiring(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{
		Status: 200, StatusText: "OK", Body: flow.Item{"ok": true},
	}}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/me",
			"auth": "oauth2",
		},
		CredentialData: map[string]flow.Credential{
			"oauth2": {Type: "oauth2", Data: map[string]any{
				"tokenUrl": "https://auth.example.com/token", "clientId": "loom",
				"clientSecret": "s3cret", "scopes": "read",
			}},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h.req.OAuth2 == nil {
		t.Fatal("request carries no oauth2 params")
	}
	if h.req.OAuth2.TokenURL != "https://auth.example.com/token" || h.req.OAuth2.ClientID != "loom" {
		t.Fatalf("oauth2 = %+v", h.req.OAuth2)
	}
	if h.req.OAuth2.ClientSecret != "s3cret" || h.req.OAuth2.Scope != "read" {
		t.Fatalf("oauth2 = %+v", h.req.OAuth2)
	}
}

func TestHTTPOAuth2RequiresCredential(t *testing.T) {
	// No oauth2 credential at all.
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/me", "auth": "oauth2",
		},
		Host: &fakeHost{resp: flow.HTTPResponse{Status: 200, Body: flow.Item{}}},
	})
	if err == nil || !strings.Contains(err.Error(), "oauth2 credential") {
		t.Fatalf("err = %v, want oauth2 credential required", err)
	}
	// Credential present but incomplete.
	_, err = httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/me", "auth": "oauth2",
		},
		CredentialData: map[string]flow.Credential{
			"oauth2": {Type: "oauth2", Data: map[string]any{"clientId": "loom"}},
		},
		Host: &fakeHost{resp: flow.HTTPResponse{Status: 200, Body: flow.Item{}}},
	})
	if err == nil || !strings.Contains(err.Error(), "tokenUrl") {
		t.Fatalf("err = %v, want tokenUrl required", err)
	}
}

func TestHTTPProxyFromCredential(t *testing.T) {
	h := &fakeHost{resp: flow.HTTPResponse{Status: 200, Body: flow.Item{}}}
	_, err := httpNode{}.Execute(context.Background(), flow.Input{
		Config: map[string]any{
			"method": "GET", "url": "https://api.example.com/me", "auth": "none",
		},
		CredentialData: map[string]flow.Credential{
			"http": {Type: "http", Data: map[string]any{
				"proxyUrl": "https://proxy.internal:8080",
			}},
		},
		Host: h,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if h.req.Proxy != "https://proxy.internal:8080" {
		t.Fatalf("proxy = %q", h.req.Proxy)
	}
}
