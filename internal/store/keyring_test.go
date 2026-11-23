package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A pre-keyring deployment (only secret.key present) upgrades transparently:
// the keyring imports the old secret, old chains keep verifying, and old
// credentials stay readable.
func TestKeyringBootstrapsFromLegacySecret(t *testing.T) {
	dir := t.TempDir()
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.key"), secret, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open legacy store: %v", err)
	}
	if err := s.CreateRun(Run{ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("run", Event{Type: "node.finished", Data: map[string]any{"i": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCredential("slack", "slack", []byte("tok")); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: the keyring has bootstrapped around the same secret.
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen under keyring: %v", err)
	}
	defer s2.Close()
	if s2.pinned {
		t.Fatal("bootstrapped keyring must not be pinned")
	}
	events, err := s2.ListEvents("run")
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.VerifyEventChain("run", events); err != nil {
		t.Fatalf("legacy chain no longer verifies: %v", err)
	}
	_, plaintext, err := s2.GetCredential("slack")
	if err != nil || string(plaintext) != "tok" {
		t.Fatalf("legacy credential unreadable: %v", err)
	}
	// key_id was stamped onto the legacy row.
	var keyID string
	if err := s2.db.QueryRow(`SELECT COALESCE(key_id, '') FROM runs WHERE id = 'run'`).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != s2.legacyKey.id {
		t.Fatalf("legacy run key_id %q, want legacy key %q", keyID, s2.legacyKey.id)
	}
}

// Rotation must not invalidate anything: old chains verify, credentials stay
// readable, new runs sign with the new key, and an in-flight run (started
// before rotation) appends with the key recorded on its row.
func TestRotateKeyKeepsHistoryVerifiable(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateRun(Run{ID: "old", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AppendEvent("old", Event{Type: "node.finished", Data: map[string]any{"i": 1}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveCredential("slack", "slack", []byte("tok")); err != nil {
		t.Fatal(err)
	}
	oldKey := s.currentKey.id

	newID, err := s.RotateKey()
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newID == oldKey {
		t.Fatal("rotation reused the old key id")
	}

	oldEvents, err := s.ListEvents("old")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEventChain("old", oldEvents); err != nil {
		t.Fatalf("chain broke after rotation: %v", err)
	}
	if _, plaintext, err := s.GetCredential("slack"); err != nil || string(plaintext) != "tok" {
		t.Fatalf("credential unreadable after rotation: %v", err)
	}

	// An in-flight run appends under its original key and stays verifiable.
	if _, err := s.AppendEvent("old", Event{Type: "node.finished", Data: map[string]any{"i": 2}}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListEvents("old")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEventChain("old", events); err != nil {
		t.Fatalf("in-flight append broke the chain: %v", err)
	}

	// New runs record the new key id.
	if err := s.CreateRun(Run{ID: "new", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "done", Trigger: "manual", StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	var keyID string
	if err := s.db.QueryRow(`SELECT key_id FROM runs WHERE id = 'new'`).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if keyID != newID {
		t.Fatalf("new run key_id %q, want current key %q", keyID, newID)
	}

	// A restart reloads the rotated keyring and everything still verifies.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen after rotation: %v", err)
	}
	defer s2.Close()
	if s2.currentKey.id != newID {
		t.Fatalf("current key after restart %q, want %q", s2.currentKey.id, newID)
	}
	events2, err := s2.ListEvents("old")
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.VerifyEventChain("old", events2); err != nil {
		t.Fatalf("chain broke after restart: %v", err)
	}
	if _, plaintext, err := s2.GetCredential("slack"); err != nil || string(plaintext) != "tok" {
		t.Fatalf("credential unreadable after restart: %v", err)
	}
}

func TestRotatePinnedSecretRefused(t *testing.T) {
	t.Setenv("LOOM_SECRET", "pinned-secret")
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.RotateKey(); err == nil {
		t.Fatal("rotation of a pinned secret must be refused")
	} else if !strings.Contains(err.Error(), "pinned") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestListKeysMarksCurrentAndLegacy(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	newID, err := s.RotateKey()
	if err != nil {
		t.Fatal(err)
	}
	keys := s.ListKeys()
	if len(keys) != 2 {
		t.Fatalf("want 2 keys, got %d", len(keys))
	}
	if !keys[0].Current || keys[0].ID != newID {
		t.Fatalf("current key must be listed first: %+v", keys)
	}
	if keys[1].Current {
		t.Fatal("second key must not be current")
	}
	if !keys[1].Legacy {
		t.Fatal("bootstrap key must be marked legacy")
	}
}

// An unknown key id on a run row is a hard verification failure: the chain
// fails closed rather than re-blessing under a different key.
func TestUnknownKeyIDFailsClosed(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE runs SET key_id = 'kmissing' WHERE id = 'run'`); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyEventChain("run", events); err == nil {
		t.Fatal("unknown key id must fail verification")
	}
}
