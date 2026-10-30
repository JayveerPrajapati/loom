package host

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	_ "github.com/lib/pq"

	"loom/internal/flow"
)

// Live talks to the real network.
type Live struct {
	Client            *http.Client
	ResolveCredential func(string) (flow.Credential, bool)
}

func NewLive(resolvers ...func(string) (flow.Credential, bool)) *Live {
	dialer := &safeDialer{dialer: net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	l := &Live{Client: &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("unsupported redirect scheme %q", req.URL.Scheme)
			}
			if blockedHost(req.URL.Hostname()) {
				return fmt.Errorf("refusing to redirect to private address %q", req.URL.Hostname())
			}
			return nil
		},
	}}
	if len(resolvers) > 0 {
		l.ResolveCredential = resolvers[0]
	}
	return l
}

// safeDialer resolves once, rejects private targets and dials the validated IP
// directly so a second DNS lookup cannot swap in a private address.
type safeDialer struct {
	dialer net.Dialer
}

func (d *safeDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
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
	for _, ip := range ips {
		if blockedIP(ip.IP) {
			return nil, fmt.Errorf("refusing to call private address %q", ip.IP)
		}
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := d.dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
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
	if blockedHost(u.Hostname()) {
		return flow.HTTPResponse{}, fmt.Errorf("refusing to call private address %q", u.Hostname())
	}

	var body io.Reader
	if len(req.BodyRaw) > 0 {
		body = bytes.NewReader(req.BodyRaw)
	} else if req.Body != nil {
		data, err := json.Marshal(req.Body)
		if err != nil {
			return flow.HTTPResponse{}, err
		}
		body = bytes.NewReader(data)
	}

	method := req.Method
	if method == "" {
		method = http.MethodGet
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, req.URL, body)
	if err != nil {
		return flow.HTTPResponse{}, err
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

	client := *l.Client
	if req.Timeout > 0 {
		client.Timeout = req.Timeout
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return flow.HTTPResponse{}, err
	}
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
	item["status"] = float64(resp.StatusCode)

	return flow.HTTPResponse{
		Status: resp.StatusCode, StatusText: http.StatusText(resp.StatusCode),
		Headers: headers, Body: item,
	}, nil
}

func (l *Live) Query(ctx context.Context, credID, sqlQuery string) ([]flow.Item, error) {
	dsn := credID
	if l.ResolveCredential != nil {
		if cred, ok := l.ResolveCredential(credID); ok {
			if u, ok := cred.Data["url"].(string); ok && strings.TrimSpace(u) != "" {
				dsn = u
			} else if u, ok := cred.Data["dsn"].(string); ok && strings.TrimSpace(u) != "" {
				dsn = u
			} else if u, ok := cred.Data["connectionString"].(string); ok && strings.TrimSpace(u) != "" {
				dsn = u
			} else if host, ok := cred.Data["host"].(string); ok && strings.TrimSpace(host) != "" {
				port := 5432
				if p, ok := cred.Data["port"].(float64); ok && p > 0 {
					port = int(p)
				}
				user, _ := cred.Data["user"].(string)
				pass, _ := cred.Data["password"].(string)
				dbname, _ := cred.Data["database"].(string)
				if dbname == "" {
					dbname, _ = cred.Data["dbname"].(string)
				}
				sslmode, _ := cred.Data["sslmode"].(string)
				if sslmode == "" {
					sslmode = "disable"
				}
				dsn = fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=%s",
					url.QueryEscape(user), url.QueryEscape(pass), host, port, dbname, sslmode)
			} else if strings.TrimSpace(cred.Raw) != "" {
				dsn = strings.TrimSpace(cred.Raw)
			}
		}
	}

	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("db.postgres: missing connection details for credential %q", credID)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("db.postgres connection error: %w", err)
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, sqlQuery)
	if err != nil {
		return nil, fmt.Errorf("db.postgres query error: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var items []flow.Item
	for rows.Next() {
		colVals := make([]any, len(cols))
		colPointers := make([]any, len(cols))
		for i := range colVals {
			colPointers[i] = &colVals[i]
		}
		if err := rows.Scan(colPointers...); err != nil {
			return nil, err
		}
		item := flow.Item{}
		for i, col := range cols {
			val := colVals[i]
			if b, ok := val.([]byte); ok {
				item[col] = string(b)
			} else {
				item[col] = val
			}
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (l *Live) Slack(ctx context.Context, credID, channel, text string) error {
	var webhookURL string
	var token string

	if l.ResolveCredential != nil {
		if cred, ok := l.ResolveCredential(credID); ok {
			if u, ok := cred.Data["webhookUrl"].(string); ok && strings.TrimSpace(u) != "" {
				webhookURL = strings.TrimSpace(u)
			} else if t, ok := cred.Data["token"].(string); ok && strings.TrimSpace(t) != "" {
				token = strings.TrimSpace(t)
			} else if strings.HasPrefix(strings.TrimSpace(cred.Raw), "http") {
				webhookURL = strings.TrimSpace(cred.Raw)
			} else if strings.TrimSpace(cred.Raw) != "" {
				token = strings.TrimSpace(cred.Raw)
			}
		}
	}

	if webhookURL == "" && token == "" {
		if strings.HasPrefix(credID, "http") {
			webhookURL = credID
		} else if strings.HasPrefix(credID, "xox") {
			token = credID
		}
	}

	if webhookURL != "" {
		payload := map[string]any{"text": text}
		if channel != "" {
			payload["channel"] = channel
		}
		resp, err := l.HTTP(ctx, flow.HTTPRequest{
			Method:      http.MethodPost,
			URL:         webhookURL,
			Body:        payload,
			ContentType: "application/json",
		})
		if err != nil {
			return fmt.Errorf("slack webhook error: %w", err)
		}
		if resp.Status >= 400 {
			return fmt.Errorf("slack webhook error (status %d): %v", resp.Status, resp.Body)
		}
		return nil
	}

	if token != "" {
		payload := map[string]any{
			"channel": channel,
			"text":    text,
		}
		resp, err := l.HTTP(ctx, flow.HTTPRequest{
			Method: http.MethodPost,
			URL:    "https://slack.com/api/chat.postMessage",
			Headers: map[string]string{
				"Authorization": "Bearer " + token,
			},
			Body:        payload,
			ContentType: "application/json",
		})
		if err != nil {
			return fmt.Errorf("slack API error: %w", err)
		}
		if resp.Status >= 400 {
			return fmt.Errorf("slack API error (status %d): %v", resp.Status, resp.Body)
		}
		if ok, exists := resp.Body["ok"].(bool); exists && !ok {
			return fmt.Errorf("slack API rejected message: %v", resp.Body["error"])
		}
		return nil
	}

	return fmt.Errorf("chat.slack: no webhook URL or bot token configured for credential %q", credID)
}

func (l *Live) Sleep(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (l *Live) Now() time.Time { return time.Now() }

// blockedHost rejects literal private targets; safeDialer also validates DNS
// results and redirects.
func blockedHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" || h == "localhost" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return blockedIP(ip)
	}
	return false
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified()
}
