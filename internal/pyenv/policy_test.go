package pyenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPolicyMissingDefaultFileAllowsAll(t *testing.T) {
	policy, err := LoadPolicy(filepath.Join(t.TempDir(), "subprocess-policy.json"), false)
	if err != nil {
		t.Fatalf("missing default file must not error: %v", err)
	}
	if err := policy.Check([]string{"anything==1.0.0"}); err != nil {
		t.Fatalf("default policy must allow pinned packages: %v", err)
	}
	if policy.MaxEnvMB != 1024 {
		t.Fatalf("default cap = %d", policy.MaxEnvMB)
	}
}

func TestPolicyExplicitMissingFileErrors(t *testing.T) {
	if _, err := LoadPolicy("/nonexistent/policy.json", true); err == nil {
		t.Fatal("an explicitly configured policy path that is missing must error, not fail open")
	}
}

func TestPolicyMalformedJSONErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(path, true); err == nil {
		t.Fatal("malformed policy must error")
	}
}

func TestPolicyEmptyAllowlistErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(`{"allowedPackages": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPolicy(path, true); err == nil {
		t.Fatal("empty allowedPackages must error — it is indistinguishable from a mistake")
	}
}

func TestPolicyAllowlistChecks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(path, []byte(`{"allowedPackages": ["httpx", "Scikit_Learn"], "maxEnvMB": 8}`), 0o644); err != nil {
		t.Fatal(err)
	}
	policy, err := LoadPolicy(path, true)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if err := policy.Check([]string{"httpx==0.27.0"}); err != nil {
		t.Fatalf("httpx must be allowed: %v", err)
	}
	// PyPI normalization: case-insensitive, underscores equal dashes.
	if err := policy.Check([]string{"scikit-learn==1.5.0"}); err != nil {
		t.Fatalf("scikit-learn must be allowed: %v", err)
	}
	if err := policy.Check([]string{"requests==2.31.0"}); err == nil {
		t.Fatal("requests must be rejected by the allowlist")
	}
}

func TestPolicySizeCap(t *testing.T) {
	root := t.TempDir()
	key := "someenv"
	dir := filepath.Join(root, key)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob := make([]byte, 2*1024*1024) // 2 MiB
	if err := os.WriteFile(filepath.Join(dir, "big.bin"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	tiny := Policy{AllowedPackages: []string{"*"}, MaxEnvMB: 1}
	if err := tiny.CheckSize(root, key); err == nil || !strings.Contains(err.Error(), "policy cap") {
		t.Fatalf("2 MiB env must exceed the 1 MiB cap, got: %v", err)
	}
	generous := Policy{AllowedPackages: []string{"*"}, MaxEnvMB: 16}
	if err := generous.CheckSize(root, key); err != nil {
		t.Fatalf("2 MiB env fits 16 MiB cap: %v", err)
	}
	size, err := EnvSize(root, key)
	if err != nil || size < 2*1024*1024 {
		t.Fatalf("EnvSize = %d, %v", size, err)
	}
}
