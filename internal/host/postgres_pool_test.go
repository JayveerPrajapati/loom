package host

import (
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func TestPostgresPoolReusesSameDSN(t *testing.T) {
	l := NewLive()
	first, err := l.postgresDB("host=one.example.com", false)
	if err != nil {
		t.Fatalf("first pool: %v", err)
	}
	second, err := l.postgresDB("host=one.example.com", false)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	if first != second {
		t.Fatal("same DSN returned different pools")
	}
	if len(l.postgresDBs) != 1 || len(l.postgresOrder) != 1 {
		t.Fatalf("pool cache = %#v order = %#v", l.postgresDBs, l.postgresOrder)
	}
}

func TestPostgresPoolSeparatesDistinctDSNs(t *testing.T) {
	l := NewLive()
	first, err := l.postgresDB("host=one.example.com", false)
	if err != nil {
		t.Fatalf("first pool: %v", err)
	}
	second, err := l.postgresDB("host=two.example.com", false)
	if err != nil {
		t.Fatalf("second pool: %v", err)
	}
	if first == second {
		t.Fatal("distinct DSNs unexpectedly shared a pool")
	}
	if len(l.postgresDBs) != 2 {
		t.Fatalf("pool count = %d, want 2", len(l.postgresDBs))
	}
}

// TestPostgresPoolKeysOnGuardSetting is the regression guard for stale pool
// reuse: a Live whose AllowPrivate changes after a pool was created must dial
// under the new setting, never a pool cached under the old one.
func TestPostgresPoolKeysOnGuardSetting(t *testing.T) {
	l := NewLive()
	strict, err := l.postgresDB("host=one.example.com", false)
	if err != nil {
		t.Fatalf("strict pool: %v", err)
	}
	permissive, err := l.postgresDB("host=one.example.com", true)
	if err != nil {
		t.Fatalf("permissive pool: %v", err)
	}
	if strict == permissive {
		t.Fatal("pool cached under strict guard was reused for AllowPrivate")
	}
	if len(l.postgresDBs) != 2 {
		t.Fatalf("pool count = %d, want 2", len(l.postgresDBs))
	}
}

func TestPostgresPoolLRUBoundAndRefresh(t *testing.T) {
	l := NewLive()
	for i := 0; i < maxCachedPostgresDBs; i++ {
		if _, err := l.postgresDB("host=old"+string(rune('a'+i))+".example.com", false); err != nil {
			t.Fatalf("pool %d: %v", i, err)
		}
	}
	if _, err := l.postgresDB("host=old0.example.com", false); err != nil {
		t.Fatalf("refresh LRU: %v", err)
	}
	if _, err := l.postgresDB("host=new.example.com", false); err != nil {
		t.Fatalf("insert pool: %v", err)
	}
	if len(l.postgresDBs) != maxCachedPostgresDBs || len(l.postgresOrder) != maxCachedPostgresDBs {
		t.Fatalf("cache size = %d order = %d, want %d", len(l.postgresDBs), len(l.postgresOrder), maxCachedPostgresDBs)
	}
	if _, exists := l.postgresDBs[postgresPoolKey{dsn: "host=old1.example.com"}]; exists {
		t.Fatal("LRU evicted the wrong DSN; refreshed old0 should survive")
	}
	if _, exists := l.postgresDBs[postgresPoolKey{dsn: "host=new.example.com"}]; !exists {
		t.Fatal("new DSN missing after eviction")
	}
}

func TestPostgresPoolCloseIsIdempotent(t *testing.T) {
	l := NewLive()
	if _, err := l.postgresDB("host=one.example.com", false); err != nil {
		t.Fatalf("pool: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := l.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if len(l.postgresDBs) != 0 || len(l.postgresOrder) != 0 {
		t.Fatalf("cache not cleared: %#v %#v", l.postgresDBs, l.postgresOrder)
	}
}

// TestPostgresDisabledTLSWarning covers the explicit sslmode=disable
// diagnostic: it must fire for both DSN forms, once per pool, never for TLS
// modes, and must not leak credential material from the DSN.
func TestPostgresDisabledTLSWarning(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	l := NewLive()
	if _, err := l.postgresDB("host=plain.example.com port=5432 sslmode=disable password=hunter2", false); err != nil {
		t.Fatalf("kwargs pool: %v", err)
	}
	first := buf.String()
	if !strings.Contains(first, "sslmode=disable") || !strings.Contains(first, "plain.example.com") {
		t.Fatalf("missing disabled-TLS warning for kwargs DSN: %q", first)
	}
	if strings.Contains(first, "hunter2") {
		t.Fatalf("warning leaked credential material: %q", first)
	}

	// Same pool again: the warning must not repeat per query.
	buf.Reset()
	if _, err := l.postgresDB("host=plain.example.com port=5432 sslmode=disable password=hunter2", false); err != nil {
		t.Fatalf("kwargs pool reuse: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("warning repeated on pool reuse: %q", buf.String())
	}

	// URL form.
	buf.Reset()
	if _, err := l.postgresDB("postgres://u:secret@db.example.com:5432/loom?sslmode=disable", false); err != nil {
		t.Fatalf("url pool: %v", err)
	}
	if msg := buf.String(); !strings.Contains(msg, "sslmode=disable") || !strings.Contains(msg, "db.example.com") {
		t.Fatalf("missing disabled-TLS warning for URL DSN: %q", msg)
	}

	// TLS modes must stay silent.
	buf.Reset()
	if _, err := l.postgresDB("host=secure.example.com sslmode=require", false); err != nil {
		t.Fatalf("require pool: %v", err)
	}
	if _, err := l.postgresDB("postgres://u:p@secure2.example.com/loom?sslmode=verify-full", false); err != nil {
		t.Fatalf("verify-full pool: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("warning emitted for TLS-enabled DSN: %q", buf.String())
	}
}

func TestPGDSNHelpers(t *testing.T) {
	for _, dsn := range []string{
		"host=db.example.com sslmode=disable",
		"host=db.example.com sslmode='disable'",
		"postgres://u:p@db.example.com:5432/loom?sslmode=disable",
	} {
		if !pgDSNDisablesTLS(dsn) {
			t.Errorf("pgDSNDisablesTLS(%q) = false, want true", dsn)
		}
		if host := pgDSNHost(dsn); host != "db.example.com" {
			t.Errorf("pgDSNHost(%q) = %q, want db.example.com", dsn, host)
		}
	}
	for _, dsn := range []string{
		"host=db.example.com",
		"host=db.example.com sslmode=require",
		"postgres://u:p@db.example.com/loom?sslmode=verify-full",
		"postgres://u:p@db.example.com/loom",
	} {
		if pgDSNDisablesTLS(dsn) {
			t.Errorf("pgDSNDisablesTLS(%q) = true, want false", dsn)
		}
	}
}
