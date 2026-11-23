package server_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loom/internal/engine"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

// TestAuthEventIPBehindTrustedProxy pins the audit-event IP derivation: a
// login behind a trusted proxy records the forwarded client hop, while the
// same login without the header records the direct address.
func TestAuthEventIPBehindTrustedProxy(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	_, loopback, err := net.ParseCIDR("127.0.0.0/8")
	if err != nil {
		t.Fatalf("parse cidr: %v", err)
	}
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version:           "test",
		Store:             st,
		Engine:            engine.New(st, host.NewSim()),
		TrustedProxyCIDRs: []*net.IPNet{loopback},
	}))
	t.Cleanup(srv.Close)

	addUser(t, st, "root@example.com", "admin", "root-password-1")
	addUser(t, st, "victim@example.com", "viewer", "viewer-password-1")
	admin := loginAs(t, srv, "root@example.com", "root-password-1")

	failedLogin := func(forwarded string) {
		t.Helper()
		raw, err := json.Marshal(map[string]string{"email": "victim@example.com", "password": "wrong"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if forwarded != "" {
			req.Header.Set("X-Forwarded-For", forwarded)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("login status = %d, want 401", resp.StatusCode)
		}
	}

	failedLogin("198.51.100.7, 203.0.113.9")
	failedLogin("")

	status, data := admin.do(t, srv, http.MethodGet, "/api/auth/events", nil, false)
	if status != http.StatusOK {
		t.Fatalf("auth events: %d %s", status, data)
	}
	var events struct {
		Events []struct {
			Type string `json:"type"`
			IP   string `json:"ip"`
		} `json:"events"`
	}
	if err := json.Unmarshal(data, &events); err != nil {
		t.Fatalf("decode events: %v: %s", err, data)
	}
	var forwardedIP, directIP bool
	for _, ev := range events.Events {
		if ev.Type != "login_failed" {
			continue
		}
		switch ev.IP {
		case "203.0.113.9":
			forwardedIP = true
		case "127.0.0.1":
			directIP = true
		}
	}
	if !forwardedIP || !directIP {
		t.Fatalf("login_failed events missing expected IPs (forwarded=%v direct=%v): %s", forwardedIP, directIP, strings.TrimSpace(string(data)))
	}
}
