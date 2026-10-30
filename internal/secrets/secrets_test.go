package secrets

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupEnvEnvelope(t *testing.T) {
	src := Source{Env: func(key string) string {
		if key == "LOOM_SECRET_OPENAI" {
			return `{"type":"openai","data":{"apiKey":"sk-test"}}`
		}
		return ""
	}}
	cred, ok := src.Lookup("openai")
	if !ok || cred.Type != "openai" || cred.Data["apiKey"] != "sk-test" {
		t.Fatalf("credential = %#v, %v", cred, ok)
	}
}

func TestLookupEnvBareData(t *testing.T) {
	src := Source{Env: func(string) string { return `{"apiKey":"bare"}` }}
	cred, ok := src.Lookup("resend")
	if !ok || cred.Type != "resend" || cred.Data["apiKey"] != "bare" {
		t.Fatalf("credential = %#v, %v", cred, ok)
	}
}

func TestLookupFileAndDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(file, []byte(`{"slack":{"type":"slack","data":{"token":"xoxb"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	src := Source{File: file}
	cred, ok := src.Lookup("slack")
	if !ok || cred.Data["token"] != "xoxb" {
		t.Fatalf("file credential = %#v, %v", cred, ok)
	}

	secretDir := filepath.Join(dir, "secrets")
	if err := os.MkdirAll(secretDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "jira.json"), []byte(`{"email":"a@example.com","token":"t"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cred, ok = Source{Dir: secretDir}.Lookup("jira")
	if !ok || cred.Data["email"] != "a@example.com" {
		t.Fatalf("dir credential = %#v, %v", cred, ok)
	}
}
