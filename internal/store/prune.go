package store

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type PruneResult struct {
	Runs   int64 `json:"runs"`
	Events int64 `json:"events"`
	Blobs  int64 `json:"blobs"`
	// Bytes is the blob storage reclaimed (or that a dry run would reclaim).
	Bytes int64 `json:"bytes"`
}

func (s *Store) Prune(before time.Time) (PruneResult, error) {
	var result PruneResult
	cutoff := before.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.Begin()
	if err != nil {
		return result, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id FROM runs
	                       WHERE status IN ('done', 'failed', 'cancelled')
	                         AND COALESCE(finished_at, started_at) < ?`, cutoff)
	if err != nil {
		return result, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}

	// Batch run deletion: one statement per chunk of runs instead of two
	// per run, so pruning a large history does not grow the statement count
	// linearly. The chunk also stays far below SQLite's variable limit.
	for start := 0; start < len(ids); start += pruneChunk {
		end := start + pruneChunk
		if end > len(ids) {
			end = len(ids)
		}
		args := make([]any, 0, end-start)
		marks := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			marks = append(marks, "?")
			args = append(args, id)
		}
		in := "(" + strings.Join(marks, ",") + ")"
		res, err := tx.Exec(`DELETE FROM events WHERE run_id IN `+in, args...)
		if err != nil {
			return result, err
		}
		if n, err := res.RowsAffected(); err == nil {
			result.Events += n
		}
		res, err = tx.Exec(`DELETE FROM runs WHERE id IN `+in, args...)
		if err != nil {
			return result, err
		}
		if n, err := res.RowsAffected(); err == nil {
			result.Runs += n
		}
	}

	keep, err := blobKeepSet(func(q string, args ...any) (*sql.Rows, error) { return tx.Query(q, args...) })
	if err != nil {
		return result, err
	}

	rows, err = tx.Query(`SELECT hash, size FROM blobs WHERE created_at < ?`, cutoff)
	if err != nil {
		return result, err
	}
	var hashes []string
	for rows.Next() {
		var hash string
		var size int64
		if err := rows.Scan(&hash, &size); err != nil {
			rows.Close()
			return result, err
		}
		// Effect tapes live only in node.effects data.blob, so input/output
		// hashes alone are not a complete reference set. Keep every hash any
		// surviving event references, including events of non-terminal runs.
		if !keep[hash] {
			hashes = append(hashes, hash)
			result.Bytes += size
		}
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	for _, hash := range hashes {
		if _, err := tx.Exec(`DELETE FROM blobs WHERE hash = ?`, hash); err != nil {
			return result, err
		}
		result.Blobs++
	}
	if err := tx.Commit(); err != nil {
		return result, err
	}
	// Notify after the commit: a woken stream re-lists and immediately sees
	// the post-deletion state instead of racing the transaction.
	for _, id := range ids {
		s.changes.Notify(id)
	}
	for _, hash := range hashes {
		_ = os.Remove(filepath.Join(s.blobDir, hash[:2], hash))
	}
	return result, nil
}

// pruneChunk bounds the number of bound variables per deletion statement.
const pruneChunk = 500

// blobKeepSet collects every blob hash still referenced by a surviving event.
// Effect tapes are referenced only inside node.effects data as {"blob": ...},
// so a set built from input_hash/output_hash alone prunes live replay data.
// Non-terminal runs keep their events, so their input/output/effect blobs are
// included automatically.
func blobKeepSet(query func(string, ...any) (*sql.Rows, error)) (map[string]bool, error) {
	return collectKeepHashes(query, nil)
}

// PruneEstimate reports exactly what Prune(before) would delete, without
// deleting anything. It uses the same selection rules: terminal runs older than
// the cutoff, their events, and blobs older than the cutoff that no surviving
// event (including effect tapes) still references. Effect tapes and the blobs
// of non-terminal runs are preserved.
func (s *Store) PruneEstimate(before time.Time) (PruneResult, error) {
	var result PruneResult
	cutoff := before.UTC().Format(time.RFC3339Nano)

	rows, err := s.read(`SELECT id FROM runs
	                       WHERE status IN ('done', 'failed', 'cancelled')
	                         AND COALESCE(finished_at, started_at) < ?`, cutoff)
	if err != nil {
		return result, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return result, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return result, err
	}
	result.Runs = int64(len(ids))

	// The keep set must match what Prune sees after deleting the candidate
	// events: scan only events of runs that survive.
	keep, err := s.blobKeepSetExcluding(ids)
	if err != nil {
		return result, err
	}

	blobs, err := s.read(`SELECT hash, size FROM blobs WHERE created_at < ?`, cutoff)
	if err != nil {
		return result, err
	}
	defer blobs.Close()
	for blobs.Next() {
		var hash string
		var size int64
		if err := blobs.Scan(&hash, &size); err != nil {
			return result, err
		}
		if !keep[hash] {
			result.Blobs++
			result.Bytes += size
		}
	}
	if err := blobs.Err(); err != nil {
		return result, err
	}

	for start := 0; start < len(ids); start += pruneChunk {
		end := start + pruneChunk
		if end > len(ids) {
			end = len(ids)
		}
		args := make([]any, 0, end-start)
		marks := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			marks = append(marks, "?")
			args = append(args, id)
		}
		res := s.readRow(`SELECT COUNT(*) FROM events WHERE run_id IN (`+
			strings.Join(marks, ",")+`)`, args...)
		var n int64
		if err := res.Scan(&n); err != nil {
			return result, err
		}
		result.Events += n
	}
	return result, nil
}

// blobKeepSetExcluding is the keep set over events whose run is not in the
// exclusion list; Prune sees the equivalent set after its transactional
// deletes, so the dry run and the real prune agree.
func (s *Store) blobKeepSetExcluding(exclude []string) (map[string]bool, error) {
	return collectKeepHashes(s.read, exclude)
}

// collectKeepHashes builds the referenced-blob keep set from three targeted
// queries: distinct input hashes, distinct output hashes, and the data column
// of node.effects rows only (the type predicate uses idx_events_type, and the
// input/output scans never touch the data column). Excluded runs are filtered
// out of every query so the estimate matches the post-deletion state.
func collectKeepHashes(query func(string, ...any) (*sql.Rows, error), exclude []string) (map[string]bool, error) {
	keep := map[string]bool{}
	notIn := ""
	args := make([]any, 0, len(exclude))
	if len(exclude) > 0 {
		marks := make([]string, len(exclude))
		for i, id := range exclude {
			marks[i] = "?"
			args = append(args, id)
		}
		notIn = ` AND run_id NOT IN (` + strings.Join(marks, ",") + `)`
	}
	scan := func(q string) error {
		rows, err := query(q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var hash string
			if err := rows.Scan(&hash); err != nil {
				return err
			}
			if hash != "" {
				keep[hash] = true
			}
		}
		return rows.Err()
	}
	if err := scan(`SELECT DISTINCT input_hash FROM events WHERE input_hash IS NOT NULL AND input_hash != ''` + notIn); err != nil {
		return nil, err
	}
	if err := scan(`SELECT DISTINCT output_hash FROM events WHERE output_hash IS NOT NULL AND output_hash != ''` + notIn); err != nil {
		return nil, err
	}
	rows, err := query(`SELECT data FROM events WHERE type = 'node.effects' AND data IS NOT NULL`+notIn, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rawData string
		if err := rows.Scan(&rawData); err != nil {
			return nil, err
		}
		if rawData == "" {
			continue
		}
		var data map[string]any
		if json.Unmarshal([]byte(rawData), &data) != nil {
			continue
		}
		if blob, ok := data["blob"].(string); ok && blob != "" {
			keep[blob] = true
		}
	}
	return keep, rows.Err()
}

// AcquireLease takes or renews a named lease. It returns false when another
// owner holds an unexpired lease. Expiry uses wall-clock nanoseconds so the
// comparison is numeric and immune to timestamp formatting.
func (s *Store) AcquireLease(name, owner string, ttl time.Duration) (bool, error) {
	now := time.Now().UTC()
	expires := now.Add(ttl).UnixNano()
	res, err := s.db.Exec(`
		INSERT INTO leases(name, owner, expires_at) VALUES(?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET owner = excluded.owner, expires_at = excluded.expires_at
		WHERE leases.owner = excluded.owner OR leases.expires_at < ?`,
		name, owner, expires, now.UnixNano())
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, nil
}

func (s *Store) ReleaseLease(name, owner string) error {
	_, err := s.db.Exec(`DELETE FROM leases WHERE name = ? AND owner = ?`, name, owner)
	return err
}

// ---------- events ----------
