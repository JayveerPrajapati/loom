package nodes

import (
	"reflect"
	"sort"
	"testing"
)

// The v1 catalog is frozen: no node type may be added, removed, or renamed
// without a deliberate version bump and a documentation update. This test
// turns that policy into a mechanical check: any accidental catalog change
// fails here first.
func TestCatalogIsFrozen(t *testing.T) {
	// Sorted, matching All()'s ordering. code.python was added deliberately
	// on 2026-11-06 (ADR-0003, operator-approved, behind
	// --enable-subprocess-nodes); data.map was added deliberately on
	// 2026-11-11 (Configuration Hub v2 plan D13, the reusable-mapping
	// node consuming Hub mappings).
	want := []string{
		"code.python",
		"code.run",
		"data.json.parse",
		"data.map",
		"data.set",
		"db.postgres",
		"flow.approval",
		"flow.emit",
		"flow.if",
		"flow.loop",
		"flow.merge",
		"flow.subflow",
		"flow.switch",
		"flow.wait",
		"http.request",
		"text.template",
		"trigger.cron",
		"trigger.event",
		"trigger.manual",
		"trigger.subflow",
		"trigger.webhook",
	}
	got := make([]string, 0, len(All()))
	for _, spec := range All() {
		got = append(got, spec.Type)
	}
	if len(got) != len(want) {
		t.Fatalf("catalog has %d node types, frozen list has %d — the v1 catalog must not change without a deliberate decision: %v",
			len(got), len(want), got)
	}
	for i, typ := range want {
		if got[i] != typ {
			t.Fatalf("catalog position %d is %q, frozen list expects %q — additions, removals and renames all require updating this test deliberately",
				i, got[i], typ)
		}
	}
}

// Configuration Hub v3 plan §5.2: the HTTP parameter-group fields are
// editor inputs that publish compiles away. They must stay declared
// (drafts validate), advanced (out of the casual path), never secret, and
// — since v4.1 E4 — Profileable so profiles can carry typed parameter
// rows; mappingRefs stays node-only because profiles never pin shared
// mappings.
func TestHTTPParameterFields(t *testing.T) {
	var spec *Definition
	for i := range All() {
		if All()[i].Type == "http.request" {
			spec = &All()[i]
			break
		}
	}
	if spec == nil {
		t.Fatal("http.request missing from catalog")
	}
	for _, name := range []string{"bodyFields", "queryFields", "headerFields", "pathFields"} {
		field, ok := spec.Config[name]
		if !ok {
			t.Fatalf("http.request has no config field %q — the parameter-group fields are part of the v3 plan and must be declared", name)
		}
		if !field.Advanced {
			t.Fatalf("http.request.%s must stay Advanced: it is a publish-compiled editor input, not a casual setting", name)
		}
		if field.Secret {
			t.Fatalf("http.request.%s must never be Secret", name)
		}
		if !field.Profileable {
			t.Fatalf("http.request.%s must be Profileable (v4.1 E4): profiles carry typed parameter rows", name)
		}
	}
	// mappingRefs stays node-only: a profile must not pin shared mappings.
	if field, ok := spec.Config["mappingRefs"]; !ok || field.Profileable {
		t.Fatalf("http.request.mappingRefs must exist and never be Profileable: mappings attach per node, not per profile")
	}
}

// Every catalog entry ships a DisplayName the editor can use directly:
// nonempty, and unique so a name never ambiguously identifies two node
// types.
func TestCatalogDisplayNames(t *testing.T) {
	seen := make(map[string]string, len(All()))
	for _, spec := range All() {
		if spec.DisplayName == "" {
			t.Fatalf("%s has an empty DisplayName — every node type needs a human-facing name", spec.Type)
		}
		if other, dup := seen[spec.DisplayName]; dup {
			t.Fatalf("DisplayName %q is used by both %s and %s — display names must be unique", spec.DisplayName, other, spec.Type)
		}
		seen[spec.DisplayName] = spec.Type
	}
}

