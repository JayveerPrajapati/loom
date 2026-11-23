package server_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"loom/internal/auth"
	"loom/internal/engine"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

func newAuthServer(t *testing.T) (*httptest.Server, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()),
	}))
	t.Cleanup(srv.Close)
	return srv, st, dir
}

func addUser(t *testing.T, st *store.Store, email, role, password string) *store.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := st.CreateUser(store.User{
		Email: email, Name: email, Role: role, PasswordHash: hash,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

type testSession struct {
	cookies []*http.Cookie
	csrf    string
}

func loginAs(t *testing.T, srv *httptest.Server, email, password string) testSession {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d: %s", resp.StatusCode, data)
	}
	session := testSession{cookies: resp.Cookies()}
	for _, cookie := range session.cookies {
		if cookie.Name == auth.CSRFCookie {
			session.csrf = cookie.Value
		}
	}
	if session.csrf == "" {
		t.Fatal("login did not set a CSRF cookie")
	}
	return session
}

func (s testSession) do(t *testing.T, srv *httptest.Server, method, path string, body any, withCSRF bool) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, cookie := range s.cookies {
		req.AddCookie(cookie)
	}
	if withCSRF {
		req.Header.Set(auth.CSRFHeader, s.csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestAuthLoginMeLogout(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@example.com", "admin", "correct-horse-battery")

	session := loginAs(t, srv, "admin@example.com", "correct-horse-battery")
	status, data := session.do(t, srv, http.MethodGet, "/api/auth/me", nil, false)
	if status != http.StatusOK {
		t.Fatalf("me = %d: %s", status, data)
	}
	me := decode(t, data)
	user, _ := me["user"].(map[string]any)
	if user["email"] != "admin@example.com" || user["role"] != "admin" {
		t.Fatalf("me = %#v", me)
	}

	status, _ = session.do(t, srv, http.MethodPost, "/api/auth/logout", nil, true)
	if status != http.StatusNoContent {
		t.Fatalf("logout = %d", status)
	}
	status, _ = session.do(t, srv, http.MethodGet, "/api/auth/me", nil, false)
	if status != http.StatusUnauthorized {
		t.Fatalf("me after logout = %d, want 401", status)
	}
}

func TestAuthRejectsBadPasswordAndUnknownUser(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@example.com", "admin", "correct-horse-battery")

	for _, body := range []map[string]string{
		{"email": "admin@example.com", "password": "wrong-password"},
		{"email": "nobody@example.com", "password": "correct-horse-battery"},
	} {
		status, data := call(t, srv, http.MethodPost, "/api/auth/login", "", body)
		if status != http.StatusUnauthorized {
			t.Fatalf("login %v = %d: %s", body["email"], status, data)
		}
		if bytes.Contains(data, []byte("wrong-password")) {
			t.Fatalf("error leaked the password: %s", data)
		}
	}
}

func TestAuthRBACMatrix(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	viewer := addUser(t, st, "viewer@example.com", "viewer", "viewer-password")
	operator := addUser(t, st, "operator@example.com", "operator", "operator-password")
	editor := addUser(t, st, "editor@example.com", "editor", "editor-password")
	admin := addUser(t, st, "admin@example.com", "admin", "admin-password-1")
	_ = viewer
	_ = operator
	_ = editor
	_ = admin

	adminSession := loginAs(t, srv, "admin@example.com", "admin-password-1")
	viewerSession := loginAs(t, srv, "viewer@example.com", "viewer-password")
	operatorSession := loginAs(t, srv, "operator@example.com", "operator-password")
	editorSession := loginAs(t, srv, "editor@example.com", "editor-password")

	wf := linearWorkflow()
	wf.ID = "rbac"
	if status, data := adminSession.do(t, srv, http.MethodPost, "/api/workflows", wf, true); status != http.StatusCreated {
		t.Fatalf("admin create = %d: %s", status, data)
	}

	cases := []struct {
		name    string
		session *testSession
		method  string
		path    string
		body    any
		csrf    bool
		want    int
	}{
		{"anonymous read", nil, http.MethodGet, "/api/workflows", nil, false, http.StatusUnauthorized},
		{"viewer read", &viewerSession, http.MethodGet, "/api/workflows", nil, false, http.StatusOK},
		{"viewer cannot edit", &viewerSession, http.MethodPost, "/api/workflows", wf, true, http.StatusForbidden},
		{"viewer cannot run", &viewerSession, http.MethodPost, "/api/workflows/rbac/runs", map[string]any{"input": map[string]any{}}, true, http.StatusForbidden},
		{"operator cannot edit", &operatorSession, http.MethodPost, "/api/workflows", wf, true, http.StatusForbidden},
		{"operator can run", &operatorSession, http.MethodPost, "/api/workflows/rbac/runs", map[string]any{"input": map[string]any{"name": "loom"}}, true, http.StatusOK},
		{"editor cannot read credentials", &editorSession, http.MethodGet, "/api/credentials", nil, false, http.StatusForbidden},
		{"editor can publish", &editorSession, http.MethodPost, "/api/workflows/rbac/publish", nil, true, http.StatusCreated},
		{"editor cannot manage users", &editorSession, http.MethodGet, "/api/users", nil, false, http.StatusForbidden},
		{"admin can read users", &adminSession, http.MethodGet, "/api/users", nil, false, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.session == nil {
				status, data := call(t, srv, tc.method, tc.path, "", tc.body)
				if status != tc.want {
					t.Fatalf("status = %d, want %d: %s", status, tc.want, data)
				}
				return
			}
			status, data := tc.session.do(t, srv, tc.method, tc.path, tc.body, tc.csrf)
			if status != tc.want {
				t.Fatalf("status = %d, want %d: %s", status, tc.want, data)
			}
		})
	}
}

func TestAuthCSRFRequiredForCookieMutations(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	session := loginAs(t, srv, "editor@example.com", "editor-password")

	wf := linearWorkflow()
	wf.ID = "csrf"
	if status, data := session.do(t, srv, http.MethodPost, "/api/workflows", wf, false); status != http.StatusForbidden {
		t.Fatalf("no-csrf status = %d, want 403: %s", status, data)
	}
	if status, data := session.do(t, srv, http.MethodPost, "/api/workflows", wf, true); status != http.StatusCreated {
		t.Fatalf("with-csrf status = %d, want 201: %s", status, data)
	}
}

func TestAuthLastAdminGuard(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	admin := addUser(t, st, "admin@example.com", "admin", "admin-password-1")
	session := loginAs(t, srv, "admin@example.com", "admin-password-1")

	status, data := session.do(t, srv, http.MethodPut, "/api/users/"+admin.ID,
		map[string]any{"role": "viewer"}, true)
	if status != http.StatusConflict {
		t.Fatalf("demote last admin = %d, want 409: %s", status, data)
	}
	status, data = session.do(t, srv, http.MethodPut, "/api/users/"+admin.ID,
		map[string]any{"disabled": true}, true)
	if status != http.StatusConflict {
		t.Fatalf("disable last admin = %d, want 409: %s", status, data)
	}
}

func TestAuthAPITokenScopesAndRevocation(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	session := loginAs(t, srv, "editor@example.com", "editor-password")

	status, data := session.do(t, srv, http.MethodPost, "/api/tokens",
		map[string]any{"name": "cli", "role": "admin"}, true)
	if status == http.StatusCreated {
		t.Fatal("token role exceeded the owner's role")
	}
	status, data = session.do(t, srv, http.MethodPost, "/api/tokens",
		map[string]any{"name": "cli", "role": "operator"}, true)
	if status != http.StatusCreated {
		t.Fatalf("create token = %d: %s", status, data)
	}
	created := decode(t, data)
	raw, _ := created["token"].(string)
	tokenID := ""
	if apiToken, ok := created["apiToken"].(map[string]any); ok {
		tokenID, _ = apiToken["id"].(string)
	}
	if raw == "" || tokenID == "" {
		t.Fatalf("token response = %#v", created)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/stats", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("token request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token read = %d", resp.StatusCode)
	}

	if status, data := session.do(t, srv, http.MethodDelete, "/api/tokens/"+tokenID, nil, true); status != http.StatusOK {
		t.Fatalf("revoke = %d: %s", status, data)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/api/stats", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("revoked token request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token read = %d, want 401", resp.StatusCode)
	}
}

func TestAuthPasswordChangeRevokesOtherSessions(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "user@example.com", "viewer", "original-password")
	first := loginAs(t, srv, "user@example.com", "original-password")
	second := loginAs(t, srv, "user@example.com", "original-password")

	status, data := first.do(t, srv, http.MethodPost, "/api/auth/password",
		map[string]string{"current": "original-password", "new": "replacement-password"}, true)
	if status != http.StatusOK {
		t.Fatalf("change password = %d: %s", status, data)
	}
	if status, _ := first.do(t, srv, http.MethodGet, "/api/auth/me", nil, false); status != http.StatusOK {
		t.Fatalf("current session = %d, want 200", status)
	}
	if status, _ := second.do(t, srv, http.MethodGet, "/api/auth/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("other session = %d, want 401", status)
	}
	if status, _ := call(t, srv, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "original-password",
	}); status != http.StatusUnauthorized {
		t.Fatalf("old password login = %d, want 401", status)
	}
	if status, data := call(t, srv, http.MethodPost, "/api/auth/login", "", map[string]string{
		"email": "user@example.com", "password": "replacement-password",
	}); status != http.StatusOK {
		t.Fatalf("new password login = %d: %s", status, data)
	}
}

