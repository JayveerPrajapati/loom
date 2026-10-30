package host

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"loom/internal/flow"
)

func TestBlockedIP(t *testing.T) {
	blocked := []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.1", "::1", "fc00::1", "0.0.0.0"}
	for _, raw := range blocked {
		if !blockedIP(net.ParseIP(raw)) {
			t.Errorf("%s should be blocked", raw)
		}
	}
	if blockedIP(net.ParseIP("8.8.8.8")) {
		t.Fatal("public address should be allowed")
	}
	if !blockedHost("localhost") || !blockedHost("127.0.0.1") {
		t.Fatal("localhost should be blocked")
	}
}

func TestLiveRejectsPrivateHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	_, err := NewLive().HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: srv.URL})
	if err == nil {
		t.Fatal("expected private HTTP target to be rejected")
	}
}

func TestLiveRedirectGuard(t *testing.T) {
	client := NewLive().Client
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1/secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.CheckRedirect(req, nil); err == nil {
		t.Fatal("expected redirect to a private address to be rejected")
	}
}
