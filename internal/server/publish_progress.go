package server

import (
	"errors"
	"regexp"
	"sync"
	"time"
)

const (
	// publishProgressFinishedTTL bounds how long a finished publish stays
	// queryable so a late poll can still read its final phases.
	publishProgressFinishedTTL = 5 * time.Minute
	// publishProgressStartedTTL bounds abandoned records whose handler
	// never reached finish (a panic or a wedged dependency).
	publishProgressStartedTTL = 30 * time.Minute
)

// publishProgressRegistry is the in-memory, per-publish progress view for
// the synchronous publish POST (audit 4E). Publish itself stays
// synchronous; the registry only lets the editor poll which phase is
// currently running.
type publishProgressRegistry struct {
	mu      sync.Mutex
	records map[string]*publishProgressRecord
}

type publishProgressRecord struct {
	ID         string
	WorkflowID string
	Actor      string
	Started    time.Time
	Finished   time.Time
	Done       bool
	Error      string
	Phases     []*publishPhaseState
}

type publishPhaseState struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // running | done | error
	Ms      int64  `json:"ms,omitempty"`
	started time.Time
}

// publishProgressSnapshot is the poll response body. Actor is server-only:
// it backs the ownership check in publishStatus and never serializes.
type publishProgressSnapshot struct {
	PublishID  string              `json:"publishId"`
	WorkflowID string              `json:"workflowId"`
	Done       bool                `json:"done"`
	Error      string              `json:"error,omitempty"`
	Phases     []publishPhaseState `json:"phases"`
	Actor      string              `json:"-"`
}

var (
	publishIDPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)
	errInvalidPublishID = errors.New("publishId must be 8-64 characters using only letters, digits, '_' and '-'")
)

// start creates the record for one publish and prunes expired ones. An
// empty publishID opts out of tracking entirely (nil record).
func (s *publishProgressRegistry) start(publishID, workflowID, actor string) *publishProgressRecord {
	if s == nil || publishID == "" {
		return nil
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[string]*publishProgressRecord{}
	}
	for id, rec := range s.records {
		if rec == nil {
			delete(s.records, id)
			continue
		}
		if rec.Done && !rec.Finished.IsZero() && now.Sub(rec.Finished) > publishProgressFinishedTTL {
			delete(s.records, id)
			continue
		}
		if now.Sub(rec.Started) > publishProgressStartedTTL {
			delete(s.records, id)
		}
	}
	rec := &publishProgressRecord{
		ID: publishID, WorkflowID: workflowID, Actor: actor, Started: now,
		Phases: []*publishPhaseState{},
	}
	s.records[publishID] = rec
	return rec
}

// phase closes the currently running phase with its elapsed ms and opens
// the named phase as running. Nil-safe so callers without tracking (the
// upgrade-apply path) can share publishWorkflowDoc.
func (s *publishProgressRegistry) phase(rec *publishProgressRecord, name string) {
	if s == nil || rec == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	closeRunningPhase(rec, now, "done")
	rec.Phases = append(rec.Phases, &publishPhaseState{
		Name: name, Status: "running", started: now,
	})
}

// finish closes the running phase (done, or error with the message) and
// marks the record complete.
func (s *publishProgressRegistry) finish(rec *publishProgressRecord, err error) {
	if s == nil || rec == nil {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	status := "done"
	if err != nil {
		status = "error"
		rec.Error = err.Error()
	}
	closeRunningPhase(rec, now, status)
	rec.Done = true
	rec.Finished = now
}

// snapshot returns a JSON-ready copy of one publish's progress under lock.
func (s *publishProgressRegistry) snapshot(publishID string) (publishProgressSnapshot, bool) {
	if s == nil || publishID == "" {
		return publishProgressSnapshot{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.records[publishID]
	if !ok || rec == nil {
		return publishProgressSnapshot{}, false
	}
	snap := publishProgressSnapshot{
		PublishID:  rec.ID,
		WorkflowID: rec.WorkflowID,
		Done:       rec.Done,
		Error:      rec.Error,
		Actor:      rec.Actor,
		Phases:     make([]publishPhaseState, 0, len(rec.Phases)),
	}
	for _, phase := range rec.Phases {
		if phase == nil {
			continue
		}
		snap.Phases = append(snap.Phases, publishPhaseState{
			Name: phase.Name, Status: phase.Status, Ms: phase.Ms,
		})
	}
	return snap, true
}

// closeRunningPhase closes the last phase when it is still running.
// Callers hold the registry lock.
func closeRunningPhase(rec *publishProgressRecord, now time.Time, status string) {
	if len(rec.Phases) == 0 {
		return
	}
	last := rec.Phases[len(rec.Phases)-1]
	if last == nil || last.Status != "running" {
		return
	}
	last.Status = status
	last.Ms = now.Sub(last.started).Milliseconds()
	if last.Ms < 0 {
		last.Ms = 0
	}
}
