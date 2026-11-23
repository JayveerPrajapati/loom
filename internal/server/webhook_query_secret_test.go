package server_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"loom/internal/engine"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

// newQuerySecretServer builds a webhook server with the query-secret
// rejection switch in the requested state.
func newQuerySecretServer(t *testing.T, reject bool) *httptest.Server {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveWorkflow(webhookSecretWorkflow("hook-qreject", "qreject")); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	srv := httptest.NewServer(server.Handler(server.Deps{
		Version:                   "test",
		Store:                     st,
		Engine:                    engine.New(st, host.NewSim()),
		AllowAnonymous:            true,
		RejectWebhookQuerySecrets: reject,
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestWebhookQuerySecretRejectionSwitch(t *testing.T) {
	body := `{"name":"loom"}`
	sign := func(b string) string {
		mac := hmac.New(sha256.New, []byte("HMAC-SECRET-CANARY"))
		mac.Write([]byte(b))
		return "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	t.Run("switch on rejects a correct query secret", func(t *testing.T) {
		srv := newQuerySecretServer(t, true)
		resp, err := http.Post(srv.URL+"/hook/qreject?secret=HEADER-SECRET-CANARY", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("webhook: %v", err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", resp.StatusCode, data)
		}
		if !strings.Contains(string(data), "query string webhook secrets are rejected") {
			t.Fatalf("error must explain the rejection: %s", data)
		}
	})

	t.Run("switch on keeps header authentication working", func(t *testing.T) {
		srv := newQuerySecretServer(t, true)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/hook/qreject", strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Webhook-Secret", "HEADER-SECRET-CANARY")
		req.Header.Set("X-Hub-Signature-256", sign(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("webhook: %v", err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("header-authenticated webhook status = %d: %s", resp.StatusCode, data)
		}
	})

	t.Run("switch off keeps query authentication compatible", func(t *testing.T) {
		srv := newQuerySecretServer(t, false)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/hook/qreject?secret=HEADER-SECRET-CANARY", strings.NewReader(body))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Hub-Signature-256", sign(body))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("webhook: %v", err)
		}
		data, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("query-authenticated webhook status = %d: %s", resp.StatusCode, data)
		}
	})
}
