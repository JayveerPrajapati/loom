package server

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"loom/internal/auth"
	"loom/internal/store"
)

func clientIP(r *http.Request, trustedProxies []*net.IPNet) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	direct := net.ParseIP(strings.Trim(host, "[]"))
	if direct == nil || !trustedIP(direct, trustedProxies) {
		return host
	}

	// X-Forwarded-For is ordered client-first, proxy-last. A trusted reverse
	// proxy must overwrite this header, so the rightmost valid entry is the
	// hop it observed. Malformed or spoof-only values fall back to the proxy
	// address rather than granting the client control of a throttle bucket.
	values := r.Header.Values("X-Forwarded-For")
	for valueIndex := len(values) - 1; valueIndex >= 0; valueIndex-- {
		parts := strings.Split(values[valueIndex], ",")
		for partIndex := len(parts) - 1; partIndex >= 0; partIndex-- {
			candidate := normalizeForwardedIP(parts[partIndex])
			if candidate == "" {
				return host
			}
			return candidate
		}
	}
	return host
}

func trustedIP(ip net.IP, networks []*net.IPNet) bool {
	for _, network := range networks {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func normalizeForwardedIP(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	value = strings.Trim(value, "[]")
	if net.ParseIP(value) == nil {
		return ""
	}
	return value
}

func publicUser(user *store.User) map[string]any {
	return map[string]any{
		"id": user.ID, "email": user.Email, "name": user.Name,
		"role": user.Role, "disabled": user.Disabled,
		"createdAt": user.CreatedAt, "updatedAt": user.UpdatedAt,
	}
}

// loginOriginAllowed rejects a browser login whose Origin host differs from
// the request host. Login is the only cookie mutation with no prior CSRF token,
// so the Origin header is the available cross-site signal. Requests without an
// Origin header (CLI, curl, older clients) keep working.
//
// Behind a proxy that rewrites Host, the public host survives only in
// X-Forwarded-Host. That header is client-controllable, so it is consulted
// only when the operator has explicitly opted in with TrustForwardedHost.
func loginOriginAllowed(r *http.Request, trustForwardedHost bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	allowed := []string{normalizeLoginHost(r.Host)}
	if trustForwardedHost {
		for _, value := range strings.Split(r.Header.Get("X-Forwarded-Host"), ",") {
			if value = strings.TrimSpace(value); value != "" {
				allowed = append(allowed, normalizeLoginHost(value))
			}
		}
	}
	for _, host := range allowed {
		if host != "" && strings.EqualFold(parsed.Hostname(), host) {
			return true
		}
	}
	return false
}

// normalizeLoginHost strips a port and IPv6 brackets so a Host or
// X-Forwarded-Host value compares equal to url.URL.Hostname().
func normalizeLoginHost(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

func (s *api) login(w http.ResponseWriter, r *http.Request) {
	if !loginOriginAllowed(r, s.deps.TrustForwardedHost) {
		writeError(w, http.StatusForbidden, "cross-origin login is not allowed")
		return
	}
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	ip := clientIP(r, s.deps.TrustedProxyCIDRs)
	key := ip + "|" + email
	ipKey := "ip|" + ip
	now := time.Now()
	// Two budgets: the per-account window and a coarse per-source window so
	// rotating the attacker-chosen email cannot force unbounded argon2 work.
	for _, budget := range []struct {
		key string
		max int
	}{{ipKey, loginIPMax}, {key, loginMax}} {
		if s.limiter.blocked(budget.key, now, budget.max) {
			remaining := s.limiter.retryAfter(budget.key, now, budget.max)
			secs := int((remaining + time.Second - 1) / time.Second)
			if secs < 1 {
				secs = 1
			}
			writeJSON(w, http.StatusTooManyRequests, map[string]any{
				"error":         "too many attempts; try again later",
				"retryAfterSec": secs,
			})
			return
		}
	}

	user, err := s.deps.Store.GetUserByEmail(email)
	if err != nil {
		// Equalise timing with a real verification for unknown accounts.
		auth.VerifyPassword(auth.DummyHash(), req.Password)
		s.limiter.fail(ipKey, now)
		s.limiter.fail(key, now)
		_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
			Type: "login_failed", Actor: "", IP: ip, Detail: email,
		})
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, req.Password) {
		s.limiter.fail(ipKey, now)
		s.limiter.fail(key, now)
		_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
			Type: "login_failed", UserID: user.ID, IP: ip, Detail: email,
		})
		writeError(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	if user.Disabled {
		// The password is correct; answer distinctly so the operator knows
		// the credentials are fine and only the account is disabled.
		// Still charge the per-source budget: valid credentials for a disabled
		// account must not be an unbounded argon2/table-growth primitive.
		s.limiter.fail(ipKey, now)
		_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
			Type: "login_failed", UserID: user.ID, IP: ip, Detail: email,
		})
		writeError(w, http.StatusForbidden, "this account is disabled")
		return
	}
	s.limiter.reset(key)
	csrf, err := s.setSession(w, r, user)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "login", UserID: user.ID, Actor: "user:" + user.ID, IP: ip,
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"user": publicUser(user), "csrfToken": csrf,
	})
}

