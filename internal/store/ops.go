package store

import (
	"time"
)

// StorageStats is the read-only operational view of the append-only
// store (storage-operations campaign, D8-1): everything is computed on
// demand with bounded queries — no new tables, no collectors.
type StorageStats struct {
	// Totals.
	Runs          int64 `json:"runs"`
	ActiveRuns    int64 `json:"activeRuns"`
	Events        int64 `json:"events"`
	Blobs         int64 `json:"blobs"`
	BlobBytes     int64 `json:"blobBytes"`
	DatabaseBytes int64 `json:"databaseBytes"` // page_count * page_size

	// Growth: events per day, oldest first, over the last
	// growthWindowDays days (D8-2).
	EventsPerDay []DayCount `json:"eventsPerDay"`

	// Projection: linear extension of the growth trend over the
	// window, to 30 days ahead. Zero when there is no trend yet.
	ProjectedEvents30d int64 `json:"projectedEvents30d"`

	// Shared blobs (the content-addressed dedup win): blobs referenced
	// by two or more events.
	SharedBlobs     int64 `json:"sharedBlobs"`
	SharedBlobBytes int64 `json:"sharedBlobBytes"`

	// Largest runs by storage footprint (top 10, D8-3).
	LargestRuns []RunStorage `json:"largestRuns"`

	// Storage by workflow (top 10 by bytes).
	LargestWorkflows []WorkflowStorage `json:"largestWorkflows"`

	// Reclaimable bytes at retention horizons (PruneEstimate rules:
	// terminal runs older than the horizon, plus unreferenced blobs).
	Reclaimable []ReclaimHorizon `json:"reclaimable"`

	generatedAt time.Time `json:"-"`
}

// GeneratedAt reports when the stats were computed.
func (s StorageStats) GeneratedAt() time.Time { return s.generatedAt }

type DayCount struct {
	Day    string `json:"day"` // YYYY-MM-DD (UTC)
	Events int64  `json:"events"`
}

type RunStorage struct {
	RunID        string `json:"runId"`
	WorkflowID   string `json:"workflowId"`
	WorkflowName string `json:"workflowName"`
	Status       string `json:"status"`
	Events       int64  `json:"events"`
	// Bytes is the approximate unique-blob footprint of the run's
	// events (input+output+effect tapes).
	Bytes int64  `json:"bytes"`
	At    string `json:"at"`
}

type WorkflowStorage struct {
	WorkflowID string `json:"workflowId"`
	Name       string `json:"name"`
	Runs       int64  `json:"runs"`
	Events     int64  `json:"events"`
	Bytes      int64  `json:"bytes"`
	LastActive string `json:"lastActive"`
}

type ReclaimHorizon struct {
	Days   int   `json:"days"`
	Runs   int64 `json:"runs"`
	Events int64 `json:"events"`
	Blobs  int64 `json:"blobs"`
	Bytes  int64 `json:"bytes"`
}

const (
	growthWindowDays = 14
	statsTop         = 10
)

