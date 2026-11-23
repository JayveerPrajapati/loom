package store

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"loom/internal/graph"
)

func openStoreAt(t *testing.T, dir string) *Store {
	t.Helper()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	return st
}

func TestPlanProfileOwnershipUsesPinnedAndPublishedEvidence(t *testing.T) {
	dir := t.TempDir()
	st := openStoreAt(t, dir)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{
			"method":  "POST",
			"url":     "https://profile.example.com",
			"timeout": 45.0,
		},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	// Publish a snapshot whose node config is the resolved profile: the
	// pinned version and published value agree for every inherited key.
	published := &graph.Workflow{
		ID: "wf-migrate", Name: "Migrate", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef: "profile-1",
				Config: map[string]any{
					"method":  "POST",
					"url":     "https://profile.example.com",
					"timeout": 45.0,
				},
			},
		},
	}
	if err := st.SaveWorkflow(published); err != nil {
		t.Fatalf("save published draft: %v", err)
	}
	if _, err := st.PublishWorkflow(published); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := st.RecordConfigProfileUsage("profile-1", 1, "wf-migrate", 1); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	// The user then edits timeout after the publish; the draft carries
	// no ownership metadata (pre-Phase-1 data directory).
	if err := st.SaveWorkflow(&graph.Workflow{
		ID: "wf-migrate", Name: "Migrate", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef: "profile-1",
				Config: map[string]any{
					"method":  "POST",
					"url":     "https://profile.example.com",
					"timeout": 90.0,
				},
			},
		},
	}); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening must not migrate: ownership changes are explicit.
	st = openStoreAt(t, dir)
	wf, err := st.GetWorkflow("wf-migrate")
	if err != nil {
		t.Fatalf("get workflow: %v", err)
	}
	if wf.Nodes[1].ProfileOverrides != nil {
		t.Fatal("opening the data directory migrated ownership without an explicit command")
	}
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.HasChanges() {
		t.Fatal("plan found no legacy nodes to migrate")
	}
	report := plan.Report
	decisions := map[string]string{}
	for _, change := range report.Changes {
		decisions[change.Key] = change.Decision
	}
	if decisions["method"] != "inherited" || decisions["url"] != "inherited" {
		t.Fatalf("matching published values not reported inherited: %#v", decisions)
	}
	if decisions["timeout"] != "override" {
		t.Fatalf("edited value not reported as override: %#v", decisions)
	}
	if pinnedProfileVersionFor(t, report, "timeout") != 1 {
		t.Fatalf("report did not record the pinned version: %#v", report.Changes)
	}

	// Planning is read-only.
	wf, err = st.GetWorkflow("wf-migrate")
	if err != nil {
		t.Fatalf("get after plan: %v", err)
	}
	if wf.Nodes[1].ProfileOverrides != nil {
		t.Fatal("planning stamped the draft")
	}

	if err := st.ApplyProfileOwnership(plan, "mig-test"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wf, err = st.GetWorkflow("wf-migrate")
	if err != nil {
		t.Fatalf("get after apply: %v", err)
	}
	overrides := wf.Nodes[1].ProfileOverrides
	if overrides == nil {
		t.Fatal("apply did not stamp an ownership map")
	}
	if !overrides["timeout"] {
		t.Fatalf("post-publish edit was not preserved as an override: %#v", overrides)
	}
	if overrides["method"] || overrides["url"] {
		t.Fatalf("publish-resolved values were marked as overrides: %#v", overrides)
	}

	// A second plan has nothing to do and does not rewrite the doc.
	plan2, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("second plan: %v", err)
	}
	if plan2.HasChanges() {
		t.Fatal("a second plan still wants to change nodes")
	}

	first, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	st = openStoreAt(t, dir)
	t.Cleanup(func() { _ = st.Close() })
	again, err := st.GetWorkflow("wf-migrate")
	if err != nil {
		t.Fatalf("get after reopen: %v", err)
	}
	second, err := json.Marshal(again)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("reopening changed the doc:\nfirst:  %s\nsecond: %s", first, second)
	}
}

