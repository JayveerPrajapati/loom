package server

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"loom/internal/graph"
)

func TestVerifyWebhookHMACLegacyBodyOnly(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	node := &graph.Node{Config: map[string]any{"hmacSecret": "secret"}}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(`{"ok":true}`))
	signature := hex.EncodeToString(mac.Sum(nil))

	if !verifyWebhookHMAC(node, []byte(`{"ok":true}`), signature, "invalid timestamp", now) {
		t.Fatal("legacy body-only HMAC was rejected")
	}
	if verifyWebhookHMAC(node, []byte(`{"ok":false}`), signature, "", now) {
		t.Fatal("tampered body was accepted")
	}
}

func TestVerifyWebhookTimestampedHMAC(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	node := &graph.Node{Config: map[string]any{
		"hmacSecret": "secret", "hmacTimestamps": true,
	}}
	timestamp := now.Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte(`{"ok":true}`))
	signature := hex.EncodeToString(mac.Sum(nil))

	if !verifyWebhookHMAC(node, []byte(`{"ok":true}`), signature, timestamp, now) {
		t.Fatal("valid timestamped HMAC was rejected")
	}
	if verifyWebhookHMAC(node, []byte(`{"ok":false}`), signature, timestamp, now) {
		t.Fatal("tampered timestamped body was accepted")
	}
	if verifyWebhookHMAC(node, []byte(`{"ok":true}`), signature, "not-a-time", now) {
		t.Fatal("malformed timestamp was accepted")
	}
}

func TestVerifyWebhookTimestampedHMACWindow(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	node := &graph.Node{Config: map[string]any{
		"hmacSecret": "secret", "hmacTimestamps": true,
	}}
	signature := func(timestamp time.Time) string {
		stamp := timestamp.Format(time.RFC3339)
		mac := hmac.New(sha256.New, []byte("secret"))
		mac.Write([]byte(stamp))
		mac.Write([]byte("."))
		mac.Write([]byte("body"))
		return hex.EncodeToString(mac.Sum(nil))
	}

	expired := now.Add(-6 * time.Minute)
	if verifyWebhookHMAC(node, []byte("body"), signature(expired), expired.Format(time.RFC3339), now) {
		t.Fatal("expired timestamp was accepted")
	}
	future := now.Add(6 * time.Minute)
	if verifyWebhookHMAC(node, []byte("body"), signature(future), future.Format(time.RFC3339), now) {
		t.Fatal("future timestamp was accepted")
	}
}

func TestVerifyWebhookTimestampedHMACRequiresOptIn(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	node := &graph.Node{Config: map[string]any{"hmacSecret": "secret"}}
	timestamp := now.Format(time.RFC3339)
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write([]byte("body"))
	if verifyWebhookHMAC(node, []byte("body"), hex.EncodeToString(mac.Sum(nil)), timestamp, now) {
		t.Fatal("timestamped HMAC was accepted without hmacTimestamps opt-in")
	}
}

func TestVerifyWebhookTimestampedModeRejectsLegacyBodyOnly(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	node := &graph.Node{Config: map[string]any{
		"hmacSecret": "secret", "hmacTimestamps": true,
	}}
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write([]byte("body"))
	if verifyWebhookHMAC(node, []byte("body"), hex.EncodeToString(mac.Sum(nil)), now.Format(time.RFC3339), now) {
		t.Fatal("timestamped mode accepted legacy body-only HMAC")
	}
}
