package store

import (
	"errors"
	"testing"

	"loom/internal/graph"
)

func testProfileStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestConfigProfileCRUDAndVersioning(t *testing.T) {
	st := testProfileStore(t)

	created, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP JSON API", NodeType: "http.request",
		Description: "House style for JSON APIs",
		Config:      map[string]any{"method": "POST", "timeout": 30.0},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d, want 1", created.Version)
	}

	got, err := st.GetConfigProfile("profile-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "HTTP JSON API" || got.Config["method"] != "POST" {
		t.Fatalf("get returned %#v", got)
	}

	updated, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP JSON API", NodeType: "http.request",
		Config: map[string]any{"method": "GET", "timeout": 60.0},
	}, 1, "user-2")
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated version = %d, want 2", updated.Version)
	}

	// The old version is still readable and unchanged — append-only history.
	v1, err := st.GetConfigProfileVersion("profile-1", 1)
	if err != nil {
		t.Fatalf("get version 1: %v", err)
	}
	if v1.Config["method"] != "POST" {
		t.Fatalf("version 1 mutated: %#v", v1.Config)
	}

	current, _ := st.GetConfigProfile("profile-1")
	if current.Config["method"] != "GET" {
		t.Fatalf("current not updated: %#v", current.Config)
	}

	versions, err := st.ListConfigProfileVersions("profile-1")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions = %v, %v", versions, err)
	}
	if versions[0].Version != 2 || versions[1].Version != 1 {
		t.Fatalf("versions not descending: %#v", versions)
	}
	if versions[1].Actor != "user-1" || versions[0].Actor != "user-2" {
		t.Fatalf("actors not recorded: %#v", versions)
	}
}

func TestResolveProfileRefsUsesExplicitOwnership(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"method": "POST", "url": "https://profile.example.com", "timeout": 45.0},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	wf := &graph.Workflow{
		ID: "wf-owned", Name: "Owned", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, ProfileRef: "profile-1",
				Config: map[string]any{
					"method":  "POST", // Deliberate but equal: still an override.
					"timeout": 10.0,
				},
				ProfileOverrides: map[string]bool{"method": true, "timeout": true},
			},
		},
	}
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	config := wf.Nodes[1].Config
	if config["method"] != "POST" || config["timeout"] != 10.0 {
		t.Fatalf("explicit overrides lost: %#v", config)
	}
	if config["url"] != "https://profile.example.com" {
		t.Fatalf("sparse profile value did not apply: %#v", config)
	}
}

func TestResolveProfileRefsLegacyNodesKeepValueWins(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"method": "POST", "url": "https://profile.example.com"},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	// No ProfileOverrides: a legacy workflow keeps its complete stored config
	// as node-owned, including the generated GET default.
	wf := &graph.Workflow{
		ID: "wf-legacy", Name: "Legacy", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, ProfileRef: "profile-1",
				Config: map[string]any{"method": "GET", "url": "https://profile.example.com"},
			},
		},
	}
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	config := wf.Nodes[1].Config
	if config["method"] != "GET" {
		t.Fatalf("legacy node config changed: %#v", config)
	}
}

func TestResolveProfileRefsKeepsUnmarkedLocalKeys(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"method": "POST"},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	wf := &graph.Workflow{
		ID: "wf-local", Name: "Local", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, ProfileRef: "profile-1",
				Config:           map[string]any{"url": "https://node.example.com"},
				ProfileOverrides: map[string]bool{},
			},
		},
	}
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	config := wf.Nodes[1].Config
	if config["url"] != "https://node.example.com" {
		t.Fatalf("unmarked local key lost: %#v", config)
	}
	if config["method"] != "POST" {
		t.Fatalf("profile value did not apply: %#v", config)
	}
}

func TestUpgradeWorkflowProfilesPreservesExplicitEqualOverride(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"method": "POST", "timeout": 30.0},
	}, "user-1"); err != nil {
		t.Fatalf("create profile v1: %v", err)
	}
	if _, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"method": "GET", "timeout": 60.0},
	}, 1, "user-1"); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if err := st.RecordConfigProfileUsage("profile-1", 1, "wf-owned", 1); err != nil {
		t.Fatalf("record usage: %v", err)
	}

	wf := &graph.Workflow{
		ID: "wf-owned", Name: "Owned", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, ProfileRef: "profile-1",
				Config:           map[string]any{"method": "POST", "url": "https://node.example.com"},
				ProfileOverrides: map[string]bool{"method": true},
			},
		},
	}
	upgraded, changes, err := st.UpgradeWorkflowProfiles(wf)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	config := upgraded.Nodes[1].Config
	if config["method"] != "POST" {
		t.Fatalf("explicit equal-value override lost: %#v", config)
	}
	if config["timeout"] != 60.0 {
		t.Fatalf("inherited value did not follow profile: %#v", config)
	}
	for _, change := range changes {
		if change.Key == "config.method" {
			t.Fatalf("override appeared as an inherited upgrade change: %#v", change)
		}
	}
	if upgraded.Nodes[1].ProfileOverrides["method"] != true {
		t.Fatalf("override metadata lost: %#v", upgraded.Nodes[1].ProfileOverrides)
	}
}

