package server_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"loom/internal/engine"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

func newTrustForwardedHostServer(t *testing.T, trust bool) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()),
		TrustForwardedHost: trust,
	}))
	t.Cleanup(srv.Close)
	return srv, st
}

// loginWithForwardedHost posts a login with an explicit Host header so the
// proxy-rewritten case can be reproduced without a real proxy.
func loginWithForwardedHost(t *testing.T, srv *httptest.Server, email, password, host, origin, forwardedHost string) int {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	if forwardedHost != "" {
		req.Header.Set("X-Forwarded-Host", forwardedHost)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// A proxy that rewrites Host leaves the public host only in
// X-Forwarded-Host. With TrustForwardedHost enabled an Origin matching that
// header is same-origin; without it the header is ignored and the login is
// still rejected.
func TestLoginForwardedHostRequiresTrust(t *testing.T) {
	const (
		host     = "loom-internal:5678"
		origin   = "https://app.example.com"
		forward  = "app.example.com"
		password = "password-123456"
	)

	t.Run("trusted matching forwarded host is allowed", func(t *testing.T) {
		srv, st := newTrustForwardedHostServer(t, true)
		addUser(t, st, "trusted@example.com", "viewer", password)
		if code := loginWithForwardedHost(t, srv, "trusted@example.com", password, host, origin, forward); code != http.StatusOK {
			t.Fatalf("trusted forwarded-host login = %d, want 200", code)
		}
	})

	t.Run("untrusted forwarded host is ignored", func(t *testing.T) {
		srv, st := newTrustForwardedHostServer(t, false)
		addUser(t, st, "untrusted@example.com", "viewer", password)
		if code := loginWithForwardedHost(t, srv, "untrusted@example.com", password, host, origin, forward); code != http.StatusForbidden {
			t.Fatalf("untrusted forwarded-host login = %d, want 403", code)
		}
	})

	t.Run("trust does not accept an evil origin", func(t *testing.T) {
		srv, st := newTrustForwardedHostServer(t, true)
		addUser(t, st, "evil@example.com", "viewer", password)
		if code := loginWithForwardedHost(t, srv, "evil@example.com", password, host, "https://evil.example", forward); code != http.StatusForbidden {
			t.Fatalf("evil-origin login = %d, want 403", code)
		}
	})
}
