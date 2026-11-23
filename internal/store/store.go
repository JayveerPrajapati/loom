// Package store persists workflows, runs, events, blobs and credentials.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Store struct {
	// db is the single-connection writer: SQLite has one writer, and every
	// mutation (run status, events, decisions) funnels through it.
	db *sql.DB
	// readDB is the read-only pool. Its connections carry
	// PRAGMA query_only=1, so the driver itself refuses a mutation that is
	// accidentally routed there, while still being able to map the WAL
	// shared-memory index (a mode=ro connection could not, and would fail
	// to open the database at all).
	readDB *sql.DB
	// readers is the configured read pool size.
	readers int
	blobDir string
	dir     string
	// keys is the on-disk (or environment-pinned) keyring. currentKey signs
	// new runs and seals new credentials; runs keep verifying against the
	// key recorded on their row, so rotation never breaks old chains.
	keys       map[string]keyEntry
	currentKey keyEntry
	legacyKey  keyEntry
	pinned     bool
	// writeMu serializes event appends so sequence allocation and chain
	// linking stay atomic inside one process.
	writeMu sync.Mutex
	// keyMu guards the keyring maps and pointers: rotations mutate them
	// while concurrent appends and credential seals read them. Key material
	// itself is immutable once created, so readers copy the entry out.
	keyMu sync.RWMutex
	// changes wakes SSE streams waiting on this store's runs.
	changes *RunChangeNotifier
	// busy counts read queries that came back with SQLITE_BUSY-class
	// errors despite the busy timeout (they should be ~zero; a growing
	// count means contention regressed).
	busy atomic.Int64
	// readQueries counts every read query so cache and performance tests
	// can prove that lookup work scales with distinct assets, not with the
	// number of nodes that reference them.
	readQueries atomic.Int64
}

// DefaultReadPoolSize is the conservative default reader concurrency. The
// writer stays at exactly one connection.
const DefaultReadPoolSize = 4

// Open opens the store with the default read pool size.
func Open(dir string) (*Store, error) {
	return OpenWithReadPool(dir, DefaultReadPoolSize)
}

// OpenWithReadPool opens the store with an explicit read pool size. Values
// below 1 fall back to the default.
func OpenWithReadPool(dir string, readers int) (*Store, error) {
	if readers < 1 {
		readers = DefaultReadPoolSize
	}
	if err := os.MkdirAll(filepath.Join(dir, "blobs"), 0o700); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, "loom.db")
	// Open both pools through the same escaped file: URI. A raw path is
	// interpreted as a DSN by the driver, so '#' and '?' in a data
	// directory would make the writer and reader point at different files.
	dbDSN := "file:" + (&url.URL{Path: dbPath}).EscapedPath()
	db, err := sql.Open("sqlite", dbDSN)
	if err != nil {
		return nil, err
	}
	// SQLite has a single writer. Mutations are frequent (run status, events),
	// and the integration suite shows a larger pool produces SQLITE_BUSY under
	// concurrent webhook/batch load. Keep one connection until every mutation
	// is serialized; AppendEvent's writeMu already makes the seq/chain safe.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	s := &Store{db: db, blobDir: filepath.Join(dir, "blobs"), dir: dir, changes: NewRunChangeNotifier(), readers: readers}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA synchronous=NORMAL`); err != nil {
		db.Close()
		return nil, err
	}
	// initCrypto first: migrate backfills the run chain tags, which need the
	// derived MAC key. It only touches files/env, never the schema.
	if err := s.initCrypto(dir); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	// The reader pool opens only after the writer has created the database
	// and its WAL sidecar files. Per-connection pragmas travel in the DSN:
	// database/sql re-runs them on every pooled connection it opens.
	//
	// dbPath is a filesystem path, not a URI: escape '#' and '?' so a data
	// directory like "loom#prod" cannot truncate the DSN and point the read
	// pool at a different (empty) database.
	readPath := (&url.URL{Path: dbPath}).EscapedPath()
	readDB, err := sql.Open("sqlite", "file:"+readPath+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	readDB.SetMaxOpenConns(readers)
	readDB.SetMaxIdleConns(readers)
	s.readDB = readDB
	// Force one connection now so a broken read configuration surfaces at
	// Open, not on the first request.
	if err := readDB.Ping(); err != nil {
		db.Close()
		readDB.Close()
		return nil, err
	}
	return s, nil
}