func TestConfigProfileOptimisticConcurrency(t *testing.T) {
	st := testProfileStore(t)
	_, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "A", NodeType: "http.request", Config: map[string]any{},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// First update from version 1 wins.
	if _, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "B", NodeType: "http.request", Config: map[string]any{},
	}, 1, "user-1"); err != nil {
		t.Fatalf("first update: %v", err)
	}

	// Second update from the same stale version 1 must lose.
	_, err = st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "C", NodeType: "http.request", Config: map[string]any{},
	}, 1, "user-2")
	if !errors.Is(err, ErrStaleConfigProfile) {
		t.Fatalf("stale update error = %v, want ErrStaleConfigProfile", err)
	}

	// Updating an unknown profile is not found, not stale.
	_, err = st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-2", Name: "X", NodeType: "http.request", Config: map[string]any{},
	}, 1, "user-1")
	if !errors.Is(err, ErrConfigProfileNotFound) {
		t.Fatalf("unknown update error = %v, want ErrConfigProfileNotFound", err)
	}

	// A fresh attempt from version 2 succeeds.
	if _, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "D", NodeType: "http.request", Config: map[string]any{},
	}, 2, "user-2"); err != nil {
		t.Fatalf("fresh update: %v", err)
	}
}

func TestConfigProfileDeleteGuardedByUsage(t *testing.T) {
	st := testProfileStore(t)
	_, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "A", NodeType: "http.request", Config: map[string]any{},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := st.DeleteConfigProfile("profile-1"); err != nil {
		t.Fatalf("delete unused: %v", err)
	}
	if err := st.DeleteConfigProfile("profile-1"); !errors.Is(err, ErrConfigProfileNotFound) {
		t.Fatalf("double delete = %v, want not found", err)
	}

	// Recreate, pin via usage, and confirm the guard.
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "A", NodeType: "http.request", Config: map[string]any{},
	}, "user-1"); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	if err := st.RecordConfigProfileUsage("profile-1", 1, "wf-1", 3); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if err := st.DeleteConfigProfile("profile-1"); !errors.Is(err, ErrConfigProfileInUse) {
		t.Fatalf("in-use delete = %v, want ErrConfigProfileInUse", err)
	}

	usage, err := st.ConfigProfileUsage("profile-1")
	if err != nil || len(usage) != 1 {
		t.Fatalf("usage = %v, %v", usage, err)
	}
	if usage[0].WorkflowID != "wf-1" || usage[0].WorkflowVersion != 3 {
		t.Fatalf("usage row = %#v", usage[0])
	}

	list, err := st.ListConfigProfiles()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v, %v", list, err)
	}
	if list[0].UsageCount != 1 {
		t.Fatalf("usage count = %d, want 1", list[0].UsageCount)
	}
}

// ApplyCatalogDefaults is the final effective-value layer for publish and run
// prep. A flow.merge node with no authored mode must resolve to the documented
// shallow default, not the append value the catalog briefly carried.
func TestApplyCatalogDefaultsMergeModeIsShallow(t *testing.T) {
	st := testProfileStore(t)
	wf := &graph.Workflow{
		ID: "merge-default", Name: "Merge default", Version: 1,
		Nodes: []graph.Node{
			{ID: "join", Type: "flow.merge", TypeVersion: 1, Config: map[string]any{}},
		},
	}
	st.ApplyCatalogDefaults(wf)
	if got := wf.Nodes[0].Config["mode"]; got != "shallow" {
		t.Fatalf("flow.merge mode = %#v, want shallow", got)
	}
	if got := wf.Nodes[0].ConfigSources["mode"]; got != graph.ConfigSourceCatalog {
		t.Fatalf("mode config source = %q, want %q", got, graph.ConfigSourceCatalog)
	}
}
