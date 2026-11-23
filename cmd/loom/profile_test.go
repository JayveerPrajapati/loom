package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"loom/internal/graph"
	"loom/internal/schemas"
	"loom/internal/store"
)

// profileCLIStore opens a data dir, seeds one profile plus a workflow that
// references it, and returns the paths the CLI tests need.
func profileCLIStore(t *testing.T) (dir string, wf *graph.Workflow) {
	t.Helper()
	dir = t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.SaveCredential("shared-http", "http", []byte(`{"token":"x"}`)); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-stripe", Name: "Stripe Production API", NodeType: "http.request",
		Description:    "Shared defaults for the Stripe API",
		Config:         map[string]any{"method": "POST", "url": "https://api.stripe.com/v1/charges", "timeout": 30.0},
		CredentialRefs: map[string]string{"http": "shared-http"},
		Retry:          &graph.RetryPolicy{MaxAttempts: 4, BackoffMs: 250},
		Tags:           []string{"payments", "prod"},
		Owner:          "payments-team",
	}, "cli-test"); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	wf = &graph.Workflow{
		ID: "wf-export-me", Name: "Charges sync", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Position: graph.Position{}, Config: map[string]any{}},
			{ID: "http", Type: "http.request", TypeVersion: 1, Name: "Create charge", Position: graph.Position{X: 260},
				Config:      map[string]any{"method": "POST", "url": "https://api.stripe.com/v1/charges"},
				Credentials: map[string]string{"http": "none"}, ProfileRef: "profile-stripe"},
		},
		Edges: []graph.Edge{{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "http", Port: "main"}}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	return dir, wf
}

