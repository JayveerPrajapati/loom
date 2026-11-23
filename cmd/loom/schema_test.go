package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"loom/internal/schemas"
	"loom/internal/store"
)

// schemaCLIStore opens a data dir and seeds one contract.
func schemaCLIStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateDataSchema(&store.DataSchema{
		ID: "schema-lead", Name: "Inbound lead", Description: "Webhook lead payload",
		Fields: []schemas.Field{
			{Name: "email", Type: schemas.TypeString, Required: true},
			{Name: "name", Type: schemas.TypeString},
			{Name: "tags", Type: schemas.TypeArray, Items: &schemas.Field{Name: "tag", Type: schemas.TypeString}},
		},
		Tags:  []string{"webhook"},
		Owner: "growth-team",
	}, "cli-test"); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return dir
}

func TestSchemaListExportImportRoundTrip(t *testing.T) {
	dir := schemaCLIStore(t)
	out := filepath.Join(dir, "schemas.json")

	if err := runSchema([]string{"export", "--data", dir, "--out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Kind    string `json:"kind"`
		Schemas []struct {
			ID     string          `json:"id"`
			Owner  string          `json:"owner"`
			Tags   []string        `json:"tags"`
			Fields []schemas.Field `json:"fields"`
		} `json:"schemas"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if file.Kind != "loom-data-schemas" || len(file.Schemas) != 1 {
		t.Fatalf("export = %s", raw)
	}
	if file.Schemas[0].ID != "schema-lead" || file.Schemas[0].Owner != "growth-team" ||
		len(file.Schemas[0].Tags) != 1 || len(file.Schemas[0].Fields) != 3 {
		t.Fatalf("export entry = %+v", file.Schemas[0])
	}
	if file.Schemas[0].Fields[2].Items == nil {
		t.Fatalf("array items lost in export: %+v", file.Schemas[0].Fields[2])
	}

	// Import into a second, empty instance.
	other := t.TempDir()
	if err := runSchema([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, err := openStore(other)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	imported, err := st.GetDataSchema("schema-lead")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if imported.Owner != "growth-team" || len(imported.Fields) != 3 ||
		imported.Fields[2].Items == nil || imported.Fields[2].Items.Type != schemas.TypeString {
		t.Fatalf("imported schema = %+v", imported)
	}

	// Re-importing the same content is a no-op.
	if err := runSchema([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	again, _ := st.GetDataSchema("schema-lead")
	if again.Version != 1 {
		t.Fatalf("unchanged re-import bumped version to %d", again.Version)
	}

	// A modified file imports as a new version.
	var mutated struct {
		Kind    string           `json:"kind"`
		Schemas []map[string]any `json:"schemas"`
	}
	_ = json.Unmarshal(raw, &mutated)
	mutated.Schemas[0]["name"] = "Inbound lead v2"
	mutatedRaw, _ := json.Marshal(mutated)
	mutatedPath := filepath.Join(dir, "schemas-v2.json")
	if err := os.WriteFile(mutatedPath, mutatedRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSchema([]string{"import", "--data", other, mutatedPath}); err != nil {
		t.Fatalf("import v2: %v", err)
	}
	bumped, _ := st.GetDataSchema("schema-lead")
	if bumped.Version != 2 || bumped.Name != "Inbound lead v2" {
		t.Fatalf("bumped schema = v%d %q", bumped.Version, bumped.Name)
	}
}

func TestSchemaImportRejectsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	bogus := filepath.Join(dir, "bogus.json")
	if err := os.WriteFile(bogus, []byte(`{"kind":"something-else","schemas":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSchema([]string{"import", "--data", dir, bogus}); err == nil {
		t.Fatal("import accepted a foreign file")
	}
	// A valid kind but an invalid contract is rejected too.
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, []byte(`{"kind":"loom-data-schemas","schemas":[{"id":"x","name":"X","fields":[{"name":"bad!","type":"string"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runSchema([]string{"import", "--data", dir, invalid}); err == nil {
		t.Fatal("import accepted an invalid contract")
	}
}
