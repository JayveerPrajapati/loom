package server_test

import (
	"net/http"
	"testing"

	"loom/internal/auth"
	"loom/internal/store"
)

// A password reset must invalidate API tokens, otherwise a stolen bearer token
// survives the incident response.
func TestPasswordResetRevokesAPITokens(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@x.io", "admin", "password-123456")
	victim := addUser(t, st, "victim@x.io", "viewer", "password-123456")

	raw, err := auth.NewToken()
	if err != nil {
		t.Fatalf("new token: %v", err)
	}
	if _, err := st.CreateAPIToken(store.APIToken{
		UserID: victim.ID, Name: "stolen", Role: "viewer", TokenHash: auth.HashToken(raw),
	}); err != nil {
		t.Fatalf("create token: %v", err)
	}

	before, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/me", nil)
	before.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(before)
	if err != nil {
		t.Fatalf("pre-reset me: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pre-reset token status = %d, want 200", resp.StatusCode)
	}

	admin := loginAs(t, srv, "admin@x.io", "password-123456")
	code, body := admin.do(t, srv, http.MethodPost, "/api/users/"+victim.ID+"/password",
		map[string]string{"password": "new-password-123"}, true)
	if code != http.StatusOK {
		t.Fatalf("reset status = %d: %s", code, body)
	}

	after, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/auth/me", nil)
	after.Header.Set("Authorization", "Bearer "+raw)
	resp, err = http.DefaultClient.Do(after)
	if err != nil {
		t.Fatalf("post-reset me: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("token still valid after password reset: status = %d", resp.StatusCode)
	}
}
