package store

import "testing"

// A DB-only attacker could previously set chain_version=0 and blank the hashes,
// turning a v2 log into an "unhashed legacy" log that verified clean. The run's
// HMAC chain tag must make that downgrade detectable.
func TestEventChainDetectsVersionDowngrade(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE runs SET chain_version = 0 WHERE id = 'run'`); err != nil {
		t.Fatalf("downgrade version: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE events SET hash = '', prev_hash = '' WHERE run_id = 'run'`); err != nil {
		t.Fatalf("strip hashes: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("run", events); err == nil {
		t.Fatal("downgraded chain passed verification")
	}
}

// A genuine pre-chain run keeps verifying: unhashed events plus a tag that
// matches its version, and a head seal stamped over the legacy shape (blank
// terminal hash) the way sealChainHeads stamps genuine pre-chain runs.
func TestLegacyUnhashedChainWithMatchingTagVerifies(t *testing.T) {
	s, _ := chainFixture(t)
	if _, err := s.db.Exec(`UPDATE runs SET chain_version = 0, chain_tag = ? WHERE id = 'run'`, s.chainTag(s.currentKey.macKey, "run", 0, "", "")); err != nil {
		t.Fatalf("stamp legacy: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE events SET hash = '', prev_hash = '' WHERE run_id = 'run'`); err != nil {
		t.Fatalf("strip hashes: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE runs SET head_mac = ? WHERE id = 'run'`, s.headMAC(s.currentKey.macKey, "run", 3, "")); err != nil {
		t.Fatalf("re-stamp legacy head seal: %v", err)
	}
	events, err := s.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if err := s.VerifyEventChain("run", events); err != nil {
		t.Fatalf("genuine legacy chain rejected: %v", err)
	}
}
