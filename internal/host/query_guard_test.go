package host

import (
	"context"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

func TestQueryRejectsPrivateTargets(t *testing.T) {
	resolve := func(string) (flow.Credential, bool) {
		return flow.Credential{Type: "postgres", Data: map[string]any{
			"host": "127.0.0.1", "port": float64(1),
			"user": "u", "password": "p", "database": "d",
		}}, true
	}
	l := NewLive(resolve)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := l.Query(ctx, "db", "select 1")
	if err == nil || !strings.Contains(err.Error(), "refusing to call private address") {
		t.Fatalf("Query to loopback err = %v, want a private-address refusal", err)
	}

	l.AllowPrivate = true
	_, err = l.Query(ctx, "db", "select 1")
	if err == nil {
		t.Fatal("AllowPrivate query unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "refusing to call private address") {
		t.Fatalf("AllowPrivate query was still refused: %v", err)
	}
}

func TestQueryRejectsMultipleStatementsBeforeDialing(t *testing.T) {
	l := NewLive()
	ctx := context.Background()

	_, err := l.Query(ctx, "", "SELECT 1; DROP TABLE IF EXISTS audit;")
	if err == nil || !strings.Contains(err.Error(), "multiple SQL statements") {
		t.Fatalf("multi-statement error = %v, want a statement-guard refusal", err)
	}
	if strings.Contains(err.Error(), "missing connection details") {
		t.Fatalf("guard ran after credential resolution: %v", err)
	}

	_, err = l.Query(ctx, "", `SELECT 'a\'; DROP TABLE x; --'`)
	if err == nil || !strings.Contains(err.Error(), "multiple SQL statements") {
		t.Fatalf("plain backslash bypass error = %v, want a statement-guard refusal", err)
	}

	for _, query := range []string{
		"SELECT 1;",
		"SELECT ';'",
		"SELECT $$;$$",
		"-- c\nSELECT 1",
		"/* c */ SELECT 1",
		"SELECT \"a;b\"",
		"SELECT 1; -- done",
	} {
		if _, err := l.Query(ctx, "", query); err == nil || !strings.Contains(err.Error(), "missing connection details") {
			t.Errorf("single statement %q was stopped by the guard: %v", query, err)
		}
	}
}
