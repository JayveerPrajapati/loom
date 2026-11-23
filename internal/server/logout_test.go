package server_test

import (
	"net/http"
	"testing"
)

func TestLogoutRequiresCSRF(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@x.io", "admin", "password-123456")
	session := loginAs(t, srv, "admin@x.io", "password-123456")

	code, body := session.do(t, srv, http.MethodPost, "/api/auth/logout", nil, false)
	if code != http.StatusForbidden {
		t.Fatalf("logout without CSRF = %d, want 403: %s", code, body)
	}

	code, body = session.do(t, srv, http.MethodPost, "/api/auth/logout", nil, true)
	if code != http.StatusNoContent && code != http.StatusOK {
		t.Fatalf("logout with CSRF = %d: %s", code, body)
	}

	code, _ = session.do(t, srv, http.MethodGet, "/api/auth/me", nil, false)
	if code != http.StatusUnauthorized {
		t.Fatalf("session survived logout: /api/auth/me = %d", code)
	}
}
