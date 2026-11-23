package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"loom/internal/auth"
)

func TestParseCutoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		in   string
		want time.Time
	}{
		{"30d", now.AddDate(0, 0, -30)},
		{"24h", now.Add(-24 * time.Hour)},
		{"2026-10-01T00:00:00Z", time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)},
	} {
		got, err := parseCutoff(test.in, now)
		if err != nil {
			t.Fatalf("parse %q: %v", test.in, err)
		}
		if !got.Equal(test.want) {
			t.Fatalf("parse %q = %s, want %s", test.in, got, test.want)
		}
	}
	for _, invalid := range []string{"", "-1d", "soon"} {
		if _, err := parseCutoff(invalid, now); err == nil {
			t.Fatalf("parse %q: expected error", invalid)
		}
	}
}

// withStdin replaces os.Stdin with a pipe carrying input for the duration of
// the test (readSecret reads os.Stdin when --password-stdin is set).
func withStdin(t *testing.T, input string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	if _, err := io.WriteString(w, input); err != nil {
		t.Fatalf("write stdin: %v", err)
	}
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() {
		os.Stdin = old
		r.Close()
	})
}

func TestUserPasswdUsageHint(t *testing.T) {
	err := runUser([]string{"passwd"})
	if err == nil {
		t.Fatal("passwd without arguments: expected an error")
	}
	// Go's flag package stops at the first positional, so the taught order
	// must put flags BEFORE the email; the old hint (email first) made the
	// trailing --password-stdin unparsable.
	if !strings.Contains(err.Error(), "usage: loom user passwd --password-stdin <email>") {
		t.Fatalf("usage hint = %q", err.Error())
	}
}

func TestUserPasswdEndToEnd(t *testing.T) {
	dir := t.TempDir()

	withStdin(t, "first-password-1")
	if err := runUser([]string{"create", "-data", dir, "--email", "cli@example.com", "--name", "CLI", "--role", "viewer", "--password-stdin"}); err != nil {
		t.Fatalf("create: %v", err)
	}

	withStdin(t, "rotated-password-2")
	if err := runUser([]string{"passwd", "-data", dir, "--password-stdin", "cli@example.com"}); err != nil {
		t.Fatalf("passwd: %v", err)
	}

	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	user, err := st.GetUserByEmail("cli@example.com")
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if auth.VerifyPassword(user.PasswordHash, "first-password-1") {
		t.Fatal("old password still verifies after rotation")
	}
	if !auth.VerifyPassword(user.PasswordHash, "rotated-password-2") {
		t.Fatal("new password does not verify")
	}
}
