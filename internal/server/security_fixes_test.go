package server_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"loom/internal/auth"
	"loom/internal/engine"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

// S1: workflow readers below editor must never see webhook credentials, while
// editors still receive the plaintext document the editor needs to edit.
func TestViewerCannotReadWebhookSecrets(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "viewer@x.io", "viewer", "password-123456")
	addUser(t, st, "editor@x.io", "editor", "password-123456")

	wf := &graph.Workflow{
		ID: "hook-secrets", Name: "Hook Secrets", Version: 1,
		Nodes: []graph.Node{{
			ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
			Config: map[string]any{
				"path": "secret-hook", "method": "POST",
				"secret": "SECRET-CANARY", "hmacSecret": "HMAC-CANARY",
				"secretHeader": "X-Custom-Secret", "hmacHeader": "X-Custom-HMAC",
			},
		}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatalf("publish workflow: %v", err)
	}
	snapshot, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	run := store.Run{
		ID: "run-secret-snapshot", WorkflowID: wf.ID, WorkflowVersion: "1",
		Status: "done", Trigger: "manual", StartedAt: time.Now(), Snapshot: snapshot,
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	viewer := loginAs(t, srv, "viewer@x.io", "password-123456")
	editor := loginAs(t, srv, "editor@x.io", "password-123456")

	check := func(session testSession, path string, wantPlaintext bool) {
		t.Helper()
		code, body := session.do(t, srv, http.MethodGet, path, nil, false)
		if code != http.StatusOK {
			t.Fatalf("GET %s = %d: %s", path, code, body)
		}
		text := string(body)
		if got := strings.Contains(text, "SECRET-CANARY"); got != wantPlaintext {
			t.Fatalf("GET %s secret canary present = %v, want %v: %s", path, got, wantPlaintext, text)
		}
		if got := strings.Contains(text, "HMAC-CANARY"); got != wantPlaintext {
			t.Fatalf("GET %s hmac canary present = %v, want %v: %s", path, got, wantPlaintext, text)
		}
		if !wantPlaintext {
			if !strings.Contains(text, "[redacted]") {
				t.Fatalf("GET %s missing redaction marker: %s", path, text)
			}
			if !strings.Contains(text, `"secretSet":true`) {
				t.Fatalf("GET %s missing secretSet: %s", path, text)
			}
		}
		if !strings.Contains(text, "X-Custom-Secret") || !strings.Contains(text, "X-Custom-HMAC") {
			t.Fatalf("GET %s hid the header names: %s", path, text)
		}
	}

	check(viewer, "/api/workflows/hook-secrets", false)
	check(viewer, "/api/workflows/hook-secrets/versions/1", false)
	check(viewer, "/api/runs/run-secret-snapshot/workflow", false)

	check(editor, "/api/workflows/hook-secrets", true)
	check(editor, "/api/workflows/hook-secrets/versions/1", true)
	check(editor, "/api/runs/run-secret-snapshot/workflow", true)
}

func webhookSecretWorkflow(id, path string) *graph.Workflow {
	return &graph.Workflow{
		ID: id, Name: "Hook Secret", Version: 1,
		Nodes: []graph.Node{
			{
				ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
				Config: map[string]any{
					"path": path, "method": "POST",
					"secret": "HEADER-SECRET-CANARY", "hmacSecret": "HMAC-SECRET-CANARY",
				},
			},
			{
				ID: "set", Type: "data.set", TypeVersion: 1, Name: "Set",
				Config: map[string]any{"values": map[string]any{"got": "hi"}},
			},
		},
		Edges: []graph.Edge{{
			From: graph.Endpoint{Node: "start", Port: "main"},
			To:   graph.Endpoint{Node: "set", Port: "main"},
		}},
	}
}

// S2: the secret/HMAC headers, Authorization and query.secret used to
// authenticate a webhook must not be persisted in runs.input.
func TestWebhookStoredInputDropsAuthSecrets(t *testing.T) {
	srv, st := newTestServer(t, "")
	wf := webhookSecretWorkflow("hook-leak", "leak")
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	canaries := []string{
		"HEADER-SECRET-CANARY", "HMAC-SECRET-CANARY",
		"QUERY-SECRET-CANARY", "AUTH-SECRET-CANARY",
	}
	sign := func(body []byte) string {
		mac := hmac.New(sha256.New, []byte("HMAC-SECRET-CANARY"))
		mac.Write(body)
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	body := []byte(`{"name":"loom"}`)
	req, err := http.NewRequest(http.MethodPost,
		srv.URL+"/hook/leak?secret=QUERY-SECRET-CANARY&keep=yes", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Secret", "HEADER-SECRET-CANARY")
	req.Header.Set("X-Hub-Signature-256", sign(body))
	req.Header.Set("Authorization", "Bearer AUTH-SECRET-CANARY")
	req.Header.Set("X-Keep", "kept")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook status = %d: %s", resp.StatusCode, data)
	}
	runID, _ := decode(t, data)["runId"].(string)
	if runID == "" {
		t.Fatalf("missing runId: %s", data)
	}
	stored, err := st.RunInput(runID)
	if err != nil {
		t.Fatalf("read stored input: %v", err)
	}
	text := string(stored)
	for _, canary := range canaries {
		if strings.Contains(text, canary) {
			t.Fatalf("stored input leaked %q: %s", canary, text)
		}
	}
	for _, hidden := range []string{"X-Webhook-Secret", "X-Hub-Signature-256", "Authorization"} {
		if strings.Contains(text, hidden) {
			t.Fatalf("stored input kept auth header %q: %s", hidden, text)
		}
	}
	if !strings.Contains(text, `"X-Keep":"kept"`) {
		t.Fatalf("stored input lost an unrelated header: %s", text)
	}
	if !strings.Contains(text, `"keep":"yes"`) {
		t.Fatalf("stored input lost an unrelated query parameter: %s", text)
	}

	// Empty body: query params are flattened into the item. The secret query
	// key must not be lifted into the top level either.
	empty := []byte(nil)
	req, err = http.NewRequest(http.MethodPost,
		srv.URL+"/hook/leak?secret=QUERY-SECRET-CANARY&keep=still", bytes.NewReader(empty))
	if err != nil {
		t.Fatalf("new empty request: %v", err)
	}
	req.Header.Set("X-Webhook-Secret", "HEADER-SECRET-CANARY")
	req.Header.Set("X-Hub-Signature-256", sign(empty))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("empty webhook: %v", err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty webhook status = %d: %s", resp.StatusCode, data)
	}
	runID, _ = decode(t, data)["runId"].(string)
	stored, err = st.RunInput(runID)
	if err != nil {
		t.Fatalf("read empty stored input: %v", err)
	}
	if strings.Contains(string(stored), "QUERY-SECRET-CANARY") {
		t.Fatalf("flattened input leaked the query secret: %s", stored)
	}
	if !strings.Contains(string(stored), `"keep":"still"`) {
		t.Fatalf("flattened input lost an unrelated query parameter: %s", stored)
	}
}

// S3: revoking the session while SSE is open must close the stream.
func TestStreamStopsWhenSessionDeleted(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "streamer@x.io", "viewer", "password-123456")
	session := loginAs(t, srv, "streamer@x.io", "password-123456")

	run := store.Run{
		ID: "run-live-stream", WorkflowID: "linear", WorkflowVersion: "1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}
	if err := st.CreateRun(run); err != nil {
		t.Fatalf("create run: %v", err)
	}

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/runs/run-live-stream/events", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for _, cookie := range session.cookies {
		req.AddCookie(cookie)
	}
	client := &http.Client{Timeout: 6 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d", resp.StatusCode)
	}

	var rawSession string
	for _, cookie := range session.cookies {
		if cookie.Name == auth.SessionCookie {
			rawSession = cookie.Value
		}
	}
	if rawSession == "" {
		t.Fatal("login did not set a session cookie")
	}
	if err := st.DeleteSession(auth.HashToken(rawSession)); err != nil {
		t.Fatalf("delete session: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(io.Discard, resp.Body)
		done <- err
	}()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("stream survived session deletion")
	}
}

// S4: --prod must mark session cookies Secure even over plain HTTP.
func TestProdForcesSecureCookies(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	addUser(t, st, "prod@x.io", "viewer", "password-123456")
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version: "test", Store: st, Engine: engine.New(st, host.NewSim()), Prod: true,
	}))
	t.Cleanup(srv.Close)

	for _, cookie := range loginAs(t, srv, "prod@x.io", "password-123456").cookies {
		if cookie.Name == auth.SessionCookie || cookie.Name == auth.CSRFCookie {
			if !cookie.Secure {
				t.Fatalf("prod cookie %q is not Secure", cookie.Name)
			}
		}
	}

	devSrv, devStore, _ := newAuthServer(t)
	addUser(t, devStore, "dev@x.io", "viewer", "password-123456")
	for _, cookie := range loginAs(t, devSrv, "dev@x.io", "password-123456").cookies {
		if cookie.Name == auth.SessionCookie || cookie.Name == auth.CSRFCookie {
			if cookie.Secure {
				t.Fatalf("dev cookie %q unexpectedly Secure", cookie.Name)
			}
		}
	}
}

