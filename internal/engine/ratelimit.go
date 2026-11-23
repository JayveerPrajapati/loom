package engine

import (
	"context"
	"time"
)

// acquireRateSlot blocks until the key has room in its rolling minute and
// then reserves a slot. The slot is never recorded before the caller can
// actually proceed, so blocked callers cannot wake together and overshoot.
func (e *Engine) acquireRateSlot(ctx context.Context, key string, perMinute int) error {
	for {
		wait := e.rateWait(key, perMinute)
		if wait <= 0 {
			if e.rateRecord(key, perMinute) {
				return nil
			}
			continue
		}
		if err := e.Host.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// rateWait reports how long the caller must wait for the key's rolling
// minute to have room, without recording a call. Pruning expired timestamps
// keeps the window compact between attempts.
func (e *Engine) rateWait(key string, perMinute int) time.Duration {
	if perMinute < 1 {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	kept := e.pruneRateWindow(key, now)
	e.rateWindows[key] = kept
	if len(kept) < perMinute {
		return 0
	}
	wait := time.Minute - now.Sub(kept[0])
	if wait < 0 {
		wait = 0
	}
	return wait
}

// rateRecord reserves a slot for the key. It re-checks under the lock so two
// callers that both observed room cannot both record and overshoot the limit.
// It returns false when the window filled up in the meantime.
func (e *Engine) rateRecord(key string, perMinute int) bool {
	if perMinute < 1 {
		return true
	}
	e.mu.Lock()
	now := e.now()
	kept := e.pruneRateWindow(key, now)
	if len(kept) >= perMinute {
		e.rateWindows[key] = kept
		e.mu.Unlock()
		return false
	}
	e.rateWindows[key] = append(kept, now)
	hook := e.onRateRecord
	e.mu.Unlock()
	if hook != nil {
		hook(key, now)
	}
	return true
}

// pruneRateWindow drops timestamps outside the rolling minute. The caller must
// hold e.mu.
func (e *Engine) pruneRateWindow(key string, now time.Time) []time.Time {
	times := e.rateWindows[key]
	kept := times[:0]
	for _, t := range times {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	return kept
}
