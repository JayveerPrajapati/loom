package store_test

import (
	"testing"
	"time"

	"loom/internal/auth"
	"loom/internal/store"
)

func openAuthStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, dir
}

func TestUserCRUDAndPassword(t *testing.T) {
	st, _ := openAuthStore(t)
	hash, _ := auth.HashPassword("password-123")
	user, err := st.CreateUser(store.User{
		Email: "Admin@Example.com", Name: "Admin", Role: "admin", PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if user.ID == "" || user.Email != "admin@example.com" {
		t.Fatalf("user = %#v", user)
	}
	if _, err := st.CreateUser(store.User{
		Email: "admin@example.com", Role: "viewer", PasswordHash: hash,
	}); err == nil {
		t.Fatal("duplicate email accepted")
	}
	if _, err := st.CreateUser(store.User{Email: "bad", Role: "viewer", PasswordHash: hash}); err == nil {
		t.Fatal("invalid email accepted")
	}
	if count, _ := st.CountUsers(); count != 1 {
		t.Fatalf("count = %d", count)
	}
	if admins, _ := st.CountAdmins(); admins != 1 {
		t.Fatalf("admins = %d", admins)
	}
	if err := st.UpdateUserRole(user.ID, "editor"); err != nil {
		t.Fatalf("role: %v", err)
	}
	updated, _ := st.GetUser(user.ID)
	if updated.Role != "editor" {
		t.Fatalf("role = %q", updated.Role)
	}
	if admins, _ := st.CountAdmins(); admins != 0 {
		t.Fatalf("admins after demote = %d", admins)
	}
	if err := st.SetUserDisabled(user.ID, true); err != nil {
		t.Fatalf("disable: %v", err)
	}
	updated, _ = st.GetUser(user.ID)
	if !updated.Disabled {
		t.Fatal("user not disabled")
	}
	newHash, _ := auth.HashPassword("new-password-1")
	if err := st.SetUserPassword(user.ID, newHash); err != nil {
		t.Fatalf("password: %v", err)
	}
	updated, _ = st.GetUser(user.ID)
	if !auth.VerifyPassword(updated.PasswordHash, "new-password-1") {
		t.Fatal("new password does not verify")
	}
	if _, err := st.GetUserByEmail("missing@example.com"); err == nil {
		t.Fatal("missing user returned")
	}
}

func TestSessionLifecycle(t *testing.T) {
	st, _ := openAuthStore(t)
	hash, _ := auth.HashPassword("password-123")
	user, _ := st.CreateUser(store.User{Email: "u@example.com", Role: "viewer", PasswordHash: hash})

	now := time.Now()
	raw := "session-token"
	if err := st.CreateSession(store.Session{
		TokenHash: auth.HashToken(raw), UserID: user.ID,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		IP: "127.0.0.1", UserAgent: "test",
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	session, gotUser, err := st.GetSessionByHash(auth.HashToken(raw))
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if session.UserID != user.ID || gotUser.Email != "u@example.com" {
		t.Fatalf("session = %#v user = %#v", session, gotUser)
	}
	if err := st.TouchSession(auth.HashToken(raw), now.Add(2*time.Minute)); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := st.DeleteSession(auth.HashToken(raw)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := st.GetSessionByHash(auth.HashToken(raw)); err == nil {
		t.Fatal("deleted session returned")
	}

	expired := "expired-token"
	_ = st.CreateSession(store.Session{
		TokenHash: auth.HashToken(expired), UserID: user.ID,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(-time.Minute),
	})
	if err := st.DeleteExpiredSessions(now); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, _, err := st.GetSessionByHash(auth.HashToken(expired)); err == nil {
		t.Fatal("expired session not pruned")
	}
}

func TestSessionBulkRevocation(t *testing.T) {
	st, _ := openAuthStore(t)
	hash, _ := auth.HashPassword("password-123")
	user, _ := st.CreateUser(store.User{Email: "u@example.com", Role: "viewer", PasswordHash: hash})
	now := time.Now()
	for _, raw := range []string{"one", "two", "three"} {
		_ = st.CreateSession(store.Session{
			TokenHash: auth.HashToken(raw), UserID: user.ID,
			CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		})
	}
	if err := st.DeleteUserSessions(user.ID, auth.HashToken("one")); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, err := st.GetSessionByHash(auth.HashToken("one")); err != nil {
		t.Fatalf("kept session missing: %v", err)
	}
	for _, raw := range []string{"two", "three"} {
		if _, _, err := st.GetSessionByHash(auth.HashToken(raw)); err == nil {
			t.Fatalf("session %q survived", raw)
		}
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	st, _ := openAuthStore(t)
	hash, _ := auth.HashPassword("password-123")
	user, _ := st.CreateUser(store.User{Email: "u@example.com", Role: "editor", PasswordHash: hash})

	raw := "api-token-value"
	token, err := st.CreateAPIToken(store.APIToken{
		UserID: user.ID, Name: "cli", Role: "operator", TokenHash: auth.HashToken(raw),
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	found, gotUser, err := st.GetAPITokenByHash(auth.HashToken(raw))
	if err != nil {
		t.Fatalf("get token: %v", err)
	}
	if found.ID != token.ID || gotUser.ID != user.ID || found.Role != "operator" {
		t.Fatalf("token = %#v user = %#v", found, gotUser)
	}
	if err := st.TouchAPIToken(token.ID, time.Now()); err != nil {
		t.Fatalf("touch: %v", err)
	}
	list, err := st.ListAPITokens(user.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %#v, %v", list, err)
	}
	if err := st.RevokeAPIToken(token.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	found, _, _ = st.GetAPITokenByHash(auth.HashToken(raw))
	if found.RevokedAt == nil {
		t.Fatal("token not marked revoked")
	}
	if err := st.RevokeAPIToken(token.ID); err == nil {
		t.Fatal("double revoke accepted")
	}
	if _, err := st.GetAPIToken("missing"); err == nil {
		t.Fatal("missing token returned")
	}
}

func TestAuthEventsAndReopen(t *testing.T) {
	st, dir := openAuthStore(t)
	hash, _ := auth.HashPassword("password-123")
	user, _ := st.CreateUser(store.User{Email: "u@example.com", Role: "admin", PasswordHash: hash})
	if err := st.AppendAuthEvent(store.AuthEvent{
		Type: "login", UserID: user.ID, Actor: "user:" + user.ID, IP: "127.0.0.1",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	events, err := st.ListAuthEvents(10)
	if err != nil || len(events) != 1 || events[0].Type != "login" {
		t.Fatalf("events = %#v, %v", events, err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := store.Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	if count, err := reopened.CountUsers(); err != nil || count != 1 {
		t.Fatalf("users after reopen = %d, %v", count, err)
	}
}