func (s *api) logout(w http.ResponseWriter, r *http.Request) {
	// Logout mutates state (it deletes the session). Require the same CSRF
	// double-submit as every other cookie-authenticated mutation so a
	// cross-site page cannot force the user out.
	if cookie, err := r.Cookie(auth.SessionCookie); err == nil && cookie.Value != "" {
		csrf, cerr := r.Cookie(auth.CSRFCookie)
		header := r.Header.Get(auth.CSRFHeader)
		if cerr != nil || csrf.Value == "" || header == "" || !auth.Equal(csrf.Value, header) {
			writeError(w, http.StatusForbidden, "csrf token missing or invalid")
			return
		}
	}
	if principal, ok := s.resolvePrincipal(r); ok && principal.Type == "user" {
		_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
			Type: "logout", UserID: principal.ID, Actor: principal.Actor(), IP: clientIP(r, s.deps.TrustedProxyCIDRs),
		})
	}
	s.clearSession(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (s *api) me(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	var user map[string]any
	switch principal.Type {
	case "user", "token":
		id := principal.UserID
		if id == "" {
			id = principal.ID
		}
		found, err := s.deps.Store.GetUser(id)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "user no longer exists")
			return
		}
		user = publicUser(found)
	case "service":
		user = map[string]any{
			"id": "service", "email": "", "name": "Service token", "role": string(auth.RoleAdmin),
		}
	default:
		user = map[string]any{
			"id": "anonymous", "email": "", "name": "Anonymous (development)",
			"role": string(auth.RoleAdmin),
		}
	}
	csrf := ""
	if cookie, err := r.Cookie(auth.CSRFCookie); err == nil {
		csrf = cookie.Value
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": user, "csrfToken": csrf})
}

