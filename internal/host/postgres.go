package host

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	"loom/internal/flow"
)

// maxCachedPostgresDBs bounds memory and database handles independently of the
// number of distinct resolved DSNs that have ever been used.
const maxCachedPostgresDBs = 8

// postgresPoolKey identifies a cached pool. The DSN carries the complete
// connection and credential material, and allowPrivate pins the dialer guard so
// a Live whose guard setting changes never reuses a pool dialed under the old
// setting.
type postgresPoolKey struct {
	dsn          string
	allowPrivate bool
}

// postgresCredentialDSN resolves a credential into a PostgreSQL DSN. The
// fallback is the credential id itself so legacy callers that passed a DSN in
// that field keep working. DSNs built from host fields default to TLS; an
// operator must explicitly opt out with sslmode=disable.
func postgresCredentialDSN(credID string, resolve func(string) (flow.Credential, bool)) string {
	dsn := credID
	if resolve == nil {
		return dsn
	}
	cred, ok := resolve(credID)
	if !ok {
		return dsn
	}
	for _, key := range []string{"url", "dsn", "connectionString"} {
		if value, ok := cred.Data[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	if host, ok := cred.Data["host"].(string); ok && strings.TrimSpace(host) != "" {
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
			sslmode = "require"
		}
		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(user, pass),
			Host:   net.JoinHostPort(host, strconv.Itoa(port)),
			Path:   "/" + dbname,
		}
		q := u.Query()
		q.Set("sslmode", sslmode)
		u.RawQuery = q.Encode()
		return u.String()
	}
	if raw := strings.TrimSpace(cred.Raw); raw != "" {
		return raw
	}
	return dsn
}

// dsnHostPort extracts host[:port] from a PostgreSQL DSN in either form
// (postgres:// URL or keyword=value list), defaulting the port to 5432.
// It returns "" when no host can be determined — the caller treats that
// as "no destination to check" and the dial itself surfaces the error.
func dsnHostPort(dsn string) string {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return ""
	}
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		host := u.Hostname()
		if host == "" {
			return ""
		}
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		if p := u.Port(); p != "" {
			return host + ":" + p
		}
		return host + ":5432"
	}
	host := ""
	port := ""
	for _, field := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			continue
		}
		switch key {
		case "host":
			host = strings.Trim(value, "'\"")
		case "port":
			port = strings.Trim(value, "'\"")
		}
	}
	if host == "" {
		return ""
	}
	if port == "" {
		port = "5432"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return host + ":" + port
}

func (l *Live) Query(ctx context.Context, credID, sqlQuery string) ([]flow.Item, error) {
	// Defence in depth: the db.postgres node validates statements, but a
	// direct caller must not be able to send multi-statement SQL through the
	// simple query protocol either.
	if err := validateSingleSQLStatement(sqlQuery); err != nil {
		return nil, err
	}
	dsn := postgresCredentialDSN(credID, l.ResolveCredential)

	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("db.postgres: missing connection details for credential %q", credID)
	}
	// Global egress policy (D7-5): the Postgres destination must pass the
	// same operator policy as HTTP targets. Deny happens before dialing.
	if l.Egress != nil {
		if hostPort := dsnHostPort(dsn); hostPort != "" {
			dest := "postgres://" + hostPort
			if err := l.Egress.Check(dest); err != nil {
				return nil, err
			}
		}
	}

	db, err := l.postgresDB(dsn, l.AllowPrivate)
	if err != nil {
		return nil, err
	}

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

// postgresDB returns the cached pool for a resolved DSN. The DSN is the cache
// identity because it contains the complete connection and credential
// material; two credentials resolving to the same DSN can safely share it.
// Rotation therefore creates a new pool without closing one that may still be
// serving an in-flight query. The bounded LRU eventually reclaims old pools.
func (l *Live) postgresDB(dsn string, allowPrivate bool) (*sql.DB, error) {
	l.postgresMu.Lock()
	defer l.postgresMu.Unlock()

	key := postgresPoolKey{dsn: dsn, allowPrivate: allowPrivate}
	if db, ok := l.postgresDBs[key]; ok {
		l.touchPostgresLRU(key)
		return db, nil
	}
	connector, err := pq.NewConnector(dsn)
	if err != nil {
		return nil, fmt.Errorf("db.postgres connection error: %w", err)
	}
	// Apply the same private-target guard as http.request. lib/pq dials
	// without a context, so the adapter resolves once and dials the validated
	// IP directly.
	connector.Dialer(guardedDialer{allowPrivate: allowPrivate, timeout: 10 * time.Second})
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	if pgDSNDisablesTLS(dsn) {
		host := pgDSNHost(dsn)
		if host == "" {
			host = "unknown host"
		}
		log.Printf("db.postgres: TLS is explicitly disabled (sslmode=disable) for %q; traffic to it is unencrypted", host)
	}

	l.postgresDBs[key] = db
	l.postgresOrder = append(l.postgresOrder, key)
	if len(l.postgresOrder) > maxCachedPostgresDBs {
		evict := l.postgresOrder[0]
		l.postgresOrder = l.postgresOrder[1:]
		if old, ok := l.postgresDBs[evict]; ok {
			_ = old.Close()
			delete(l.postgresDBs, evict)
		}
	}
	return db, nil
}

// pgDSNDisablesTLS reports whether a DSN explicitly opts out of TLS. It
// accepts both URL form (postgres://...?sslmode=disable) and lib/pq keyword
// form (sslmode=disable, quotes optional).
func pgDSNDisablesTLS(dsn string) bool {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Query().Get("sslmode") == "disable"
	}
	for _, part := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(part, "=")
		if !ok || key != "sslmode" {
			continue
		}
		return strings.Trim(value, "'\"") == "disable"
	}
	return false
}

// pgDSNHost extracts a printable host for diagnostics without leaking any
// credential material from the DSN.
func pgDSNHost(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Scheme != "" && u.Host != "" {
		return u.Hostname()
	}
	for _, part := range strings.Fields(dsn) {
		key, value, ok := strings.Cut(part, "=")
		if ok && key == "host" {
			return strings.Trim(value, "'\"")
		}
	}
	return ""
}

func (l *Live) touchPostgresLRU(key postgresPoolKey) {
	for i, candidate := range l.postgresOrder {
		if candidate != key {
			continue
		}
		l.postgresOrder = append(l.postgresOrder[:i], l.postgresOrder[i+1:]...)
		l.postgresOrder = append(l.postgresOrder, key)
		return
	}
}
