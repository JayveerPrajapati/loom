package server_test

import (
	"fmt"
	"net/http"
	"testing"
)

// The per-account limiter keys on the caller-chosen email, so rotating emails
// must still hit a coarse per-source budget before any argon2 work.
func TestLoginIPBudgetStopsEmailRotation(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@x.io", "admin", "password-123456")

	const ipBudget = 60
	for i := 0; i < ipBudget; i++ {
		status, body := postLogin(t, srv, fmt.Sprintf("ghost%d@evil.test", i), "wrong")
		if status != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401: %s", i, status, body)
		}
	}
	status, body := postLogin(t, srv, "one-more@evil.test", "wrong")
	if status != http.StatusTooManyRequests {
		t.Fatalf("after %d failures from one IP, status = %d, want 429: %s", ipBudget, status, body)
	}
}
