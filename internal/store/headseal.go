package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
)

// The chain-head seal closes the tail-truncation hole: a hash chain verifies
// any gap-free prefix, so deleting the last N events of a run left a
// verifying chain and loom doctor passed. Each run row now records how many
// events the chain had and the hash of its last event, MACed under the run's
// signing key. A database-only attacker can delete events but cannot forge
// the MAC, so the stored head no longer matches the rows that remain.

// headSealScheme is bumped whenever the sealed payload changes. The marker
// lives in the data directory, not the database — the seal defends against
// database write access, so a DB-only attacker must not be able to delete
// the marker and have the next restart re-bless a truncated store.
const headSealScheme = "1"

// headMAC is the keyed digest binding a run's event count and terminal hash.
// Length-prefixing mirrors hashEvent/chainTag so the fields cannot be
// re-split ambiguously.
func (s *Store) headMAC(mac []byte, runID string, seq int64, lastHash string) string {
	h := hmac.New(sha256.New, mac)
	write := func(value string) {
		h.Write([]byte(strconv.Itoa(len(value))))
		h.Write([]byte{':'})
		h.Write([]byte(value))
	}
	write("loom-chain-head-v1")
	write(runID)
	write(strconv.FormatInt(seq, 10))
	write(lastHash)
	return hex.EncodeToString(h.Sum(nil))
}

// headSealBatchSize is how many still-unsealed runs one migration
// transaction stamps. Bounded so a large store migrates in resumable
// chunks: each committed batch is durable progress, and a crash mid-way
// resumes from the first still-unsealed row instead of redoing the whole
// table.
const headSealBatchSize = 500

// sealChainHeads stamps head_seq/head_mac onto every run once per scheme.
// It runs during migrate(), after stampLegacyKeyIDs and backfillChainTags,
// so per-run keys are already recorded. Idempotent: a sealed directory
// returns immediately, and a re-open leaves every stored hash unchanged.
// Work proceeds in committed batches of headSealBatchSize runs, so a
// failure mid-migration keeps the progress already made.
func (s *Store) sealChainHeads() error {
	marker := filepath.Join(s.dir, "head.sealed")
	if data, err := os.ReadFile(marker); err == nil {
		if string(data) == headSealScheme || string(data) == headSealScheme+"\n" {
			return nil
		}
	}
	sealed := 0
	for {
		n, err := s.sealChainHeadBatch()
		if err != nil {
			return err
		}
		if n == 0 {
			break
		}
		sealed += n
		log.Printf("head seal migration: stamped %d run(s) so far", sealed)
	}
	return os.WriteFile(marker, []byte(headSealScheme+"\n"), 0o600)
}

// sealChainHeadBatch stamps up to headSealBatchSize still-unsealed runs in
// one transaction and returns how many it sealed. Unsealed means
// head_mac = ”: live CreateRun and AppendEvent paths stamp every run they
// touch, so empty MACs only exist in a pre-migration database — which is
// what makes the state itself the resumable progress marker. The scheme
// marker stays the completion gate: once written, no re-blessing can happen,
// so a database-only attacker blanking a MAC cannot get a truncated chain
// re-sealed.
func (s *Store) sealChainHeadBatch() (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id, COALESCE(key_id, '') FROM runs WHERE head_mac = '' ORDER BY rowid LIMIT ?`, headSealBatchSize)
	if err != nil {
		return 0, err
	}
	type pending struct {
		id    string
		keyID string
	}
	var batch []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.id, &p.keyID); err != nil {
			rows.Close()
			return 0, err
		}
		batch = append(batch, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(batch) == 0 {
		return 0, nil
	}

	countStmt, err := tx.Prepare(`SELECT COUNT(*), COALESCE((SELECT hash FROM events WHERE run_id = ? ORDER BY seq DESC LIMIT 1), '') FROM events WHERE run_id = ?`)
	if err != nil {
		return 0, err
	}
	defer countStmt.Close()
	updateStmt, err := tx.Prepare(`UPDATE runs SET head_seq = ?, head_mac = ? WHERE id = ?`)
	if err != nil {
		return 0, err
	}
	defer updateStmt.Close()

	for _, p := range batch {
		key, err := s.keyFor(p.keyID)
		if err != nil {
			return 0, fmt.Errorf("seal run %s: %w", p.id, err)
		}
		var seq int64
		var last string
		if err := countStmt.QueryRow(p.id, p.id).Scan(&seq, &last); err != nil {
			return 0, err
		}
		if _, err := updateStmt.Exec(seq, s.headMAC(key.macKey, p.id, seq, last), p.id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(batch), nil
}
