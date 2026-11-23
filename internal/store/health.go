package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// HealthReport is the result of a full store integrity scan. OK is true only
// when every recorded event chain verifies and every blob in the index
// still hashes to its name. The *Count fields report total findings; the
// lists keep at most healthFailureCap details so a large broken store
// cannot grow the report without bound.
type HealthReport struct {
	RunsChecked   int
	EventsChecked int
	ChainFailures []string
	// ChainFailureCount is the total number of runs that failed chain
	// verification; len(ChainFailures) is capped.
	ChainFailureCount int

	BlobsIndexed int
	BlobFailures []string
	// BlobFailureCount is the total number of blobs that failed; the list
	// is capped.
	BlobFailureCount int

	OrphanBlobFiles   []string
	ReferencedMissing []string
	// OrphanBlobFileCount and ReferencedMissingCount are the totals; the
	// lists are capped.
	OrphanBlobFileCount    int
	ReferencedMissingCount int

	// OwnershipFailures lists invalid explicit-ownership or publish-source
	// metadata in workflow docs. OwnershipFailureCount is the total; the
	// list is capped.
	OwnershipFailures     []string
	OwnershipFailureCount int

	PinnedSecret bool
	CurrentKeyID string
	LegacyKeyID  string
	KeyCount     int

	// BootstrapPasswordSet records that LOOM_BOOTSTRAP_ADMIN_PASSWORD is
	// still exported in this process. It has no effect once users exist —
	// the flag exists so operators get a persistent signal to unset it.
	BootstrapPasswordSet bool
}

// healthFailureCap bounds each retained findings list. Beyond the cap the
// report records the total count and a truncation marker instead.
const healthFailureCap = 20

// OK reports whether the store is internally consistent.
func (r *HealthReport) OK() bool {
	return r.ChainFailureCount == 0 &&
		r.BlobFailureCount == 0 &&
		r.OrphanBlobFileCount == 0 &&
		r.ReferencedMissingCount == 0 &&
		r.OwnershipFailureCount == 0
}

// ownershipFailure records one capped ownership finding.
func (r *HealthReport) ownershipFailure(format string, args ...any) {
	r.OwnershipFailureCount++
	if len(r.OwnershipFailures) < healthFailureCap {
		r.OwnershipFailures = append(r.OwnershipFailures, fmt.Sprintf(format, args...))
	}
}

// VacuumInto writes a consistent, compacted copy of the database to dest.
// It is the snapshot primitive for backups: the copy is complete on its own,
// without the live WAL files.
func (s *Store) VacuumInto(dest string) error {
	escaped := strings.ReplaceAll(dest, "'", "''")
	if _, err := s.db.Exec(`VACUUM INTO '` + escaped + `'`); err != nil {
		return fmt.Errorf("vacuum into: %w", err)
	}
	return nil
}

// HealthOptions tunes a health scan for large stores.
type HealthOptions struct {
	// AfterRunID resumes the run scan positioned after this run in the
	// (started_at, id) order: runs up to and including it are assumed
	// verified by an earlier pass and skipped. Unknown ids verify from the
	// beginning. It exists so `loom doctor --resume` can continue a scan
	// interrupted mid-store instead of starting over.
	AfterRunID string
	// OnProgress fires after each successfully verified run with that run's
	// id and the cumulative counts, so a CLI can persist a checkpoint. It
	// never fires for a run that failed verification, so checkpoints only
	// ever mark clean prefixes.
	OnProgress func(runID string, runs, events int)
}

// CheckHealth walks every run's event chain and every indexed blob. It never
// executes workflow code and does not mutate the store. The event-chain
// verification doubles as a keyring check: a missing or wrong signing key
// fails the chain, so a backup without its keyring never passes as healthy.
func (s *Store) CheckHealth() (*HealthReport, error) {
	return s.CheckHealthOpts(HealthOptions{})
}

