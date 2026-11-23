package store

import (
	"bytes"
	"encoding/json"
	"testing"

	"loom/internal/graph"
)

// P7-4: ownership migration fuzz targets. The pure target exercises the
// equality primitive the migration depends on; the store target checks
// the invariants that matter for data safety:
//   - authored config is byte-identical after migration,
//   - ownership only marks keys the node stores, and never false,
//   - a key is inherited only with pinned-profile and published-snapshot
//     equality,
//   - a second open is idempotent,
//   - the report's counts match its decisions,
//   - the migrated doc passes the structural ownership health scan.
func FuzzJSONEqual(f *testing.F) {
	seeds := [][2]string{
		{"", ""},
		{"a", "a"},
		{"a", "b"},
		{"45", "45.0"},
		{"null", ""},
		{"[]", "[]"},
		{"{ }", "{}"},
		{`{"a":1}`, `{"a":1}`},
		{`{"a":1}`, `{"a":2}`},
		{"مرحبا", "مرحبا"},
		{"line\nbreak", "line\nbreak"},
	}
	for _, seed := range seeds {
		f.Add(seed[0], seed[1])
	}
	f.Fuzz(func(t *testing.T, a, b string) {
		if !jsonEqual(a, a) {
			t.Fatalf("jsonEqual(%q, %q) is not reflexive", a, a)
		}
		if jsonEqual(a, b) != jsonEqual(b, a) {
			t.Fatalf("jsonEqual(%q, %q) is not symmetric", a, b)
		}
	})
}

