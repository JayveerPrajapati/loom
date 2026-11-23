package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"loom/internal/flow"
	"loom/internal/store"
	"strings"
)

// containsRedaction reports whether any string in value still carries the
// store's display-redaction marker.
func containsRedaction(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, "[redacted]")
	case map[string]any:
		for _, item := range v {
			if containsRedaction(item) {
				return true
			}
		}
	case flow.Item:
		for _, item := range v {
			if containsRedaction(item) {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if containsRedaction(item) {
				return true
			}
		}
	case []string:
		for _, item := range v {
			if strings.Contains(item, "[redacted]") {
				return true
			}
		}
	}
	return false
}

// RedactForDisplay applies the store's display mask to data read from raw
// blobs. Replay and execution keep the original bytes; API/UI surfaces use this.
func RedactForDisplay[T any](v T) T {
	data, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out T
	if err := json.Unmarshal(store.RedactJSON(data), &out); err != nil {
		return v
	}
	return out
}

func hashJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// verifyBlob reloads a content-addressed blob and returns its digest. A
// mismatch means the stored file no longer matches its name.
func (e *Engine) verifyBlob(hash string) (string, error) {
	if hash == "" {
		return "", nil
	}
	data, err := e.Store.LoadBlob(hash)
	if err != nil {
		return "", fmt.Errorf("recorded blob %s: %w", hash, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// verifyRunBlobs checks that every blob referenced by the run still hashes to
// its recorded value, including the effect tapes stored in node.effects data.
// It does not execute anything.
func (e *Engine) verifyRunBlobs(events []store.Event) error {
	for _, ev := range events {
		for _, hash := range []string{ev.InputHash, ev.OutputHash} {
			if hash == "" {
				continue
			}
			actual, err := e.verifyBlob(hash)
			if err != nil {
				return err
			}
			if actual != hash {
				return fmt.Errorf("blob %s is corrupted", hash)
			}
		}
		if ev.Type != nodeEffectsEvent {
			continue
		}
		hash, _ := ev.Data["blob"].(string)
		if hash == "" {
			continue
		}
		actual, err := e.verifyBlob(hash)
		if err != nil {
			return fmt.Errorf("effect tape missing for node %q: %w", ev.NodeID, err)
		}
		if actual != hash {
			return fmt.Errorf("effect tape for node %q is corrupted", ev.NodeID)
		}
	}
	return nil
}
