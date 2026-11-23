package store

import (
	"encoding/json"
	"strings"
	"testing"
)

// Profile-Centric UX Phase 1 (P0-1): the bundle model on ConfigProfile —
// round-trip through the version doc, the append-only tolerance rule for
// pre-bundle version docs (P0-8), and version bumps on bundle changes.

func TestConfigProfileBundleRoundTrip(t *testing.T) {
	st := testProfileStore(t)

	created, err := st.CreateConfigProfile(&ConfigProfile{
		ID:       "profile-bundle",
		Name:     "Order API",
		NodeType: "http.request",
		Config:   map[string]any{"method": "POST"},
		Bundle: &ProfileBundle{
			Mappings: map[string]string{"query": "pagination-query", "body": "order-body"},
			Schemas:  map[string]string{"input": "order-request", "output": "order-response"},
		},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Bundle == nil || created.Bundle.Mappings["query"] != "pagination-query" {
		t.Fatalf("created bundle = %+v", created.Bundle)
	}

	got, err := st.GetConfigProfile("profile-bundle")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Bundle == nil {
		t.Fatalf("bundle lost on get")
	}
	if got.Bundle.Mappings["body"] != "order-body" || got.Bundle.Schemas["input"] != "order-request" {
		t.Fatalf("bundle round-trip = %+v", got.Bundle)
	}

	// Listing hydrates the bundle too.
	list, err := st.ListConfigProfiles()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	found := false
	for _, p := range list {
		if p.ID == "profile-bundle" {
			found = p.Bundle != nil && p.Bundle.Schemas["output"] == "order-response"
		}
	}
	if !found {
		t.Fatalf("bundle not listed: %+v", list)
	}

	// An old-version read (GetConfigProfileVersion) also carries it.
	ver, err := st.GetConfigProfileVersion("profile-bundle", 1)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	if ver.Bundle == nil || len(ver.Bundle.Mappings) != 2 {
		t.Fatalf("version bundle = %+v", ver.Bundle)
	}
}

func TestConfigProfileBundleChangeBumpsVersion(t *testing.T) {
	st := testProfileStore(t)

	_, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-bump", Name: "P", NodeType: "http.request",
		Config: map[string]any{},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	first, err := st.GetConfigProfile("profile-bump")
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	// A bundle change is a content change: it must append a version.
	updated, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-bump", Name: "P", NodeType: "http.request",
		Config: map[string]any{},
		Bundle: &ProfileBundle{Mappings: map[string]string{"query": "m-1"}},
	}, first.Version, "user-1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}
	if updated.Bundle == nil || updated.Bundle.Mappings["query"] != "m-1" {
		t.Fatalf("updated bundle = %+v", updated.Bundle)
	}

	// Removing the bundle is also a versioned change.
	cleared, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-bump", Name: "P", NodeType: "http.request",
		Config: map[string]any{},
	}, updated.Version, "user-1")
	if err != nil {
		t.Fatalf("update 2: %v", err)
	}
	if cleared.Bundle != nil {
		t.Fatalf("bundle should be nil after clear, got %+v", cleared.Bundle)
	}
	if cleared.Version != 3 {
		t.Fatalf("version = %d, want 3", cleared.Version)
	}
}

func TestConfigProfileLegacyDocWithoutBundle(t *testing.T) {
	st := testProfileStore(t)

	// A pre-Phase-1 version doc: no bundle key anywhere. Simulated by
	// inserting the row the way the old code would have.
	at := now()
	legacy := `{"name":"Legacy","config":{"method":"GET"}}`
	if _, err := st.db.Exec(`INSERT INTO config_profiles (id, name, node_type, description, current_version, created_at, updated_at)
		VALUES ('profile-legacy', 'Legacy', 'http.request', '', 1, ?, ?)`, at, at); err != nil {
		t.Fatalf("insert profile: %v", err)
	}
	if _, err := st.db.Exec(`INSERT INTO config_profile_versions (profile_id, version, doc, content_hash, actor, created_at)
		VALUES ('profile-legacy', 1, ?, ?, 'user-1', ?)`, legacy, contentHash([]byte(legacy)), at); err != nil {
		t.Fatalf("insert version: %v", err)
	}

	got, err := st.GetConfigProfile("profile-legacy")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Bundle != nil {
		t.Fatalf("legacy doc must read with nil bundle, got %+v", got.Bundle)
	}
	if got.Name != "Legacy" {
		t.Fatalf("name = %q", got.Name)
	}

	// The raw doc must not grow a bundle key by being read.
	var doc []byte
	if err := st.readRow(`SELECT v.doc FROM config_profile_versions v WHERE v.profile_id = ? AND v.version = 1`, "profile-legacy").Scan(&doc); err != nil {
		t.Fatalf("read doc: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(doc, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := raw["bundle"]; ok {
		t.Fatalf("legacy doc gained a bundle key: %s", doc)
	}
	if strings.Contains(string(doc), "bundle") {
		t.Fatalf("legacy doc text changed: %s", doc)
	}

	// Updating a legacy profile keeps the old version readable as-is and
	// writes the bundle only into the new version.
	updated, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-legacy", Name: "Legacy", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
		Bundle: &ProfileBundle{Schemas: map[string]string{"input": "s-1"}},
	}, 1, "user-1")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("version = %d, want 2", updated.Version)
	}
	v1, err := st.GetConfigProfileVersion("profile-legacy", 1)
	if err != nil {
		t.Fatalf("get v1: %v", err)
	}
	if v1.Bundle != nil {
		t.Fatalf("v1 must stay bundle-free, got %+v", v1.Bundle)
	}
	v2, err := st.GetConfigProfileVersion("profile-legacy", 2)
	if err != nil {
		t.Fatalf("get v2: %v", err)
	}
	if v2.Bundle == nil || v2.Bundle.Schemas["input"] != "s-1" {
		t.Fatalf("v2 bundle = %+v", v2.Bundle)
	}
}