func pinnedProfileVersionFor(t *testing.T, report OwnershipMigrationReport, key string) int {
	t.Helper()
	for _, change := range report.Changes {
		if change.Key == key {
			return change.PinnedProfileVersion
		}
	}
	t.Fatalf("report has no decision for key %q", key)
	return 0
}

func TestMigrateProfileOwnershipNeverPublishedIsConservative(t *testing.T) {
	dir := t.TempDir()
	st := openStoreAt(t, dir)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"method": "POST", "timeout": 45.0},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := st.SaveWorkflow(&graph.Workflow{
		ID: "wf-never-published", Name: "Never published", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef: "profile-1",
				Config: map[string]any{
					"method":  "POST",
					"timeout": 45.0,
					"url":     "https://node.example.com",
				},
			},
		},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	st = openStoreAt(t, dir)
	t.Cleanup(func() { _ = st.Close() })
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.HasChanges() {
		t.Fatal("plan found no legacy nodes to migrate")
	}
	if err := st.ApplyProfileOwnership(plan, "mig-never-published"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wf, err := st.GetWorkflow("wf-never-published")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	overrides := wf.Nodes[1].ProfileOverrides
	if overrides == nil {
		t.Fatal("migration did not stamp an ownership map")
	}
	// Ambiguous equal values default to override; a key the profile does
	// not supply stays local without a marker.
	if !overrides["method"] || !overrides["timeout"] {
		t.Fatalf("ambiguous profile-supplied values were not preserved: %#v", overrides)
	}
	if overrides["url"] {
		t.Fatalf("local key was marked as an override: %#v", overrides)
	}
}

func TestMigrateProfileOwnershipWithoutProfileEvidencePreservesEverything(t *testing.T) {
	dir := t.TempDir()
	st := openStoreAt(t, dir)
	if err := st.SaveWorkflow(&graph.Workflow{
		ID: "wf-orphan", Name: "Orphan profile", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef: "missing-profile",
				Config:     map[string]any{"url": "https://node.example.com"},
			},
		},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	st = openStoreAt(t, dir)
	t.Cleanup(func() { _ = st.Close() })
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.HasChanges() {
		t.Fatal("plan found no legacy node to migrate")
	}
	if err := st.ApplyProfileOwnership(plan, "mig-orphan"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wf, err := st.GetWorkflow("wf-orphan")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !wf.Nodes[1].ProfileOverrides["url"] {
		t.Fatalf("stored value was not preserved without profile evidence: %#v", wf.Nodes[1].ProfileOverrides)
	}
}

func seedOwnershipFixture(t *testing.T, st *Store, workflowID string) {
	t.Helper()
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"url": "https://profile.example.com"},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := st.SaveWorkflow(&graph.Workflow{
		ID: workflowID, Name: "Migrate", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef: "profile-1",
				Config:     map[string]any{"url": "https://node.example.com"},
			},
		},
	}); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
}

func workflowDoc(t *testing.T, st *Store, workflowID string) []byte {
	t.Helper()
	var doc []byte
	if err := st.readRow(`SELECT doc FROM workflows WHERE id = ?`, workflowID).Scan(&doc); err != nil {
		t.Fatalf("read workflow doc: %v", err)
	}
	return doc
}

func TestApplyProfileOwnershipRefusesStalePlan(t *testing.T) {
	st := testProfileStore(t)
	seedOwnershipFixture(t, st, "wf-stale")
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.HasChanges() {
		t.Fatal("plan found no legacy node")
	}
	wf, err := st.GetWorkflow("wf-stale")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wf.Name = "Edited after the plan"
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save edit: %v", err)
	}
	if err := st.ApplyProfileOwnership(plan, "mig-stale"); err == nil ||
		!strings.Contains(err.Error(), "changed since the dry-run") {
		t.Fatalf("apply did not refuse a stale plan: %v", err)
	}
}

