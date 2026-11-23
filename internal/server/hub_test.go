package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"loom/internal/store"
)

// Configuration Hub v3 plan D1: node-type-centric views. The hub
// endpoints compose the entity stores read-only — one round-trip per
// page, writes stay on the per-entity endpoints.
func TestHubOverviewCounts(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "House API", "nodeType": "http.request", "config": map[string]any{"method": "POST"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	for i, kind := range []string{"query", "headers", "item"} {
		status, data = call(t, srv, http.MethodPost, "/api/mappings", "test-token", map[string]any{
			"name": "M" + kind, "kind": kind,
			"rows": []map[string]any{{"target": "a", "source": "1"}},
		})
		if status != http.StatusCreated {
			t.Fatalf("create %s mapping = %d: %s", kind, status, data)
		}
		_ = i
	}
	status, data = call(t, srv, http.MethodPost, "/api/data-schemas", "test-token", map[string]any{
		"name": "Order shape",
		"fields": []map[string]any{
			{"name": "id", "type": "string", "required": true},
		},
	})
	if status != http.StatusCreated {
		t.Fatalf("create schema = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/hub", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("overview = %d: %s", status, data)
	}
	var overview struct {
		Profiles     map[string]int `json:"profiles"`
		MappingKinds map[string]int `json:"mappingKinds"`
		Schemas      int            `json:"schemas"`
	}
	if err := json.Unmarshal(data, &overview); err != nil {
		t.Fatal(err)
	}
	if overview.Profiles["http.request"] != 1 {
		t.Fatalf("profile counts = %v", overview.Profiles)
	}
	if overview.MappingKinds["query"] != 1 || overview.MappingKinds["headers"] != 1 || overview.MappingKinds["item"] != 1 {
		t.Fatalf("mapping kind counts = %v", overview.MappingKinds)
	}
	if overview.Schemas != 1 {
		t.Fatalf("schema count = %d", overview.Schemas)
	}
}

func TestHubNodeTypePage(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, data := call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "House API", "nodeType": "http.request", "config": map[string]any{"method": "POST"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create profile = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/config-profiles", "test-token", map[string]any{
		"name": "Postgres ops", "nodeType": "db.postgres", "config": map[string]any{"operation": "query"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create other profile = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/mappings", "test-token", map[string]any{
		"name": "Paging", "kind": "query", "rows": []map[string]any{{"target": "limit", "source": "20"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create query mapping = %d: %s", status, data)
	}
	status, data = call(t, srv, http.MethodPost, "/api/mappings", "test-token", map[string]any{
		"name": "Lead mapping", "rows": []map[string]any{{"target": "email", "source": "{{ $json.email }}"}},
	})
	if status != http.StatusCreated {
		t.Fatalf("create item mapping = %d: %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/hub/http.request", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("hub page = %d: %s", status, data)
	}
	var page struct {
		NodeType     string                `json:"nodeType"`
		MappingKinds []string              `json:"mappingKinds"`
		Profiles     []store.ConfigProfile `json:"profiles"`
		Mappings     []store.Mapping       `json:"mappings"`
		Schemas      []store.DataSchema    `json:"schemas"`
		SchemaSlot   string                `json:"schemaSlot"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatal(err)
	}
	if page.NodeType != "http.request" {
		t.Fatalf("nodeType = %q", page.NodeType)
	}
	if len(page.MappingKinds) != 4 {
		t.Fatalf("mapping kinds = %v", page.MappingKinds)
	}
	if len(page.Profiles) != 1 || page.Profiles[0].Name != "House API" {
		t.Fatalf("profiles = %+v, want only the http.request one", page.Profiles)
	}
	if len(page.Mappings) != 1 || page.Mappings[0].Kind != "query" {
		t.Fatalf("mappings = %+v, want only the query-kind one", page.Mappings)
	}
	if page.SchemaSlot != "output" {
		t.Fatalf("schema slot = %q, want output", page.SchemaSlot)
	}

	// The webhook trigger is the one input-contract surface.
	status, data = call(t, srv, http.MethodGet, "/api/hub/trigger.webhook", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("webhook hub page = %d: %s", status, data)
	}
	var webhookPage struct {
		SchemaSlot string `json:"schemaSlot"`
	}
	_ = json.Unmarshal(data, &webhookPage)
	if webhookPage.SchemaSlot != "input" {
		t.Fatalf("webhook schema slot = %q, want input", webhookPage.SchemaSlot)
	}

	// Unknown types 404; node types without mappings see an empty list.
	status, _ = call(t, srv, http.MethodGet, "/api/hub/not.a-type", "test-token", nil)
	if status != http.StatusNotFound {
		t.Fatalf("unknown type = %d, want 404", status)
	}
	status, data = call(t, srv, http.MethodGet, "/api/hub/text.template", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("text.template hub page = %d: %s", status, data)
	}
	var textPage struct {
		Mappings []store.Mapping `json:"mappings"`
	}
	_ = json.Unmarshal(data, &textPage)
	if len(textPage.Mappings) != 0 {
		t.Fatalf("text.template mappings = %+v, want none", textPage.Mappings)
	}
}

// The entity pages' filters (v3 plan D1): mappings by kind, profiles by
// node type — and bad filters are 400s, not empty 200s.
func TestHubEntityFilters(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	status, _ := call(t, srv, http.MethodPost, "/api/mappings", "test-token", map[string]any{
		"name": "Paging", "kind": "query", "rows": []map[string]any{{"target": "limit", "source": "20"}},
	})
	if status != http.StatusCreated {
		t.Fatal("create query mapping failed")
	}
	status, _ = call(t, srv, http.MethodPost, "/api/mappings", "test-token", map[string]any{
		"name": "Lead mapping", "rows": []map[string]any{{"target": "email", "source": "{{ $json.email }}"}},
	})
	if status != http.StatusCreated {
		t.Fatal("create item mapping failed")
	}

	status, data := call(t, srv, http.MethodGet, "/api/mappings?kind=query", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("kind filter = %d: %s", status, data)
	}
	var filtered struct {
		Mappings []store.Mapping `json:"mappings"`
	}
	_ = json.Unmarshal(data, &filtered)
	if len(filtered.Mappings) != 1 || filtered.Mappings[0].Kind != "query" {
		t.Fatalf("filtered mappings = %+v", filtered.Mappings)
	}

	status, _ = call(t, srv, http.MethodGet, "/api/mappings?kind=cookie", "test-token", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("bad kind = %d, want 400", status)
	}

	// v4.1 E2: the merged hub page's stat-row link filters by node type —
	// http.request consumes the param kinds, so the item-kind mapping drops
	// out; a kind outside the type's kinds is a 400, not an empty 200.
	status, data = call(t, srv, http.MethodGet, "/api/mappings?nodeType=http.request", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("nodeType mappings filter = %d: %s", status, data)
	}
	var typeFiltered struct {
		Mappings []store.Mapping `json:"mappings"`
	}
	_ = json.Unmarshal(data, &typeFiltered)
	if len(typeFiltered.Mappings) != 1 || typeFiltered.Mappings[0].Kind != "query" {
		t.Fatalf("type-filtered mappings = %+v, want only the query one", typeFiltered.Mappings)
	}

	status, _ = call(t, srv, http.MethodGet, "/api/mappings?nodeType=http.request&kind=item", "test-token", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("kind not used by type = %d, want 400", status)
	}

	status, data = call(t, srv, http.MethodGet, "/api/mappings?nodeType=http.request&kind=query", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("combined filter = %d: %s", status, data)
	}
	_ = json.Unmarshal(data, &typeFiltered)
	if len(typeFiltered.Mappings) != 1 || typeFiltered.Mappings[0].Kind != "query" {
		t.Fatalf("combined-filtered mappings = %+v", typeFiltered.Mappings)
	}

	status, _ = call(t, srv, http.MethodGet, "/api/mappings?nodeType=bogus.type", "test-token", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("bad nodeType on mappings = %d, want 400", status)
	}

	status, data = call(t, srv, http.MethodGet, "/api/config-profiles?nodeType=http.request", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("nodeType filter = %d: %s", status, data)
	}
	var profiles struct {
		Profiles []store.ConfigProfile `json:"profiles"`
	}
	_ = json.Unmarshal(data, &profiles)
	for _, p := range profiles.Profiles {
		if p.NodeType != "http.request" {
			t.Fatalf("filtered profiles contain %q for %s", p.Name, p.NodeType)
		}
	}

	status, _ = call(t, srv, http.MethodGet, "/api/config-profiles?nodeType=bogus.type", "test-token", nil)
	if status != http.StatusBadRequest {
		t.Fatalf("bad nodeType = %d, want 400", status)
	}
}
