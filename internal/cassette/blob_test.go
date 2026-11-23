package cassette

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"loom/internal/flow"
)

// TestHTTPRequestSecretsNeverRecorded pins the effect-tape privacy
// contract: oauth2 parameters (which carry the client secret) and the
// proxy url never enter the recorded payload, and the authorization
// header stays redacted.
func TestHTTPRequestSecretsNeverRecorded(t *testing.T) {
	req := flow.HTTPRequest{
		Method: "POST", URL: "https://api.example.com/me",
		Headers: map[string]string{"Authorization": "Bearer tok", "X-Open": "1"},
		Body:    flow.Item{"a": 1},
		OAuth2: &flow.OAuth2Params{
			TokenURL: "https://auth.example.com/token",
			ClientID: "loom", ClientSecret: "s3cret", Scope: "read",
		},
		Proxy: "http://proxy.example.com:8080",
	}
	payload := requestPayload(req)
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, secret := range []string{"s3cret", "auth.example.com", "proxy.example.com"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("recorded payload leaks %q: %s", secret, encoded)
		}
	}
	if payload["headers"].(map[string]string)["Authorization"] != "[redacted]" {
		t.Fatalf("authorization must be redacted: %v", payload["headers"])
	}
	if payload["headers"].(map[string]string)["X-Open"] != "1" {
		t.Fatalf("open header must survive: %v", payload["headers"])
	}
}

// savingHost implements the optional blob capability on top of a base host.
type savingHost struct {
	*countingHost
	saved map[string][]byte
}

func (h *savingHost) SaveBlob(data []byte) (string, int, error) {
	hash := hashJSON(data)
	h.saved[hash] = append([]byte(nil), data...)
	return hash, len(data), nil
}

func (h *savingHost) LoadBlob(hash string) ([]byte, error) {
	data, ok := h.saved[hash]
	if !ok {
		return nil, errors.New("no such blob")
	}
	return data, nil
}

func TestBlobSaveRecordsAndReplays(t *testing.T) {
	base := &savingHost{countingHost: &countingHost{}, saved: map[string][]byte{}}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)

	var saver flow.BlobSaver = rec
	hash, size, err := saver.SaveBlob([]byte(`{"page":1}`))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if size != 10 {
		t.Fatalf("size = %d, want 10", size)
	}
	if len(base.saved) != 1 || base.saved[hash] == nil {
		t.Fatalf("underlying host save missing: %v", base.saved)
	}

	blobs := memBlobs{}
	tapeHash, count, err := rec.Flush(blobs)
	if err != nil || count != 1 {
		t.Fatalf("flush = %q %d %v", tapeHash, count, err)
	}
	if len(rec.Tape.Entries) != 1 || rec.Tape.Entries[0].Kind != KindBlob {
		t.Fatalf("tape entries = %+v", rec.Tape.Entries)
	}

	// Replaying the node's save returns the taped hash for whatever bytes
	// the node reconstructed; hermetic replay must not write.
	replay := NewReplayer(rec.Tape)
	replay.Blobs = blobs
	replayHash, replaySize, err := replay.SaveBlob([]byte(`{"page":1}`))
	if err != nil {
		t.Fatalf("replay save: %v", err)
	}
	if replayHash != hash || replaySize != size {
		t.Fatalf("replay = %q %d, want %q %d", replayHash, replaySize, hash, size)
	}
	if len(blobs) != 1 {
		t.Fatalf("replay wrote blobs: %d", len(blobs))
	}
}

func TestReplayerBlobWithoutStoreFailsClosed(t *testing.T) {
	replay := NewReplayer(&Tape{})
	if _, err := replay.LoadBlob("deadbeef"); err == nil || !strings.Contains(err.Error(), "no blob store") {
		t.Fatalf("err = %v, want fail-closed without a store", err)
	}
}

func TestRecorderBlobForwardsLoad(t *testing.T) {
	base := &savingHost{countingHost: &countingHost{}, saved: map[string][]byte{}}
	hash, _, err := base.SaveBlob([]byte("payload"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	rec := NewRecorder(base, "run-1", "node-1", 1, 0)

	var loader flow.BlobLoader = rec
	data, err := loader.LoadBlob(hash)
	if err != nil || string(data) != "payload" {
		t.Fatalf("load = %q %v", data, err)
	}
	// Loads are not effects: only the request built from the bytes is.
	if len(rec.Tape.Entries) != 0 {
		t.Fatalf("entries = %d, want 0", len(rec.Tape.Entries))
	}
}

func TestRecorderBlobWithoutCapabilityErrors(t *testing.T) {
	rec := NewRecorder(&countingHost{}, "run-1", "node-1", 1, 0)
	var saver flow.BlobSaver = rec
	if _, _, err := saver.SaveBlob([]byte("x")); err == nil || !strings.Contains(err.Error(), "cannot save blobs") {
		t.Fatalf("err = %v, want capability error", err)
	}
}
