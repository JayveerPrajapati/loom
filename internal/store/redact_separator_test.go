package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactValueMasksSeparatorKeys(t *testing.T) {
	in := map[string]any{"response": map[string]any{"headers": map[string]any{
		"set-cookie":    "session=secret",
		"Set-Cookie":    "session=secret",
		"x-api-key":     "k",
		"access_token":  "t",
		"client-secret": "c",
		"Content-Type":  "application/json",
	}}}
	out, err := json.Marshal(RedactValue(in))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(out)
	for _, leak := range []string{"session=secret", `"k"`, `"t"`, `"c"`} {
		if strings.Contains(s, leak) {
			t.Fatalf("redaction missed %q: %s", leak, s)
		}
	}
	if !strings.Contains(s, "application/json") {
		t.Fatalf("redaction ate a non-sensitive value: %s", s)
	}
}

func TestRedactJSONMasksSetCookie(t *testing.T) {
	raw := []byte(`{"headers":{"set-cookie":"session=secret"}}`)
	if out := string(RedactJSON(raw)); strings.Contains(out, "session=secret") {
		t.Fatalf("RedactJSON missed Set-Cookie: %s", out)
	}
}
