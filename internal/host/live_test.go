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
	blocked := []string{
		"127.0.0.1", "10.0.0.1", "192.168.1.1", "169.254.1.1",
		"100.64.0.1", "100.100.100.200", "100.127.255.255",
		"::ffff:100.64.0.1", "::ffff:100.100.100.200",
		"::1", "fc00::1", "0.0.0.0",
		// NAT64 well-known prefix embedding private IPv4 targets.
		"64:ff9b::7f00:1", "64:ff9b::a00:1", "64:ff9b::6464:64c8",
		// 6to4 embedding private IPv4 targets.
		"2002:7f00:1::", "2002:0a00:1::",
	}
	for _, raw := range blocked {
		if !blockedIP(net.ParseIP(raw)) {
			t.Errorf("%s should be blocked", raw)
		}
	}
	allowed := []string{
		"100.63.255.255", "100.128.0.0", "8.8.8.8",
		// NAT64/6to4 embedding public IPv4 targets stay reachable.
		"64:ff9b::808:808", "2002:0808:0808::",
		// The local-use NAT64 prefix 64:ff9b:1::/48 is not the well-known
		// prefix and embeds no IPv4 target; it stays subject only to the
		// ordinary IPv6 rules.
		"64:ff9b:1::1",
	}
	for _, raw := range allowed {
		if blockedIP(net.ParseIP(raw)) {
			t.Errorf("%s should be allowed", raw)
		}
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

func TestLiveAllowPrivateTargets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	l := NewLive()
	l.AllowPrivate = true
	resp, err := l.HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: srv.URL})
	if err != nil {
		t.Fatalf("allow-private request to a loopback target failed: %v", err)
	}
	if resp.Status != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Status)
	}
}

func TestLiveAllowPrivateRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()

	l := NewLive()
	l.AllowPrivate = true
	if _, err := l.HTTP(context.Background(), flow.HTTPRequest{Method: http.MethodGet, URL: source.URL}); err != nil {
		t.Fatalf("allow-private redirect to a loopback target failed: %v", err)
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
