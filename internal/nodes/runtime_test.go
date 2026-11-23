package nodes

import (
	"context"
	"strings"
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

func TestRegistryMatchesCatalog(t *testing.T) {
	registry := Registry()
	for _, def := range All() {
		if _, ok := registry[def.Type]; !ok {
			t.Errorf("catalog type %q has no implementation", def.Type)
		}
	}
	for typ := range registry {
		// Connector-authored types dispatch through the shared
		// connector. prefix entry (Campaign 4); the catalog freezes
		// built-ins only.
		if strings.HasPrefix(typ, ConnectorTypePrefix) {
			continue
		}
		if _, ok := Get(typ); !ok {
			t.Errorf("registered type %q is not in the catalog", typ)
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

func TestTemplateNode(t *testing.T) {
	item := runNode(t, templateNode{}, map[string]any{"template": "Hi there"})
	if item["text"] != "Hi there" {
		t.Fatalf("text = %v", item["text"])
	}
}
