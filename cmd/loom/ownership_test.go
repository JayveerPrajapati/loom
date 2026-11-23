package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"loom/internal/graph"
	"loom/internal/store"
)

// P1: the explicit migration CLI. The data directory must never migrate on
// open; a dry-run writes a 0600 report without touching drafts; --apply
// refuses without a recent verified backup; the apply stamps ownership; and
// --rollback restores the exact pre-migration draft.
func TestOwnershipMigrateDryRunApplyRollback(t *testing.T) {
	if os.Getenv("LOOM_SECRET") != "" || os.Getenv("LOOM_SECRET_KEY_FILE") != "" {
		t.Skip("backup verification must use the archive keyring")
	}
	dir := t.TempDir()
	st, err := openStore(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.CreateConfigProfile(&store.ConfigProfile{
		ID: "profile-1", Name: "HTTP API", NodeType: "http.request",
		Config: map[string]any{"url": "https://profile.example.com"},
	}, "user-1"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if err := st.SaveWorkflow(&graph.Workflow{
		ID: "wf-cli", Name: "CLI migration", Version: 1,
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
	before, err := st.GetWorkflow("wf-cli")
	if err != nil {
		t.Fatalf("get before: %v", err)
	}
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		t.Fatalf("marshal before: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := runOwnership([]string{"migrate", "--data", dir, "--dry-run"}); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	reportPath := filepath.Join(dir, store.OwnershipMigrationReportFile)
	info, err := os.Stat(reportPath)
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("report mode = %o, want 600", info.Mode().Perm())
	}
	raw, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	if !strings.Contains(string(raw), `"decision": "override"`) {
		t.Fatalf("report does not record the override decision:\n%s", raw)
	}

	// The dry-run is read-only.
	st, err = openStore(dir)
	if err != nil {
		t.Fatalf("reopen after dry run: %v", err)
	}
	afterDry, err := st.GetWorkflow("wf-cli")
	if err != nil {
		t.Fatalf("get after dry run: %v", err)
	}
	afterDryJSON, err := json.Marshal(afterDry)
	if err != nil {
		t.Fatalf("marshal after dry run: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close after dry run: %v", err)
	}
	if !bytes.Equal(beforeJSON, afterDryJSON) {
		t.Fatal("the dry-run changed the draft")
	}

	if err := runOwnership([]string{"migrate", "--data", dir, "--apply"}); err == nil ||
		!strings.Contains(err.Error(), "backup") {
		t.Fatalf("apply without a backup did not refuse: %v", err)
	}

	backup := filepath.Join(t.TempDir(), "ownership-backup.tar.gz")
	if err := runBackup([]string{"--data", dir, "--out", backup}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if err := runOwnership([]string{"migrate", "--data", dir, "--apply", "--backup", backup}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	st, err = openStore(dir)
	if err != nil {
		t.Fatalf("reopen after apply: %v", err)
	}
	migrated, err := st.GetWorkflow("wf-cli")
	if err != nil {
		t.Fatalf("get after apply: %v", err)
	}
	if migrated.Nodes[1].ProfileOverrides == nil || !migrated.Nodes[1].ProfileOverrides["url"] {
		t.Fatalf("apply did not stamp the ownership map: %#v", migrated.Nodes[1].ProfileOverrides)
	}
	if migrated.Nodes[1].Config["url"] != "https://node.example.com" {
		t.Fatalf("apply changed the authored value: %#v", migrated.Nodes[1].Config)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close after apply: %v", err)
	}

	if err := runOwnership([]string{"migrate", "--data", dir, "--rollback"}); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	st, err = openStore(dir)
	if err != nil {
		t.Fatalf("reopen after rollback: %v", err)
	}
	restored, err := st.GetWorkflow("wf-cli")
	if err != nil {
		t.Fatalf("get after rollback: %v", err)
	}
	restoredJSON, err := json.Marshal(restored)
	if err != nil {
		t.Fatalf("marshal restored: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close after rollback: %v", err)
	}
	if !bytes.Equal(beforeJSON, restoredJSON) {
		t.Fatalf("rollback did not restore the pre-migration draft:\nbefore: %s\nafter:  %s", beforeJSON, restoredJSON)
	}
}
