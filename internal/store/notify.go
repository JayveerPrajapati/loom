package store

import "sync"

// RunChangeNotifier fans out run mutations to waiting SSE streams. A stream
// subscribes for its run id and blocks on the returned channel; every store
// mutation that can change what the stream would send — an appended event,
// a status flip, a resume, a cancellation, a terminal state — pokes that
// run's subscribers so the stream wakes and re-reads instead of polling.
//
// Signals are coalescing hints, not queues: a channel is buffered at one
// and Notify never blocks, so a burst of mutations leaves one pending
// signal and the woken stream catches up by listing everything after its
// last delivered seq. A lost or missed signal is covered by the subscriber's
// own fallback poll.
type RunChangeNotifier struct {
	mu   sync.Mutex
	subs map[string]map[chan struct{}]struct{}
}

// NewRunChangeNotifier builds an empty notifier.
func NewRunChangeNotifier() *RunChangeNotifier {
	return &RunChangeNotifier{subs: map[string]map[chan struct{}]struct{}{}}
}

// Subscribe registers for change signals about one run. The cancel func
// removes the registration and must be called exactly once; after cancel
// the channel delivers nothing and can be dropped.
func (n *RunChangeNotifier) Subscribe(runID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	n.mu.Lock()
	if n.subs == nil {
		n.subs = map[string]map[chan struct{}]struct{}{}
	}
	runSubs, ok := n.subs[runID]
	if !ok {
		runSubs = map[chan struct{}]struct{}{}
		n.subs[runID] = runSubs
	}
	runSubs[ch] = struct{}{}
	n.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			n.mu.Lock()
			if subs, ok := n.subs[runID]; ok {
				delete(subs, ch)
				if len(subs) == 0 {
					delete(n.subs, runID)
				}
			}
			n.mu.Unlock()
		})
	}
	return ch, cancel
}

// Notify wakes every subscriber of the run. It is safe to call with no
// subscribers and never blocks: channels are buffered at one, so the send
// has a default case and can be made while holding the lock (unlike an
// unbuffered send, holding the lock cannot deadlock here). A subscriber
// that already has a pending signal stays pending — one wake is enough to
// catch up.
func (n *RunChangeNotifier) Notify(runID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for ch := range n.subs[runID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
