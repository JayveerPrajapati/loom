package pyenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestKeyIsDeterministicAndOrderInsensitive(t *testing.T) {
	a := Key([]string{"httpx==0.27.0", "pandas==2.2.0"})
	b := Key([]string{"pandas==2.2.0", "httpx==0.27.0"})
	if a != b {
		t.Fatalf("key must ignore requirement order: %s vs %s", a, b)
	}
	if len(a) != 64 {
		t.Fatalf("key = %q", a)
	}
	if Key([]string{"httpx==0.27.0"}) == Key([]string{"httpx==0.28.0"}) {
		t.Fatal("different versions must produce different keys")
	}
}

func TestValidRequirement(t *testing.T) {
	valid := []string{"httpx==0.27.0", "pandas==2.2.0", "a_b-c==1.0.0rc1", "pkg==2!1.0"}
	for _, req := range valid {
		if !ValidRequirement(req) {
			t.Errorf("ValidRequirement(%q) = false", req)
		}
	}
	invalid := []string{"httpx", "httpx>=0.27", "httpx==", "==1.0", "", "pkg name==1.0", "pkg==1.0; rm -rf"}
	for _, req := range invalid {
		if ValidRequirement(req) {
			t.Errorf("ValidRequirement(%q) = true", req)
		}
	}
}

func TestBuildWithoutUvFails(t *testing.T) {
	// Detect uv the same way Build does: any uv on PATH makes the
	// missing-uv failure path untestable on this machine.
	if _, err := exec.LookPath("uv"); err == nil {
		t.Skip("uv present; failure path not testable")
	}
	if _, err := Build(t.TempDir(), []string{"httpx==0.27.0"}); err == nil {
		t.Fatal("Build must fail clearly when uv is missing")
	}
}

func TestBuildRejectsUnpinned(t *testing.T) {
	if _, err := Build(t.TempDir(), []string{"httpx"}); err == nil {
		t.Fatal("Build must reject unpinned requirements before invoking uv")
	}
}

func TestBuildUsesUvAndWritesManifest(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	uv := filepath.Join(bin, "uv")
	script := `#!/bin/sh
case "$1" in
venv)
	mkdir -p "$4/bin"
	touch "$4/bin/python"
	;;
pip)
	;;
--version)
	echo "uv 0.5.0"
	;;
*)
	exit 2
	;;
esac
`
	if err := os.WriteFile(uv, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	key, err := Build(root, []string{"httpx==0.27.0"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if key != Key([]string{"httpx==0.27.0"}) {
		t.Fatalf("key = %s, want the requirements hash", key)
	}
	if _, err := os.Stat(PythonPath(root, key)); err != nil {
		t.Fatalf("PythonPath: %v", err)
	}
	manifest, err := LoadManifest(root, key)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if len(manifest.Requirements) != 1 || manifest.Requirements[0] != "httpx==0.27.0" || manifest.UvVersion != "uv 0.5.0" {
		t.Fatalf("manifest = %+v", manifest)
	}
}

func TestLoadManifestVerifiesHash(t *testing.T) {
	root := t.TempDir()
	key := Key([]string{"httpx==0.27.0"})
	if _, err := LoadManifest(root, key); err == nil {
		t.Fatal("missing manifest must fail")
	}

	// A manifest whose requirements hash to the key verifies.
	dir := filepath.Join(root, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, dir, key, []string{"httpx==0.27.0"})
	if _, err := LoadManifest(root, key); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}

	// A tampered manifest (different requirements, same directory) fails.
	writeManifest(t, dir, key, []string{"httpx==0.99.0"})
	if _, err := LoadManifest(root, key); err == nil {
		t.Fatal("tampered manifest must fail")
	}
}

func writeManifest(t *testing.T, dir, key string, requirements []string) {
	t.Helper()
	manifest := `{"key":"` + key + `","requirements":["` + requirements[0] + `"],"uvVersion":"uv 0.5.0"}`
	if err := os.WriteFile(filepath.Join(dir, "loom-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
}