// CheckHealthOpts is the streaming form of CheckHealth: run ids are scanned
// one row at a time, each run's chain is verified without materializing its
// event list, and blob membership is checked with point queries instead of
// loading every blob hash into memory. Memory stays bounded regardless of
// store size.
func (s *Store) CheckHealthOpts(opts HealthOptions) (*HealthReport, error) {
	s.keyMu.RLock()
	report := &HealthReport{
		PinnedSecret: s.pinned,
		CurrentKeyID: s.currentKey.id,
		LegacyKeyID:  s.legacyKey.id,
		KeyCount:     len(s.keys),

		BootstrapPasswordSet: os.Getenv("LOOM_BOOTSTRAP_ADMIN_PASSWORD") != "",
	}
	s.keyMu.RUnlock()

	skipping := opts.AfterRunID != ""
	seenAfter := !skipping
	// A checkpoint may only ever mark an all-verified prefix: once a run
	// fails, progress stops firing for the rest of the scan (the scan
	// itself continues), so a resumed pass cannot skip past the damage.
	progressable := true
	scanRuns := func() error {
		ids, err := s.read(`SELECT id FROM runs ORDER BY started_at, id`)
		if err != nil {
			return err
		}
		for ids.Next() {
			var id string
			if err := ids.Scan(&id); err != nil {
				ids.Close()
				return err
			}
			if skipping {
				if id == opts.AfterRunID {
					skipping = false
					seenAfter = true
				}
				continue
			}
			events, err := s.verifyRunChain(id)
			if err != nil {
				report.ChainFailureCount++
				progressable = false
				if len(report.ChainFailures) < healthFailureCap {
					report.ChainFailures = append(report.ChainFailures, fmt.Sprintf("%s: %v", id, err))
				}
				continue
			}
			report.RunsChecked++
			report.EventsChecked += events
			if opts.OnProgress != nil && progressable {
				opts.OnProgress(id, report.RunsChecked, report.EventsChecked)
			}
		}
		if err := ids.Err(); err != nil {
			ids.Close()
			return err
		}
		return ids.Close()
	}
	if err := scanRuns(); err != nil {
		return nil, err
	}
	// The checkpointed run no longer exists (pruned or recreated); falling
	// behind means never verifying anything, so scan from the beginning.
	if !seenAfter {
		skipping = false
		if err := scanRuns(); err != nil {
			return nil, err
		}
	}
	if report.ChainFailureCount > len(report.ChainFailures) {
		report.ChainFailures = append(report.ChainFailures,
			fmt.Sprintf("… and %d more", report.ChainFailureCount-len(report.ChainFailures)))
	}

	// Every blob in the index must exist on disk and hash to its name.
	blobs, err := s.read(`SELECT hash, size FROM blobs`)
	if err != nil {
		return nil, err
	}
	for blobs.Next() {
		var hash string
		var size int64
		if err := blobs.Scan(&hash, &size); err != nil {
			blobs.Close()
			return nil, err
		}
		report.BlobsIndexed++
		if report.BlobFailureCount >= healthFailureCap {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.blobDir, hash[:2], hash))
		if err != nil {
			report.BlobFailureCount++
			report.BlobFailures = append(report.BlobFailures, fmt.Sprintf("%s: %v", hash, err))
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != hash {
			report.BlobFailureCount++
			report.BlobFailures = append(report.BlobFailures, fmt.Sprintf("%s: content does not hash to its name", hash))
			continue
		}
		if int64(len(data)) != size {
			report.BlobFailureCount++
			report.BlobFailures = append(report.BlobFailures, fmt.Sprintf("%s: size %d, index says %d", hash, len(data), size))
		}
	}
	if err := blobs.Err(); err != nil {
		blobs.Close()
		return nil, err
	}
	blobs.Close()
	if report.BlobFailureCount > len(report.BlobFailures) {
		report.BlobFailures = append(report.BlobFailures,
			fmt.Sprintf("… and %d more", report.BlobFailureCount-len(report.BlobFailures)))
	}

	// Every hash referenced by an event column must be in the index. Checked
	// with point queries so the blob index never has to be held in memory.
	refs, err := s.read(`SELECT DISTINCT input_hash FROM events WHERE input_hash != ''
		UNION SELECT DISTINCT output_hash FROM events WHERE output_hash != ''`)
	if err != nil {
		return nil, err
	}
	for refs.Next() {
		var hash string
		if err := refs.Scan(&hash); err != nil {
			refs.Close()
			return nil, err
		}
		var one int
		if err := s.readRow(`SELECT 1 FROM blobs WHERE hash = ?`, hash).Scan(&one); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				report.ReferencedMissingCount++
				if len(report.ReferencedMissing) < healthFailureCap {
					report.ReferencedMissing = append(report.ReferencedMissing, hash)
				}
				continue
			}
			refs.Close()
			return nil, err
		}
	}
	if err := refs.Err(); err != nil {
		refs.Close()
		return nil, err
	}
	refs.Close()
	if report.ReferencedMissingCount > len(report.ReferencedMissing) {
		report.ReferencedMissing = append(report.ReferencedMissing,
			fmt.Sprintf("… and %d more", report.ReferencedMissingCount-len(report.ReferencedMissing)))
	}

	// Files on disk that the index does not know about. They are not corrupt
	// state by themselves, but they bloat backups and hide prune failures.
	err = filepath.WalkDir(s.blobDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil || d.IsDir() {
			return werr
		}
		name := d.Name()
		if len(name) == 64 {
			var one int
			switch err := s.readRow(`SELECT 1 FROM blobs WHERE hash = ?`, name).Scan(&one); {
			case errors.Is(err, sql.ErrNoRows):
				report.OrphanBlobFileCount++
				if len(report.OrphanBlobFiles) < healthFailureCap {
					report.OrphanBlobFiles = append(report.OrphanBlobFiles, name)
				}
			case err != nil:
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if report.OrphanBlobFileCount > len(report.OrphanBlobFiles) {
		report.OrphanBlobFiles = append(report.OrphanBlobFiles,
			fmt.Sprintf("… and %d more", report.OrphanBlobFileCount-len(report.OrphanBlobFiles)))
	}

	// Explicit profile ownership and publish provenance must name stored
	// keys and known origins; a malformed map would silently change
	// effective values or mislabel origins.
	if err := s.scanProfileOwnership(report); err != nil {
		return nil, err
	}
	if report.OwnershipFailureCount > len(report.OwnershipFailures) {
		report.OwnershipFailures = append(report.OwnershipFailures,
			fmt.Sprintf("… and %d more", report.OwnershipFailureCount-len(report.OwnershipFailures)))
	}
	return report, nil
}
