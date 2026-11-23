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

func TestBlockedHostEdges(t *testing.T) {
	if !blockedHost("") || !blockedHost("localhost") || !blockedHost("10.0.0.1") {
		t.Fatal("expected private/empty hosts to be blocked")
	}
	if blockedHost("example.com") || blockedHost("8.8.8.8") {
		t.Fatal("public hosts should be allowed")
	}
}

func TestSafeDialerRejectsPrivateAndMalformed(t *testing.T) {
	l := &Live{}
	if _, err := l.dialContext(context.Background(), "tcp", "127.0.0.1:80"); err == nil {
		t.Fatal("loopback dial was allowed")
	}
	if _, err := l.dialContext(context.Background(), "tcp", "no-port"); err == nil {
		t.Fatal("malformed address was allowed")
	}
}

func TestLiveHTTPRejectsUnsafeTargets(t *testing.T) {
	l := NewLive()
	cases := []string{
		"ftp://example.com/x",
		"not a url",
		"http://127.0.0.1/x",
		"http://localhost/x",
	}
	for _, target := range cases {
		if _, err := l.HTTP(context.Background(), flow.HTTPRequest{URL: target}); err == nil {
			t.Errorf("%q was accepted", target)
		}
	}
}

func TestBuildHTTPRequest(t *testing.T) {
	req, err := buildHTTPRequest(context.Background(), flow.HTTPRequest{URL: "https://example.com/x"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if req.Method != http.MethodGet || req.Header.Get("content-type") != "" {
		t.Fatalf("default request = %s ct=%q", req.Method, req.Header.Get("content-type"))
	}

	req, err = buildHTTPRequest(context.Background(), flow.HTTPRequest{
		Method: "post", URL: "https://example.com/x",
		Body:           map[string]any{"a": 1},
		Headers:        map[string]string{"X-Test": "v"},
		IdempotencyKey: "key",
	})
	if err != nil {
		t.Fatalf("build json body: %v", err)
	}
	if req.Method != "post" || req.Header.Get("content-type") != "application/json" ||
		req.Header.Get("idempotency-key") != "key" || req.Header.Get("X-Test") != "v" {
		t.Fatalf("json request headers = %#v", req.Header)
	}

	req, err = buildHTTPRequest(context.Background(), flow.HTTPRequest{
		URL: "https://example.com/x", BodyRaw: []byte("raw"), ContentType: "text/csv",
		Headers: map[string]string{"Idempotency-Key": "explicit"},
	})
	if err != nil {
		t.Fatalf("build raw body: %v", err)
	}
	if req.Header.Get("content-type") != "text/csv" || req.Header.Get("Idempotency-Key") != "explicit" {
		t.Fatalf("raw request headers = %#v", req.Header)
	}
	if got := req.Header.Get("idempotency-key"); got != "explicit" {
		t.Fatalf("idempotency-key = %q, want explicit", got)
	}

	if _, err := buildHTTPRequest(context.Background(), flow.HTTPRequest{URL: "://bad"}); err == nil {
		t.Fatal("invalid URL accepted")
	}
}

func TestDecodeHTTPResponse(t *testing.T) {
	header := http.Header{"X-Multi": {"v"}, "Content-Type": {"application/json"}}

	object := decode(t, &http.Response{StatusCode: 201, Header: header, Body: io.NopCloser(strings.NewReader(`{"id":"x"}`))})
	if object.Status != 201 || object.StatusText != "Created" || object.Body["id"] != "x" || object.Body["status"] != float64(201) {
		t.Fatalf("object = %#v", object)
	}
	if object.Headers["x-multi"] != "v" {
		t.Fatalf("headers = %#v", object.Headers)
	}

	collision := decode(t, &http.Response{StatusCode: 201, Header: header, Body: io.NopCloser(strings.NewReader(`{"status":"BODY-STATUS","ok":"BODY-OK","headers":"BODY-HEADERS"}`))})
	if collision.Body["status"] != "BODY-STATUS" || collision.Body["ok"] != "BODY-OK" || collision.Body["headers"] != "BODY-HEADERS" {
		t.Fatalf("body keys were clobbered by the envelope: %#v", collision.Body)
	}

	array := decode(t, &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(`[1,2]`))})
	items, ok := array.Body["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("array = %#v", array.Body)
	}

	text := decode(t, &http.Response{StatusCode: 204, Body: io.NopCloser(strings.NewReader("hello"))})
	if text.Body["body"] != "hello" {
		t.Fatalf("text = %#v", text.Body)
	}

	empty := decode(t, &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))})
	if empty.Body["body"] != "" {
		t.Fatalf("empty = %#v", empty.Body)
	}
}

