package host

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/lib/pq"

	"loom/internal/flow"
)

// Profile connection tests: a user editing a Configuration Hub profile
// wants to know the endpoint (or database) answers before publishing. The
// tests ride the exact same guard, proxy and TLS rules as real runs, and
// never touch the cached pools — a test connection is opened, pinged and
// closed.

// TestHTTPProfile issues a GET against the profile's URL through the same
// guarded client http.request runs use.
func (l *Live) TestHTTPProfile(ctx context.Context, rawURL string, timeout time.Duration) (int, int64, error) {
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 5 * time.Second
	}
	start := time.Now()
	resp, err := l.HTTP(ctx, flow.HTTPRequest{Method: "GET", URL: rawURL, Timeout: timeout})
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return 0, latency, err
	}
	return resp.Status, latency, nil
}

// TestPostgresProfile dials the profile's postgres credential and confirms
// the server answers. It uses a throwaway connection, not the LRU pool: a
// test must never evict a live run's cached pool.
func (l *Live) TestPostgresProfile(ctx context.Context, credID string, timeout time.Duration) (int64, error) {
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 5 * time.Second
	}
	dsn := postgresCredentialDSN(credID, l.ResolveCredential)
	start := time.Now()
	connector, err := pq.NewConnector(dsn)
	if err != nil {
		return 0, fmt.Errorf("db.postgres connection error: %w", err)
	}
	connector.Dialer(guardedDialer{allowPrivate: l.AllowPrivate, timeout: timeout})
	db := sql.OpenDB(connector)
	defer db.Close()
	pingCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		return time.Since(start).Milliseconds(), err
	}
	return time.Since(start).Milliseconds(), nil
}