// S5: login rejects a cross-origin browser request but leaves CLI/curl alone.
func TestLoginRejectsCrossOrigin(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "login@x.io", "viewer", "password-123456")

	post := func(origin string) int {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"email": "login@x.io", "password": "password-123456"})
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("login: %v", err)
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.StatusCode
	}

	if code := post("http://evil.example"); code != http.StatusForbidden {
		t.Fatalf("cross-origin login = %d, want 403", code)
	}
	if code := post(srv.URL); code != http.StatusOK {
		t.Fatalf("same-origin login = %d, want 200", code)
	}
	if code := post(""); code != http.StatusOK {
		t.Fatalf("no-origin login = %d, want 200", code)
	}
}

// S6: concurrent creates of the same webhook path must not both pass the
// check-then-save window.
func TestConcurrentWorkflowCreateSerializesWebhookPaths(t *testing.T) {
	srv, _ := newTestServer(t, "")

	workflow := func(id string) *graph.Workflow {
		wf := linearWorkflow()
		wf.ID = id
		wf.Name = "Race " + id
		wf.Nodes[0] = graph.Node{
			ID: "start", Type: "trigger.webhook", TypeVersion: 1, Name: "Hook",
			Config: map[string]any{"path": "race", "method": "POST"},
		}
		return wf
	}

	statuses := make(chan int, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, id := range []string{"race-a", "race-b"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			raw, err := json.Marshal(workflow(id))
			if err != nil {
				statuses <- 0
				return
			}
			<-start
			resp, err := http.Post(srv.URL+"/api/workflows", "application/json", bytes.NewReader(raw))
			if err != nil {
				statuses <- 0
				return
			}
			defer resp.Body.Close()
			_, _ = io.Copy(io.Discard, resp.Body)
			statuses <- resp.StatusCode
		}(id)
	}
	close(start)
	wg.Wait()
	close(statuses)

	counts := map[int]int{}
	for status := range statuses {
		counts[status]++
	}
	if counts[http.StatusCreated] != 1 || counts[http.StatusUnprocessableEntity] != 1 {
		t.Fatalf("concurrent creates = %v, want one 201 and one 422", counts)
	}
}
