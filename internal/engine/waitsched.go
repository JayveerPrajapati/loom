package engine

import (
	"context"
	"time"
)

// WaitScheduler resumes durable waits at their persisted deadline instead of
// waiting for the fallback sweep. Exactly one goroutine (Run) owns the timer
// handle; every state change — a run parking, the fallback tick, a timer
// firing, startup — funnels into a reconcile that first resumes due runs and
// then re-arms the timer from the store's earliest pending wakeup. Because
// ResumeTimer remains the atomic claim mechanism, a redundant or slightly
// early reconcile is harmless: it simply finds nothing due.
//
// The store is the source of truth, so Schedule is a non-blocking poke rather
// than a deadline handoff: the next reconcile re-reads MIN(resume_at) and arms
// for whatever is earliest. A later deadline therefore never displaces an
// earlier armed timer, and a lost poke is caught by the fallback sweep.
type WaitScheduler struct {
	resumeDue  func(ctx context.Context) int
	nextWakeup func(now time.Time) (*time.Time, error)
	interval   time.Duration // fallback sweep; crash recovery for missed timers

	// OnResume, when set, receives the count of runs a reconcile resumed.
	OnResume func(n int)

	poke  chan struct{}
	timer *time.Timer
}

// NewWaitScheduler builds a scheduler. resumeDue is typically
// Engine.ResumeDue and nextWakeup Store.NextWakeup; interval is the
// fallback sweep period and must be positive.
func NewWaitScheduler(resumeDue func(ctx context.Context) int, nextWakeup func(now time.Time) (*time.Time, error), interval time.Duration) *WaitScheduler {
	return &WaitScheduler{
		resumeDue:  resumeDue,
		nextWakeup: nextWakeup,
		interval:   interval,
		poke:       make(chan struct{}, 1),
	}
}

// Schedule asks the scheduler to recompute its timer. It never blocks and
// coalesces: a poke arriving while a reconcile is already pending is a no-op,
// because that reconcile reads the same store.
func (s *WaitScheduler) Schedule() {
	select {
	case s.poke <- struct{}{}:
	default:
	}
}

// Run drives the scheduler until ctx is cancelled. It reconciles immediately
// (restart recovery for waits that came due while the process was down), then
// reacts to pokes, the fallback sweep, and timer firings. Run returns exactly
// once; a timer armed when the context is cancelled is stopped and never
// fires into a dead loop.
func (s *WaitScheduler) Run(ctx context.Context) {
	s.reconcile(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		var timerC <-chan time.Time
		if s.timer != nil {
			timerC = s.timer.C
		}
		select {
		case <-ctx.Done():
			if s.timer != nil {
				s.timer.Stop()
				s.timer = nil
			}
			return
		case <-s.poke:
			s.reconcile(ctx)
		case <-ticker.C:
			s.reconcile(ctx)
		case <-timerC:
			s.reconcile(ctx)
		}
	}
}

// reconcile resumes everything due and re-arms the timer for the earliest
// pending wakeup. Only Run calls it, which is what keeps the timer handle
// single-owner.
func (s *WaitScheduler) reconcile(ctx context.Context) {
	if n := s.resumeDue(ctx); n > 0 && s.OnResume != nil {
		s.OnResume(n)
	}
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	next, err := s.nextWakeup(time.Now())
	if err != nil || next == nil {
		return
	}
	delay := time.Until(*next)
	if delay < 0 {
		delay = 0
	}
	s.timer = time.NewTimer(delay)
}
