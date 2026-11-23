// Package bus provides an in-process publish/subscribe bus for workflow
// events: flow.emit nodes publish, workflows with a matching trigger.event
// node start a run.
package bus

import (
	"sync"

	"loom/internal/flow"
)

type Bus struct {
	mu       sync.RWMutex
	handlers []func(flow.Event)
}

// Subscribe registers a handler for every published event. Handlers run on
// the publisher's goroutine; a panicking handler is recovered so it cannot
// take down the emitting run.
func (b *Bus) Subscribe(fn func(flow.Event)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, fn)
}

// Publish fans an event out to every subscriber.
func (b *Bus) Publish(ev flow.Event) {
	b.mu.RLock()
	handlers := make([]func(flow.Event), len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()
	for _, fn := range handlers {
		runHandler(fn, ev)
	}
}

func runHandler(fn func(flow.Event), ev flow.Event) {
	defer func() { _ = recover() }()
	fn(ev)
}
