// Package pyenv resolves Python dependency environments. Requirements are
// pinned ("name==version") and hashed into an environment key; environments
// are built once at publish time with uv and live on the filesystem under
// <data>/envs/<key>/ — runs never install dependencies or touch the network.
package pyenv

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// requirementPattern pins every entry to name==version so resolution is
// deterministic and the key is a pure function of the requirement set.
var requirementPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*==[A-Za-z0-9][A-Za-z0-9.+!-]*$`)

// ValidRequirement reports whether one requirement line is pinned.
func ValidRequirement(req string) bool {
	return requirementPattern.MatchString(strings.TrimSpace(req))
}

// Key derives the deterministic environment key for a requirement set:
// the sha256 of the sorted pinned requirements. Order does not matter.
func Key(requirements []string) string {
	sorted := make([]string, 0, len(requirements))
	for _, req := range requirements {
		sorted = append(sorted, strings.TrimSpace(req))
	}
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, "\n")))
	return hex.EncodeToString(sum[:])
}

// Manifest is the tamper-evidence record inside an environment directory.
// Its requirements must hash to the directory name.
type Manifest struct {
	Key          string   `json:"key"`
	Requirements []string `json:"requirements"`
	UvVersion    string   `json:"uvVersion"`
}

// Build creates (or reuses) the environment for requirements under root,
// using uv: `uv venv` then `uv pip install` with the pinned set. It is a
// publish-time operation — the only phase allowed to install packages.
func Build(root string, requirements []string) (string, error) {
	for _, req := range requirements {
		if !ValidRequirement(req) {
			return "", fmt.Errorf("pyenv: requirement %q must be pinned as name==version", req)
		}
	}
	// The child processes run with Dir = root, so every path handed to
	// uv must be absolute — a relative root (the default -data ./data)
	// would otherwise create the environment under a doubled path.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if _, err := exec.LookPath("uv"); err != nil {
		return "", fmt.Errorf("pyenv: uv is not on PATH — install it to publish workflows with Python dependencies (https://docs.astral.sh/uv/)")
	}
	key := Key(requirements)
	dir := filepath.Join(root, key)

	if existing, err := LoadManifest(root, key); err == nil && existing != nil {
		return key, nil
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	if err := os.RemoveAll(dir); err != nil {
		return "", err
	}
	if out, err := run(root, "uv", "venv", "--python", "3", dir); err != nil {
		return "", fmt.Errorf("pyenv: uv venv: %w: %s", err, out)
	}
	installArgs := append([]string{"pip", "install", "--python", filepath.Join(dir, "bin", "python")}, requirements...)
	if out, err := run(root, "uv", installArgs...); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("pyenv: uv pip install: %w: %s", err, out)
	}
	uvVersion, _ := runOutput("", "uv", "--version")
	manifest := Manifest{Key: key, Requirements: requirements, UvVersion: strings.TrimSpace(uvVersion)}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "loom-manifest.json"), data, 0o644); err != nil {
		return "", err
	}
	return key, nil
}

// LoadManifest reads an environment's manifest, verifying that its pinned
// requirements hash to the directory name. It is what `loom doctor` uses —
// same trust model as every other tamper check.
func LoadManifest(root, key string) (*Manifest, error) {
	data, err := os.ReadFile(filepath.Join(root, key, "loom-manifest.json"))
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.Key != key || Key(manifest.Requirements) != key {
		return nil, fmt.Errorf("pyenv: environment %s manifest does not hash to its name", key)
	}
	return &manifest, nil
}

// PythonPath returns the environment's interpreter.
func PythonPath(root, key string) string {
	return filepath.Join(root, key, "bin", "python")
}

func run(dir string, name string, args ...string) (string, error) {
	out, err := runOutput(dir, name, args...)
	if err != nil {
		return out, err
	}
	return out, nil
}

func runOutput(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// EnvInfo is one built dependency environment with its disk size —
// the operator-facing publish/status surface (Phase 5).
type EnvInfo struct {
	Key          string   `json:"key"`
	Requirements []string `json:"requirements"`
	UvVersion    string   `json:"uvVersion,omitempty"`
	SizeMB       float64  `json:"sizeMB"`
}

// List reports every manifest-verified environment under root, sorted
// by key. An environment whose manifest fails verification (wrong hash,
// missing manifest) is skipped rather than reported: the listing never
// surfaces an environment the trust model would reject.
func List(root string) []EnvInfo {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []EnvInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		key := entry.Name()
		manifest, err := LoadManifest(root, key)
		if err != nil {
			continue
		}
		size, err := EnvSize(root, key)
		if err != nil {
			continue
		}
		out = append(out, EnvInfo{
			Key: key, Requirements: manifest.Requirements, UvVersion: manifest.UvVersion,
			SizeMB: math.Round(float64(size)/1024/1024*10) / 10,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}
