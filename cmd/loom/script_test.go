package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loom/internal/store"
)

// scriptCLIStore opens a data dir and seeds two scripts.
func scriptCLIStore(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.CreateScript(&store.Script{
		ID: "scr-shape", Name: "Shape", Language: store.ScriptLanguagePython,
		Description: "Normalize the item", Source: "return {'ok': True}",
		Requirements: []string{"idna==3.10"}, TimeoutSeconds: 300, MaxMemoryMB: 1024,
		Tags: []string{"shape"}, Owner: "growth-team",
	}, "cli-test"); err != nil {
		t.Fatalf("seed python script: %v", err)
	}
	if _, err := st.CreateScript(&store.Script{
		ID: "scr-greet", Name: "Greet", Language: store.ScriptLanguageJavaScript,
		Source: "return { hello: 'world' };", TimeoutSeconds: 60,
	}, "cli-test"); err != nil {
		t.Fatalf("seed js script: %v", err)
	}
	return dir
}

func TestScriptListExportImportRoundTrip(t *testing.T) {
	dir := scriptCLIStore(t)
	out := filepath.Join(dir, "scripts.json")

	if err := runScript([]string{"export", "--data", dir, "--out", out}); err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Kind    string `json:"kind"`
		Scripts []struct {
			ID           string   `json:"id"`
			Language     string   `json:"language"`
			Source       string   `json:"source"`
			Requirements []string `json:"requirements"`
			Owner        string   `json:"owner"`
		} `json:"scripts"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if file.Kind != "loom-scripts" || len(file.Scripts) != 2 {
		t.Fatalf("export = %s kinds=%d", file.Kind, len(file.Scripts))
	}

	// Import into a fresh instance: both scripts land at v1.
	other := t.TempDir()
	if err := runScript([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("import: %v", err)
	}
	st, err := openStore(other)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	defer st.Close()
	imported, err := st.GetScript("scr-shape")
	if err != nil {
		t.Fatalf("get imported: %v", err)
	}
	if imported.Language != store.ScriptLanguagePython || imported.Source != "return {'ok': True}" ||
		len(imported.Requirements) != 1 || imported.TimeoutSeconds != 300 || imported.MaxMemoryMB != 1024 ||
		imported.Owner != "growth-team" {
		t.Fatalf("imported script = %#v", imported)
	}

	// Re-importing the identical file is a no-op; a changed source bumps
	// the version (the file stays fully intact — only the source text is
	// patched).
	if err := runScript([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("re-import: %v", err)
	}
	changed := filepath.Join(dir, "scripts-changed.json")
	patchedRaw := strings.Replace(string(raw), "return {'ok': True}", "return {'ok': 1}", 1)
	if patchedRaw == string(raw) {
		t.Fatal("source text not found in export")
	}
	if err := os.WriteFile(changed, []byte(patchedRaw), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runScript([]string{"import", "--data", other, changed}); err != nil {
		t.Fatalf("import changed: %v", err)
	}
	bumped, err := st.GetScript("scr-shape")
	if err != nil || bumped.Version != 2 || bumped.Source != "return {'ok': 1}" {
		t.Fatalf("bumped script = %#v (v%d), %v", bumped, bumped.Version, err)
	}

	// list output names both.
	if err := runScript([]string{"list", "--data", other}); err != nil {
		t.Fatalf("list: %v", err)
	}
}

func TestScriptImportRejectsForeignFiles(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "foreign.json")
	if err := os.WriteFile(file, []byte(`{"kind":"loom-data-schemas","schemas":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runScript([]string{"import", "--data", dir, file}); err == nil {
		t.Fatal("foreign file accepted")
	}
	// A bad language is rejected with the entry named.
	lang := filepath.Join(dir, "bad-lang.json")
	if err := os.WriteFile(lang, []byte(`{"kind":"loom-scripts","scripts":[{"id":"x","name":"X","language":"ruby","source":"puts 1"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runScript([]string{"import", "--data", dir, lang}); err == nil {
		t.Fatal("unknown language accepted")
	}
}

// The hub bundle round-trips scripts losslessly (Phase 4 acceptance).
func TestHubCarriesScripts(t *testing.T) {
	dir := scriptCLIStore(t)
	out := filepath.Join(dir, "hub.json")
	if err := runHub([]string{"export", "--data", dir, "--out", out}); err != nil {
		t.Fatalf("hub export: %v", err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var bundle hubBundle
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("parse hub bundle: %v", err)
	}
	if bundle.Kind != "loom-hub" || len(bundle.Scripts) != 2 {
		t.Fatalf("bundle scripts = %d (%s)", len(bundle.Scripts), bundle.Kind)
	}

	other := t.TempDir()
	if err := runHub([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("hub import: %v", err)
	}
	st, err := openStore(other)
	if err != nil {
		t.Fatalf("open other: %v", err)
	}
	defer st.Close()
	for _, id := range []string{"scr-shape", "scr-greet"} {
		if _, err := st.GetScript(id); err != nil {
			t.Fatalf("script %s missing after hub import: %v", id, err)
		}
	}
	// Re-import is a no-op ("unchanged, skipped" path).
	if err := runHub([]string{"import", "--data", other, out}); err != nil {
		t.Fatalf("hub re-import: %v", err)
	}
}
