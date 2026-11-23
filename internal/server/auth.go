package server

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"loom/internal/auth"
	"loom/internal/store"
)

type principalKey struct{}

// principalFrom returns the authenticated caller attached by require.
func principalFrom(ctx context.Context) (auth.Principal, bool) {
	principal, ok := ctx.Value(principalKey{}).(auth.Principal)
	return principal, ok
}

// hideRunToken drops the approval capability token from a run payload unless
// the caller may act on approvals. The token is an unauthenticated capability
// URL (/hook/approval/{token}); a viewer who can read it can decide the run and
// bypass the operator gate, so it is only returned to operator and above.
func hideRunToken(r *http.Request, run *store.Run) {
	if principal, ok := principalFrom(r.Context()); ok && principal.Role.Allows(auth.RoleOperator) {
		return
	}
	run.ResumeToken = ""
	run.ResumeExpiresAt = nil
	run.ResumeRequestedAt = nil
}

func hideRunTokens(r *http.Request, runs []store.Run) {
	for i := range runs {
		hideRunToken(r, &runs[i])
	}
}

// actorFor returns the attribution string for the current request.
func actorFor(r *http.Request) string {
	if principal, ok := principalFrom(r.Context()); ok {
		return principal.Actor()
	}
	return "system:engine"
}

// actorIdentity returns the stable id and the human label for the caller. The
// id is what audit rows keep; the label is what people read.
func actorIdentity(r *http.Request) (string, string) {
	principal, ok := principalFrom(r.Context())
	if !ok {
		return "", "system:engine"
	}
	id := principal.UserID
	if id == "" {
		id = principal.ID
	}
	return id, principal.Actor()
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

func minRole(a, b auth.Role) auth.Role {
	if a.Rank() <= b.Rank() {
		return a
	}
	return b
}

// resolvePrincipal authenticates a request from either a bearer token or the
// session cookie. Bearer wins so CLI/API-token callers never need CSRF.
func (s *api) resolvePrincipal(r *http.Request) (auth.Principal, bool) {
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		raw := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if s.deps.Token != "" && auth.Equal(raw, s.deps.Token) {
			return auth.Principal{Type: "service", Role: auth.RoleAdmin}, true
		}
		token, user, err := s.deps.Store.GetAPITokenByHash(auth.HashToken(raw))
		if err != nil || token.RevokedAt != nil || user.Disabled {
			return auth.Principal{}, false
		}
		if token.ExpiresAt != nil && token.ExpiresAt.Before(time.Now()) {
			return auth.Principal{}, false
		}
		_ = s.deps.Store.TouchAPIToken(token.ID, time.Now())
		return auth.Principal{
			Type: "token", ID: token.ID, UserID: user.ID, Email: user.Email,
			Role: minRole(auth.Role(token.Role), auth.Role(user.Role)),
		}, true
	}

	cookie, err := r.Cookie(auth.SessionCookie)
	if err != nil || strings.TrimSpace(cookie.Value) == "" {
		if s.deps.AllowAnonymous && s.deps.Token == "" {
			return auth.Principal{Type: "anonymous", Role: auth.RoleAdmin}, true
		}
		return auth.Principal{}, false
	}
	hash := auth.HashToken(cookie.Value)
	session, user, err := s.deps.Store.GetSessionByHash(hash)
	if err != nil || user.Disabled || session.ExpiresAt.Before(time.Now()) {
		_ = s.deps.Store.DeleteSession(hash)
		return auth.Principal{}, false
	}
	_ = s.deps.Store.TouchSession(hash, time.Now())
	return auth.Principal{
		Type: "user", ID: user.ID, UserID: user.ID, Email: user.Email,
		Role: auth.Role(user.Role), Session: hash,
	}, true
}

