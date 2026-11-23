package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupRejectsPathTraversal(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "secrets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "stolen.json"), []byte(`{"token":"outside"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	src := Source{Dir: dir}
	for _, id := range []string{"../stolen", "../../etc/passwd", "sub/cred", "..", ".", "", "/etc/passwd", `..\stolen`} {
		if cred, ok := src.Lookup(id); ok {
			t.Fatalf("Lookup(%q) resolved outside the secrets dir: %+v", id, cred)
		}
	}

	if err := os.WriteFile(filepath.Join(dir, "ok.json"), []byte(`{"type":"http","data":{"token":"inside"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, ok := src.Lookup("ok")
	if !ok || cred.Data["token"] != "inside" {
		t.Fatalf("valid credential did not resolve: %+v ok=%v", cred, ok)
	}
}

func TestValidCredentialID(t *testing.T) {
	for _, id := range []string{"openai", "cred_http", "svc-1", "a.b", "A9", "my cred", "cred:prod", "clé"} {
		if !ValidCredentialID(id) {
			t.Errorf("ValidCredentialID(%q) = false, want true", id)
		}
	}
	for _, id := range []string{"", ".", "..", "a..b", "../x", "a/b", `a\b`, "a\x00b"} {
		if ValidCredentialID(id) {
			t.Errorf("ValidCredentialID(%q) = true, want false", id)
		}
	}
}