func TestRollbackProfileOwnershipRestoresOriginalBytes(t *testing.T) {
	st := testProfileStore(t)
	seedOwnershipFixture(t, st, "wf-rollback")
	original := workflowDoc(t, st, "wf-rollback")
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := st.ApplyProfileOwnership(plan, "mig-rollback"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if bytes.Equal(original, workflowDoc(t, st, "wf-rollback")) {
		t.Fatal("apply did not change the draft")
	}
	id, err := st.LatestOwnershipMigrationID()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if id != "mig-rollback" {
		t.Fatalf("latest migration = %q, want mig-rollback", id)
	}
	report, err := st.RollbackProfileOwnership("mig-rollback")
	if err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if report.Workflows != 1 || report.Nodes != 1 {
		t.Fatalf("rollback report = %#v, want one workflow and one node", report)
	}
	if !bytes.Equal(original, workflowDoc(t, st, "wf-rollback")) {
		t.Fatal("rollback did not restore the exact original bytes")
	}
}

func TestRollbackProfileOwnershipRefusesDrift(t *testing.T) {
	st := testProfileStore(t)
	seedOwnershipFixture(t, st, "wf-drift")
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := st.ApplyProfileOwnership(plan, "mig-drift"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	wf, err := st.GetWorkflow("wf-drift")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	wf.Name = "Edited after apply"
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save edit: %v", err)
	}
	if _, err := st.RollbackProfileOwnership("mig-drift"); err == nil ||
		!strings.Contains(err.Error(), "changed after the migration") {
		t.Fatalf("rollback did not refuse drift: %v", err)
	}
}

func TestApplyProfileOwnershipWritesAuditEvents(t *testing.T) {
	st := testProfileStore(t)
	seedOwnershipFixture(t, st, "wf-audit")
	plan, err := st.PlanProfileOwnership()
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if err := st.ApplyProfileOwnership(plan, "mig-audit"); err != nil {
		t.Fatalf("apply: %v", err)
	}
	events, err := st.ListAuthEvents(50)
	if err != nil {
		t.Fatalf("list auth events: %v", err)
	}
	for _, event := range events {
		if event.Type == "ownership.migrated" &&
			strings.Contains(event.Detail, `"workflowId":"wf-audit"`) &&
			strings.Contains(event.Detail, `"key":"url"`) {
			return
		}
	}
	t.Fatalf("no ownership.migrated audit event for wf-audit: %#v", events)
}

func TestProfileOverridesEmptyMapRoundTrips(t *testing.T) {
	st := testProfileStore(t)
	if err := st.SaveWorkflow(&graph.Workflow{
		ID: "wf-roundtrip", Name: "Round trip", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				Config:           map[string]any{"url": "https://node.example.com"},
				ProfileOverrides: map[string]bool{},
			},
		},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	wf, err := st.GetWorkflow("wf-roundtrip")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wf.Nodes[1].ProfileOverrides == nil {
		t.Fatal("empty profileOverrides map was dropped by the JSON round-trip")
	}
}

func TestCheckHealthDetectsInvalidOwnershipMetadata(t *testing.T) {
	st := testProfileStore(t)
	valid := &graph.Workflow{
		ID: "wf-valid", Name: "Valid", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef:       "profile-1",
				Config:           map[string]any{"url": "https://node.example.com"},
				ProfileOverrides: map[string]bool{"url": true},
				ConfigSources:    map[string]string{"url": graph.ConfigSourceNode},
			},
		},
	}
	if err := st.SaveWorkflow(valid); err != nil {
		t.Fatalf("save valid: %v", err)
	}
	report, err := st.CheckHealth()
	if err != nil {
		t.Fatalf("check health: %v", err)
	}
	if report.OwnershipFailureCount != 0 || !report.OK() {
		t.Fatalf("valid ownership flagged: %#v", report.OwnershipFailures)
	}

	invalid := &graph.Workflow{
		ID: "wf-invalid", Name: "Invalid", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef:       "profile-1",
				Config:           map[string]any{"url": "https://node.example.com"},
				ProfileOverrides: map[string]bool{"url": false, "ghost": true},
				ConfigSources: map[string]string{
					"url":   "bogus",
					"ghost": graph.ConfigSourceProfile,
				},
			},
		},
	}
	raw, err := json.Marshal(invalid)
	if err != nil {
		t.Fatalf("marshal invalid: %v", err)
	}
	if err := st.SaveWorkflow(invalid); err != nil {
		t.Fatalf("save invalid: %v", err)
	}
	// SaveWorkflow strips publish provenance, so write the malformed
	// source map straight into the stored doc to exercise the scan.
	if _, err := st.db.Exec(`UPDATE workflows SET doc = ? WHERE id = ?`, raw, invalid.ID); err != nil {
		t.Fatalf("write invalid doc: %v", err)
	}
	report, err = st.CheckHealth()
	if err != nil {
		t.Fatalf("check health: %v", err)
	}
	if report.OK() {
		t.Fatal("invalid ownership metadata passed the health check")
	}
	if report.OwnershipFailureCount != 4 {
		t.Fatalf("ownership findings = %d, want 4: %#v", report.OwnershipFailureCount, report.OwnershipFailures)
	}
	joined := strings.Join(report.OwnershipFailures, "\n")
	for _, want := range []string{
		`profileOverrides["url"] is false`,
		`profileOverrides["ghost"] marks a key the node does not store`,
		`configSources["url"] has unknown origin "bogus"`,
		`configSources["ghost"] names a key the node does not store`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing finding %q in:\n%s", want, joined)
		}
	}
}

