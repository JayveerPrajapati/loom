package auth_test

import (
	"strings"
	"testing"

	"loom/internal/auth"
)

func TestRoleOrdering(t *testing.T) {
	if !auth.RoleAdmin.Allows(auth.RoleEditor) || !auth.RoleEditor.Allows(auth.RoleOperator) {
		t.Fatal("role hierarchy is not ordered")
	}
	if auth.RoleViewer.Allows(auth.RoleOperator) {
		t.Fatal("viewer must not satisfy operator")
	}
	if auth.Role("bogus").Rank() != 0 || auth.Role("bogus").Allows(auth.RoleViewer) {
		t.Fatal("unknown role must not grant access")
	}
	for _, name := range []string{"viewer", "operator", "editor", "admin", " ADMIN "} {
		if _, ok := auth.ParseRole(name); !ok {
			t.Fatalf("ParseRole(%q) failed", name)
		}
	}
	if _, ok := auth.ParseRole("owner"); ok {
		t.Fatal("accepted an unknown role")
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	encoded, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "argon2id$") {
		t.Fatalf("unexpected encoding %q", encoded)
	}
	if !auth.VerifyPassword(encoded, "correct horse battery staple") {
		t.Fatal("correct password rejected")
	}
	if auth.VerifyPassword(encoded, "wrong password") {
		t.Fatal("wrong password accepted")
	}
	if auth.VerifyPassword("not-a-hash", "x") {
		t.Fatal("malformed hash accepted")
	}
	if _, err := auth.HashPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
}

func TestTokensAreUniqueAndHashed(t *testing.T) {
	a, err := auth.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	b, err := auth.NewToken()
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	if a == b || len(a) < 40 {
		t.Fatalf("tokens = %q, %q", a, b)
	}
	if auth.HashToken(a) == auth.HashToken(b) {
		t.Fatal("distinct tokens collided")
	}
	if auth.HashToken(a) != auth.HashToken(a) {
		t.Fatal("hash is not deterministic")
	}
	if !auth.Equal(a, a) || auth.Equal(a, b) {
		t.Fatal("constant-time equality is wrong")
	}
}

func TestPrincipalActor(t *testing.T) {
	cases := map[auth.Principal]string{
		{Type: "user", ID: "u1", Email: "ada@example.com"}:  "ada@example.com",
		{Type: "token", ID: "t1", Email: "ada@example.com"}: "ada@example.com",
		{Type: "user", ID: "u2"}:                            "user:u2",
		{Type: "token", ID: "t2"}:                           "token:t2",
		{Type: "service"}:                                   "service:loom-token",
		{Type: "anonymous"}:                                 "anonymous",
	}
	for principal, want := range cases {
		if got := principal.Actor(); got != want {
			t.Fatalf("Actor(%+v) = %q, want %q", principal, got, want)
		}
	}
}