// read runs a read-only query on the read pool. Reads never queue behind
// the writer's transactions: WAL readers take their own snapshot.
func (s *Store) read(query string, args ...any) (*sql.Rows, error) {
	s.readQueries.Add(1)
	rows, err := s.readDB.Query(query, args...)
	if err != nil && isBusy(err) {
		s.busy.Add(1)
	}
	return rows, err
}

// readRow runs a single-row read-only query on the read pool.
func (s *Store) readRow(query string, args ...any) *sql.Row {
	s.readQueries.Add(1)
	row := s.readDB.QueryRow(query, args...)
	if isBusy(row.Err()) {
		s.busy.Add(1)
	}
	return row
}

// BusyErrors reports how many read queries failed with SQLITE_BUSY-class
// errors since open. It should stay at or near zero.
func (s *Store) BusyErrors() int64 { return s.busy.Load() }

// ReadQueryCount reports how many read queries have been issued since open
// (or since ResetReadQueryCount). Tests use it to prove deduplication.
func (s *Store) ReadQueryCount() int64 { return s.readQueries.Load() }

// ResetReadQueryCount clears the read-query counter. Intended for tests.
func (s *Store) ResetReadQueryCount() { s.readQueries.Store(0) }

// ReadPoolSize reports the configured read pool size.
func (s *Store) ReadPoolSize() int { return s.readers }

// isBusy matches the driver's locked-database errors across the text
// variants modernc.org/sqlite produces.
func isBusy(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "database is locked") || strings.Contains(msg, "database table is locked") || strings.Contains(msg, "SQLITE_BUSY")
}

func (s *Store) Close() error {
	readErr := s.readDB.Close()
	writeErr := s.db.Close()
	// database/sql closes in-use connections asynchronously, once the query
	// holding them finishes. Wait for both pools to drain so a caller that
	// removes the data directory right after Close (tests do exactly that)
	// does not race SQLite's sidecar files and fail with "directory not
	// empty". Bounded: a stuck query delays Close by at most this long.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s.readDB.Stats().OpenConnections == 0 && s.db.Stats().OpenConnections == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if writeErr != nil {
		return writeErr
	}
	return readErr
}

// SubscribeRunChanges registers for change signals about one run; see
// RunChangeNotifier. The SSE stream waits on the channel and re-reads state
// on wake instead of polling.
func (s *Store) SubscribeRunChanges(runID string) (<-chan struct{}, func()) {
	return s.changes.Subscribe(runID)
}

