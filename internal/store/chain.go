package store

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

type Event struct {
	Seq        int64          `json:"seq"`
	Type       string         `json:"type"`
	At         time.Time      `json:"at"`
	NodeID     string         `json:"nodeId,omitempty"`
	Attempt    int            `json:"attempt,omitempty"`
	InputHash  string         `json:"inputHash,omitempty"`
	OutputHash string         `json:"outputHash,omitempty"`
	DurationMs int64          `json:"durationMs,omitempty"`
	Error      string         `json:"error,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
	Actor      string         `json:"actor,omitempty"`
	// PrevHash and Hash form an HMAC-SHA256 chain over the stored event bytes.
	// An in-place edit or re-ordered event is detectable without the key.
	PrevHash string `json:"prevHash,omitempty"`
	Hash     string `json:"hash,omitempty"`
	// RunID is part of the digest but not the JSON payload.
	RunID string `json:"-"`
	// rawData is the exact bytes stored in the data column, used by
	// VerifyEventChain so verification never re-serializes a map.
	rawData []byte
}

func (s *Store) AppendEvent(runID string, ev Event) (int64, error) {
	seqs, err := s.AppendEvents(runID, []Event{ev})
	if err != nil {
		return 0, err
	}
	return seqs[0], nil
}

// AppendEvents appends multiple events for one run in a single transaction:
// the chain metadata (max seq, previous hash, chain version, signing key) is
// read once and the head seal is written once, so a batch costs one commit
// instead of one per event. Sequence numbers stay gap-free and the batch is
// atomic: one failed event rolls back the whole append.
func (s *Store) AppendEvents(runID string, events []Event) ([]int64, error) {
	if len(events) == 0 {
		return nil, nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var seq int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) + 1 FROM events WHERE run_id = ?`, runID).Scan(&seq); err != nil {
		return nil, err
	}
	var prev string
	if err := tx.QueryRow(`SELECT COALESCE(hash, '') FROM events WHERE run_id = ? ORDER BY seq DESC LIMIT 1`, runID).Scan(&prev); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	var chainVersion int
	var keyID string
	if err := tx.QueryRow(`SELECT COALESCE(chain_version, 0), COALESCE(key_id, '') FROM runs WHERE id = ?`, runID).Scan(&chainVersion, &keyID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	// Sign with the key recorded on the run row, not the current keyring
	// key: a run that started before a rotation must finish its chain under
	// the key that signed its earlier events.
	signingKey, err := s.keyFor(keyID)
	if err != nil {
		return nil, err
	}

	seqs := make([]int64, len(events))
	for i := range events {
		ev := events[i]
		// Round-trip through JSON first: RedactValue's type switch only matches
		// plain map[string]any, not named map types such as flow.Item, so a nested
		// response body used to be stored with its headers unmasked.
		raw, err := json.Marshal(ev.Data)
		if err != nil {
			return nil, err
		}
		data := RedactJSON(raw)
		ev.Seq = seq
		ev.RunID = runID
		ev.PrevHash = prev
		ev.Hash = s.hashEvent(signingKey.macKey, runID, ev, data, prev, chainVersion)
		if _, err := tx.Exec(`
			INSERT INTO events (run_id, seq, type, at, node_id, attempt, input_hash, output_hash, duration_ms, error, data, prev_hash, hash, actor)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			runID, seq, ev.Type, ev.At.UTC().Format(time.RFC3339Nano), ev.NodeID, ev.Attempt,
			ev.InputHash, ev.OutputHash, ev.DurationMs, ev.Error, data, ev.PrevHash, ev.Hash, ev.Actor); err != nil {
			return nil, err
		}
		seqs[i] = seq
		prev = ev.Hash
		seq++
	}
	// Keep the chain-head seal current inside the same transaction: the run
	// row must always record (count, terminal hash) as of this append.
	lastSeq := seq - 1
	if _, err := tx.Exec(`UPDATE runs SET head_seq = ?, head_mac = ? WHERE id = ?`,
		lastSeq, s.headMAC(signingKey.macKey, runID, lastSeq, prev), runID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	// The notifier coalesces by design: subscribers re-list everything after
	// their last delivered seq, so one wake per batch loses nothing.
	s.changes.Notify(runID)
	return seqs, nil
}

// hashEvent is the canonical keyed digest for one event. Length-prefixing every
// field avoids delimiter ambiguity. It hashes the stored bytes verbatim so
// verification never re-serializes a map.
func (s *Store) hashEvent(mac []byte, runID string, ev Event, data []byte, prev string, chainVersion int) string {
	h := hmac.New(sha256.New, mac)
	write := func(value string) {
		h.Write([]byte(strconv.Itoa(len(value))))
		h.Write([]byte{':'})
		h.Write([]byte(value))
	}
	write(runID)
	write(strconv.FormatInt(ev.Seq, 10))
	write(ev.Type)
	write(ev.At.UTC().Format(time.RFC3339Nano))
	write(ev.NodeID)
	write(strconv.Itoa(ev.Attempt))
	write(ev.InputHash)
	write(ev.OutputHash)
	write(strconv.FormatInt(ev.DurationMs, 10))
	write(ev.Error)
	if chainVersion >= 2 {
		write(ev.Actor)
	}
	write(string(data))
	write(prev)
	return hex.EncodeToString(h.Sum(nil))
}

// chainTag binds a run's chain version, execution mode and cassette source to
// the MAC key. Because it is an HMAC, database write access is not enough to
// downgrade the chain or flip a hermetic run onto the live host: the attacker
// would also have to forge a tag they cannot compute.
func (s *Store) chainTag(mac []byte, runID string, version int, effects, cassetteRunID string) string {
	h := hmac.New(sha256.New, mac)
	write := func(value string) {
		h.Write([]byte(strconv.Itoa(len(value))))
		h.Write([]byte{':'})
		h.Write([]byte(value))
	}
	write("loom-chain-tag-v1")
	write(runID)
	write(strconv.Itoa(version))
	write(effects)
	write(cassetteRunID)
	return hex.EncodeToString(h.Sum(nil))
}

// chainTagScheme is bumped whenever the tag payload changes. A one-time
// migration re-stamps existing rows under the new scheme; after that, an empty
// or mismatched tag is a tamper signal, never a reason to re-bless the row.
const chainTagScheme = "2"

// backfillChainTags stamps rows once per tag scheme. The marker lives in the
// data directory, not the database: the chain tag defends against database
// write access, so a DB-only attacker must not be able to delete the marker
// and have the next restart re-bless a downgraded chain or flipped effects.
func (s *Store) backfillChainTags() error {
	marker := filepath.Join(s.dir, "chain.migrated")
	if data, err := os.ReadFile(marker); err == nil {
		if string(data) == chainTagScheme || string(data) == chainTagScheme+"\n" {
			return nil
		}
	}
	type pending struct {
		id       string
		version  int
		effects  string
		cassette string
		keyID    string
	}
	var rows []pending
	// Migration-time read: runs before the read pool is opened, so it
	// stays on the writer connection.
	res, err := s.db.Query(`SELECT id, COALESCE(chain_version, 0), COALESCE(effects, ''), COALESCE(cassette_run_id, ''),
		COALESCE(key_id, '')
		FROM runs`)
	if err != nil {
		return err
	}
	for res.Next() {
		var p pending
		if err := res.Scan(&p.id, &p.version, &p.effects, &p.cassette, &p.keyID); err != nil {
			res.Close()
			return err
		}
		rows = append(rows, p)
	}
	if err := res.Err(); err != nil {
		res.Close()
		return err
	}
	res.Close()
	for _, p := range rows {
		key, err := s.keyFor(p.keyID)
		if err != nil {
			return err
		}
		if _, err := s.db.Exec(`UPDATE runs SET chain_tag = ? WHERE id = ?`,
			s.chainTag(key.macKey, p.id, p.version, p.effects, p.cassette), p.id); err != nil {
			return err
		}
	}
	return os.WriteFile(marker, []byte(chainTagScheme+"\n"), 0o600)
}

// VerifyEventChain checks gap-free sequencing and the keyed event chain. Runs
// created before the chain existed have no hashes; a log whose first event is
// unhashed is accepted as legacy, but only if the run's chain tag proves the
// stored chain_version was not downgraded.
//
// The events come from the caller, so the seal row and the log may be read on
// different snapshots; prefer VerifyChain, which reads both in one snapshot.
func (s *Store) VerifyEventChain(runID string, events []Event) error {
	var chainVersion int
	var chainTag, effects, cassetteRunID, keyID string
	var headSeq int64
	var headSeal string
	err := s.readRow(`SELECT COALESCE(chain_version, 0), COALESCE(chain_tag, ''),
		COALESCE(effects, ''), COALESCE(cassette_run_id, ''), COALESCE(key_id, ''),
		COALESCE(head_seq, 0), COALESCE(head_mac, '') FROM runs WHERE id = ?`, runID).
		Scan(&chainVersion, &chainTag, &effects, &cassetteRunID, &keyID, &headSeq, &headSeal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.verifyChainRow(runID, events, err == nil, chainVersion, chainTag, effects, cassetteRunID, keyID, headSeq, headSeal)
}

// VerifyChain reads the run's seal row and its event log in a single read
// snapshot, verifies the chain, and returns the consistent log. Reading them
// as separate statements let an append that committed in between look like
// tampering: the seal recorded N+1 events while the freshly read log had N.
func (s *Store) VerifyChain(runID string) ([]Event, error) {
	tx, err := s.readDB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var chainVersion int
	var chainTag, effects, cassetteRunID, keyID string
	var headSeq int64
	var headSeal string
	err = tx.QueryRow(`SELECT COALESCE(chain_version, 0), COALESCE(chain_tag, ''),
		COALESCE(effects, ''), COALESCE(cassette_run_id, ''), COALESCE(key_id, ''),
		COALESCE(head_seq, 0), COALESCE(head_mac, '') FROM runs WHERE id = ?`, runID).
		Scan(&chainVersion, &chainTag, &effects, &cassetteRunID, &keyID, &headSeq, &headSeal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	found := err == nil
	rows, err := tx.Query(`
		SELECT seq, type, at, COALESCE(node_id, ''), COALESCE(attempt, 0),
		       COALESCE(input_hash, ''), COALESCE(output_hash, ''), COALESCE(duration_ms, 0),
		       COALESCE(error, ''), data, COALESCE(prev_hash, ''), COALESCE(hash, ''),
		       COALESCE(actor, '')
		FROM events WHERE run_id = ? AND seq > 0 ORDER BY seq`, runID)
	if err != nil {
		return nil, err
	}
	events, err := scanEvents(rows, runID)
	if err != nil {
		return nil, err
	}
	if err := s.verifyChainRow(runID, events, found, chainVersion, chainTag, effects, cassetteRunID, keyID, headSeq, headSeal); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) verifyChainRow(runID string, events []Event, found bool,
	chainVersion int, chainTag, effects, cassetteRunID, keyID string,
	headSeq int64, headSeal string) error {
	// Verify with the key that signed the run; a key missing from the
	// keyring fails closed instead of re-blessing under another key.
	verifyKey, kerr := s.keyFor(keyID)
	if kerr != nil {
		return kerr
	}
	if found {
		want := s.chainTag(verifyKey.macKey, runID, chainVersion, effects, cassetteRunID)
		if !hmac.Equal([]byte(chainTag), []byte(want)) {
			return fmt.Errorf("run %s: chain version tag mismatch", runID)
		}
		// The head seal: the run row records how many events the chain ended
		// with and the hash of its last event, MACed under the signing key.
		// A gap-free prefix still verifies, so without this check deleting
		// the tail of a run was invisible.
		last := ""
		if len(events) > 0 {
			last = events[len(events)-1].Hash
		}
		if want := s.headMAC(verifyKey.macKey, runID, int64(len(events)), last); !hmac.Equal([]byte(headSeal), []byte(want)) {
			return fmt.Errorf("run %s: chain head seal mismatch (run row records %d events, %d present)", runID, headSeq, len(events))
		}
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			return fmt.Errorf("event log gap: expected seq %d, got %d", i+1, ev.Seq)
		}
	}
	if len(events) == 0 || events[0].Hash == "" {
		if chainVersion >= 1 {
			return fmt.Errorf("event chain missing for chained run %s", runID)
		}
		return nil
	}
	prev := ""
	for _, ev := range events {
		data := ev.rawData
		if data == nil {
			var err error
			data, err = json.Marshal(ev.Data)
			if err != nil {
				return fmt.Errorf("event %d data: %w", ev.Seq, err)
			}
		}
		if err := s.verifyChainEvent(verifyKey.macKey, runID, ev, data, prev, chainVersion); err != nil {
			return err
		}
		prev = ev.Hash
	}
	return nil
}

// verifyChainEvent checks one event's chain links — previous-hash chaining
// and the keyed hash over the raw stored bytes — against the predecessor
// hash. Sequence numbering is the caller's concern: the sliced verifier
// pre-checks the whole log, the streaming verifier counts as it scans.
func (s *Store) verifyChainEvent(mac []byte, runID string, ev Event, data []byte, prev string, chainVersion int) error {
	if ev.PrevHash != prev {
		return fmt.Errorf("event chain broken at seq %d: prev hash %q, want %q", ev.Seq, ev.PrevHash, prev)
	}
	if want := s.hashEvent(mac, runID, ev, data, prev, chainVersion); !hmac.Equal([]byte(ev.Hash), []byte(want)) {
		return fmt.Errorf("event %d hash mismatch", ev.Seq)
	}
	return nil
}

// verifyRunChain streams one run's chain verification inside a single read
// snapshot without materializing the event list or unmarshalling event
// data, so integrity scans over large stores stay memory-bounded. It
// returns the number of events verified.
func (s *Store) verifyRunChain(runID string) (int, error) {
	tx, err := s.readDB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var chainVersion int
	var chainTag, effects, cassetteRunID, keyID string
	var headSeq int64
	var headSeal string
	err = tx.QueryRow(`SELECT COALESCE(chain_version, 0), COALESCE(chain_tag, ''),
		COALESCE(effects, ''), COALESCE(cassette_run_id, ''), COALESCE(key_id, ''),
		COALESCE(head_seq, 0), COALESCE(head_mac, '') FROM runs WHERE id = ?`, runID).
		Scan(&chainVersion, &chainTag, &effects, &cassetteRunID, &keyID, &headSeq, &headSeal)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	found := err == nil

	verifyKey, kerr := s.keyFor(keyID)
	if kerr != nil {
		return 0, kerr
	}
	if found {
		want := s.chainTag(verifyKey.macKey, runID, chainVersion, effects, cassetteRunID)
		if !hmac.Equal([]byte(chainTag), []byte(want)) {
			return 0, fmt.Errorf("run %s: chain version tag mismatch", runID)
		}
	}

	rows, err := tx.Query(`
		SELECT seq, type, at, COALESCE(node_id, ''), COALESCE(attempt, 0),
		       COALESCE(input_hash, ''), COALESCE(output_hash, ''), COALESCE(duration_ms, 0),
		       COALESCE(error, ''), data, COALESCE(prev_hash, ''), COALESCE(hash, ''),
		       COALESCE(actor, '')
		FROM events WHERE run_id = ? ORDER BY seq`, runID)
	if err != nil {
		return 0, err
	}
	var count int64
	var last, first string
	var firstSet, legacy bool
	prev := ""
	for rows.Next() {
		var ev Event
		var at string
		var data []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &at, &ev.NodeID, &ev.Attempt,
			&ev.InputHash, &ev.OutputHash, &ev.DurationMs, &ev.Error, &data,
			&ev.PrevHash, &ev.Hash, &ev.Actor); err != nil {
			rows.Close()
			return 0, err
		}
		ev.RunID = runID
		ev.At, _ = time.Parse(time.RFC3339Nano, at)
		count++
		if ev.Seq != count {
			rows.Close()
			return 0, fmt.Errorf("event log gap: expected seq %d, got %d", count, ev.Seq)
		}
		if !firstSet {
			first, firstSet = ev.Hash, true
			legacy = first == ""
		}
		if !legacy {
			if err := s.verifyChainEvent(verifyKey.macKey, runID, ev, data, prev, chainVersion); err != nil {
				rows.Close()
				return 0, err
			}
		}
		prev = ev.Hash
		last = ev.Hash
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	if found {
		if want := s.headMAC(verifyKey.macKey, runID, count, last); !hmac.Equal([]byte(headSeal), []byte(want)) {
			return 0, fmt.Errorf("run %s: chain head seal mismatch (run row records %d events, %d present)", runID, headSeq, count)
		}
	}
	if count == 0 || first == "" {
		if chainVersion >= 1 {
			return 0, fmt.Errorf("event chain missing for chained run %s", runID)
		}
		return 0, nil
	}
	return int(count), nil
}

func (s *Store) ListEvents(runID string) ([]Event, error) {
	return s.ListEventsAfter(runID, 0)
}

// ListEventsAfter returns only events with a sequence greater than after, so
// streaming clients can poll incrementally instead of re-reading the log.
func (s *Store) ListEventsAfter(runID string, after int64) ([]Event, error) {
	rows, err := s.read(`
		SELECT seq, type, at, COALESCE(node_id, ''), COALESCE(attempt, 0),
		       COALESCE(input_hash, ''), COALESCE(output_hash, ''), COALESCE(duration_ms, 0),
		       COALESCE(error, ''), data, COALESCE(prev_hash, ''), COALESCE(hash, ''),
		       COALESCE(actor, '')
		FROM events WHERE run_id = ? AND seq > ? ORDER BY seq`, runID, after)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows, runID)
}

// scanEvents drains an event query into the Event slice, closing the rows.
func scanEvents(rows *sql.Rows, runID string) ([]Event, error) {
	defer rows.Close()

	var out []Event
	for rows.Next() {
		var ev Event
		var at string
		var data []byte
		if err := rows.Scan(&ev.Seq, &ev.Type, &at, &ev.NodeID, &ev.Attempt,
			&ev.InputHash, &ev.OutputHash, &ev.DurationMs, &ev.Error, &data,
			&ev.PrevHash, &ev.Hash, &ev.Actor); err != nil {
			return nil, err
		}
		ev.At, _ = time.Parse(time.RFC3339Nano, at)
		ev.RunID = runID
		ev.rawData = data
		if len(data) > 0 {
			_ = json.Unmarshal(data, &ev.Data)
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}