func decode(t *testing.T, resp *http.Response) flow.HTTPResponse {
	t.Helper()
	out, err := decodeHTTPResponse(resp)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return out
}

func TestSleepAndNow(t *testing.T) {
	l := NewLive()
	if err := l.Sleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Sleep(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled sleep = %v", err)
	}
	if l.Now().IsZero() {
		t.Fatal("Now returned zero")
	}
}

func TestLiveQueryValidation(t *testing.T) {
	l := NewLive()
	if _, err := l.Query(context.Background(), "", "select 1"); err == nil {
		t.Fatal("empty connection details accepted")
	}

	l = NewLive(func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{
			"host": "127.0.0.1", "port": float64(1), "user": "u", "password": "p",
			"database": "d", "sslmode": "disable",
		}}, true
	})
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if _, err := l.Query(ctx, "cred", "select 1"); err == nil {
		t.Fatal("unreachable postgres query succeeded")
	}

	l = NewLive(func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{"url": "postgres://127.0.0.1:1/d?sslmode=disable"}}, true
	})
	if _, err := l.Query(ctx, "cred", "select 1"); err == nil {
		t.Fatal("unreachable url query succeeded")
	}
}

func TestLiveSlackValidation(t *testing.T) {
	l := NewLive()
	if err := l.Slack(context.Background(), "unknown", "", "hi"); err == nil {
		t.Fatal("slack without configuration succeeded")
	}
	if err := l.Slack(context.Background(), "http://127.0.0.1/hook", "", "hi"); err == nil {
		t.Fatal("slack webhook to a private address succeeded")
	}

	l = NewLive(func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{"webhookUrl": "http://127.0.0.1/hook"}}, true
	})
	if err := l.Slack(context.Background(), "cred", "", "hi"); err == nil {
		t.Fatal("slack webhook from credential to a private address succeeded")
	}

	l = NewLive(func(string) (flow.Credential, bool) {
		return flow.Credential{Raw: "http://127.0.0.1/hook"}, true
	})
	if err := l.Slack(context.Background(), "cred", "", "hi"); err == nil {
		t.Fatal("slack raw webhook to a private address succeeded")
	}
}

func TestSimHost(t *testing.T) {
	s := NewSim()
	if s.Now().IsZero() {
		t.Fatal("Sim.Now returned zero")
	}
	if err := s.Slack(context.Background(), "", "", ""); err != nil {
		t.Fatalf("Sim.Slack: %v", err)
	}
	if err := s.Sleep(context.Background(), time.Hour); err != nil {
		t.Fatalf("Sim.Sleep: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Sleep(ctx, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("Sim.Sleep cancelled = %v", err)
	}

	rows, err := s.Query(context.Background(), "", "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("Sim.Query = %d rows, %v", len(rows), err)
	}

	for i := 1; i <= 3; i++ {
		if _, err := s.HTTP(context.Background(), flow.HTTPRequest{URL: "https://sim/flaky"}); err == nil {
			t.Fatalf("flaky call %d succeeded early", i)
		}
	}
	resp, err := s.HTTP(context.Background(), flow.HTTPRequest{URL: "https://sim/flaky"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("flaky recovery = %#v, %v", resp, err)
	}

	resp, err = s.HTTP(context.Background(), flow.HTTPRequest{URL: "https://sim/orders/1"})
	if err != nil || resp.Body["id"] != "A-1042" {
		t.Fatalf("orders = %#v, %v", resp.Body, err)
	}
	resp, err = s.HTTP(context.Background(), flow.HTTPRequest{URL: "https://sim/unknown"})
	if err != nil || resp.Body["ok"] != true {
		t.Fatalf("default = %#v, %v", resp.Body, err)
	}
}
