package host

import (
	"net/url"
	"testing"

	"loom/internal/flow"
)

func TestPostgresCredentialDSNRequiresTLSByDefault(t *testing.T) {
	resolve := func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{
			"host":     "db.example.com",
			"port":     float64(5432),
			"user":     "loom",
			"password": "secret",
			"database": "loom",
		}}, true
	}
	dsn := postgresCredentialDSN("legacy", resolve)
	parsed, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if got := parsed.Query().Get("sslmode"); got != "require" {
		t.Fatalf("sslmode = %q, want require (dsn %s)", got, dsn)
	}
	if parsed.Hostname() != "db.example.com" || parsed.Path != "/loom" {
		t.Fatalf("unexpected host/database: %s", dsn)
	}
}

func TestPostgresCredentialDSNPreservesExplicitTLSModes(t *testing.T) {
	for _, mode := range []string{"disable", "require", "verify-full"} {
		resolve := func(string) (flow.Credential, bool) {
			return flow.Credential{Data: map[string]any{
				"host": "db.example.com", "database": "loom", "sslmode": mode,
			}}, true
		}
		parsed, err := url.Parse(postgresCredentialDSN("legacy", resolve))
		if err != nil {
			t.Fatalf("%s: parse dsn: %v", mode, err)
		}
		if got := parsed.Query().Get("sslmode"); got != mode {
			t.Fatalf("sslmode = %q, want %q", got, mode)
		}
	}
}

func TestPostgresCredentialDSNPrefersCompleteConnectionStrings(t *testing.T) {
	const complete = "postgres://user:pass@db.example.com/loom?sslmode=verify-full"
	resolve := func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{
			"url": complete, "host": "ignored.example.com",
		}}, true
	}
	if got := postgresCredentialDSN("legacy", resolve); got != complete {
		t.Fatalf("url credential = %q, want %q", got, complete)
	}

	raw := flow.Credential{Raw: complete}
	resolve = func(string) (flow.Credential, bool) { return raw, true }
	if got := postgresCredentialDSN("legacy", resolve); got != complete {
		t.Fatalf("raw credential = %q, want %q", got, complete)
	}
}
