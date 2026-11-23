// Package secrets resolves credentials from deployment secret stores.
//
// It is deliberately small: environment variables, a JSON secrets file and a
// directory of per-credential JSON files. Cloud secret managers can write one
// of these forms (or a sidecar can) without Loom linking their SDKs.
package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"loom/internal/flow"
)

// Source reads external credentials. Empty fields are skipped.
type Source struct {
	Dir  string
	File string
	Env  func(string) string
}

// FromEnv builds a Source from LOOM_SECRETS_DIR, LOOM_SECRETS_FILE and the
// process environment.
func FromEnv() Source {
	return Source{
		Dir:  os.Getenv("LOOM_SECRETS_DIR"),
		File: os.Getenv("LOOM_SECRETS_FILE"),
		Env:  os.Getenv,
	}
}

// Lookup returns the first credential found for id. Environment values use
// LOOM_SECRET_<ID> and may be either {"type","data"} or a bare data object.
// ValidCredentialID reports whether id is safe to use as a file name and as a
// lookup key. It rejects path separators, dot segments and control characters,
// but allows the broader names env/file secret sources already supported
// (spaces, colons, unicode) so existing workflows keep working.
func ValidCredentialID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	if strings.ContainsAny(id, `/\`) {
		return false
	}
	for _, r := range id {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(id, ".") {
		if part == "" {
			return false
		}
	}
	return true
}

// within reports whether path resolves inside dir after cleaning.
func within(dir, path string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s Source) Lookup(id string) (flow.Credential, bool) {
	if s.Env != nil {
		if raw := s.Env(envName(id)); raw != "" {
			if cred, ok := parse(id, []byte(raw)); ok {
				return cred, true
			}
		}
	}
	if s.File != "" {
		if data, err := os.ReadFile(s.File); err == nil {
			var entries map[string]json.RawMessage
			if json.Unmarshal(data, &entries) == nil {
				if raw, ok := entries[id]; ok {
					if cred, ok := parse(id, raw); ok {
						return cred, true
					}
				}
			}
		}
	}
	if s.Dir != "" && ValidCredentialID(id) {
		path := filepath.Join(s.Dir, id+".json")
		if within(s.Dir, path) {
			if data, err := os.ReadFile(path); err == nil {
				if cred, ok := parse(id, data); ok {
					return cred, true
				}
			}
		}
	}
	return flow.Credential{}, false
}

func parse(id string, raw []byte) (flow.Credential, bool) {
	var envelope struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Data != nil {
		typ := envelope.Type
		if typ == "" {
			typ = id
		}
		return flow.Credential{Type: typ, Data: envelope.Data, Raw: string(raw)}, true
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return flow.Credential{}, false
	}
	return flow.Credential{Type: id, Data: data, Raw: string(raw)}, true
}

func envName(id string) string {
	var b strings.Builder
	b.WriteString("LOOM_SECRET_")
	for _, r := range strings.ToUpper(id) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}
