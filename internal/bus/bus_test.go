package bus

import (
	"testing"

	"loom/internal/flow"
)

func TestBusDeliversToSubscribers(t *testing.T) {
	var got []flow.Event
	b := &Bus{}
	b.Subscribe(func(ev flow.Event) { got = append(got, ev) })
	b.Publish(flow.Event{Name: "ping", Item: flow.Item{"hello": "world"}, Hops: 1, SourceRunID: "run_x"})
	if len(got) != 1 {
		t.Fatalf("delivered %d events, want 1", len(got))
	}
	if got[0].Name != "ping" || got[0].Hops != 1 || got[0].SourceRunID != "run_x" {
		t.Fatalf("event = %+v, want the published fields", got[0])
	}
	if got[0].Item["hello"] != "world" {
		t.Fatalf("item = %v, want hello=world", got[0].Item)
	}
}

func TestBusSurvivesPanickingHandler(t *testing.T) {
	got := 0
	b := &Bus{}
	b.Subscribe(func(flow.Event) { panic("boom") })
	b.Subscribe(func(flow.Event) { got++ })
	b.Publish(flow.Event{Name: "ping"})
	if got != 1 {
		t.Fatalf("handler after the panicking one ran %d times, want 1", got)
	}
}
