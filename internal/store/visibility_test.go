package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// Profile-Centric UX Phase 1 (P0-3, D-P0a/D-P0b/D-P0c): governance columns
// on the three asset tables — visibility and creator — plus the
// share/unshare store methods and the in-place migration for pre-Phase-1
// data directories.

func TestAssetVisibilityRoundTrip(t *testing.T) {
	st := testProfileStore(t)

	p, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "p1", Name: "P", NodeType: "http.request", Config: map[string]any{},
		Visibility: VisibilityPrivate, CreatorID: "user-1",
	}, "user-1")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if p.Visibility != VisibilityPrivate || p.CreatorID != "user-1" {
		t.Fatalf("created = %s/%s", p.Visibility, p.CreatorID)
	}
	got, err := st.GetConfigProfile("p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Visibility != VisibilityPrivate || got.CreatorID != "user-1" {
		t.Fatalf("got = %s/%s", got.Visibility, got.CreatorID)
	}

	m, err := st.CreateMapping(&Mapping{
		ID: "m1", Name: "M", Kind: "query", Rows: []MappingRow{},
		Visibility: VisibilityPrivate, CreatorID: "user-1",
	}, "user-1")
	if err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if m.Visibility != VisibilityPrivate {
		t.Fatalf("mapping visibility = %s", m.Visibility)
	}
	mgot, err := st.GetMapping("m1")
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	if mgot.Visibility != VisibilityPrivate || mgot.CreatorID != "user-1" {
		t.Fatalf("mapping got = %s/%s", mgot.Visibility, mgot.CreatorID)
	}

	_, err = st.CreateDataSchema(&DataSchema{
		ID: "s1", Name: "S",
		Visibility: VisibilityShared, CreatorID: "user-2",
	}, "user-2")
	if err != nil {
		t.Fatalf("create schema: %v", err)
	}
	sgot, err := st.GetDataSchema("s1")
	if err != nil {
		t.Fatalf("get schema: %v", err)
	}
	if sgot.Visibility != VisibilityShared || sgot.CreatorID != "user-2" {
		t.Fatalf("schema got = %s/%s", sgot.Visibility, sgot.CreatorID)
	}
}

func TestAssetVisibilityUnsetNormalizesShared(t *testing.T) {
	st := testProfileStore(t)
	p, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "p2", Name: "P", NodeType: "http.request", Config: map[string]any{},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if p.Visibility != VisibilityShared {
		t.Fatalf("empty visibility = %q, want shared", p.Visibility)
	}
}

func TestAssetShareUnshare(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "p3", Name: "P", NodeType: "http.request", Config: map[string]any{},
		Visibility: VisibilityPrivate, CreatorID: "user-1",
	}, "user-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.CreateMapping(&Mapping{ID: "m3", Name: "M", Rows: []MappingRow{}}, "user-1"); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if _, err := st.CreateDataSchema(&DataSchema{ID: "s3", Name: "S"}, "user-1"); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	// Sharing never creates a version.
	if err := st.SetConfigProfileVisibility("p3", VisibilityShared); err != nil {
		t.Fatalf("share profile: %v", err)
	}
	got, _ := st.GetConfigProfile("p3")
	if got.Version != 1 || got.Visibility != VisibilityShared {
		t.Fatalf("profile = v%d/%s, want v1/shared", got.Version, got.Visibility)
	}
	if err := st.SetConfigProfileVisibility("p3", VisibilityPrivate); err != nil {
		t.Fatalf("unshare profile: %v", err)
	}
	got, _ = st.GetConfigProfile("p3")
	if got.Visibility != VisibilityPrivate {
		t.Fatalf("profile visibility = %s", got.Visibility)
	}

	if err := st.SetMappingVisibility("m3", VisibilityPrivate); err != nil {
		t.Fatalf("share mapping: %v", err)
	}
	mgot, _ := st.GetMapping("m3")
	if mgot.Visibility != VisibilityPrivate {
		t.Fatalf("mapping visibility = %s", mgot.Visibility)
	}
	if err := st.SetDataSchemaVisibility("s3", VisibilityPrivate); err != nil {
		t.Fatalf("share schema: %v", err)
	}
	sgot, _ := st.GetDataSchema("s3")
	if sgot.Visibility != VisibilityPrivate {
		t.Fatalf("schema visibility = %s", sgot.Visibility)
	}

	// Unknown ids surface the not-found errors; invalid values are
	// rejected without touching the row.
	if err := st.SetConfigProfileVisibility("nope", VisibilityShared); err != ErrConfigProfileNotFound {
		t.Fatalf("missing profile err = %v", err)
	}
	if err := st.SetMappingVisibility("m3", "friends"); err != ErrInvalidVisibility {
		t.Fatalf("invalid visibility err = %v", err)
	}
	mgot, _ = st.GetMapping("m3")
	if mgot.Visibility != VisibilityPrivate {
		t.Fatalf("mapping visibility changed on invalid input: %s", mgot.Visibility)
	}
}