// require enforces a minimum role and CSRF for cookie-authenticated mutations.
func (s *api) require(min auth.Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		principal, ok := s.resolvePrincipal(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !principal.Role.Allows(min) {
			writeError(w, http.StatusForbidden, "insufficient permission")
			return
		}
		if principal.Type == "user" && isUnsafeMethod(r.Method) {
			header := r.Header.Get(auth.CSRFHeader)
			if s.deps.CSRFOverride != "" {
				if !auth.Equal(header, s.deps.CSRFOverride) {
					writeError(w, http.StatusForbidden, "csrf token missing or invalid")
					return
				}
			} else {
				csrf, err := r.Cookie(auth.CSRFCookie)
				if err != nil || csrf.Value == "" || header == "" || !auth.Equal(csrf.Value, header) {
					writeError(w, http.StatusForbidden, "csrf token missing or invalid")
					return
				}
			}
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	}
}

// route registers an API route with an explicit minimum role.
func (s *api) route(mux *http.ServeMux, pattern string, min auth.Role, handler http.HandlerFunc) {
	if !min.Allows(auth.RoleViewer) {
		panic("server: route " + pattern + " has an invalid role")
	}
	mux.HandleFunc(pattern, s.require(min, handler))
}

func (s *api) cookieSecure(r *http.Request) bool {
	return s.deps.Prod || s.deps.CookieSecure || r.TLS != nil
}

// setSession issues the session and CSRF cookies. Only the session hash is
// stored server-side; the CSRF value is a double-submit cookie.
func (s *api) setSession(w http.ResponseWriter, r *http.Request, user *store.User) (string, error) {
	raw, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	csrf, err := auth.NewToken()
	if err != nil {
		return "", err
	}
	if s.deps.CSRFOverride != "" {
		csrf = s.deps.CSRFOverride
	}
	now := time.Now()
	ttl := s.deps.SessionTTL
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	if host == "" {
		host = r.RemoteAddr
	}
	if err := s.deps.Store.CreateSession(store.Session{
		TokenHash: auth.HashToken(raw), UserID: user.ID,
		CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(ttl),
		IP: host, UserAgent: r.UserAgent(),
	}); err != nil {
		return "", err
	}
	secure := s.cookieSecure(r)
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookie, Value: raw, Path: "/",
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
		Expires: now.Add(ttl), MaxAge: int(ttl.Seconds()),
	})
	http.SetCookie(w, &http.Cookie{
		Name: auth.CSRFCookie, Value: csrf, Path: "/",
		HttpOnly: false, Secure: secure, SameSite: http.SameSiteLaxMode,
		Expires: now.Add(ttl), MaxAge: int(ttl.Seconds()),
	})
	return csrf, nil
}

func (s *api) clearSession(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookie); err == nil && cookie.Value != "" {
		_ = s.deps.Store.DeleteSession(auth.HashToken(cookie.Value))
	}
	expired := time.Unix(0, 0)
	for _, name := range []string{auth.SessionCookie, auth.CSRFCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", HttpOnly: name == auth.SessionCookie,
			Secure: s.cookieSecure(r), SameSite: http.SameSiteLaxMode,
			Expires: expired, MaxAge: -1,
		})
	}
}

// loginLimiter is a small per-IP+email sliding window.
type loginLimiter struct {
	mu      sync.Mutex
	entries map[string]*loginEntry
}

type loginEntry struct {
	count int
	first time.Time
}

const (
	loginWindow = 15 * time.Minute
	loginMax    = 10
	// loginIPMax bounds failures per source address. The per-account key
	// includes the email the caller chooses, so without a coarse IP budget an
	// attacker rotates emails and forces one full argon2id verification per
	// request indefinitely.
	loginIPMax = 60
)

func (l *loginLimiter) blocked(key string, now time.Time, max int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok {
		return false
	}
	if now.Sub(entry.first) > loginWindow {
		delete(l.entries, key)
		return false
	}
	return entry.count >= max
}

// retryAfter reports how long the lockout for key lasts. The window is
// anchored at the first failure and attempts while blocked do not extend
// it, so this is the time until the entry ages out of the window.
func (l *loginLimiter) retryAfter(key string, now time.Time, max int) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[key]
	if !ok || entry.count < max {
		return 0
	}
	remaining := loginWindow - now.Sub(entry.first)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func (l *loginLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.entries == nil {
		l.entries = map[string]*loginEntry{}
	}
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.first) > loginWindow {
		l.entries[key] = &loginEntry{count: 1, first: now}
		return
	}
	entry.count++
	if len(l.entries) > 4096 {
		for key, item := range l.entries {
			if now.Sub(item.first) > loginWindow {
				delete(l.entries, key)
			}
		}
	}
}

func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}
