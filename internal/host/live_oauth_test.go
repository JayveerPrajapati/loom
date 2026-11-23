package host

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

func TestOAuth2TokenCacheHit(t *testing.T) {
	l := NewLive()
	l.tokenCache["https://auth.example.com/token\x00loom\x00read"] =
		cachedToken{token: "cached-token", expires: time.Now().Add(10 * time.Minute)}
	token, err := l.oauth2Token(context.Background(), &flow.OAuth2Params{
		TokenURL: "https://auth.example.com/token", ClientID: "loom", Scope: "read",
	})
	if err != nil {
		t.Fatalf("oauth2Token: %v", err)
	}
	if token != "cached-token" {
		t.Fatalf("token = %q, want the cached value", token)
	}
}

func TestOAuth2TokenNearExpiryRefetches(t *testing.T) {
	l := NewLive()
	// Within the one-minute margin the cache must not be used; the
	// refresh path attempts a real token request, which cannot resolve in
	// tests — proving the cache entry was bypassed.
	l.tokenCache["https://auth.example.com/token\x00loom\x00"] =
		cachedToken{token: "stale-token", expires: time.Now().Add(30 * time.Second)}
	_, err := l.oauth2Token(context.Background(), &flow.OAuth2Params{
		TokenURL: "https://auth.example.com/token", ClientID: "loom",
	})
	if err == nil {
		t.Fatal("expected a refresh attempt (unresolvable token url), got success")
	}
	if !strings.Contains(err.Error(), "token request") {
		t.Fatalf("err = %v, want a token request failure", err)
	}
}

func TestOAuth2MissingParams(t *testing.T) {
	l := NewLive()
	if _, err := l.oauth2Token(context.Background(), &flow.OAuth2Params{}); err == nil {
		t.Fatal("empty params must fail")
	}
	if _, err := l.oauth2Token(context.Background(), &flow.OAuth2Params{
		TokenURL: "https://auth.example.com/token",
	}); err == nil {
		t.Fatal("missing clientId must fail")
	}
}

func TestProxyTransportCached(t *testing.T) {
	l := NewLive()
	proxy, err := url.Parse("http://proxy.example.com:8080")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	first := l.proxyTransport(proxy)
	second := l.proxyTransport(proxy)
	if first != second {
		t.Fatal("proxy transports must be cached per proxy url")
	}
	if first.Proxy == nil {
		t.Fatal("proxy transport must set Proxy")
	}
	targetReq, _ := http.NewRequest(http.MethodGet, "https://api.example.com", nil)
	target, err := first.Proxy(targetReq)
	if err != nil || target == nil || target.Host != "proxy.example.com:8080" {
		t.Fatalf("proxy target = %v, %v", target, err)
	}
}