// Phase 1 P0-1: bundles round-trip through profile export/import, a
// changed bundle is new content (version bump), and imported profiles are
// creator-less — they behave as shared (D-P0a).
func TestProfileBundleExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateMapping(&store.Mapping{ID: "m-q", Name: "Paging", Kind: store.MappingKindQuery,
		Rows: []store.MappingRow{{Target: "limit", Source: "20"}}}, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateDataSchema(&store.DataSchema{ID: "sch-in", Name: "Payload"}, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-bundled", Name: "Bundled", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
		Bundle: &store.ProfileBundle{
			Mappings: map[string]string{"query": "m-q"},
			Schemas:  map[string]string{"input": "sch-in"},
		},
	}, "cli"); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "profiles.json")
	if err := runProfile([]string{"export", "--data", dir, "--out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Profiles []struct {
			Bundle *store.ProfileBundle `json:"bundle"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if len(file.Profiles) != 1 || file.Profiles[0].Bundle == nil ||
		file.Profiles[0].Bundle.Mappings["query"] != "m-q" {
		t.Fatalf("export bundle = %+v", file.Profiles)
	}

	other := t.TempDir()
	if err := runProfile([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st2, err := openStore(other)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	imported, err := st2.GetConfigProfile("profile-bundled")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if imported.Bundle == nil || imported.Bundle.Mappings["query"] != "m-q" ||
		imported.Bundle.Schemas["input"] != "sch-in" {
		t.Fatalf("imported bundle = %+v", imported.Bundle)
	}
	if imported.Visibility != store.VisibilityShared || imported.CreatorID != "" {
		t.Fatalf("imported profile visibility=%q creator=%q, want shared and empty", imported.Visibility, imported.CreatorID)
	}

	// The bundle refs index followed the import: the mapping is guarded.
	if err := st2.DeleteMapping("m-q"); err == nil {
		t.Fatal("delete guarded mapping after import: expected error")
	}

	// Re-import unchanged: no version bump.
	if err := runProfile([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	again, _ := st2.GetConfigProfile("profile-bundled")
	if again.Version != 1 {
		t.Fatalf("unchanged re-import bumped to v%d", again.Version)
	}

	// A changed bundle is new content.
	var mutated struct {
		Kind     string           `json:"kind"`
		Profiles []map[string]any `json:"profiles"`
	}
	_ = json.Unmarshal(raw, &mutated)
	mutated.Profiles[0]["bundle"] = map[string]any{
		"mappings": map[string]any{"query": "sch-in"}, // wrong content, still a change
	}
	mutatedRaw, _ := json.Marshal(mutated)
	mutatedPath := filepath.Join(other, "profiles-v2.json")
	if err := os.WriteFile(mutatedPath, mutatedRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runProfile([]string{"import", "--data", other, mutatedPath}); err != nil {
		t.Fatalf("import v2: %v", err)
	}
	bumped, _ := st2.GetConfigProfile("profile-bundled")
	if bumped.Version != 2 {
		t.Fatalf("bundle change did not bump version (%d)", bumped.Version)
	}
}

func TestProfileListExportImportRoundTrip(t *testing.T) {
	dir, _ := profileCLIStore(t)
	out := filepath.Join(dir, "profiles.json")

	if err := runProfile([]string{"export", "--data", dir, "--out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Kind     string `json:"kind"`
		Profiles []struct {
			ID    string   `json:"id"`
			Tags  []string `json:"tags"`
			Owner string   `json:"owner"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if file.Kind != "loom-config-profiles" || len(file.Profiles) != 1 {
		t.Fatalf("export = %s", raw)
	}
	if file.Profiles[0].ID != "profile-stripe" || file.Profiles[0].Owner != "payments-team" ||
		len(file.Profiles[0].Tags) != 2 {
		t.Fatalf("export entry = %+v", file.Profiles[0])
	}

	// Import into a second, empty instance.
	other := t.TempDir()
	if err := runProfile([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, err := openStore(other)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	imported, err := st.GetConfigProfile("profile-stripe")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if imported.Owner != "payments-team" || imported.Retry == nil || imported.Retry.MaxAttempts != 4 {
		t.Fatalf("imported profile = %+v", imported)
	}
	if imported.Config["url"] != "https://api.stripe.com/v1/charges" {
		t.Fatalf("imported config = %#v", imported.Config)
	}

	// Re-importing the same content is a no-op.
	if err := runProfile([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	again, _ := st.GetConfigProfile("profile-stripe")
	if again.Version != 1 {
		t.Fatalf("unchanged re-import bumped version to %d", again.Version)
	}

	// A modified file imports as a new version.
	var mutated struct {
		Kind     string           `json:"kind"`
		Profiles []map[string]any `json:"profiles"`
	}
	_ = json.Unmarshal(raw, &mutated)
	mutated.Profiles[0]["name"] = "Stripe Production API v2"
	mutatedRaw, _ := json.Marshal(mutated)
	mutatedPath := filepath.Join(dir, "profiles-v2.json")
	if err := os.WriteFile(mutatedPath, mutatedRaw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runProfile([]string{"import", "--data", other, mutatedPath}); err != nil {
		t.Fatalf("import v2: %v", err)
	}
	bumped, _ := st.GetConfigProfile("profile-stripe")
	if bumped.Version != 2 || bumped.Name != "Stripe Production API v2" {
		t.Fatalf("bumped profile = v%d %q", bumped.Version, bumped.Name)
	}
}

func TestWorkflowExportIsSelfContained(t *testing.T) {
	dir, wf := profileCLIStore(t)
	// A node can carry ownership markers for its profile; the export drops
	// them with the reference so the imported draft has no dangling intent.
	seed, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	wf.Nodes[1].ProfileOverrides = map[string]bool{"timeout": true}
	if err := seed.SaveWorkflow(wf); err != nil {
		t.Fatalf("seed ownership: %v", err)
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("close seed store: %v", err)
	}
	out := filepath.Join(dir, "wf.json")
	if err := runExport([]string{"--data", dir, "--out", out, "wf-export-me"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var exported graph.Workflow
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	node := exported.Nodes[1]
	if node.ProfileRef != "" {
		t.Fatalf("export still carries profileRef %q — the artifact is not self-contained", node.ProfileRef)
	}
	if node.ProfileOverrides != nil {
		t.Fatalf("export still carries ownership markers without a profileRef: %#v", node.ProfileOverrides)
	}
	// The profile's values arrived inline: its retry policy and the
	// profile-supplied timeout override (node had none).
	if node.Retry == nil || node.Retry.MaxAttempts != 4 {
		t.Fatalf("exported retry = %#v", node.Retry)
	}
	if node.Config["timeout"] != 30.0 {
		t.Fatalf("exported config = %#v", node.Config)
	}
}

// Phase 1 P0-2: loom export resolves bundle content too — mapping rows
// and schema fields travel inline, and every asset id is stripped.
func TestWorkflowExportResolvesBundlesSelfContained(t *testing.T) {
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateMapping(&store.Mapping{ID: "m-item", Name: "Identity", Kind: store.MappingKindItem,
		Rows: []store.MappingRow{{Target: "email", Source: "{{ $json.email }}"}}}, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateDataSchema(&store.DataSchema{ID: "sch-payload", Name: "Payload",
		Fields: []schemas.Field{{Name: "email", Type: "string"}}}, "cli"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-map", Name: "Mapper", NodeType: "data.map",
		Bundle: &store.ProfileBundle{
			Mappings: map[string]string{"item": "m-item"},
			Schemas:  map[string]string{"input": "sch-payload"},
		},
	}, "cli"); err != nil {
		t.Fatal(err)
	}
	wf := &graph.Workflow{
		ID: "wf-bundle-export", Name: "Bundle export", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{ID: "map", Type: "data.map", TypeVersion: 1, Config: map[string]any{}, ProfileRef: "profile-map"},
		},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}

	out := filepath.Join(dir, "wf.json")
	if err := runExport([]string{"--data", dir, "--out", out, wf.ID}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var exported graph.Workflow
	if err := json.Unmarshal(raw, &exported); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	node := exported.Nodes[1]
	if node.ProfileRef != "" {
		t.Fatalf("export still carries profileRef %q", node.ProfileRef)
	}
	// Like published snapshots: refs stay for traceability, content rides
	// inline.
	if got, _ := node.Config["mappingRef"].(string); got != "m-item" {
		t.Fatalf("exported mappingRef = %q", got)
	}
	rows, _ := node.Config["mappingRows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("exported mappingRows = %#v", node.Config["mappingRows"])
	}
	if node.InputSchemaRef != "sch-payload" || len(node.InputSchemaFields) != 1 {
		t.Fatalf("exported input schema = %q fields=%#v", node.InputSchemaRef, node.InputSchemaFields)
	}

	// The artifact imports cleanly into an instance that has none of the
	// assets.
	other := t.TempDir()
	if err := runImport([]string{"--data", other, out}); err != nil {
		t.Fatalf("import into empty instance: %v", err)
	}
}

func TestProfileImportRejectsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	bogus := filepath.Join(dir, "bogus.json")
	if err := os.WriteFile(bogus, []byte(`{"kind":"something-else","profiles":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runProfile([]string{"import", "--data", dir, bogus}); err == nil {
		t.Fatal("import accepted a foreign file")
	}
}