func TestAssetVisibilityMigration(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "loom.db"))
	if err != nil {
		t.Fatal(err)
	}
	// The pre-Phase-1 schema: every current table minus the two
	// governance columns on the three asset tables.
	old := strings.NewReplacer(
		"\n\tvisibility TEXT NOT NULL DEFAULT 'shared',", "",
		"\n\tcreator_id TEXT NOT NULL DEFAULT '',", "",
	).Replace(schema)
	if _, err := db.Exec(old); err != nil {
		t.Fatalf("old schema: %v", err)
	}
	at := "2026-11-12T00:00:00Z"
	for _, stmt := range []string{
		`INSERT INTO config_profiles (id, name, node_type, description, current_version, created_at, updated_at) VALUES ('p-old','P','http.request','',1,?,?)`,
		`INSERT INTO config_profile_versions (profile_id, version, doc, content_hash, actor, created_at) VALUES ('p-old', 1, '{}', 'h', 'user-1', ?)`,
		`INSERT INTO mappings (id, name, kind, description, current_version, created_at, updated_at) VALUES ('m-old','M','item','',1,?,?)`,
		`INSERT INTO mapping_versions (mapping_id, version, doc, content_hash, actor, created_at) VALUES ('m-old', 1, '{}', 'h', 'user-1', ?)`,
		`INSERT INTO data_schemas (id, name, description, current_version, created_at, updated_at) VALUES ('s-old','S','',1,?,?)`,
		`INSERT INTO data_schema_versions (schema_id, version, doc, content_hash, actor, created_at) VALUES ('s-old', 1, '{}', 'h', 'user-1', ?)`,
	} {
		if _, err := db.Exec(stmt, at, at); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st.Close()

	// D-P0c: existing assets upgrade to shared with no creator.
	p, err := st.GetConfigProfile("p-old")
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if p.Visibility != VisibilityShared || p.CreatorID != "" {
		t.Fatalf("legacy profile = %s/%s, want shared/empty", p.Visibility, p.CreatorID)
	}
	m, err := st.GetMapping("m-old")
	if err != nil {
		t.Fatalf("get mapping: %v", err)
	}
	if m.Visibility != VisibilityShared || m.CreatorID != "" {
		t.Fatalf("legacy mapping = %s/%s", m.Visibility, m.CreatorID)
	}
	s, err := st.GetDataSchema("s-old")
	if err != nil {
		t.Fatalf("get schema: %v", err)
	}
	if s.Visibility != VisibilityShared || s.CreatorID != "" {
		t.Fatalf("legacy schema = %s/%s", s.Visibility, s.CreatorID)
	}

	// And a legacy asset can be promoted/demoted afterwards.
	if err := st.SetConfigProfileVisibility("p-old", VisibilityPrivate); err != nil {
		t.Fatalf("share legacy: %v", err)
	}
	p, _ = st.GetConfigProfile("p-old")
	if p.Visibility != VisibilityPrivate {
		t.Fatalf("after share = %s", p.Visibility)
	}
}