func FuzzMigrateProfileOwnershipPreservesAuthoredValues(f *testing.F) {
	type seed struct {
		method, url    string
		timeout        int
		includeTimeout bool
		publish        bool
		extra          string
	}
	seeds := []seed{
		{"POST", "https://profile.example.com", 45, true, true, "local"},
		{"POST", "https://profile.example.com", 45, true, false, ""},
		{"GET", "https://node.example.com", 90, true, true, "local"},
		{"", "", 0, false, true, ""},
		{"POST", "https://profile.example.com", 45, false, true, "local"},
	}
	for _, s := range seeds {
		f.Add(s.method, s.url, s.timeout, s.includeTimeout, s.publish, s.extra)
	}
	f.Fuzz(func(t *testing.T, method, url string, timeout int, includeTimeout, publish bool, extra string) {
		dir := t.TempDir()
		st, err := Open(dir)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		profileConfig := map[string]any{
			"method":  "POST",
			"url":     "https://profile.example.com",
			"timeout": 45.0,
		}
		if _, err := st.CreateConfigProfile(&ConfigProfile{
			ID: "profile-1", Name: "Fuzz profile", NodeType: "http.request",
			Config: profileConfig,
		}, "user-1"); err != nil {
			t.Fatalf("create profile: %v", err)
		}

		if publish {
			published := &graph.Workflow{
				ID: "wf-fuzz", Name: "Fuzz", Version: 1,
				Nodes: []graph.Node{
					{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
					{
						ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
						ProfileRef: "profile-1",
						Config:     cloneConfig(profileConfig),
					},
				},
			}
			if err := st.SaveWorkflow(published); err != nil {
				t.Fatalf("save published: %v", err)
			}
			if _, err := st.PublishWorkflow(published); err != nil {
				t.Fatalf("publish: %v", err)
			}
			if err := st.RecordConfigProfileUsage("profile-1", 1, "wf-fuzz", 1); err != nil {
				t.Fatalf("record usage: %v", err)
			}
		}

		draftConfig := map[string]any{"method": method, "url": url, "extra": extra}
		if includeTimeout {
			draftConfig["timeout"] = float64(timeout)
		}
		draft := &graph.Workflow{
			ID: "wf-fuzz", Name: "Fuzz", Version: 1,
			Nodes: []graph.Node{
				{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
				{
					ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
					ProfileRef: "profile-1",
					Config:     cloneConfig(draftConfig),
				},
			},
		}
		if err := st.SaveWorkflow(draft); err != nil {
			t.Fatalf("save draft: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close before migration: %v", err)
		}

		st, err = Open(dir)
		if err != nil {
			t.Fatalf("reopen before migration: %v", err)
		}
		t.Cleanup(func() { _ = st.Close() })
		plan, err := st.PlanProfileOwnership()
		if err != nil {
			t.Fatalf("plan: %v", err)
		}
		if !plan.HasChanges() {
			t.Fatal("migration plan found no legacy nodes")
		}
		if err := st.ApplyProfileOwnership(plan, "fuzz-migration"); err != nil {
			t.Fatalf("apply: %v", err)
		}
		migrated, err := st.GetWorkflow("wf-fuzz")
		if err != nil {
			t.Fatalf("get migrated: %v", err)
		}
		httpNode := findNode(migrated, "http")
		if httpNode == nil {
			t.Fatal("migrated workflow lost the http node")
		}
		if !jsonEqual(httpNode.Config, draftConfig) {
			t.Fatalf("authored config changed: before %s after %s",
				jsonValue(draftConfig), jsonValue(httpNode.Config))
		}
		if httpNode.ProfileOverrides == nil {
			t.Fatal("migration did not stamp an ownership map")
		}
		for key, override := range httpNode.ProfileOverrides {
			if _, stored := httpNode.Config[key]; !stored {
				t.Fatalf("ownership key %q is not stored in config", key)
			}
			if !override {
				t.Fatalf("ownership key %q is false; inherited keys must be omitted", key)
			}
		}
		for key := range draftConfig {
			if _, profileSupplies := profileConfig[key]; !profileSupplies {
				if httpNode.ProfileOverrides[key] {
					t.Fatalf("local key %q was marked as an override", key)
				}
				continue
			}
			wantInherited := publish && jsonEqual(draftConfig[key], profileConfig[key])
			if got := !httpNode.ProfileOverrides[key]; got != wantInherited {
				t.Fatalf("key %q inherited=%v, want %v (publish=%v, value=%s, profile=%s)",
					key, got, wantInherited, publish, jsonValue(draftConfig[key]), jsonValue(profileConfig[key]))
			}
		}

		health := &HealthReport{}
		if err := st.scanProfileOwnership(health); err != nil {
			t.Fatalf("scan ownership: %v", err)
		}
		if health.OwnershipFailureCount != 0 {
			t.Fatalf("migrated doc has ownership findings: %#v", health.OwnershipFailures)
		}

		report := plan.Report
		if report.Inherited+report.Overrides != len(report.Changes) {
			t.Fatalf("report counts do not match decisions: %#v", report)
		}
		if report.Nodes != 1 || report.Workflows != 1 {
			t.Fatalf("report scope = %d workflow(s) / %d node(s), want 1/1", report.Workflows, report.Nodes)
		}

		first, err := json.Marshal(migrated)
		if err != nil {
			t.Fatalf("marshal migrated: %v", err)
		}
		if err := st.Close(); err != nil {
			t.Fatalf("close after migration: %v", err)
		}
		st, err = Open(dir)
		if err != nil {
			t.Fatalf("reopen for idempotence: %v", err)
		}
		again, err := st.GetWorkflow("wf-fuzz")
		if err != nil {
			t.Fatalf("get after second open: %v", err)
		}
		secondPlan, err := st.PlanProfileOwnership()
		if err != nil {
			t.Fatalf("second plan: %v", err)
		}
		if secondPlan.HasChanges() {
			t.Fatal("second plan still wants to change nodes")
		}
		second, err := json.Marshal(again)
		if err != nil {
			t.Fatalf("marshal second: %v", err)
		}
		if !bytes.Equal(first, second) {
			t.Fatalf("second open changed the doc:\nfirst:  %s\nsecond: %s", first, second)
		}
	})
}

func findNode(wf *graph.Workflow, id string) *graph.Node {
	for i := range wf.Nodes {
		if wf.Nodes[i].ID == id {
			return &wf.Nodes[i]
		}
	}
	return nil
}
