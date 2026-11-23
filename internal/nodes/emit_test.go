package nodes

import (
	"context"
	"strings"
	"testing"

	"loom/internal/flow"
)

// recordingHost is a Host stand-in that records published events.
type recordingHost struct {
	flow.Host
	events []flow.Event
}

func (h *recordingHost) Publish(ev flow.Event) { h.events = append(h.events, ev) }

// bareHost implements Host without the EventPublisher capability.
type bareHost struct{ flow.Host }

func emitInput(host flow.Host, hops int) flow.Input {
	return flow.Input{
		Config:  map[string]any{"event": "ping", "payload": map[string]any{"hello": "world"}},
		RunID:   "run_test",
		NodeID:  "emit",
		Host:    host,
		Context: &flow.Context{Run: flow.RunContext{ID: "run_test", Trigger: "manual", Status: "running", Hops: hops}},
	}
}

func TestEmitNodePublishesEvent(t *testing.T) {
	h := &recordingHost{}
	out, err := emitNode{}.Execute(context.Background(), emitInput(h, 0))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(h.events) != 1 {
		t.Fatalf("published %d events, want 1", len(h.events))
	}
	ev := h.events[0]
	if ev.Name != "ping" {
		t.Fatalf("event name = %q, want ping", ev.Name)
	}
	if ev.SourceRunID != "run_test" {
		t.Fatalf("source run = %q, want run_test", ev.SourceRunID)
	}
	if ev.Hops != 1 {
		t.Fatalf("hops = %d, want 1", ev.Hops)
	}
	if ev.Item["hello"] != "world" {
		t.Fatalf("payload = %v, want hello=world", ev.Item)
	}
	if out["main"]["hello"] != "world" {
		t.Fatalf("main output = %v, want the payload", out["main"])
	}
}

func TestEmitNodeRequiresEventName(t *testing.T) {
	in := emitInput(&recordingHost{}, 0)
	in.Config = map[string]any{"payload": map[string]any{"hello": "world"}}
	_, err := emitNode{}.Execute(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "event name is required") {
		t.Fatalf("err = %v, want event-name error", err)
	}
}

func TestEmitNodeCapsEventHops(t *testing.T) {
	h := &recordingHost{}
	_, err := emitNode{}.Execute(context.Background(), emitInput(h, MaxEventHops))
	if err == nil || !strings.Contains(err.Error(), "possible cycle") {
		t.Fatalf("err = %v, want cycle error at the hop cap", err)
	}
	if len(h.events) != 0 {
		t.Fatalf("published %d events past the cap, want 0", len(h.events))
	}
}

func TestEmitNodeNoOpWithoutPublisher(t *testing.T) {
	out, err := emitNode{}.Execute(context.Background(), emitInput(&bareHost{}, 0))
	if err != nil {
		t.Fatalf("execute without publisher: %v", err)
	}
	if out["main"]["hello"] != "world" {
		t.Fatalf("main output = %v, want the payload", out["main"])
	}
}