func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	for name, typ := range map[string]string{
		"parent_run_id":     "TEXT",
		"parent_node_id":    "TEXT",
		"root_run_id":       "TEXT NOT NULL DEFAULT ''",
		"workflow_snapshot": "BLOB",
		"job_id":            "TEXT",
		"template_version":  "INTEGER",
		"chain_version":     "INTEGER NOT NULL DEFAULT 0",
		"chain_tag":         "TEXT NOT NULL DEFAULT ''",
		"key_id":            "TEXT",
		"effects":           "TEXT NOT NULL DEFAULT ''",
		"cassette_run_id":   "TEXT NOT NULL DEFAULT ''",
		"input":             "TEXT",
		"actor":             "TEXT NOT NULL DEFAULT ''",
		"resume_at":         "TEXT",
		"head_seq":          "INTEGER NOT NULL DEFAULT 0",
		"head_mac":          "TEXT NOT NULL DEFAULT ''",
	} {
		ok, err := s.hasColumn("runs", name)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if ok {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE runs ADD COLUMN ` + name + ` ` + typ); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}
	for name, typ := range map[string]string{
		"prev_hash": "TEXT",
		"hash":      "TEXT",
		"actor":     "TEXT NOT NULL DEFAULT ''",
	} {
		ok, err := s.hasColumn("events", name)
		if err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
		if ok {
			continue
		}
		if _, err := s.db.Exec(`ALTER TABLE events ADD COLUMN ` + name + ` ` + typ); err != nil {
			return fmt.Errorf("migrate %s: %w", name, err)
		}
	}
	ok, err := s.hasColumn("workflows", "published_version")
	if err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if !ok {
		if _, err := s.db.Exec(`ALTER TABLE workflows ADD COLUMN published_version INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("migrate published_version: %w", err)
		}
	}
	if ok, err := s.hasColumn("runs", "resume_expires_at"); err != nil {
		return fmt.Errorf("migrate resume_expires_at: %w", err)
	} else if !ok {
		if _, err := s.db.Exec(`ALTER TABLE runs ADD COLUMN resume_expires_at TEXT`); err != nil {
			return fmt.Errorf("migrate resume_expires_at: %w", err)
		}
	}
	// Configuration Hub v3 (D4): mappings carry the parameter group their
	// rows compile into; pre-v3 mapping rows are item mappings. The index
	// is created here, after the column exists on every database.
	if ok, err := s.hasColumn("mappings", "kind"); err != nil {
		return fmt.Errorf("migrate mapping kind: %w", err)
	} else if !ok {
		if _, err := s.db.Exec(`ALTER TABLE mappings ADD COLUMN kind TEXT NOT NULL DEFAULT 'item'`); err != nil {
			return fmt.Errorf("migrate mapping kind: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_mappings_kind ON mappings(kind)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Profile-Centric UX Phase 1 (D-P0c): every existing
	// profile/mapping/schema becomes shared with no creator — only new
	// assets default to private. The hasColumn dance keeps pre-Phase-1
	// data directories upgrading in place.
	for _, table := range []string{"config_profiles", "mappings", "data_schemas"} {
		for _, col := range []string{"visibility TEXT NOT NULL DEFAULT 'shared'", "creator_id TEXT NOT NULL DEFAULT ''"} {
			name := strings.SplitN(col, " ", 2)[0]
			ok, err := s.hasColumn(table, name)
			if err != nil {
				return fmt.Errorf("migrate %s.%s: %w", table, name, err)
			}
			if ok {
				continue
			}
			if _, err := s.db.Exec(`ALTER TABLE ` + table + ` ADD COLUMN ` + col); err != nil {
				return fmt.Errorf("migrate %s.%s: %w", table, name, err)
			}
		}
	}
	// Stamp the signing key onto runs that predate the keyring before the
	// chain-tag backfill runs; backfill itself resolves per-row keys.
	if err := s.stampLegacyKeyIDs(); err != nil {
		return fmt.Errorf("migrate key ids: %w", err)
	}
	if err := s.backfillChainTags(); err != nil {
		return fmt.Errorf("migrate chain tags: %w", err)
	}
	if err := s.sealChainHeads(); err != nil {
		return fmt.Errorf("migrate chain heads: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_parent ON runs(parent_run_id)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_job ON runs(job_id)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_root ON runs(root_run_id)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// The wait scheduler reads the earliest pending wakeup and DueWakeups
	// scans due ones; the composite index keeps both off a full runs scan as
	// the run history grows.
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_waiting_resume ON runs(status, resume_at)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Queue admission and per-workflow history: OldestQueuedRun, AdmitRun,
	// CountActiveRuns and ListRuns all filter by workflow and status before
	// ordering by recency.
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_workflow_status_started ON runs(workflow_id, status, started_at)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Workspace-wide status views (waiting badge, stale-run recovery) filter
	// by status alone and order by recency.
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_runs_status_started ON runs(status, started_at)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	// Prune's blob reference scan reads only node.effects data rows; the index
	// keeps that lookup off a full table scan as the event log grows.
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_events_type ON events(type)`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if err := s.initDecisions(); err != nil {
		return err
	}
	if _, err := s.db.Exec(authSchema); err != nil {
		return fmt.Errorf("migrate auth: %w", err)
	}
	// Existing databases keep the example workflows they were seeded with.
	// Upgrade the stored copies so retired node types do not reappear on the
	// canvas after the catalog drops them.
	if err := s.refreshBundledExamples(); err != nil {
		return fmt.Errorf("migrate examples: %w", err)
	}
	// Profile ownership is migrated explicitly by `loom ownership migrate`;
	// opening a data directory must never rewrite drafts. The legacy
	// equality branch keeps un-migrated drafts working for one release.
	if _, err := s.db.Exec(`UPDATE schema_version SET version = 8`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// initCrypto resolves the signing/encryption keys. LOOM_SECRET and
// LOOM_SECRET_KEY_FILE pin a single key that cannot be rotated in place;
// otherwise the on-disk keyring under <dir>/keys/ is used, bootstrapped from
// a pre-keyring secret.key if one exists.
func (s *Store) initCrypto(dir string) error {
	if secret := []byte(os.Getenv("LOOM_SECRET")); len(secret) > 0 {
		return s.initPinnedKey(secret, "env")
	}
	if keyPath := os.Getenv("LOOM_SECRET_KEY_FILE"); keyPath != "" {
		data, err := os.ReadFile(keyPath)
		if err != nil {
			return fmt.Errorf("LOOM_SECRET_KEY_FILE: %w", err)
		}
		return s.initPinnedKey(data, "external")
	}
	return s.initKeyring(dir)
}

// ---------- workflows ----------

func marshalMap(value map[string]any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// ---------- runs ----------

type scanner interface {
	Scan(dest ...any) error
}

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

const authSchema = `
CREATE TABLE IF NOT EXISTS users (
	id TEXT PRIMARY KEY,
	email TEXT NOT NULL UNIQUE COLLATE NOCASE,
	name TEXT NOT NULL DEFAULT '',
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL,
	disabled INTEGER NOT NULL DEFAULT 0,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	password_changed_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_seen_at TEXT NOT NULL,
	expires_at TEXT NOT NULL,
	ip TEXT NOT NULL DEFAULT '',
	user_agent TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);

CREATE TABLE IF NOT EXISTS api_tokens (
	id TEXT PRIMARY KEY,
	user_id TEXT NOT NULL,
	name TEXT NOT NULL,
	token_hash TEXT NOT NULL UNIQUE,
	role TEXT NOT NULL,
	created_at TEXT NOT NULL,
	last_used_at TEXT,
	expires_at TEXT,
	revoked_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_user ON api_tokens(user_id);

CREATE TABLE IF NOT EXISTS auth_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	at TEXT NOT NULL,
	type TEXT NOT NULL,
	user_id TEXT NOT NULL DEFAULT '',
	actor TEXT NOT NULL DEFAULT '',
	ip TEXT NOT NULL DEFAULT '',
	detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_auth_events_at ON auth_events(at);
`

const schema = `
CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL);
INSERT INTO schema_version (version) SELECT 1 WHERE NOT EXISTS (SELECT 1 FROM schema_version);

CREATE TABLE IF NOT EXISTS leases (
	name TEXT PRIMARY KEY,
	owner TEXT NOT NULL,
	expires_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS workflows (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	version INTEGER NOT NULL,
	published_version INTEGER NOT NULL DEFAULT 0,
	doc BLOB NOT NULL,
	updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workflow_versions (
	workflow_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	published_at TEXT NOT NULL,
	PRIMARY KEY (workflow_id, version)
);
CREATE INDEX IF NOT EXISTS idx_workflow_versions_workflow ON workflow_versions(workflow_id, version DESC);

-- Explicit loom ownership migrate --apply safety net: one row per changed
-- node, with the exact workflow docs before and after the migration so a
-- rollback can restore bytes and refuse when a draft changed afterwards.
CREATE TABLE IF NOT EXISTS ownership_migration_backup (
	migration_id TEXT NOT NULL,
	workflow_id TEXT NOT NULL,
	node_id TEXT NOT NULL,
	original_node TEXT NOT NULL,
	original_doc BLOB NOT NULL,
	migrated_doc BLOB NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (migration_id, workflow_id, node_id)
);
CREATE INDEX IF NOT EXISTS idx_ownership_migration_backup_created ON ownership_migration_backup(created_at DESC);

CREATE TABLE IF NOT EXISTS jobs (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	template_id TEXT NOT NULL,
	template_version INTEGER NOT NULL,
	enabled INTEGER NOT NULL DEFAULT 1,
	trigger_type TEXT NOT NULL DEFAULT 'manual',
	input_defaults BLOB,
	trigger_config BLOB,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_jobs_template ON jobs(template_id);

CREATE TABLE IF NOT EXISTS runs (
	id TEXT PRIMARY KEY,
	workflow_id TEXT NOT NULL,
	workflow_version TEXT NOT NULL,
	status TEXT NOT NULL,
	trigger TEXT NOT NULL,
	started_at TEXT NOT NULL,
	finished_at TEXT,
	error TEXT,
	resume_node TEXT,
	resume_token TEXT,
	resume_data BLOB,
	resume_at TEXT,
	resume_expires_at TEXT,
	head_seq INTEGER NOT NULL DEFAULT 0,
	head_mac TEXT NOT NULL DEFAULT '',
	parent_run_id TEXT,
	parent_node_id TEXT,
	root_run_id TEXT NOT NULL DEFAULT '',
	workflow_snapshot BLOB,
	job_id TEXT,
	template_version INTEGER,
	chain_version INTEGER NOT NULL DEFAULT 1,
	actor TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_runs_workflow ON runs(workflow_id);

CREATE TABLE IF NOT EXISTS events (
	run_id TEXT NOT NULL,
	seq INTEGER NOT NULL,
	type TEXT NOT NULL,
	at TEXT NOT NULL,
	node_id TEXT,
	attempt INTEGER,
	input_hash TEXT,
	output_hash TEXT,
	duration_ms INTEGER,
	error TEXT,
	data BLOB,
	prev_hash TEXT,
	hash TEXT,
	actor TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (run_id, seq)
);

CREATE TABLE IF NOT EXISTS blobs (
	hash TEXT PRIMARY KEY,
	size INTEGER NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS credentials (
	id TEXT PRIMARY KEY,
	type TEXT NOT NULL,
	data BLOB NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS config_profiles (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	node_type TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	current_version INTEGER NOT NULL DEFAULT 0,
	visibility TEXT NOT NULL DEFAULT 'shared',
	creator_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_config_profiles_node_type ON config_profiles(node_type);

CREATE TABLE IF NOT EXISTS config_profile_versions (
	profile_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (profile_id, version)
);

CREATE TABLE IF NOT EXISTS config_profile_usage (
	profile_id TEXT NOT NULL,
	profile_version INTEGER NOT NULL,
	workflow_id TEXT NOT NULL,
	workflow_version INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (profile_id, profile_version, workflow_id, workflow_version)
);
CREATE INDEX IF NOT EXISTS idx_config_profile_usage_workflow ON config_profile_usage(workflow_id);

CREATE TABLE IF NOT EXISTS profile_bundle_refs (
	profile_id TEXT NOT NULL,
	asset_kind TEXT NOT NULL,
	asset_id TEXT NOT NULL,
	PRIMARY KEY (profile_id, asset_kind, asset_id)
);
CREATE INDEX IF NOT EXISTS idx_profile_bundle_refs_asset ON profile_bundle_refs(asset_id);

CREATE TABLE IF NOT EXISTS data_schemas (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	current_version INTEGER NOT NULL DEFAULT 0,
	visibility TEXT NOT NULL DEFAULT 'shared',
	creator_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_data_schemas_name ON data_schemas(name);

CREATE TABLE IF NOT EXISTS mappings (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	kind TEXT NOT NULL DEFAULT 'item',
	description TEXT NOT NULL DEFAULT '',
	current_version INTEGER NOT NULL DEFAULT 0,
	visibility TEXT NOT NULL DEFAULT 'shared',
	creator_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_mappings_name ON mappings(name);

CREATE TABLE IF NOT EXISTS mapping_versions (
	mapping_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (mapping_id, version)
);

CREATE TABLE IF NOT EXISTS data_schema_versions (
	schema_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (schema_id, version)
);

CREATE TABLE IF NOT EXISTS data_schema_usage (
	schema_id TEXT NOT NULL,
	schema_version INTEGER NOT NULL,
	workflow_id TEXT NOT NULL,
	workflow_version INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (schema_id, schema_version, workflow_id, workflow_version)
);
CREATE INDEX IF NOT EXISTS idx_data_schema_usage_workflow ON data_schema_usage(workflow_id);

CREATE TABLE IF NOT EXISTS mapping_usage (
	mapping_id TEXT NOT NULL,
	mapping_version INTEGER NOT NULL,
	workflow_id TEXT NOT NULL,
	workflow_version INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (mapping_id, mapping_version, workflow_id, workflow_version)
);
CREATE INDEX IF NOT EXISTS idx_mapping_usage_workflow ON mapping_usage(workflow_id);

CREATE TABLE IF NOT EXISTS scripts (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	language TEXT NOT NULL DEFAULT 'javascript',
	description TEXT NOT NULL DEFAULT '',
	current_version INTEGER NOT NULL DEFAULT 0,
	visibility TEXT NOT NULL DEFAULT 'shared',
	creator_id TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scripts_name ON scripts(name);
CREATE INDEX IF NOT EXISTS idx_scripts_language ON scripts(language);

CREATE TABLE IF NOT EXISTS script_versions (
	script_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (script_id, version)
);

CREATE TABLE IF NOT EXISTS script_usage (
	script_id TEXT NOT NULL,
	script_version INTEGER NOT NULL,
	workflow_id TEXT NOT NULL,
	workflow_version INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (script_id, script_version, workflow_id, workflow_version)
);
CREATE INDEX IF NOT EXISTS idx_script_usage_workflow ON script_usage(workflow_id);

CREATE TABLE IF NOT EXISTS connectors (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	description TEXT NOT NULL DEFAULT '',
	current_version INTEGER NOT NULL DEFAULT 1,
	visibility TEXT NOT NULL DEFAULT 'shared',
	creator_id TEXT NOT NULL DEFAULT '',
	tags TEXT NOT NULL DEFAULT '[]',
	owner TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_connectors_name ON connectors(name);

CREATE TABLE IF NOT EXISTS connector_versions (
	connector_id TEXT NOT NULL,
	version INTEGER NOT NULL,
	doc BLOB NOT NULL,
	content_hash TEXT NOT NULL,
	actor TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	PRIMARY KEY (connector_id, version)
);

CREATE TABLE IF NOT EXISTS connector_usage (
	connector_id TEXT NOT NULL,
	connector_version INTEGER NOT NULL,
	workflow_id TEXT NOT NULL,
	workflow_version INTEGER NOT NULL,
	created_at TEXT NOT NULL,
	PRIMARY KEY (connector_id, connector_version, workflow_id, workflow_version)
);
CREATE INDEX IF NOT EXISTS idx_connector_usage_workflow ON connector_usage(workflow_id);
`
