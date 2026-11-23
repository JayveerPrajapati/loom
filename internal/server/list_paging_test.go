package server_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func pageCreate(t *testing.T, srv *httptest.Server, path string, body map[string]any) {
	t.Helper()
	status, data := call(t, srv, http.MethodPost, path, "test-token", body)
	if status != http.StatusCreated && status != http.StatusOK {
		t.Fatalf("POST %s = %d: %s", path, status, data)
	}
}

func pageURL(path, extra string) string {
	if strings.Contains(path, "?") {
		return path + "&" + extra
	}
	return path + "?" + extra
}

func assertPageSearch(t *testing.T, srv *httptest.Server, path, key, query string) {
	t.Helper()
	status, data := call(t, srv, http.MethodGet, pageURL(path, "limit=1&q="+url.QueryEscape(query)), "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, status, data)
	}
	body := decode(t, data)
	items, ok := body[key].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("%s items = %#v", key, body[key])
	}
	if got := int(body["count"].(float64)); got != 1 {
		t.Fatalf("%s count = %d, want 1", key, got)
	}
	if got := int(body["limit"].(float64)); got != 1 {
		t.Fatalf("%s limit = %d, want 1", key, got)
	}
	if body["hasMore"] != false {
		t.Fatalf("%s hasMore = %v, want false", key, body["hasMore"])
	}
	if cursor, _ := body["nextCursor"].(string); cursor != "" {
		t.Fatalf("%s nextCursor = %q, want empty", key, cursor)
	}
}

func assertPageCursor(t *testing.T, srv *httptest.Server, path, key string) {
	t.Helper()
	status, data := call(t, srv, http.MethodGet, pageURL(path, "limit=1"), "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, status, data)
	}
	first := decode(t, data)
	firstItems, ok := first[key].([]any)
	if !ok || len(firstItems) != 1 {
		t.Fatalf("%s first page = %#v", key, first[key])
	}
	firstID := firstItems[0].(map[string]any)["id"].(string)
	cursor, _ := first["nextCursor"].(string)
	if cursor == "" || first["hasMore"] != true {
		t.Fatalf("%s first cursor = %q hasMore = %v", key, cursor, first["hasMore"])
	}
	status, data = call(t, srv, http.MethodGet, pageURL(path, "limit=1&cursor="+url.QueryEscape(cursor)), "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("GET %s cursor = %d: %s", path, status, data)
	}
	second := decode(t, data)
	secondItems, ok := second[key].([]any)
	if !ok || len(secondItems) != 1 {
		t.Fatalf("%s second page = %#v", key, second[key])
	}
	secondID := secondItems[0].(map[string]any)["id"].(string)
	if firstID == secondID {
		t.Fatalf("%s repeated %s across pages", key, firstID)
	}
	if int(second["count"].(float64)) != int(first["count"].(float64)) {
		t.Fatalf("%s count moved from %v to %v", key, first["count"], second["count"])
	}
	if cursor, _ := second["nextCursor"].(string); cursor != "" {
		t.Fatalf("%s second cursor = %q, want empty", key, cursor)
	}
}

func workflowPageBody(id, name string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "version": 1,
		"nodes": []map[string]any{
			{"id": "start", "type": "trigger.manual", "typeVersion": 1, "name": "Start",
				"position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}},
			{"id": "shape", "type": "data.set", "typeVersion": 1, "name": "Shape",
				"position": map[string]any{"x": 260, "y": 0},
				"config":   map[string]any{"values": map[string]any{"message": "hello"}, "keepOnlySet": true}},
		},
		"edges": []map[string]any{
			{"from": map[string]any{"node": "start", "port": "main"},
				"to": map[string]any{"node": "shape", "port": "main"}},
		},
	}
}

func TestHubNodeTypeSkipProfiles(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")
	pageCreate(t, srv, "/api/config-profiles", validProfileBody())

	status, data := call(t, srv, http.MethodGet, "/api/hub/http.request", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("hub = %d: %s", status, data)
	}
	full := decode(t, data)
	if got := len(full["profiles"].([]any)); got != 1 {
		t.Fatalf("hub profiles = %d, want 1", got)
	}

	status, data = call(t, srv, http.MethodGet, "/api/hub/http.request?profiles=0", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("hub meta = %d: %s", status, data)
	}
	meta := decode(t, data)
	profiles, ok := meta["profiles"].([]any)
	if !ok || len(profiles) != 0 {
		t.Fatalf("meta profiles = %#v, want empty array", meta["profiles"])
	}
	if _, ok := meta["mappingKinds"]; !ok {
		t.Fatalf("meta response lost mappingKinds: %s", data)
	}
	if _, ok := meta["schemas"]; !ok {
		t.Fatalf("meta response lost schemas: %s", data)
	}
}

