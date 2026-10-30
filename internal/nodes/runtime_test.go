package nodes

import (
	"context"
	"regexp"
	"testing"

	"loom/internal/flow"
)

func runNode(t *testing.T, node flow.Node, config map[string]any) flow.Item {
	t.Helper()
	out, err := node.Execute(context.Background(), flow.Input{Config: config, Item: flow.Item{}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out["main"]
}

func TestChatText(t *testing.T) {
	body := flow.Item{"choices": []any{map[string]any{"message": map[string]any{"content": "hello"}}}}
	if got := chatText(body); got != "hello" {
		t.Fatalf("chatText = %q", got)
	}
}

func TestRegistryCoversCatalog(t *testing.T) {
	registry := Registry()
	for _, def := range All() {
		if _, ok := registry[def.Type]; !ok {
			t.Errorf("catalog type %q has no implementation", def.Type)
		}
	}
}

func TestJSONParseNode(t *testing.T) {
	item := runNode(t, jsonParseNode{}, map[string]any{"text": `{"email":"a@example.com"}`})
	if item["email"] != "a@example.com" {
		t.Fatalf("email = %v", item["email"])
	}
	item = runNode(t, jsonParseNode{}, map[string]any{"text": `[1,2]`})
	items, ok := item["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("items = %#v", item["items"])
	}
}

func TestCSVParseNode(t *testing.T) {
	item := runNode(t, csvParseNode{}, map[string]any{"text": "name,age\nAda,36\nLinus,55\n"})
	rows, ok := item["rows"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("rows = %#v", item["rows"])
	}
	first := rows[0].(map[string]any)
	if first["name"] != "Ada" || first["age"] != "36" {
		t.Fatalf("first row = %#v", first)
	}
}

func TestTemplateNode(t *testing.T) {
	item := runNode(t, templateNode{}, map[string]any{"template": "Hi there"})
	if item["text"] != "Hi there" {
		t.Fatalf("text = %v", item["text"])
	}
}

func TestHMACNode(t *testing.T) {
	item := runNode(t, hmacNode{}, map[string]any{
		"value": "hello", "secret": "key", "algorithm": "sha256",
	})
	signature, _ := item["signature"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(signature) {
		t.Fatalf("signature = %q", signature)
	}
}

func TestUUIDNode(t *testing.T) {
	item := runNode(t, uuidNode{}, nil)
	uuid, _ := item["uuid"].(string)
	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(uuid) {
		t.Fatalf("uuid = %q", uuid)
	}
}

func TestDateFormatNode(t *testing.T) {
	item := runNode(t, dateFormatNode{}, map[string]any{
		"value": "2026-10-26T12:00:00Z", "layout": "2006-01-02", "timezone": "UTC",
	})
	if item["date"] != "2026-10-26" {
		t.Fatalf("date = %v", item["date"])
	}
}