func TestAuthDisabledUserSessionRejected(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@example.com", "admin", "admin-password-1")
	user := addUser(t, st, "user@example.com", "viewer", "user-password-1")
	admin := loginAs(t, srv, "admin@example.com", "admin-password-1")
	session := loginAs(t, srv, "user@example.com", "user-password-1")

	if status, data := admin.do(t, srv, http.MethodPut, "/api/users/"+user.ID,
		map[string]any{"disabled": true}, true); status != http.StatusOK {
		t.Fatalf("disable = %d: %s", status, data)
	}
	if status, _ := session.do(t, srv, http.MethodGet, "/api/auth/me", nil, false); status != http.StatusUnauthorized {
		t.Fatalf("disabled session = %d, want 401", status)
	}
}

func TestAuthActorRecordedAndTamperDetected(t *testing.T) {
	srv, st, dir := newAuthServer(t)
	addUser(t, st, "editor@example.com", "editor", "editor-password")
	session := loginAs(t, srv, "editor@example.com", "editor-password")

	wf := linearWorkflow()
	wf.ID = "actor"
	if status, data := session.do(t, srv, http.MethodPost, "/api/workflows", wf, true); status != http.StatusCreated {
		t.Fatalf("create = %d: %s", status, data)
	}
	status, data := session.do(t, srv, http.MethodPost, "/api/workflows/actor/runs",
		map[string]any{"input": map[string]any{"name": "loom"}}, true)
	if status != http.StatusOK {
		t.Fatalf("run = %d: %s", status, data)
	}
	runID, _ := decode(t, data)["runId"].(string)
	if runID == "" {
		t.Fatalf("missing runId: %s", data)
	}
	status, data = session.do(t, srv, http.MethodGet, "/api/runs/"+runID, nil, false)
	if status != http.StatusOK {
		t.Fatalf("get run = %d: %s", status, data)
	}
	run := decode(t, data)
	actor, _ := run["actor"].(string)
	if !strings.Contains(actor, "@") {
		t.Fatalf("run actor = %q, want the user's email", actor)
	}

	db, err := sql.Open("sqlite", filepath.Join(dir, "loom.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE events SET actor = 'user:attacker' WHERE run_id = ? AND seq = 2`, runID); err != nil {
		t.Fatalf("tamper actor: %v", err)
	}
	status, data = session.do(t, srv, http.MethodPost, "/api/runs/"+runID+"/replay",
		map[string]any{"mode": "full"}, true)
	if status != http.StatusUnprocessableEntity {
		t.Fatalf("replay after actor tamper = %d, want 422: %s", status, data)
	}
}

func TestAuthLoginRateLimit(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@example.com", "admin", "admin-password-1")

	last := 0
	for attempt := 1; attempt <= 11; attempt++ {
		last, _ = call(t, srv, http.MethodPost, "/api/auth/login", "", map[string]string{
			"email": "admin@example.com", "password": "wrong-password",
		})
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("attempt 11 = %d, want 429", last)
	}
}

func TestAuthLoginLockoutReportsRetryAfter(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "locked@example.com", "viewer", "locked-password-1")

	for i := 0; i < 10; i++ {
		if status, _ := postLogin(t, srv, "locked@example.com", "wrong-password"); status != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401", i, status)
		}
	}
	status, data := postLogin(t, srv, "locked@example.com", "wrong-password")
	if status != http.StatusTooManyRequests {
		t.Fatalf("attempt 11 = %d, want 429: %s", status, data)
	}
	var body struct {
		Error         string `json:"error"`
		RetryAfterSec int    `json:"retryAfterSec"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("decode 429 body: %v", err)
	}
	if body.RetryAfterSec <= 0 || body.RetryAfterSec > 900 {
		t.Fatalf("retryAfterSec = %d, want 1..900", body.RetryAfterSec)
	}
	// A correct password is also rejected while the lockout holds, and does
	// not extend it (the window is anchored at the first failure).
	if status, _ = postLogin(t, srv, "locked@example.com", "locked-password-1"); status != http.StatusTooManyRequests {
		t.Fatalf("correct password during lockout = %d, want 429", status)
	}
}

func TestAuthTokensCannotCrossTenant(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@example.com", "admin", "admin-password-1")
	addUser(t, st, "alice@example.com", "viewer", "alice-password-1")
	addUser(t, st, "bob@example.com", "viewer", "bob-password-1")

	alice := loginAs(t, srv, "alice@example.com", "alice-password-1")
	bob := loginAs(t, srv, "bob@example.com", "bob-password-1")

	status, data := alice.do(t, srv, http.MethodPost, "/api/tokens",
		map[string]any{"name": "alice token"}, true)
	if status != http.StatusCreated {
		t.Fatalf("create token = %d: %s", status, data)
	}
	created := decode(t, data)
	raw, _ := created["token"].(string)
	tokenID := ""
	if apiToken, ok := created["apiToken"].(map[string]any); ok {
		tokenID, _ = apiToken["id"].(string)
	}
	if raw == "" || tokenID == "" {
		t.Fatalf("token response = %#v", created)
	}

	bearer := func(method, path string) (int, []byte) {
		req, err := http.NewRequest(method, srv.URL+path, nil)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		req.Header.Set("Authorization", "Bearer "+raw)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, body
	}

	status, data = bearer(http.MethodGet, "/api/tokens")
	if status != http.StatusOK {
		t.Fatalf("token list = %d: %s", status, data)
	}
	list := decode(t, data)
	tokens, _ := list["tokens"].([]any)
	if len(tokens) != 1 {
		t.Fatalf("token principal listed %d tokens, want 1: %s", len(tokens), data)
	}

	if status, data := bob.do(t, srv, http.MethodDelete, "/api/tokens/"+tokenID, nil, true); status != http.StatusForbidden {
		t.Fatalf("bob revoke alice token = %d, want 403: %s", status, data)
	}
	if status, data := bearer(http.MethodDelete, "/api/tokens/"+tokenID); status != http.StatusForbidden {
		t.Fatalf("token revoked tokens = %d, want 403: %s", status, data)
	}
}

func postLogin(t *testing.T, srv *httptest.Server, email, password string) (int, []byte) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data
}

func TestAuthDisabledLoginDistinctResponse(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "admin@example.com", "admin", "admin-password-1")
	user := addUser(t, st, "disabled@example.com", "viewer", "user-password-1")
	admin := loginAs(t, srv, "admin@example.com", "admin-password-1")

	if status, data := admin.do(t, srv, http.MethodPut, "/api/users/"+user.ID,
		map[string]any{"disabled": true}, true); status != http.StatusOK {
		t.Fatalf("disable = %d: %s", status, data)
	}

	// Correct password on a disabled account: a distinct 403 so the operator
	// knows the credentials are fine and only the account is disabled.
	status, data := postLogin(t, srv, "disabled@example.com", "user-password-1")
	if status != http.StatusForbidden {
		t.Fatalf("disabled login = %d, want 403: %s", status, data)
	}
	if !bytes.Contains(data, []byte("disabled")) {
		t.Fatalf("disabled login message = %s", data)
	}

	// Wrong password still gets the generic 401.
	if status, data = postLogin(t, srv, "disabled@example.com", "wrong-password"); status != http.StatusUnauthorized {
		t.Fatalf("disabled wrong-password login = %d, want 401: %s", status, data)
	}

	// A correct password on a disabled account is not a brute-force attempt:
	// it must not count toward the rate limiter (10 fails per 15 minutes).
	for i := 0; i < 12; i++ {
		if status, _ = postLogin(t, srv, "disabled@example.com", "user-password-1"); status != http.StatusForbidden {
			t.Fatalf("repeat disabled login %d = %d, want 403", i, status)
		}
	}
}
