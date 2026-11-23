package host

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loom/internal/egress"
	"loom/internal/flow"
)

func TestLiveEgressDenyBlocksUnlistedTarget(t *testing.T) {
	// httptest binds loopback; opt into private targets so the assertion
	// exercises the egress layer rather than the SSRF guard.
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	l := NewLive()
	l.AllowPrivate = true
	l.Egress = &egress.Policy{Mode: egress.ModeDeny}
	_, err := l.HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "not in the global allowlist") {
		t.Fatalf("want global-allowlist denial, got %v", err)
	}
}

func TestLiveEgressDenyAllowsListedTarget(t *testing.T) {
	// httptest binds loopback; opt into private targets so the assertion
	// exercises the egress layer rather than the SSRF guard.
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	dest, err := egress.ParseDestination(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	l := NewLive()
	l.AllowPrivate = true
	l.Egress = &egress.Policy{Mode: egress.ModeDeny, Allowlist: []string{dest}}
	if _, err := l.HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: server.URL}); err != nil {
		t.Fatalf("listed destination should pass: %v", err)
	}
}

func TestLiveEgressAllowAllByDefault(t *testing.T) {
	// httptest binds loopback; opt into private targets so the assertion
	// exercises the egress layer rather than the SSRF guard.
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	l := NewLive()
	l.AllowPrivate = true
	if l.Egress != nil {
		t.Fatal("NewLive must default to no egress policy")
	}
	if _, err := l.HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: server.URL}); err != nil {
		t.Fatalf("no policy should allow: %v", err)
	}
}

func TestLiveEgressDenyBlocksOAuthTokenURL(t *testing.T) {
	// httptest binds loopback; opt into private targets so the assertion
	// exercises the egress layer rather than the SSRF guard.
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	l := NewLive()
	// The request target itself is allowlisted, but the OAuth2 token
	// fetch target is not — the token hop must be denied too.
	l.AllowPrivate = true
	// The request target is allowlisted; the token URL is not — the token
	// hop must be the one denied.
	l.Egress = &egress.Policy{Mode: egress.ModeDeny, Allowlist: []string{"https://api.example.com"}}
	_, err := l.HTTP(context.Background(), flow.HTTPRequest{
		Method: http.MethodGet,
		URL:    "https://api.example.com", // not reachable; policy fires first
		OAuth2: &flow.OAuth2Params{TokenURL: server.URL, ClientID: "x", ClientSecret: "y"},
	})
	if err == nil || !strings.Contains(err.Error(), "oauth2 token fetch") {
		t.Fatalf("want oauth2 token denial, got %v", err)
	}
}

func TestLiveEgressIndependentOfAllowPrivate(t *testing.T) {
	// AllowPrivate disables the SSRF guard but must NOT bypass egress
	// rules (D7-4).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	l := NewLive()
	l.AllowPrivate = true
	l.Egress = &egress.Policy{Mode: egress.ModeDeny}
	_, err := l.HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: server.URL})
	if err == nil || !strings.Contains(err.Error(), "not in the global allowlist") {
		t.Fatalf("egress must survive allow-private, got %v", err)
	}
}

func TestLiveEgressDenyBlocksPrivateProxy(t *testing.T) {
	// httptest binds loopback; opt into private targets so the assertion
	// exercises the egress layer rather than the SSRF guard.
	t.Helper()
	l := NewLive()
	l.AllowPrivate = true
	// The request target is allowlisted; the proxy is not.
	l.Egress = &egress.Policy{Mode: egress.ModeDeny, Allowlist: []string{"https://api.example.com"}}
	_, err := l.HTTP(context.Background(), flow.HTTPRequest{
		Method: http.MethodGet,
		URL:    "https://api.example.com",
		Proxy:  "http://127.0.0.1:3128",
	})
	if err == nil || !strings.Contains(err.Error(), "proxy") {
		t.Fatalf("want proxy denial, got %v", err)
	}
}

func TestLiveEgressDenyBlocksPostgresDestination(t *testing.T) {
	l := NewLive()
	l.Egress = &egress.Policy{Mode: egress.ModeDeny}
	_, err := l.Query(context.Background(), "pg", "SELECT 1")
	// Credential "pg" does not resolve; the DSN fallback is the literal
	// "pg" (no host), which yields no destination — the egress layer
	// must not crash, and the query fails on its own terms.
	if err == nil {
		t.Fatal("unresolvable credential should fail")
	}
	// Now a resolvable credential whose host is not allowlisted.
	l.ResolveCredential = func(id string) (flow.Credential, bool) {
		return flow.Credential{Type: "postgres", Data: map[string]any{
			"host": "db.internal", "user": "u", "password": "p", "database": "d",
		}}, true
	}
	_, err = l.Query(context.Background(), "pg", "SELECT 1")
	if err == nil || !strings.Contains(err.Error(), "not in the global allowlist") {
		t.Fatalf("want postgres egress denial, got %v", err)
	}
	// Allowlisted host passes the egress layer (then fails at dial, which
	// proves the policy check let it through).
	l.Egress = &egress.Policy{Mode: egress.ModeDeny, Allowlist: []string{"postgres://db.internal:5432"}}
	_, err = l.Query(context.Background(), "pg", "SELECT 1")
	if err == nil || strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("allowlisted postgres should proceed past egress (dial failure expected), got %v", err)
	}
}

func TestLiveEgressKeywordDSNHostPort(t *testing.T) {
	l := NewLive()
	l.ResolveCredential = func(id string) (flow.Credential, bool) {
		return flow.Credential{Type: "postgres", Data: map[string]any{
			"dsn": "host=kw.internal port=6543 user=u password=p dbname=d",
		}}, true
	}
	l.Egress = &egress.Policy{Mode: egress.ModeDeny, Allowlist: []string{"postgres://kw.internal:6543"}}
	_, err := l.Query(context.Background(), "pg", "SELECT 1")
	if err == nil || strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("keyword-DSN destination should match the allowlist (dial failure expected), got %v", err)
	}
}