func TestAssetListPagingAndSearch(t *testing.T) {
	srv, _, _ := newTestServerAt(t, "test-token")

	for _, name := range []string{"Alpha API", "Beta API"} {
		body := validProfileBody()
		body["name"] = name
		body["config"] = map[string]any{
			"method": "GET",
			"url":    "https://api.example.com/" + strings.ToLower(strings.ReplaceAll(name, " ", "-")),
		}
		pageCreate(t, srv, "/api/config-profiles", body)
	}
	for _, name := range []string{"Alpha mapping", "Beta mapping"} {
		body := validMappingBody()
		body["name"] = name
		pageCreate(t, srv, "/api/mappings", body)
	}
	for _, name := range []string{"Alpha contract", "Beta contract"} {
		body := validSchemaBody()
		body["name"] = name
		pageCreate(t, srv, "/api/data-schemas", body)
	}
	for _, name := range []string{"Alpha script", "Beta script"} {
		body := validScriptBody()
		body["name"] = name
		pageCreate(t, srv, "/api/scripts", body)
	}
	for i, name := range []string{"Alpha connector", "Beta connector"} {
		body := validConnectorBody()
		body["id"] = fmt.Sprintf("conn-page-%d", i)
		body["name"] = name
		pageCreate(t, srv, "/api/connectors", body)
	}
	for _, spec := range [][2]string{
		{"wf-page-alpha", "Alpha workflow"},
		{"wf-page-beta", "Beta workflow"},
	} {
		pageCreate(t, srv, "/api/workflows", workflowPageBody(spec[0], spec[1]))
	}
	for _, id := range []string{"cred-page-alpha", "cred-page-beta"} {
		pageCreate(t, srv, "/api/credentials", map[string]any{
			"id": id, "type": "http", "data": map[string]any{"token": "x"},
		})
	}

	cases := []struct {
		name  string
		path  string
		key   string
		query string
	}{
		{"profiles", "/api/config-profiles", "profiles", "Alpha API"},
		{"mappings", "/api/mappings", "mappings", "Alpha mapping"},
		{"schemas", "/api/data-schemas", "schemas", "Alpha contract"},
		{"scripts", "/api/scripts?language=javascript", "scripts", "Alpha script"},
		{"connectors", "/api/connectors", "connectors", "Alpha connector"},
		{"workflows", "/api/workflows", "workflows", "Alpha workflow"},
		{"credentials", "/api/credentials", "credentials", "cred-page-alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPageCursor(t, srv, tc.path, tc.key)
			assertPageSearch(t, srv, tc.path, tc.key, tc.query)
		})
	}

	// Connector pages hydrate operations and usage in the batched path.
	status, data := call(t, srv, http.MethodGet, "/api/connectors?limit=1", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("connector page = %d: %s", status, data)
	}
	connectors := decode(t, data)["connectors"].([]any)
	first := connectors[0].(map[string]any)
	operations, ok := first["operations"].([]any)
	if !ok || len(operations) != 1 {
		t.Fatalf("connector operations = %#v", first["operations"])
	}

	// The legacy unpaged response keeps its exact envelope.
	status, data = call(t, srv, http.MethodGet, "/api/config-profiles", "test-token", nil)
	if status != http.StatusOK {
		t.Fatalf("unpaged profiles = %d: %s", status, data)
	}
	legacy := decode(t, data)
	if _, ok := legacy["limit"]; ok {
		t.Fatalf("unpaged response gained limit: %s", data)
	}
	if _, ok := legacy["nextCursor"]; ok {
		t.Fatalf("unpaged response gained nextCursor: %s", data)
	}
	if got := int(legacy["count"].(float64)); got != 2 {
		t.Fatalf("unpaged count = %d, want 2", got)
	}

	// Invalid page inputs fail before any data is returned.
	for _, path := range []string{
		"/api/config-profiles?limit=abc",
		"/api/config-profiles?limit=0",
		"/api/config-profiles?limit=1&cursor=not-base64",
	} {
		status, data := call(t, srv, http.MethodGet, path, "test-token", nil)
		if status != http.StatusBadRequest {
			t.Fatalf("GET %s = %d: %s", path, status, data)
		}
	}
}
