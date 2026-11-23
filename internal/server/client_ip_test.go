package server

import (
	"net"
	"net/http"
	"testing"
)

func TestClientIPWithoutTrustedProxy(t *testing.T) {
	req := httptestRequest(t, "203.0.113.10:44444")
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	if got := clientIP(req, nil); got != "203.0.113.10" {
		t.Fatalf("clientIP = %q, want direct address", got)
	}
}

func TestClientIPUsesTrustedProxyForwardedHop(t *testing.T) {
	_, network, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	req := httptestRequest(t, "10.1.2.3:44444")
	req.Header.Set("X-Forwarded-For", "198.51.100.20, 203.0.113.10")
	if got := clientIP(req, []*net.IPNet{network}); got != "203.0.113.10" {
		t.Fatalf("clientIP = %q, want rightmost forwarded hop", got)
	}
}

func TestClientIPRejectsSpoofedHeaderFromUntrustedProxy(t *testing.T) {
	_, network, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	req := httptestRequest(t, "192.0.2.9:44444")
	req.Header.Set("X-Forwarded-For", "198.51.100.20")
	if got := clientIP(req, []*net.IPNet{network}); got != "192.0.2.9" {
		t.Fatalf("clientIP = %q, want direct address", got)
	}
}

func TestClientIPHandlesIPv6AndMalformedForwardedValues(t *testing.T) {
	_, network, err := net.ParseCIDR("2001:db8::/32")
	if err != nil {
		t.Fatal(err)
	}
	req := httptestRequest(t, "[2001:db8::1]:44444")
	req.Header.Set("X-Forwarded-For", "[2001:db8:ffff::42]")
	if got := clientIP(req, []*net.IPNet{network}); got != "2001:db8:ffff::42" {
		t.Fatalf("clientIP = %q, want IPv6 forwarded hop", got)
	}

	req = httptestRequest(t, "[2001:db8::1]:44444")
	req.Header.Set("X-Forwarded-For", "not-an-ip")
	if got := clientIP(req, []*net.IPNet{network}); got != "2001:db8::1" {
		t.Fatalf("clientIP = %q, want fallback to trusted proxy", got)
	}
}

func TestClientIPMultipleForwardedHeadersUseRightmostValue(t *testing.T) {
	_, network, err := net.ParseCIDR("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	req := httptestRequest(t, "10.1.2.3:44444")
	req.Header.Add("X-Forwarded-For", "198.51.100.20")
	req.Header.Add("X-Forwarded-For", "203.0.113.10")
	if got := clientIP(req, []*net.IPNet{network}); got != "203.0.113.10" {
		t.Fatalf("clientIP = %q, want rightmost header value", got)
	}
}

func httptestRequest(t *testing.T, remoteAddr string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "http://loom.example", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.RemoteAddr = remoteAddr
	return req
}