// Phase 4 D4-1 freezes the script reference boundary on the code nodes:
// scriptRef is the picker (Basics, never Advanced, never Secret) and is
// NEVER Profileable — scripts reach nodes through node refs and profile
// bundles, never through profile config overlays. source stays the
// node-local escape hatch and is never Profileable either.
func TestScriptRefFields(t *testing.T) {
	wantLanguage := map[string]string{"code.run": "javascript", "code.python": "python"}
	for _, spec := range All() {
		_, isCode := wantLanguage[spec.Type]
		if !isCode {
			continue
		}
		ref, ok := spec.Config["scriptRef"]
		if !ok {
			t.Fatalf("%s has no config field %q — the Phase 4 script picker needs it declared", spec.Type, "scriptRef")
		}
		if ref.Advanced {
			t.Fatalf("%s.scriptRef must stay in Basics: it is the primary picker, not an advanced setting", spec.Type)
		}
		if ref.Secret {
			t.Fatalf("%s.scriptRef must never be Secret", spec.Type)
		}
		if ref.Profileable {
			t.Fatalf("%s.scriptRef must never be Profileable: scripts reach nodes through node refs and profile bundles, never profile config", spec.Type)
		}
		if ref.Required {
			t.Fatalf("%s.scriptRef must not be required: node-local source is the escape hatch", spec.Type)
		}
		source, ok := spec.Config["source"]
		if !ok {
			t.Fatalf("%s has no config field %q", spec.Type, "source")
		}
		if source.Profileable {
			t.Fatalf("%s.source must never be Profileable: script source reaches nodes only through scriptRef resolution (Phase 4 freeze)", spec.Type)
		}
		if source.Required {
			t.Fatalf("%s.source must not be catalog-required: a scriptRef supplies the code (the source-or-script rule lives in graph.Validate)", spec.Type)
		}
	}
}

// The Profileable allowlist (Configuration Hub decision D1, plan §18): a
// profile may only set fields the catalog explicitly marks, and a Secret
// field can never be Profileable. Both rules are mechanical here so an
// accidental catalog edit fails before it ships.
func TestProfileableFields(t *testing.T) {
	want := map[string][]string{
		"http.request":    {"auth", "body", "bodyFields", "bodyType", "contentType", "headerFields", "headers", "inlineThresholdBytes", "method", "pagination", "pathFields", "pathParams", "query", "queryFields", "rateLimitPerMinute", "responseMode", "successCodes", "timeout", "url"},
		"code.python":     {"maxMemoryMB", "requirements", "timeoutSeconds"},
		"code.run":        {"language", "outputType", "timeoutSeconds"},
		"db.postgres":     {"operation"},
		"flow.approval":   {"channel", "linkExpiresMinutes", "onTimeout", "reminderMinutes", "timeoutSeconds"},
		"trigger.webhook": {"hmacHeader", "hmacTimestamps", "method", "secretHeader", "strictSchema", "timestampHeader"},
	}
	for _, spec := range All() {
		got := []string{}
		for name, field := range spec.Config {
			if field.Secret && field.Profileable {
				t.Fatalf("%s.%q is both Secret and Profileable — secret fields can never enter a profile", spec.Type, name)
			}
			if field.Profileable {
				got = append(got, name)
			}
		}
		expected := want[spec.Type]
		if expected == nil {
			expected = []string{}
		}
		sort.Strings(got)
		sort.Strings(expected)
		if !reflect.DeepEqual(got, expected) {
			t.Fatalf("%s Profileable fields = %v, want %v — the allowlist is a deliberate decision (plan §18 D1); update it consciously",
				spec.Type, got, expected)
		}
	}
}

// TestBundleIsNeverAConfigField freezes the Phase 1 P0-6 boundary: bundle
// references compose mappings and schemas into a profile, and they live
// OUTSIDE the config map — validated as asset ids, never as node
// configuration. A catalog field named "bundle" would silently turn the
// composition layer into ordinary config and bypass that structural
// separation, and ValidateProfileConfig must keep rejecting it as an
// unknown config key for every node type.
func TestBundleIsNeverAConfigField(t *testing.T) {
	for _, spec := range All() {
		if _, ok := spec.Config["bundle"]; ok {
			t.Fatalf("%s declares a config field %q — bundle references must stay outside config (Phase 1 P0-6)",
				spec.Type, "bundle")
		}
		err := ValidateProfileConfig(spec, map[string]any{
			"bundle": map[string]any{"mappings": map[string]any{"query": "m-1"}},
		})
		if err == nil {
			t.Fatalf("ValidateProfileConfig accepted a bundle inside config for %s — bundle refs must bypass config validation only by living outside the config map",
				spec.Type)
		}
	}
}
