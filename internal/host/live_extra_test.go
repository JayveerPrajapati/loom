package host

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fakeLive(fn roundTripFunc) *Live {
	l := NewLive()
	l.Client = &http.Client{Transport: fn}
	return l
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": {"application/json"}, "X-Test": {"v"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestLiveHTTPSuccessThroughTransport(t *testing.T) {
	var got *http.Request
	var gotBody string
	l := fakeLive(func(r *http.Request) (*http.Response, error) {
		got = r
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		return jsonResponse(201, `{"id":"x"}`), nil
	})

	resp, err := l.HTTP(context.Background(), flow.HTTPRequest{
		Method: "POST", URL: "https://example.com/api",
		Body:           map[string]any{"a": 1},
		Headers:        map[string]string{"X-Custom": "v"},
		Timeout:        time.Second,
		IdempotencyKey: "key",
	})
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	if resp.Status != 201 || resp.Body["id"] != "x" || resp.Headers["x-test"] != "v" {
		t.Fatalf("response = %#v", resp)
	}
	if got.Method != "POST" || got.URL.String() != "https://example.com/api" {
		t.Fatalf("request = %s %s", got.Method, got.URL)
	}
	if got.Header.Get("content-type") != "application/json" ||
		got.Header.Get("idempotency-key") != "key" || got.Header.Get("X-Custom") != "v" {
		t.Fatalf("headers = %#v", got.Header)
	}
	if gotBody != `{"a":1}` {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestLiveHTTPRawBodyAndTransportError(t *testing.T) {
	var gotBody, gotType string
	l := fakeLive(func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		gotType = r.Header.Get("content-type")
		return jsonResponse(200, `not-json`), nil
	})
	resp, err := l.HTTP(context.Background(), flow.HTTPRequest{
		URL: "https://example.com/raw", BodyRaw: []byte("a,b"), ContentType: "text/csv",
	})
	if err != nil {
		t.Fatalf("http: %v", err)
	}
	if gotBody != "a,b" || gotType != "text/csv" {
		t.Fatalf("raw request body=%q type=%q", gotBody, gotType)
	}
	if resp.Body["body"] != "not-json" {
		t.Fatalf("text response = %#v", resp.Body)
	}

	l = fakeLive(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("transport down")
	})
	if _, err := l.HTTP(context.Background(), flow.HTTPRequest{URL: "https://example.com/x"}); err == nil {
		t.Fatal("transport error was swallowed")
	}
}

func TestLiveSlackPaths(t *testing.T) {
	ok := fakeLive(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"ok":true}`), nil
	})
	webhookCred := func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{"webhookUrl": "https://example.com/hook"}}, true
	}
	ok.ResolveCredential = webhookCred
	if err := ok.Slack(context.Background(), "cred", "#ops", "hi"); err != nil {
		t.Fatalf("webhook slack: %v", err)
	}

	failing := fakeLive(func(*http.Request) (*http.Response, error) {
		return jsonResponse(500, `{"error":"boom"}`), nil
	})
	failing.ResolveCredential = webhookCred
	if err := failing.Slack(context.Background(), "cred", "#ops", "hi"); err == nil {
		t.Fatal("webhook 500 was accepted")
	}

	tokenCred := func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{"token": "xoxb-1"}}, true
	}
	ok.ResolveCredential = tokenCred
	if err := ok.Slack(context.Background(), "cred", "#ops", "hi"); err != nil {
		t.Fatalf("token slack: %v", err)
	}

	rejected := fakeLive(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"ok":false,"error":"channel_not_found"}`), nil
	})
	rejected.ResolveCredential = tokenCred
	if err := rejected.Slack(context.Background(), "cred", "#ops", "hi"); err == nil {
		t.Fatal("slack ok=false was accepted")
	}

	unauthorized := fakeLive(func(*http.Request) (*http.Response, error) {
		return jsonResponse(401, `{}`), nil
	})
	unauthorized.ResolveCredential = tokenCred
	if err := unauthorized.Slack(context.Background(), "cred", "#ops", "hi"); err == nil {
		t.Fatal("slack 401 was accepted")
	}

	rawWebhook := fakeLive(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `ok`), nil
	})
	rawWebhook.ResolveCredential = func(string) (flow.Credential, bool) {
		return flow.Credential{Raw: "https://example.com/raw-hook"}, true
	}
	if err := rawWebhook.Slack(context.Background(), "cred", "", "hi"); err != nil {
		t.Fatalf("raw webhook slack: %v", err)
	}

	// No resolver: a URL-shaped credential id is treated as a webhook and a
	// xox-prefixed id as a bot token.
	rawIDs := fakeLive(func(*http.Request) (*http.Response, error) {
		return jsonResponse(200, `{"ok":true}`), nil
	})
	if err := rawIDs.Slack(context.Background(), "https://example.com/hook", "", "hi"); err != nil {
		t.Fatalf("id webhook slack: %v", err)
	}
	if err := rawIDs.Slack(context.Background(), "xoxb-2", "", "hi"); err != nil {
		t.Fatalf("id token slack: %v", err)
	}

	noCred := NewLive()
	if err := noCred.Slack(context.Background(), "cred", "", "hi"); err == nil {
		t.Fatal("slack without configuration was accepted")
	}
}
