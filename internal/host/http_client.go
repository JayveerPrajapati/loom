package host

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"encoding/json"
	"net/http"
	"net/url"

	"loom/internal/egress"
	"loom/internal/flow"
)

// dialContext resolves once, rejects private targets (unless AllowPrivate is
// set) and dials the validated IP directly so a second DNS lookup cannot swap
// in a private address.
func (l *Live) dialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for %q", host)
	}
	if !l.AllowPrivate {
		for _, ip := range ips {
			if blockedIP(ip.IP) {
				return nil, fmt.Errorf("refusing to call private address %q", ip.IP)
			}
		}
	}
	dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func (l *Live) HTTP(ctx context.Context, req flow.HTTPRequest) (flow.HTTPResponse, error) {
	u, err := url.Parse(req.URL)
	if err != nil {
		return flow.HTTPResponse{}, fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return flow.HTTPResponse{}, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	if !l.AllowPrivate && blockedHost(u.Hostname()) {
		return flow.HTTPResponse{}, fmt.Errorf("refusing to call private address %q", u.Hostname())
	}
	// Global egress policy (D7-5): every outbound hop — the request
	// target, the OAuth2 token URL, and a configured proxy — must pass.
	// It applies before any network I/O and is independent of
	// AllowPrivate.
	if l.Egress != nil {
		dest, err := egress.ParseDestination(req.URL)
		if err != nil {
			return flow.HTTPResponse{}, err
		}
		if err := l.Egress.Check(dest); err != nil {
			return flow.HTTPResponse{}, err
		}
		if req.OAuth2 != nil {
			tokenDest, err := egress.ParseDestination(req.OAuth2.TokenURL)
			if err != nil {
				return flow.HTTPResponse{}, err
			}
			if err := l.Egress.Check(tokenDest); err != nil {
				return flow.HTTPResponse{}, fmt.Errorf("oauth2 token fetch: %w", err)
			}
		}
	}

	// OAuth2 client-credentials flow: the token fetch happens inside the
	// guarded host, so its transport rules apply and the effect tape only
	// ever sees the final request/response pair. Tokens live in memory
	// with their expiry; they are never persisted.
	if req.OAuth2 != nil {
		token, err := l.oauth2Token(ctx, req.OAuth2)
		if err != nil {
			return flow.HTTPResponse{}, err
		}
		if req.Headers == nil {
			req.Headers = map[string]string{}
		}
		if _, exists := req.Headers["Authorization"]; !exists {
			req.Headers["Authorization"] = "Bearer " + token
		}
	}

	httpReq, err := buildHTTPRequest(ctx, req)
	if err != nil {
		return flow.HTTPResponse{}, err
	}
	client := *l.Client
	if req.Timeout > 0 {
		client.Timeout = req.Timeout
	}
	// Per-request proxy (credential-configured) or the server-wide proxy.
	// Each proxy gets its own transport; proxies are trusted configuration,
	// so the target guard applies to the proxy address, not the final hop.
	// The egress policy also gates the proxy address itself — routing
	// around the allowlist through an unlisted proxy must not be possible.
	if proxy := req.Proxy; proxy != "" || l.Proxy != "" {
		proxyURL := proxy
		if proxyURL == "" {
			proxyURL = l.Proxy
		}
		parsed, err := url.Parse(proxyURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return flow.HTTPResponse{}, fmt.Errorf("invalid proxy url %q", proxyURL)
		}
		if !l.AllowPrivate && blockedHost(parsed.Hostname()) {
			return flow.HTTPResponse{}, fmt.Errorf("refusing to use private proxy %q (enable --allow-private-targets if it is intentional)", parsed.Hostname())
		}
		if l.Egress != nil {
			proxyDest, err := egress.ParseDestination(proxyURL)
			if err != nil {
				return flow.HTTPResponse{}, err
			}
			if err := l.Egress.Check(proxyDest); err != nil {
				return flow.HTTPResponse{}, fmt.Errorf("proxy %q blocked by %w", proxyURL, err)
			}
		}
		client.Transport = l.proxyTransport(parsed)
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return flow.HTTPResponse{}, err
	}
	return decodeHTTPResponse(resp)
}

// proxyTransport returns a cached transport dialing through the proxy.
func (l *Live) proxyTransport(proxy *url.URL) *http.Transport {
	l.proxyMu.Lock()
	defer l.proxyMu.Unlock()
	key := proxy.String()
	if transport, ok := l.transports[key]; ok {
		return transport
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyURL(proxy),
		DialContext:           l.dialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	l.transports[key] = transport
	return transport
}

// buildHTTPRequest turns a flow request into a net/http request. It performs no
// network I/O, which keeps the header/body rules directly testable.
func buildHTTPRequest(ctx context.Context, req flow.HTTPRequest) (*http.Request, error) {
	var body io.Reader
	if len(req.BodyRaw) > 0 {
		body = bytes.NewReader(req.BodyRaw)
	} else if req.Body != nil {
		data, err := json.Marshal(req.Body)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return nil, err
	}
	for k, v := range req.Headers {
		httpReq.Header.Set(k, v)
	}
	if httpReq.Header.Get("content-type") == "" {
		switch {
		case req.ContentType != "":
			httpReq.Header.Set("content-type", req.ContentType)
		case req.Body != nil:
			httpReq.Header.Set("content-type", "application/json")
		}
	}
	if req.IdempotencyKey != "" && httpReq.Header.Get("idempotency-key") == "" {
		httpReq.Header.Set("idempotency-key", req.IdempotencyKey)
	}
	return httpReq, nil
}

// decodeHTTPResponse maps a net/http response onto the flow shape. Bodies are
// capped at 10 MiB; JSON objects and arrays are unpacked, anything else is kept
// as text.
func decodeHTTPResponse(resp *http.Response) (flow.HTTPResponse, error) {
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return flow.HTTPResponse{}, err
	}

	headers := make(map[string]string, len(resp.Header))
	for k := range resp.Header {
		headers[strings.ToLower(k)] = resp.Header.Get(k)
	}

	item := flow.Item{"body": string(raw)}
	if len(raw) > 0 {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err == nil {
			switch v := decoded.(type) {
			case map[string]any:
				item = flow.Item(v)
			case []any:
				item = flow.Item{"items": v}
			}
		}
	}
	// The response envelope must not destroy a body field named "status";
	// the node layer adds statusCode as the collision-free alias.
	if _, exists := item["status"]; !exists {
		item["status"] = float64(resp.StatusCode)
	}

	return flow.HTTPResponse{
		Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode),
		Headers: headers, Body: item, Raw: raw,
	}, nil
}

// guardedDialer adapts the HTTP private-target guard to lib/pq's dialer
// interface, which has no context.
type guardedDialer struct {
	allowPrivate bool
	timeout      time.Duration
}

func (d guardedDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialTimeout(network, address, d.timeout)
}

func (d guardedDialer) DialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	// Unix-socket Postgres DSNs (host=/var/run/postgresql) have no IP to
	// validate; the operator chose a local socket path.
	if network == "unix" {
		dialer := net.Dialer{Timeout: timeout}
		return dialer.Dial(network, address)
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if timeout <= 0 {
		timeout = d.timeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no addresses for %q", host)
	}
	if !d.allowPrivate {
		for _, ip := range ips {
			if blockedIP(ip.IP) {
				return nil, fmt.Errorf("refusing to call private address %q", ip.IP)
			}
		}
	}
	dialer := net.Dialer{Timeout: timeout}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no address for %q", host)
	}
	return nil, lastErr
}
