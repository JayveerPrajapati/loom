package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"loom/internal/store"
)

// mappingCLIStore opens a data dir and seeds one mapping.
func mappingCLIStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateMapping(&store.Mapping{
		ID: "mapping-lead", Name: "Lead to customer", Description: "Normalizes leads",
		Rows: []store.MappingRow{
			{Target: "email", Source: "{{ $json.email }}", Required: true},
			{Target: "fullName", Source: "{{ $json.first }} {{ $json.last }}"},
		},
		Tags:  []string{"crm"},
		Owner: "growth-team",
	}, "cli-test"); err != nil {
		t.Fatalf("seed mapping: %v", err)
	}
	return dir
}

func TestMappingListExportImportRoundTrip(t *testing.T) {
	dir := mappingCLIStore(t)
	out := filepath.Join(dir, "mappings.json")

	if err := runMapping([]string{"list", "-data", dir}); err != nil {
		t.Fatalf("list: %v", err)
	}
	if err := runMapping([]string{"export", "-data", dir, "-out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}

	// A fresh instance imports the file and gets the mapping at v1.
	fresh := t.TempDir()
	if err := runMapping([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, err := openStore(fresh)
	if err != nil {
		t.Fatalf("open fresh store: %v", err)
	}
	defer st.Close()
	imported, err := st.GetMapping("mapping-lead")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if imported.Version != 1 || len(imported.Rows) != 2 || imported.Owner != "growth-team" {
		t.Fatalf("imported = %+v", imported)
	}
	if imported.Rows[0].Target != "email" || !imported.Rows[0].Required {
		t.Fatalf("imported rows = %+v", imported.Rows)
	}

	// Re-importing the identical file is a no-op; importing a changed one
	// appends a version.
	if err := runMapping([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	changed, _ := os.ReadFile(out)
	var file struct {
		Kind     string `json:"kind"`
		Mappings []struct {
			ID   string             `json:"id"`
			Name string             `json:"name"`
			Rows []store.MappingRow `json:"rows"`
		} `json:"mappings"`
	}
	_ = json.Unmarshal(changed, &file)
	file.Mappings[0].Rows = append(file.Mappings[0].Rows, store.MappingRow{Target: "city", Source: "{{ $json.city }}"})
	changedPath := filepath.Join(fresh, "changed.json")
	changedRaw, _ := json.Marshal(file)
	if err := os.WriteFile(changedPath, changedRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runMapping([]string{"import", "-data", fresh, changedPath}); err != nil {
		t.Fatalf("import changed: %v", err)
	}
	bumped, err := st.GetMapping("mapping-lead")
	if err != nil {
		t.Fatalf("get bumped: %v", err)
	}
	if bumped.Version != 2 || len(bumped.Rows) != 3 {
		t.Fatalf("bumped = %+v", bumped)
	}
}

func TestMappingKindRoundTripAndFilter(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.CreateMapping(&store.Mapping{
		ID: "mapping-item", Name: "Item mapping",
		Rows: []store.MappingRow{{Target: "email", Source: "{{ $json.email }}"}},
	}, "cli-test"); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if _, err := st.CreateMapping(&store.Mapping{
		ID: "mapping-q", Name: "Query mapping", Kind: store.MappingKindQuery,
		Rows: []store.MappingRow{{Target: "limit", Source: "20"}},
	}, "cli-test"); err != nil {
		t.Fatalf("seed query: %v", err)
	}
	st.Close()

	out := filepath.Join(dir, "mappings.json")
	if err := runMapping([]string{"export", "-data", dir, "-out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	var file struct {
		Mappings []struct {
			ID   string `json:"id"`
			Kind string `json:"kind"`
		} `json:"mappings"`
	}
	raw, _ := os.ReadFile(out)
	_ = json.Unmarshal(raw, &file)
	kinds := map[string]string{}
	for _, m := range file.Mappings {
		kinds[m.ID] = m.Kind
	}
	if kinds["mapping-q"] != store.MappingKindQuery {
		t.Fatalf("exported query kind = %q", kinds["mapping-q"])
	}
	if kinds["mapping-item"] != "" && kinds["mapping-item"] != store.MappingKindItem {
		t.Fatalf("exported item kind = %q", kinds["mapping-item"])
	}

	// Import preserves the kind; list -kind narrows to it.
	fresh := t.TempDir()
	if err := runMapping([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st2, err := openStore(fresh)
	if err != nil {
		t.Fatalf("open fresh: %v", err)
	}
	defer st2.Close()
	imported, err := st2.GetMapping("mapping-q")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if imported.Kind != store.MappingKindQuery {
		t.Fatalf("imported kind = %q", imported.Kind)
	}

	// A kind change in the file is a content change, not a no-op skip.
	var edit struct {
		Kind     string               `json:"kind"`
		Mappings []mappingExportEntry `json:"mappings"`
	}
	_ = json.Unmarshal(raw, &edit)
	for i := range edit.Mappings {
		if edit.Mappings[i].ID == "mapping-q" {
			edit.Mappings[i].Kind = store.MappingKindHeaders
		}
	}
	edited, _ := json.Marshal(edit)
	editedPath := filepath.Join(fresh, "edited.json")
	if err := os.WriteFile(editedPath, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runMapping([]string{"import", "-data", fresh, editedPath}); err != nil {
		t.Fatalf("import edited: %v", err)
	}
	editedMapping, _ := st2.GetMapping("mapping-q")
	if editedMapping.Kind != store.MappingKindHeaders || editedMapping.Version != 2 {
		t.Fatalf("edited mapping = %+v", editedMapping)
	}

	// Bad kinds are rejected up front.
	if err := runMapping([]string{"list", "-data", fresh, "-kind", "cookie"}); err == nil {
		t.Fatal("list with bad kind should fail")
	}
}

func TestMappingImportRejectsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "foreign.json")
	if err := os.WriteFile(path, []byte(`{"kind":"other","mappings":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runMapping([]string{"import", "-data", dir, path}); err == nil {
		t.Fatal("foreign file accepted")
	}
}