func (s *api) changePassword(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	if principal.Type != "user" {
		writeError(w, http.StatusBadRequest, "password changes require a signed-in user")
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.deps.Store.GetUser(principal.ID)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "user no longer exists")
		return
	}
	if !auth.VerifyPassword(user.PasswordHash, req.Current) {
		writeError(w, http.StatusForbidden, "current password is incorrect")
		return
	}
	hash, err := auth.HashPassword(req.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Store.SetUserPassword(user.ID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.DeleteUserSessions(user.ID, principal.Session)
	if err := s.deps.Store.RevokeAPITokensForUser(user.ID); err != nil {
		log.Printf("revoke api tokens for %s: %v", user.ID, err)
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "password_changed", UserID: user.ID, Actor: principal.Actor(), IP: clientIP(r, s.deps.TrustedProxyCIDRs),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *api) listUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.deps.Store.ListUsers()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(users))
	for i := range users {
		out = append(out, publicUser(&users[i]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

func (s *api) createUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	var req struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role, ok := auth.ParseRole(req.Role)
	if !ok {
		writeError(w, http.StatusBadRequest, "role must be viewer, operator, editor or admin")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.deps.Store.CreateUser(store.User{
		Email: req.Email, Name: strings.TrimSpace(req.Name),
		Role: string(role), PasswordHash: hash,
	})
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "user_created", UserID: user.ID, Actor: principal.Actor(), IP: clientIP(r, s.deps.TrustedProxyCIDRs),
		Detail: user.Email,
	})
	writeJSON(w, http.StatusCreated, publicUser(user))
}

func (s *api) updateUser(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	id := r.PathValue("id")
	user, err := s.deps.Store.GetUser(id)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Role != nil {
		role, ok := auth.ParseRole(*req.Role)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if user.Role == string(auth.RoleAdmin) && role != auth.RoleAdmin {
			if admins, _ := s.deps.Store.CountAdmins(); admins <= 1 {
				writeError(w, http.StatusConflict, "cannot demote the last admin")
				return
			}
		}
		if err := s.deps.Store.UpdateUserRole(id, string(role)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		user.Role = string(role)
	}
	if req.Disabled != nil {
		if *req.Disabled && user.Role == string(auth.RoleAdmin) {
			if admins, _ := s.deps.Store.CountAdmins(); admins <= 1 {
				writeError(w, http.StatusConflict, "cannot disable the last admin")
				return
			}
		}
		if err := s.deps.Store.SetUserDisabled(id, *req.Disabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if *req.Disabled {
			_ = s.deps.Store.DeleteUserSessions(id, "")
		}
		user.Disabled = *req.Disabled
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "user_updated", UserID: user.ID, Actor: principal.Actor(),
		IP: clientIP(r, s.deps.TrustedProxyCIDRs), Detail: user.Role,
	})
	writeJSON(w, http.StatusOK, publicUser(user))
}

func (s *api) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	user, err := s.deps.Store.GetUser(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.Store.SetUserPassword(user.ID, hash); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.DeleteUserSessions(user.ID, "")
	if err := s.deps.Store.RevokeAPITokensForUser(user.ID); err != nil {
		log.Printf("revoke api tokens for %s: %v", user.ID, err)
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "password_reset", UserID: user.ID, Actor: principal.Actor(), IP: clientIP(r, s.deps.TrustedProxyCIDRs),
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *api) listTokens(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	userID := r.URL.Query().Get("user")
	switch principal.Type {
	case "user":
		userID = principal.UserID
	case "token":
		// A bearer token may only inspect its owner's tokens, never the whole set.
		userID = principal.UserID
		if query := r.URL.Query().Get("user"); query != "" && query != userID {
			writeError(w, http.StatusForbidden, "insufficient permission")
			return
		}
	}
	if userID != "" && !principal.Role.Allows(auth.RoleAdmin) && principal.UserID != userID {
		writeError(w, http.StatusForbidden, "insufficient permission")
		return
	}
	tokens, err := s.deps.Store.ListAPITokens(userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *api) createToken(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	if principal.Type != "user" {
		writeError(w, http.StatusBadRequest, "create tokens from the web session or the CLI")
		return
	}
	// Re-validate the session: a password reset may have revoked it between
	// require() and this insert, and a token minted now would outlive the reset.
	if principal.Session != "" {
		session, user, err := s.deps.Store.GetSessionByHash(principal.Session)
		if err != nil || user.Disabled || session.ExpiresAt.Before(time.Now()) {
			writeError(w, http.StatusUnauthorized, "session is no longer valid")
			return
		}
	}
	var req struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := readJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	role := principal.Role
	if strings.TrimSpace(req.Role) != "" {
		parsed, ok := auth.ParseRole(req.Role)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid role")
			return
		}
		if !principal.Role.Allows(parsed) {
			writeError(w, http.StatusForbidden, "token role cannot exceed your own")
			return
		}
		role = parsed
	}
	raw, err := auth.NewToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var expires *time.Time
	if strings.TrimSpace(req.ExpiresAt) != "" {
		parsed, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "expiresAt must be RFC3339")
			return
		}
		if !parsed.After(time.Now()) {
			writeError(w, http.StatusBadRequest, "expiresAt must be in the future")
			return
		}
		if max := s.deps.MaxTokenLifetime; max > 0 && parsed.After(time.Now().Add(max)) {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("expiresAt exceeds the maximum token lifetime (%s); ask an operator to raise --max-token-lifetime", max))
			return
		}
		expires = &parsed
	} else if max := s.deps.MaxTokenLifetime; max > 0 {
		// No explicit expiry: the token still gets a bounded lifetime
		// instead of living until manually revoked.
		defaultExpiry := time.Now().Add(max)
		expires = &defaultExpiry
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "api token"
	}
	token, err := s.deps.Store.CreateAPIToken(store.APIToken{
		UserID: principal.UserID, Name: name, Role: string(role),
		TokenHash: auth.HashToken(raw), ExpiresAt: expires,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "token_created", UserID: principal.UserID, Actor: principal.Actor(),
		IP: clientIP(r, s.deps.TrustedProxyCIDRs), Detail: token.ID,
	})
	writeJSON(w, http.StatusCreated, map[string]any{"token": raw, "apiToken": token})
}

func (s *api) revokeToken(w http.ResponseWriter, r *http.Request) {
	principal, _ := principalFrom(r.Context())
	token, err := s.deps.Store.GetAPIToken(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if principal.Type == "token" {
		writeError(w, http.StatusForbidden, "manage tokens from a user session or the CLI")
		return
	}
	if principal.Type == "user" && token.UserID != principal.UserID && !principal.Role.Allows(auth.RoleAdmin) {
		writeError(w, http.StatusForbidden, "insufficient permission")
		return
	}
	if err := s.deps.Store.RevokeAPIToken(token.ID); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	_ = s.deps.Store.AppendAuthEvent(store.AuthEvent{
		Type: "token_revoked", UserID: token.UserID, Actor: principal.Actor(),
		IP: clientIP(r, s.deps.TrustedProxyCIDRs), Detail: token.ID,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked"})
}

func (s *api) listAuthEvents(w http.ResponseWriter, r *http.Request) {
	events, err := s.deps.Store.ListAuthEvents(200)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
