package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"loom/internal/nodes"
	"loom/internal/store"
)

// `loom connector` CLI (Campaign 4): list/export/import round-trip and
// hub bundle carriage, mirroring hub_test.go's direct-call style.

func connectorCLIStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := &store.Connector{
		ID: "conn-cli", Name: "Example API",
		Operations: []store.ConnectorOperation{{
			Op: "list", Method: "GET", URL: "{{cred.baseUrl}}/things",
			Fields: map[string]nodes.Field{
				"limit": {Type: nodes.FieldNumber, Required: false},
			},
		}},
	}
	if _, err := st.CreateConnector(c, "cli-test"); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	return dir
}

func TestConnectorCLIRoundTrip(t *testing.T) {
	dir := connectorCLIStore(t)
	out := filepath.Join(t.TempDir(), "connectors.json")
	if err := runConnector([]string{"export", "-data", dir, "-out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Kind       string            `json:"kind"`
		Connectors []json.RawMessage `json:"connectors"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if file.Kind != "loom-connectors" || len(file.Connectors) != 1 {
		t.Fatalf("export = %s", raw)
	}

	fresh := t.TempDir()
	if err := runConnector([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, err := openStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.GetConnector("conn-cli")
	if err != nil {
		t.Fatalf("imported connector: %v", err)
	}
	if len(got.Operations) != 1 || got.Operations[0].Method != "GET" {
		t.Fatalf("imported operations = %#v", got.Operations)
	}

	// Re-import is a no-op.
	if err := runConnector([]string{"import", "-data", fresh, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	got2, _ := st.GetConnector("conn-cli")
	if got2.Version != got.Version {
		t.Fatalf("re-import bumped version: %d -> %d", got.Version, got2.Version)
	}
}

func TestConnectorHubBundleCarriesConnectors(t *testing.T) {
	dir := connectorCLIStore(t)
	bundlePath := filepath.Join(t.TempDir(), "hub.json")
	if err := runHub([]string{"export", "-data", dir, "-out", bundlePath}); err != nil {
		t.Fatalf("hub export: %v", err)
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle struct {
		Kind       string            `json:"kind"`
		Connectors []json.RawMessage `json:"connectors"`
	}
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	if bundle.Kind != "loom-hub" || len(bundle.Connectors) != 1 {
		t.Fatalf("hub bundle = %s", raw)
	}

	fresh := t.TempDir()
	if err := runHub([]string{"import", "-data", fresh, bundlePath}); err != nil {
		t.Fatalf("hub import: %v", err)
	}
	st, err := openStore(fresh)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.GetConnector("conn-cli"); err != nil {
		t.Fatalf("connector missing after hub import: %v", err)
	}
}
