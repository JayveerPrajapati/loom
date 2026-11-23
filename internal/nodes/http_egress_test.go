package nodes

import (
	"context"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

// egressRecorderHost records the request targets the node tried to dial.
type egressRecorderHost struct {
	targets []string
}

func (h *egressRecorderHost) HTTP(_ context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.targets = append(h.targets, req.URL)
	return flow.HTTPResponse{Status: 200, Body: flow.Item{}}, nil
}

func (h *egressRecorderHost) Query(context.Context, string, string) ([]flow.Item, error) {
	return nil, nil
}

func (h *egressRecorderHost) Slack(context.Context, string, string, string) error { return nil }

func (h *egressRecorderHost) Sleep(context.Context, time.Duration) error { return nil }

func (h *egressRecorderHost) Now() time.Time { return time.Unix(0, 0) }

func TestHTTPCredentialAllowedHostsBlocksOutsideTarget(t *testing.T) {
	h := &egressRecorderHost{}
	in := flow.Input{
		Config: map[string]any{
			"url":       "https://evil.example.net/api",
			"method":    "GET",
			"bodyType":  "json",
			"auth":      "credential",
			"headers":   map[string]any{"Authorization": "Bearer x"},
			"tokenName": "token",
		},
		CredentialData: map[string]flow.Credential{"http": {
			Type: "http",
			Data: map[string]any{"allowedHosts": []any{"api.github.com", "*.api.slack.com"}},
		}},
		Credentials: map[string]string{"http": "github-prod"},
		Host:        h,
		RunID:       "run",
		NodeID:      "node",
	}
	_, err := Registry()["http.request"].Execute(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), `credential "github-prod"`) {
		t.Fatalf("want credential-named denial, got %v", err)
	}
	if len(h.targets) != 0 {
		t.Fatalf("no request may be dialed after denial, got %v", h.targets)
	}
}

func TestHTTPCredentialAllowedHostsAllowsListed(t *testing.T) {
	h := &egressRecorderHost{}
	in := flow.Input{
		Config: map[string]any{
			"url":       "https://api.github.com/repos/loom/loom",
			"method":    "GET",
			"bodyType":  "json",
			"auth":      "credential",
			"headers":   map[string]any{"Authorization": "Bearer x"},
			"tokenName": "token",
		},
		CredentialData: map[string]flow.Credential{"http": {
			Type: "http",
			Data: map[string]any{"allowedHosts": []any{"api.github.com"}},
		}},
		Credentials: map[string]string{"http": "github-prod"},
		Host:        h,
		RunID:       "run",
		NodeID:      "node",
	}
	if _, err := Registry()["http.request"].Execute(context.Background(), in); err != nil {
		t.Fatalf("listed host should pass: %v", err)
	}
	if len(h.targets) != 1 {
		t.Fatalf("expected exactly one dialed request, got %v", h.targets)
	}
}

func TestHTTPCredentialAllowedHostsCommaStringForm(t *testing.T) {
	h := &egressRecorderHost{}
	in := flow.Input{
		Config: map[string]any{
			"url":       "https://files.api.slack.com/data",
			"method":    "GET",
			"bodyType":  "json",
			"auth":      "credential",
			"headers":   map[string]any{"Authorization": "Bearer x"},
			"tokenName": "token",
		},
		CredentialData: map[string]flow.Credential{"http": {
			Type: "http",
			Data: map[string]any{"allowedHosts": "api.github.com, *.api.slack.com"},
		}},
		Credentials: map[string]string{"http": "slack"},
		Host:        h,
		RunID:       "run",
		NodeID:      "node",
	}
	if _, err := Registry()["http.request"].Execute(context.Background(), in); err != nil {
		t.Fatalf("comma-string wildcard host should pass: %v", err)
	}
}

func TestHTTPCredentialNoRestrictionImposesNothing(t *testing.T) {
	h := &egressRecorderHost{}
	in := flow.Input{
		Config: map[string]any{
			"url":       "https://anywhere.example.net/x",
			"method":    "GET",
			"bodyType":  "json",
			"auth":      "credential",
			"headers":   map[string]any{"Authorization": "Bearer x"},
			"tokenName": "token",
		},
		CredentialData: map[string]flow.Credential{"http": {
			Type: "http",
			Data: map[string]any{"token": "t"},
		}},
		Credentials: map[string]string{"http": "plain"},
		Host:        h,
		RunID:       "run",
		NodeID:      "node",
	}
	if _, err := Registry()["http.request"].Execute(context.Background(), in); err != nil {
		t.Fatalf("credential without allowedHosts must not restrict: %v", err)
	}
}