// StorageStats computes the operational view. Horizons are the
// retention choices surfaced for forecasting (7/30/90 days).
func (s *Store) StorageStats() (StorageStats, error) {
	var stats StorageStats
	stats.generatedAt = time.Now()

	if err := s.readRow(`SELECT COUNT(*) FROM runs`).Scan(&stats.Runs); err != nil {
		return stats, err
	}
	if err := s.readRow(`SELECT COUNT(*) FROM runs
		WHERE status NOT IN ('done', 'failed', 'cancelled')`).Scan(&stats.ActiveRuns); err != nil {
		return stats, err
	}
	if err := s.readRow(`SELECT COUNT(*) FROM events`).Scan(&stats.Events); err != nil {
		return stats, err
	}
	if err := s.readRow(`SELECT COUNT(*), COALESCE(SUM(size), 0) FROM blobs`).
		Scan(&stats.Blobs, &stats.BlobBytes); err != nil {
		return stats, err
	}

	// Database size from SQLite's own page accounting.
	var pageCount, pageSize int64
	if err := s.readRow(`PRAGMA page_count`).Scan(&pageCount); err != nil {
		return stats, err
	}
	if err := s.readRow(`PRAGMA page_size`).Scan(&pageSize); err != nil {
		return stats, err
	}
	stats.DatabaseBytes = pageCount * pageSize

	// Daily event counts over the growth window, oldest first.
	rows, err := s.read(`SELECT substr(at, 1, 10) AS day, COUNT(*)
		FROM events
		WHERE at >= ?
		GROUP BY day ORDER BY day`,
		time.Now().UTC().AddDate(0, 0, -growthWindowDays).Format(time.RFC3339Nano))
	if err != nil {
		return stats, err
	}
	for rows.Next() {
		var day DayCount
		if err := rows.Scan(&day.Day, &day.Events); err != nil {
			rows.Close()
			return stats, err
		}
		stats.EventsPerDay = append(stats.EventsPerDay, day)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return stats, err
	}
	rows.Close()
	stats.ProjectedEvents30d = projectTrend(stats.EventsPerDay, 30)

	// Shared blobs: hashes referenced by 2+ events (union of input and
	// output references). One pass, set-based.
	sharedBlobs, err := s.read(`
		WITH refs AS (
			SELECT input_hash AS hash FROM events WHERE input_hash IS NOT NULL AND input_hash != ''
			UNION ALL
			SELECT output_hash FROM events WHERE output_hash IS NOT NULL AND output_hash != ''
		)
		SELECT COUNT(*), COALESCE(SUM(b.size), 0)
		FROM (SELECT hash FROM refs GROUP BY hash HAVING COUNT(*) >= 2) multi
		JOIN blobs b ON b.hash = multi.hash`)
	if err != nil {
		return stats, err
	}
	for sharedBlobs.Next() {
		if err := sharedBlobs.Scan(&stats.SharedBlobs, &stats.SharedBlobBytes); err != nil {
			sharedBlobs.Close()
			return stats, err
		}
	}
	if err := sharedBlobs.Err(); err != nil {
		sharedBlobs.Close()
		return stats, err
	}
	sharedBlobs.Close()

	// Per-run unique referenced bytes, set-based: the inner UNION
	// dedupes (run, hash) pairs, so a blob referenced by several
	// events of the same run counts once.
	largest, err := s.read(`
		WITH run_refs AS (
			SELECT run_id, input_hash AS hash FROM events WHERE input_hash IS NOT NULL AND input_hash != ''
			UNION
			SELECT run_id, output_hash FROM events WHERE output_hash IS NOT NULL AND output_hash != ''
		),
		run_bytes AS (
			SELECT rr.run_id, COALESCE(SUM(b.size), 0) AS bytes
			FROM (SELECT DISTINCT run_id, hash FROM run_refs) rr
			JOIN blobs b ON b.hash = rr.hash
			GROUP BY rr.run_id
		)
		SELECT r.id, r.workflow_id, COALESCE(w.name, ''), r.status,
		       (SELECT COUNT(*) FROM events e WHERE e.run_id = r.id),
		       COALESCE(rb.bytes, 0), r.started_at
		FROM runs r
		LEFT JOIN run_bytes rb ON rb.run_id = r.id
		LEFT JOIN workflows w ON w.id = r.workflow_id
		ORDER BY rb.bytes DESC, r.started_at DESC
		LIMIT ?`, statsTop)
	if err != nil {
		return stats, err
	}
	for largest.Next() {
		var run RunStorage
		if err := largest.Scan(&run.RunID, &run.WorkflowID, &run.WorkflowName, &run.Status,
			&run.Events, &run.Bytes, &run.At); err != nil {
			largest.Close()
			return stats, err
		}
		stats.LargestRuns = append(stats.LargestRuns, run)
	}
	if err := largest.Err(); err != nil {
		largest.Close()
		return stats, err
	}
	largest.Close()

	// Storage by workflow (top N by bytes).
	byWorkflow, err := s.read(`
		WITH wf_refs AS (
			SELECT r.workflow_id AS wf, e.input_hash AS hash
			FROM events e JOIN runs r ON r.id = e.run_id
			WHERE e.input_hash IS NOT NULL AND e.input_hash != ''
			UNION
			SELECT r.workflow_id, e.output_hash
			FROM events e JOIN runs r ON r.id = e.run_id
			WHERE e.output_hash IS NOT NULL AND e.output_hash != ''
		),
		wf_bytes AS (
			SELECT wr.wf, COALESCE(SUM(b.size), 0) AS bytes
			FROM (SELECT DISTINCT wf, hash FROM wf_refs) wr
			JOIN blobs b ON b.hash = wr.hash
			GROUP BY wr.wf
		)
		SELECT r.workflow_id, COALESCE(w.name, r.workflow_id),
		       COUNT(DISTINCT r.id), COUNT(e.seq),
		       COALESCE(wb.bytes, 0), MAX(r.started_at)
		FROM runs r
		JOIN events e ON e.run_id = r.id
		LEFT JOIN workflows w ON w.id = r.workflow_id
		LEFT JOIN wf_bytes wb ON wb.wf = r.workflow_id
		GROUP BY r.workflow_id
		ORDER BY wb.bytes DESC
		LIMIT ?`, statsTop)
	if err != nil {
		return stats, err
	}
	for byWorkflow.Next() {
		var wf WorkflowStorage
		if err := byWorkflow.Scan(&wf.WorkflowID, &wf.Name, &wf.Runs,
			&wf.Events, &wf.Bytes, &wf.LastActive); err != nil {
			byWorkflow.Close()
			return stats, err
		}
		stats.LargestWorkflows = append(stats.LargestWorkflows, wf)
	}
	if err := byWorkflow.Err(); err != nil {
		byWorkflow.Close()
		return stats, err
	}
	byWorkflow.Close()

	// Reclaimable bytes at retention horizons.
	for _, days := range []int{7, 30, 90} {
		estimate, err := s.PruneEstimate(time.Now().UTC().AddDate(0, 0, -days))
		if err != nil {
			return stats, err
		}
		stats.Reclaimable = append(stats.Reclaimable, ReclaimHorizon{
			Days: days, Runs: estimate.Runs, Events: estimate.Events,
			Blobs: estimate.Blobs, Bytes: estimate.Bytes,
		})
	}
	return stats, nil
}

// projectTrend extends the daily counts linearly `ahead` days beyond
// the last observed day: each future day contributes its fitted level
// (intercept + slope·index), clamped at zero. With fewer than two
// days of history there is no trend to extend.
func projectTrend(perDay []DayCount, ahead int) int64 {
	if len(perDay) < 2 {
		return 0
	}
	// Least squares fit of y = a + b·i over the observed days.
	n := float64(len(perDay))
	var sumI, sumY, sumII, sumIY float64
	for i, day := range perDay {
		fi := float64(i)
		sumI += fi
		sumY += float64(day.Events)
		sumII += fi * fi
		sumIY += fi * float64(day.Events)
	}
	denom := n*sumII - sumI*sumI
	if denom == 0 {
		return 0
	}
	slope := (n*sumIY - sumI*sumY) / denom
	intercept := (sumY - slope*sumI) / n
	var total int64
	for k := 1; k <= ahead; k++ {
		level := intercept + slope*float64(len(perDay)-1+k)
		if level > 0 {
			total += int64(level)
		}
	}
	return total
}
