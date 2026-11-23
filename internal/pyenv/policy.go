package pyenv

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Policy is the operator's opt-in choke on the two supply-chain risks of
// code.python dependencies: WHICH packages may be installed at publish
// time, and how much disk a built environment may occupy. It lives in a
// JSON file (default <data>/subprocess-policy.json) so it can be reviewed
// like code and mounted read-only like the keyring.
type Policy struct {
	// AllowedPackages is a list of package names, or ["*"] for allow-all
	// (the default when no policy file exists).
	AllowedPackages []string `json:"allowedPackages"`
	// MaxEnvMB caps the on-disk size of one built environment.
	MaxEnvMB int64 `json:"maxEnvMB"`
}

// DefaultPolicy allows every pinned package and caps environments at
// 1 GiB. It is what an absent policy file means — the flag
// --enable-subprocess-nodes remains the primary gate.
func DefaultPolicy() Policy {
	return Policy{AllowedPackages: []string{"*"}, MaxEnvMB: 1024}
}

// LoadPolicy reads a policy file. A missing file at the DEFAULT path means
// default policy (and no error); a missing file at an explicitly chosen
// path is an operator mistake and errors loudly rather than failing open.
func LoadPolicy(path string, explicit bool) (Policy, error) {
	policy := DefaultPolicy()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			return policy, nil
		}
		return policy, fmt.Errorf("pyenv: read policy: %w", err)
	}
	if err := json.Unmarshal(data, &policy); err != nil {
		return DefaultPolicy(), fmt.Errorf("pyenv: policy %s is not valid JSON: %w", path, err)
	}
	if len(policy.AllowedPackages) == 0 {
		return DefaultPolicy(), fmt.Errorf("pyenv: policy %s: allowedPackages must not be empty (use [\"*\"] to allow all)", path)
	}
	if policy.MaxEnvMB <= 0 {
		policy.MaxEnvMB = DefaultPolicy().MaxEnvMB
	}
	return policy, nil
}

// Check reports whether every requirement's package name is allowed.
// Names are compared case-insensitively (PyPI normalizes with dashes).
func (p Policy) Check(requirements []string) error {
	allowed := make(map[string]bool, len(p.AllowedPackages))
	wildcard := false
	for _, name := range p.AllowedPackages {
		if name == "*" {
			wildcard = true
		}
		allowed[normalizeName(name)] = true
	}
	for _, req := range requirements {
		name := packageName(req)
		if name == "" {
			continue
		}
		if wildcard || allowed[normalizeName(name)] {
			continue
		}
		return fmt.Errorf("pyenv: package %q is not in the allowedPackages policy (data/subprocess-policy.json)", name)
	}
	return nil
}

// EnvSize returns the on-disk size of a built environment in bytes.
func EnvSize(root, key string) (int64, error) {
	var total int64
	dir := filepath.Join(root, key)
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

// CheckSize reports whether a built environment fits the policy's cap.
func (p Policy) CheckSize(root, key string) error {
	total, err := EnvSize(root, key)
	if err != nil {
		return err
	}
	if total > p.MaxEnvMB*1024*1024 {
		return fmt.Errorf("pyenv: environment %s is %d MiB, over the %d MiB policy cap", shortKey(key), total/(1024*1024), p.MaxEnvMB)
	}
	return nil
}

// shortKey renders a key for logs without assuming hash length.
func shortKey(key string) string {
	if len(key) > 12 {
		return key[:12]
	}
	return key
}

// packageName extracts the package name from a pinned "name==version"
// requirement.
func packageName(req string) string {
	if i := strings.Index(req, "=="); i > 0 {
		return strings.TrimSpace(req[:i])
	}
	return ""
}

// normalizeName applies PyPI's rule: case-insensitive, dashes equal
// underscores.
func normalizeName(name string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(name)), "_", "-")
}
