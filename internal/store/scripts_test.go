package store

import (
	"encoding/json"
	"reflect"
	"testing"

	"loom/internal/graph"
)

// Phase 4 (reusable scripts): the script store mirrors the mapping
// machinery — append-only version docs, stale-update protection, usage
// pins, delete guards, language filtering.
func TestScriptRoundTrip(t *testing.T) {
	st := testProfileStore(t)

	created, err := st.CreateScript(&Script{
		ID: "scr-1", Name: "Upper-case item", Language: ScriptLanguageJavaScript,
		Source: "return { ...$json, text: String($json.text).toUpperCase() };",
		Tags:   []string{"text"}, Owner: "team-a",
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 || created.Visibility != "shared" {
		t.Fatalf("created version/visibility = %d/%q", created.Version, created.Visibility)
	}

	got, err := st.GetScript("scr-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Language != ScriptLanguageJavaScript || got.Source == "" {
		t.Fatalf("get language/source = %q/%q", got.Language, got.Source)
	}
	if !reflect.DeepEqual(got.Tags, []string{"text"}) || got.Owner != "team-a" {
		t.Fatalf("get tags/owner = %v/%q", got.Tags, got.Owner)
	}

	// The language rides the version doc so any version is
	// self-contained.
	row := st.readRow(`SELECT doc FROM script_versions WHERE script_id = ? AND version = 1`, "scr-1")
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("read version doc: %v", err)
	}
	var doc struct {
		Language string `json:"language"`
		Source   string `json:"source"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal doc: %v", err)
	}
	if doc.Language != ScriptLanguageJavaScript || doc.Source != got.Source {
		t.Fatalf("doc language/source = %q/%q", doc.Language, doc.Source)
	}
}

func TestScriptPythonRequirementsValidation(t *testing.T) {
	st := testProfileStore(t)

	if _, err := st.CreateScript(&Script{ID: "scr-py", Name: "Bad", Language: ScriptLanguagePython,
		Source: "return {'ok': True}", Requirements: []string{"idna"}}, "u"); err == nil {
		t.Fatal("unpinned requirement accepted")
	}
	if _, err := st.CreateScript(&Script{ID: "scr-js", Name: "Bad", Language: ScriptLanguageJavaScript,
		Source: "return $json;", Requirements: []string{"idna==3.10"}}, "u"); err == nil {
		t.Fatal("requirements on a javascript script accepted")
	}
	if _, err := st.CreateScript(&Script{ID: "scr-py", Name: "Good", Language: ScriptLanguagePython,
		Source: "return {'ok': True}", Requirements: []string{"idna==3.10", "certifi==2025.1.1"}}, "u"); err != nil {
		t.Fatalf("pinned python requirements rejected: %v", err)
	}
	if _, err := st.CreateScript(&Script{ID: "scr-x", Name: "Bad", Language: "ruby", Source: "x"}, "u"); err == nil {
		t.Fatal("unknown language accepted")
	}
}

func TestScriptStaleUpdateAndLanguageImmutability(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateScript(&Script{ID: "scr-2", Name: "v1", Language: ScriptLanguageJavaScript, Source: "return 1;"}, "u"); err != nil {
		t.Fatalf("create: %v", err)
	}
	updated, err := st.UpdateScript(&Script{ID: "scr-2", Name: "v2", Language: ScriptLanguageJavaScript, Source: "return 2;"}, 1, "u")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated version = %d, want 2", updated.Version)
	}
	if _, err := st.UpdateScript(&Script{ID: "scr-2", Name: "v3", Language: ScriptLanguageJavaScript, Source: "return 3;"}, 1, "u"); err != ErrStaleScript {
		t.Fatalf("stale update error = %v, want ErrStaleScript", err)
	}
	if _, err := st.UpdateScript(&Script{ID: "missing", Name: "x", Language: ScriptLanguageJavaScript, Source: "x"}, 1, "u"); err != ErrScriptNotFound {
		t.Fatalf("missing update error = %v, want ErrScriptNotFound", err)
	}
	if _, err := st.UpdateScript(&Script{ID: "scr-2", Name: "v3", Language: ScriptLanguagePython, Source: "return 3;"}, 2, "u"); err == nil {
		t.Fatal("language flip accepted")
	}
	// The failed flip must not have burned a version.
	got, err := st.GetScript("scr-2")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Version != 2 {
		t.Fatalf("version after rejected update = %d, want 2", got.Version)
	}
}

func TestScriptVersionReconstruction(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateScript(&Script{ID: "scr-3", Name: "First", Language: ScriptLanguagePython,
		Source: "return 1;", Requirements: []string{"idna==3.10"}, TimeoutSeconds: 120, MaxMemoryMB: 256}, "u"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := st.UpdateScript(&Script{ID: "scr-3", Name: "Second", Language: ScriptLanguagePython,
		Source: "return 2;", Requirements: []string{}, TimeoutSeconds: 30}, 1, "u"); err != nil {
		t.Fatalf("update: %v", err)
	}
	old, err := st.GetScriptVersion("scr-3", 1)
	if err != nil {
		t.Fatalf("get version 1: %v", err)
	}
	if old.Name != "First" || old.Source != "return 1;" || old.TimeoutSeconds != 120 || old.MaxMemoryMB != 256 {
		t.Fatalf("v1 reconstruction = %+v", old)
	}
	if !reflect.DeepEqual(old.Requirements, []string{"idna==3.10"}) {
		t.Fatalf("v1 requirements = %v", old.Requirements)
	}
	versions, err := st.ListScriptVersions("scr-3")
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 || versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions = %+v", versions)
	}
}

func TestScriptListByLanguageAndUsageCount(t *testing.T) {
	st := testProfileStore(t)
	for _, s := range []Script{
		{ID: "js-1", Name: "JS one", Language: ScriptLanguageJavaScript, Source: "return 1;"},
		{ID: "js-2", Name: "JS two", Language: ScriptLanguageJavaScript, Source: "return 2;"},
		{ID: "py-1", Name: "Py one", Language: ScriptLanguagePython, Source: "return 3;"},
	} {
		if _, err := st.CreateScript(&s, "u"); err != nil {
			t.Fatalf("create %s: %v", s.ID, err)
		}
	}
	js, err := st.ListScriptsByLanguage(ScriptLanguageJavaScript)
	if err != nil {
		t.Fatalf("list js: %v", err)
	}
	if len(js) != 2 {
		t.Fatalf("js count = %d, want 2", len(js))
	}
	if _, err := st.ListScriptsByLanguage("ruby"); err == nil {
		t.Fatal("unknown language accepted")
	}
	if err := st.RecordScriptUsage("js-1", 1, "wf-1", 3); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if err := st.RecordScriptUsage("js-1", 1, "wf-2", 1); err != nil {
		t.Fatalf("record usage 2: %v", err)
	}
	all, err := st.ListScripts()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("total = %d, want 3", len(all))
	}
	for _, s := range all {
		if s.ID == "js-1" && s.UsageCount != 2 {
			t.Fatalf("js-1 usage count = %d, want 2", s.UsageCount)
		}
	}
	usage, err := st.ScriptUsage("js-1")
	if err != nil || len(usage) != 2 {
		t.Fatalf("usage = %v, %v", usage, err)
	}
	if count, err := st.ScriptUsageCount("js-1"); err != nil || count != 2 {
		t.Fatalf("usage count = %d, %v", count, err)
	}
}

func TestScriptDeleteGuards(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateScript(&Script{ID: "scr-4", Name: "Guarded", Language: ScriptLanguageJavaScript, Source: "return 1;"}, "u"); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Usage pin guards the delete.
	if err := st.RecordScriptUsage("scr-4", 1, "wf-1", 1); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if err := st.DeleteScript("scr-4"); err != ErrScriptInUse {
		t.Fatalf("in-use delete error = %v, want ErrScriptInUse", err)
	}

	// Deleting the workflow releases the pin (usage pins are references,
	// not history).
	if err := st.SaveWorkflow(&graph.Workflow{ID: "wf-1", Name: "Guarded flow", Nodes: []graph.Node{}, Edges: []graph.Edge{}}); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
	if err := st.DeleteWorkflow("wf-1"); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}

	// A profile bundle reference guards the delete too.
	if _, err := st.db.Exec(`INSERT INTO config_profiles (id, name, node_type, description, current_version, visibility, creator_id, created_at, updated_at)
		VALUES ('prof-1', 'Bundle holder', 'code.run', '', 1, 'shared', '', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO profile_bundle_refs (profile_id, asset_kind, asset_id) VALUES ('prof-1', 'script', 'scr-4')`); err != nil {
		t.Fatalf("seed bundle ref: %v", err)
	}
	err := st.DeleteScript("scr-4")
	if err == nil || err.Error() == "" || err.Error() == ErrScriptInUse.Error() {
		t.Fatalf("bundle-ref delete error = %v, want named profile", err)
	}

	// With both references gone, the delete succeeds.
	if _, err := st.db.Exec(`DELETE FROM profile_bundle_refs WHERE asset_id = 'scr-4'`); err != nil {
		t.Fatalf("clear bundle ref: %v", err)
	}
	if err := st.DeleteScript("scr-4"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.GetScript("scr-4"); err != ErrScriptNotFound {
		t.Fatalf("get after delete = %v, want ErrScriptNotFound", err)
	}
	if err := st.DeleteScript("scr-4"); err != ErrScriptNotFound {
		t.Fatalf("double delete = %v, want ErrScriptNotFound", err)
	}
}
