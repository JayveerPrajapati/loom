package cassette

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"loom/internal/flow"
)

type memBlobs map[string][]byte

func (m memBlobs) SaveBlob(data []byte) (string, error) {
	hash := hashJSON(data)
	m[hash] = append([]byte(nil), data...)
	return hash, nil
}

func (m memBlobs) LoadBlob(hash string) ([]byte, error) {
	data, ok := m[hash]
	if !ok {
		return nil, errors.New("no such blob")
	}
	return append([]byte(nil), data...), nil
}

type countingHost struct {
	flow.Host
	calls int
	resp  flow.HTTPResponse
	err   error
}

func (h *countingHost) HTTP(context.Context, flow.HTTPRequest) (flow.HTTPResponse, error) {
	h.calls++
	return h.resp, h.err
}

func (h *countingHost) Query(context.Context, string, string) ([]flow.Item, error) {
	h.calls++
	return []flow.Item{{"ok": true}}, nil
}

func (h *countingHost) Slack(context.Context, string, string, string) error {
	h.calls++
	return nil
}

func (h *countingHost) Sleep(context.Context, time.Duration) error { return nil }
func (h *countingHost) Now() time.Time                             { return time.Unix(0, 0).UTC() }

type publisherHost struct {
	*countingHost
	published []flow.Event
}

func (h *publisherHost) Publish(ev flow.Event) { h.published = append(h.published, ev) }

func TestRecorderForwardsEventPublisherAndReplayerDoesNot(t *testing.T) {
	base := &publisherHost{countingHost: &countingHost{}}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)

	var host flow.Host = rec
	publisher, ok := host.(flow.EventPublisher)
	if !ok {
		t.Fatal("recorder dropped the optional EventPublisher capability")
	}
	publisher.Publish(flow.Event{Name: "ping"})
	if len(base.published) != 1 || base.published[0].Name != "ping" {
		t.Fatalf("published = %+v, want one ping", base.published)
	}

	var replayHost flow.Host = NewReplayer(&Tape{})
	if _, publishes := replayHost.(flow.EventPublisher); publishes {
		t.Fatal("a hermetic replayer must not publish events")
	}
}

func TestRecorderCapturesEffectsAndRedactsSecrets(t *testing.T) {
	base := &countingHost{resp: flow.HTTPResponse{Status: 200, Body: flow.Item{"id": "42"}}}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)

	resp, err := rec.HTTP(context.Background(), flow.HTTPRequest{
		Method: "post", URL: "https://example.test/x",
		Headers:        map[string]string{"Authorization": "Bearer secret", "Accept": "json"},
		Body:           map[string]any{"a": 1},
		IdempotencyKey: "run-1:node-1",
	})
	if err != nil {
		t.Fatalf("record http: %v", err)
	}
	if resp.Status != 200 {
		t.Fatalf("response not passed through: %+v", resp)
	}
	if _, err := rec.Query(context.Background(), "pg", "select 1"); err != nil {
		t.Fatalf("record query: %v", err)
	}
	if err := rec.Slack(context.Background(), "slack", "#ops", "hello"); err != nil {
		t.Fatalf("record slack: %v", err)
	}

	blobs := memBlobs{}
	hash, count, err := rec.Flush(blobs)
	if err != nil {
		t.Fatalf("flush: %v", err)
	}
	if count != 3 {
		t.Fatalf("want 3 effects, got %d", count)
	}
	data, ok := blobs[hash]
	if !ok {
		t.Fatal("tape blob missing")
	}
	if got := string(data); strings.Contains(got, "Bearer secret") {
		t.Fatalf("authorization header leaked into tape: %s", got)
	}
	if !strings.Contains(string(data), `"kind":"http"`) || !strings.Contains(string(data), `"index":0`) {
		t.Fatalf("tape missing http entry: %s", data)
	}
}

func TestReplayerServesRecordedResponsesWithoutCallingHost(t *testing.T) {
	base := &countingHost{resp: flow.HTTPResponse{Status: 201, Body: flow.Item{"ok": true}}}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)
	if _, err := rec.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/x"}); err != nil {
		t.Fatalf("record: %v", err)
	}
	blobs := memBlobs{}
	if _, _, err := rec.Flush(blobs); err != nil {
		t.Fatalf("flush: %v", err)
	}

	replay := NewReplayer(rec.Tape)
	resp, err := replay.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/x"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if resp.Status != 201 || resp.Body["ok"] != true {
		t.Fatalf("unexpected replayed response: %+v", resp)
	}
	if base.calls != 1 {
		t.Fatalf("replay touched the base host: %d calls", base.calls)
	}
	if len(replay.Diffs()) != 0 || len(replay.Misses()) != 0 {
		t.Fatalf("clean replay reported diffs=%v misses=%v", replay.Diffs(), replay.Misses())
	}
}

func TestReplayerFailsClosedOnMissingEffect(t *testing.T) {
	tape := &Tape{RunID: "run-1", NodeID: "node-1", Attempt: 1}
	replay := NewReplayer(tape)
	_, err := replay.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/x"})
	if !errors.Is(err, ErrMiss) {
		t.Fatalf("want ErrMiss, got %v", err)
	}
	if len(replay.Misses()) != 1 {
		t.Fatalf("want one miss, got %d", len(replay.Misses()))
	}
}

func TestReplayerReportsRequestDrift(t *testing.T) {
	base := &countingHost{resp: flow.HTTPResponse{Status: 200}}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)
	if _, err := rec.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/old"}); err != nil {
		t.Fatalf("record: %v", err)
	}

	replay := NewReplayer(rec.Tape)
	// Same ordinal, different URL: a mismatched tape must fail closed rather
	// than serve a plausible but wrong response.
	_, err := replay.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/new"})
	if err == nil {
		t.Fatal("drifted request served a recorded response")
	}
	diffs := replay.Diffs()
	if len(diffs) != 1 || diffs[0].Kind != KindHTTP {
		t.Fatalf("want one http diff, got %+v", diffs)
	}
}

func TestReplayerReplaysRecordedErrors(t *testing.T) {
	base := &countingHost{err: errors.New("upstream 503")}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)
	if _, err := rec.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/x"}); err == nil {
		t.Fatal("want an error from the base host")
	}

	replay := NewReplayer(rec.Tape)
	_, err := replay.HTTP(context.Background(), flow.HTTPRequest{Method: "GET", URL: "https://example.test/x"})
	if err == nil || err.Error() != "upstream 503" {
		t.Fatalf("want the recorded error, got %v", err)
	}
}
