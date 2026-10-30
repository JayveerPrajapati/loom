package store

import (
	"encoding/json"
	"testing"
)

func TestRedactValue(t *testing.T) {
	input := map[string]any{
		"username": "alice",
		"password": "supersecretpassword",
		"nested": map[string]any{
			"api_key": "key_123456",
			"public":  "ok",
		},
		"headers": map[string]any{
			"Authorization": "Bearer abc.def.ghi",
			"Accept":        "application/json",
		},
		"items": []any{
			map[string]any{
				"token": "tok_xyz",
				"id":    123,
			},
		},
	}

	redacted := RedactValue(input).(map[string]any)

	if redacted["username"] != "alice" {
		t.Errorf("expected username alice, got %v", redacted["username"])
	}
	if redacted["password"] != "[redacted]" {
		t.Errorf("expected password [redacted], got %v", redacted["password"])
	}

	nested := redacted["nested"].(map[string]any)
	if nested["api_key"] != "[redacted]" {
		t.Errorf("expected api_key [redacted], got %v", nested["api_key"])
	}
	if nested["public"] != "ok" {
		t.Errorf("expected public ok, got %v", nested["public"])
	}

	headers := redacted["headers"].(map[string]any)
	if headers["Authorization"] != "[redacted]" {
		t.Errorf("expected Authorization [redacted], got %v", headers["Authorization"])
	}
	if headers["Accept"] != "application/json" {
		t.Errorf("expected Accept application/json, got %v", headers["Accept"])
	}

	items := redacted["items"].([]any)
	item0 := items[0].(map[string]any)
	if item0["token"] != "[redacted]" {
		t.Errorf("expected token [redacted], got %v", item0["token"])
	}
	if item0["id"] != 123 {
		t.Errorf("expected id 123, got %v", item0["id"])
	}
}

func TestRedactJSON(t *testing.T) {
	raw := []byte(`{"apiKey":"secret-value","normal":"hello"}`)
	out := RedactJSON(raw)

	var res map[string]any
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if res["apiKey"] != "[redacted]" {
		t.Errorf("expected apiKey to be redacted, got %v", res["apiKey"])
	}
	if res["normal"] != "hello" {
		t.Errorf("expected normal hello, got %v", res["normal"])
	}
}
