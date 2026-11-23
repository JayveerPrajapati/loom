package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"loom/internal/schemas"
	"loom/internal/store"
)

// hubCLIStore opens a data dir seeded with one of every asset kind.
func hubCLIStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-1", Name: "HTTP default", NodeType: "http.request",
		Config: map[string]any{"method": "GET", "timeoutMs": 5000},
		Tags:   []string{"http"},
	}, "cli-test"); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.CreateDataSchema(&store.DataSchema{
		ID: "schema-1", Name: "Lead", Fields: []schemas.Field{{Name: "email", Type: schemas.TypeString, Required: true}},
	}, "cli-test"); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	if _, err := st.CreateMapping(&store.Mapping{
		ID: "mapping-1", Name: "Lead normalize",
		Rows: []store.MappingRow{{Target: "email", Source: "{{ $json.email }}", Required: true}},
	}, "cli-test"); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	if _, err := st.CreateMapping(&store.Mapping{
		ID: "mapping-query", Name: "Paging defaults", Kind: store.MappingKindQuery,
		Rows: []store.MappingRow{{Target: "limit", Source: "20"}},
	}, "cli-test"); err != nil {
		t.Fatalf("seed query mapping: %v", err)
	}
	return dir
}

func TestHubExportImportRoundTrip(t *testing.T) {
	dir := hubCLIStore(t)
	out := filepath.Join(dir, "hub.json")

	if err := runHub([]string{"export", "-data", dir, "-out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	var bundle hubBundle
	raw, _ := os.ReadFile(out)
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("parse bundle: %v", err)
	}
	if bundle.Kind != "loom-hub" {
		t.Fatalf("kind = %s", bundle.Kind)
	}
	if len(bundle.Profiles) != 1 || len(bundle.Schemas) != 1 || len(bundle.Mappings) != 2 {
		t.Fatalf("bundle = %+v", bundle)
	}
	// The query-kind mapping keeps its kind in the bundle (v3 D4).
	for _, entry := range bundle.Mappings {
		if entry.ID == "mapping-query" && entry.Kind != store.MappingKindQuery {
			t.Fatalf("mapping-query kind = %q", entry.Kind)
		}
	}

	// A fresh instance imports the whole bundle at v1 each.
	fresh := t.TempDir()
	if err := runHub([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, err := openStore(fresh)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	defer st.Close()
	for _, check := range []struct {
		label string
		err   error
	}{
		{"profile", func() error { _, err := st.GetConfigProfile("profile-1"); return err }()},
		{"schema", func() error { _, err := st.GetDataSchema("schema-1"); return err }()},
		{"mapping", func() error { _, err := st.GetMapping("mapping-1"); return err }()},
	} {
		if check.err != nil {
			t.Fatalf("%s not imported: %v", check.label, check.err)
		}
	}

	// Re-importing the identical bundle is a no-op everywhere.
	if err := runHub([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	profile, _ := st.GetConfigProfile("profile-1")
	if profile.Version != 1 {
		t.Fatalf("re-import bumped profile to v%d", profile.Version)
	}
	// The kind survives the round trip.
	query, err := st.GetMapping("mapping-query")
	if err != nil {
		t.Fatalf("get imported query mapping: %v", err)
	}
	if query.Kind != store.MappingKindQuery {
		t.Fatalf("imported query mapping kind = %q", query.Kind)
	}
}

func TestHubImportRejectsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foreign.json")
	if err := os.WriteFile(path, []byte(`{"kind":"loom-config-profiles","profiles":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runHub([]string{"import", "-data", dir, path}); err == nil {
		t.Fatal("foreign file accepted")
	}
}
