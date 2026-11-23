package host

import (
	"context"
	"os"
	"testing"
	"time"

	"loom/internal/flow"
)

// TestLiveQueryPostgres runs against a real server. CI provides one via the
// postgres service container; locally it skips unless the DSN is set.
func TestLiveQueryPostgres(t *testing.T) {
	dsn := os.Getenv("LOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LOOM_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	l := NewLive()
	rows, err := l.Query(ctx, dsn, "select 1 as n, 'x'::text as s")
	if err != nil {
		t.Fatalf("query by dsn: %v", err)
	}
	if len(rows) != 1 || rows[0]["n"] == nil || rows[0]["s"] != "x" {
		t.Fatalf("rows = %#v", rows)
	}

	l = NewLive(func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{"url": dsn}}, true
	})
	rows, err = l.Query(ctx, "cred", "select 2 as n")
	if err != nil || len(rows) != 1 {
		t.Fatalf("query by url credential = %#v, %v", rows, err)
	}

	l = NewLive(func(string) (flow.Credential, bool) {
		return flow.Credential{Data: map[string]any{
			"host": "localhost", "port": float64(5432),
			"user": "loom", "password": "loom", "database": "loom",
			"sslmode": "disable",
		}}, true
	})
	rows, err = l.Query(ctx, "cred", "select 3 as n")
	if err != nil || len(rows) != 1 {
		t.Fatalf("query by host fields = %#v, %v", rows, err)
	}
}

func BenchmarkLiveQueryPostgresFresh(b *testing.B) {
	dsn := os.Getenv("LOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		b.Skip("LOOM_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		l := NewLive()
		if _, err := l.Query(ctx, dsn, "select 1"); err != nil {
			b.Fatalf("fresh query: %v", err)
		}
		if err := l.Close(); err != nil {
			b.Fatalf("fresh close: %v", err)
		}
	}
}

func BenchmarkLiveQueryPostgresCached(b *testing.B) {
	dsn := os.Getenv("LOOM_TEST_POSTGRES_DSN")
	if dsn == "" {
		b.Skip("LOOM_TEST_POSTGRES_DSN not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	l := NewLive()
	defer l.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Query(ctx, dsn, "select 1"); err != nil {
			b.Fatalf("cached query: %v", err)
		}
	}
}
