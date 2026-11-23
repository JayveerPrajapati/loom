package store_test

import (
	"testing"

	"loom/internal/graph"
	"loom/internal/store"
)

func TestCredentialUsageScansProfilesAndPublishedVersions(t *testing.T) {
	st, _ := openAuthStore(t)
	if err := st.SaveCredential("cred-1", "http", []byte(`{"token":"secret"}`)); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	profile, err := st.CreateConfigProfile(&store.ConfigProfile{
		Name: "HTTP profile", NodeType: "http.request",
		Config:         map[string]any{"method": "GET", "url": "https://api.example.com"},
		CredentialRefs: map[string]string{"auth": "cred-1"},
	}, "test")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}
	wf := &graph.Workflow{
		ID: "wf-1", Name: "Workflow one",
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", Config: map[string]any{}},
			{ID: "http", Type: "http.request", Config: map[string]any{"method": "GET", "url": "https://api.example.com"}, Credentials: map[string]string{"auth": "cred-1"}},
		},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatalf("publish workflow: %v", err)
	}

	usage, err := st.CredentialUsage("cred-1")
	if err != nil {
		t.Fatalf("credential usage: %v", err)
	}
	if len(usage.Profiles) != 1 {
		t.Fatalf("profiles = %#v, want one", usage.Profiles)
	}
	if usage.Profiles[0].ProfileID != profile.ID || usage.Profiles[0].Name != "HTTP profile" {
		t.Fatalf("profile use = %#v", usage.Profiles[0])
	}
	if len(usage.Profiles[0].Slots) != 1 || usage.Profiles[0].Slots[0] != "auth" {
		t.Fatalf("profile slots = %#v", usage.Profiles[0].Slots)
	}
	if len(usage.Workflows) != 1 {
		t.Fatalf("workflows = %#v, want one published version (current row deduped)", usage.Workflows)
	}
	if usage.Workflows[0].WorkflowID != "wf-1" || usage.Workflows[0].Version != 1 || usage.Workflows[0].Current {
		t.Fatalf("workflow use = %#v", usage.Workflows[0])
	}
	if len(usage.Workflows[0].Slots) != 1 || usage.Workflows[0].Slots[0] != "auth" {
		t.Fatalf("workflow slots = %#v", usage.Workflows[0].Slots)
	}

	counts, err := st.CredentialUsageCounts()
	if err != nil {
		t.Fatalf("credential usage counts: %v", err)
	}
	if counts["cred-1"] != 2 {
		t.Fatalf("usage count = %d, want 2 (profile + published version)", counts["cred-1"])
	}

	// A draft that differs from the published version adds a current row.
	wf.Nodes[1].Credentials = map[string]string{"auth": "cred-2"}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save draft: %v", err)
	}
	usage, err = st.CredentialUsage("cred-1")
	if err != nil {
		t.Fatalf("credential usage after draft: %v", err)
	}
	if len(usage.Workflows) != 1 || usage.Workflows[0].Current {
		t.Fatalf("historical version missing after draft change: %#v", usage.Workflows)
	}
	draftUsage, err := st.CredentialUsage("cred-2")
	if err != nil {
		t.Fatalf("credential usage for draft: %v", err)
	}
	if len(draftUsage.Workflows) != 1 || !draftUsage.Workflows[0].Current {
		t.Fatalf("draft row = %#v, want one current row", draftUsage.Workflows)
	}
}

func TestCredentialUsageCountsEachPublishedVersion(t *testing.T) {
	st, _ := openAuthStore(t)
	wf := &graph.Workflow{
		ID: "wf-versions", Name: "Versioned",
		Nodes: []graph.Node{{
			ID: "http", Type: "http.request", Config: map[string]any{"method": "GET", "url": "https://api.example.com"},
			Credentials: map[string]string{"auth": "cred-1"},
		}},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatalf("publish v1: %v", err)
	}
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	usage, err := st.CredentialUsage("cred-1")
	if err != nil {
		t.Fatalf("credential usage: %v", err)
	}
	if len(usage.Workflows) != 2 {
		t.Fatalf("workflows = %#v, want two published versions", usage.Workflows)
	}
	counts, err := st.CredentialUsageCounts()
	if err != nil {
		t.Fatalf("credential usage counts: %v", err)
	}
	if counts["cred-1"] != 2 {
		t.Fatalf("usage count = %d, want 2", counts["cred-1"])
	}
}

func TestCredentialMetadataAndMissingUsage(t *testing.T) {
	st, _ := openAuthStore(t)
	if err := st.SaveCredential("cred-meta", "postgres", []byte(`{"url":"postgres://example"}`)); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	exists, err := st.CredentialExists("cred-meta")
	if err != nil || !exists {
		t.Fatalf("CredentialExists = %v, %v", exists, err)
	}
	info, err := st.GetCredentialInfo("cred-meta")
	if err != nil {
		t.Fatalf("GetCredentialInfo: %v", err)
	}
	if info.Type != "postgres" || info.ID != "cred-meta" {
		t.Fatalf("credential info = %#v", info)
	}
	if _, err := st.GetCredentialInfo("missing"); err == nil {
		t.Fatal("GetCredentialInfo(missing) succeeded")
	}
	usage, err := st.CredentialUsage("missing")
	if err != nil {
		t.Fatalf("CredentialUsage(missing): %v", err)
	}
	if len(usage.Profiles) != 0 || len(usage.Workflows) != 0 {
		t.Fatalf("missing usage = %#v, want empty slices", usage)
	}
}