func TestPublishedSnapshotRetainsConfigSources(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{
			"method":  "POST",
			"url":     "https://profile.example.com",
			"timeout": 45.0,
		},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	wf := &graph.Workflow{
		ID: "wf-sources", Name: "Sources", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "http", Type: "http.request", TypeVersion: 1, Name: "Fetch",
				ProfileRef: "profile-1",
				Config: map[string]any{
					"url":     "https://node.example.com",
					"timeout": 90.0,
				},
				ProfileOverrides: map[string]bool{"url": true, "timeout": true},
			},
		},
	}
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	st.ApplyCatalogDefaults(wf)
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatalf("publish: %v", err)
	}
	snapshot, err := st.GetWorkflowVersion("wf-sources", 1)
	if err != nil {
		t.Fatalf("get version: %v", err)
	}
	sources := snapshot.Nodes[1].ConfigSources
	want := map[string]string{
		"method":       graph.ConfigSourceProfile,
		"url":          graph.ConfigSourceNode,
		"timeout":      graph.ConfigSourceNode,
		"bodyType":     graph.ConfigSourceCatalog,
		"auth":         graph.ConfigSourceCatalog,
		"successCodes": graph.ConfigSourceCatalog,
	}
	for key, wantSource := range want {
		if sources[key] != wantSource {
			t.Fatalf("configSources[%q] = %q, want %q (all: %#v)", key, sources[key], wantSource, sources)
		}
	}
	if snapshot.Nodes[1].Config["timeout"] != 90.0 {
		t.Fatalf("override value lost in the snapshot: %#v", snapshot.Nodes[1].Config)
	}
	if snapshot.Nodes[1].Config["method"] != "POST" {
		t.Fatalf("profile value missing in the snapshot: %#v", snapshot.Nodes[1].Config)
	}
}

func TestPublishedSnapshotRetainsScriptSource(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateScript(&Script{
		ID: "script-1", Name: "Helper", Language: ScriptLanguageJavaScript,
		Source: "return 1;", TimeoutSeconds: 5,
	}, "user-1"); err != nil {
		t.Fatalf("create script: %v", err)
	}
	wf := &graph.Workflow{
		ID: "wf-script", Name: "Script", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{
				ID: "code", Type: "code.run", TypeVersion: 1, Name: "Code",
				Config: map[string]any{"language": "javascript", "scriptRef": "script-1"},
			},
		},
	}
	if _, err := st.ResolveScriptRefs(wf); err != nil {
		t.Fatalf("resolve script: %v", err)
	}
	sources := wf.Nodes[1].ConfigSources
	if sources["source"] != graph.ConfigSourceScript {
		t.Fatalf("inlined script source not annotated: %#v", sources)
	}
	if sources["timeoutSeconds"] != graph.ConfigSourceScript {
		t.Fatalf("script runtime default not annotated: %#v", sources)
	}
}
