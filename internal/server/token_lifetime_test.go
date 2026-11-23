package server_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

func newTokenLifetimeServer(t *testing.T, max time.Duration) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()),
		MaxTokenLifetime: max,
	}))
	t.Cleanup(srv.Close)
	addUser(t, st, "root@example.com", "admin", "root-password-1")
	return srv
}

func createTokenRequest(t *testing.T, srv *httptest.Server, session testSession, expiresAt string) (int, []byte) {
	t.Helper()
	body := map[string]string{"name": "t"}
	if expiresAt != "" {
		body["expiresAt"] = expiresAt
	}
	return session.do(t, srv, http.MethodPost, "/api/tokens", body, true)
}

// TestTokenMaxLifetime pins the API-token lifetime policy: an unqualified
// token gets exactly the maximum lifetime, longer and past expiries are
// rejected, and a zero maximum disables the cap.
func TestTokenMaxLifetime(t *testing.T) {
	const max = 30 * 24 * time.Hour
	srv := newTokenLifetimeServer(t, max)
	session := loginAs(t, srv, "root@example.com", "root-password-1")

	status, data := createTokenRequest(t, srv, session, "")
	if status != http.StatusCreated {
		t.Fatalf("default expiry: status = %d, body %s", status, data)
	}
	var created struct {
		APIToken struct {
			ExpiresAt *time.Time `json:"expiresAt"`
		} `json:"apiToken"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode: %v: %s", err, data)
	}
	if created.APIToken.ExpiresAt == nil {
		t.Fatalf("token without explicit expiry must inherit the maximum lifetime: %s", data)
	}
	off := time.Until(*created.APIToken.ExpiresAt) - max
	if off < -time.Minute || off > time.Minute {
		t.Fatalf("defaulted expiry off by %s (want ~%s from now)", off, max)
	}

	// Explicit expiry within the cap is honored verbatim.
	within := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	status, _ = createTokenRequest(t, srv, session, within)
	if status != http.StatusCreated {
		t.Fatalf("in-cap expiry: status = %d", status)
	}

	// Explicit expiry beyond the cap is rejected.
	beyond := time.Now().Add(max + 24*time.Hour).UTC().Format(time.RFC3339)
	status, data = createTokenRequest(t, srv, session, beyond)
	if status != http.StatusBadRequest {
		t.Fatalf("over-cap expiry: status = %d, body %s", status, data)
	}

	// A past expiry is rejected regardless of the cap.
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	status, _ = createTokenRequest(t, srv, session, past)
	if status != http.StatusBadRequest {
		t.Fatalf("past expiry: status = %d", status)
	}
}

// TestTokenLifetimeUncapped covers the explicit operator opt-out: a zero
// maximum allows non-expiring tokens and arbitrarily long expiries.
func TestTokenLifetimeUncapped(t *testing.T) {
	srv := newTokenLifetimeServer(t, 0)
	session := loginAs(t, srv, "root@example.com", "root-password-1")

	status, data := createTokenRequest(t, srv, session, "")
	if status != http.StatusCreated {
		t.Fatalf("uncapped default: status = %d, body %s", status, data)
	}
	var created struct {
		APIToken struct {
			ExpiresAt *time.Time `json:"expiresAt"`
		} `json:"apiToken"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatalf("decode: %v: %s", err, data)
	}
	if created.APIToken.ExpiresAt != nil {
		t.Fatalf("uncapped token without expiry must not expire: %s", data)
	}

	far := time.Now().Add(50 * 365 * 24 * time.Hour).UTC().Format(time.RFC3339)
	status, _ = createTokenRequest(t, srv, session, far)
	if status != http.StatusCreated {
		t.Fatalf("uncapped far expiry: status = %d", status)
	}

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	status, _ = createTokenRequest(t, srv, session, past)
	if status != http.StatusBadRequest {
		t.Fatalf("past expiry must still be rejected when uncapped: status = %d", status)
	}
}
