package host

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"loom/internal/egress"
	"loom/internal/flow"
)

// Live talks to the real network.
type Live struct {
	Client            *http.Client
	ResolveCredential func(string) (flow.Credential, bool)

	// AllowPrivate permits requests to private and loopback targets. It is
	// the operator opt-in behind serve's --allow-private-targets flag; the
	// default keeps the SSRF private-target guard fully enforced.
	AllowPrivate bool

	// Egress is the operator-authored global egress policy
	// (--egress-policy / <data>/egress-policy.json). A zero value allows
	// every destination; ModeDeny flips the layer to allowlist-only. It is
	// independent of AllowPrivate: opting into private targets does not
	// bypass egress rules, and egress rules do not relax the SSRF guard.
	Egress *egress.Policy

	// Proxy is an optional server-wide HTTP proxy. It is trusted operator
	// configuration: requests through it are dialed to the proxy address
	// (which itself must pass the target guard unless AllowPrivate is set),
	// and the proxy, not Loom, connects to the final target.
	Proxy string

	proxyMu       sync.Mutex
	tokenMu       sync.Mutex
	tokenCache    map[string]cachedToken
	transports    map[string]*http.Transport
	postgresMu    sync.Mutex
	postgresDBs   map[postgresPoolKey]*sql.DB
	postgresOrder []postgresPoolKey

	// EnableSubprocess gates code.python execution. The operator opt-in
	// behind --enable-subprocess-nodes: subprocess execution is the
	// largest security surface Loom offers.
	EnableSubprocess bool
	// DenySubprocessNetwork cuts code.python's network at the OS level
	// (--subprocess-network deny, isolation-runner campaign D9-1):
	// Linux empties the network namespace, darwin denies network in the
	// Seatbelt profile. Default false — scripts may call out. When the
	// mechanism is unavailable the execution fails closed (D9-2).
	DenySubprocessNetwork bool
	// EnvsRoot is the directory of resolved dependency environments
	// (<data>/envs); environments are built at publish time, runs only
	// read them.
	EnvsRoot string
}

func NewLive(resolvers ...func(string) (flow.Credential, bool)) *Live {
	l := &Live{
		tokenCache:  map[string]cachedToken{},
		transports:  map[string]*http.Transport{},
		postgresDBs: map[postgresPoolKey]*sql.DB{},
	}
	transport := &http.Transport{
		DialContext:           l.dialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	}
	l.Client = &http.Client{
		Timeout:   30 * time.Second,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("stopped after 10 redirects")
			}
			if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
				return fmt.Errorf("unsupported redirect scheme %q", req.URL.Scheme)
			}
			if !l.AllowPrivate && blockedHost(req.URL.Hostname()) {
				return fmt.Errorf("refusing to redirect to private address %q", req.URL.Hostname())
			}
			return nil
		},
	}
	if len(resolvers) > 0 {
		l.ResolveCredential = resolvers[0]
	}
	return l
}

// Close releases cached PostgreSQL pools. It is safe to call more than once.
func (l *Live) Close() error {
	l.postgresMu.Lock()
	defer l.postgresMu.Unlock()
	var firstErr error
	for _, db := range l.postgresDBs {
		if err := db.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	l.postgresDBs = map[postgresPoolKey]*sql.DB{}
	l.postgresOrder = nil
	return firstErr
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
