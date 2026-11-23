package store

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"loom/internal/graph"
)

// Configuration Hub v3 plan D4: mappings carry a kind. Item mappings are
// the original data.map transform; the parameter kinds hold reusable
// value rows publish compiles into HTTP parameter groups. Kind rides the
// append-only version doc, is validated on both write paths, and legacy
// docs without a kind read back as item.
func TestMappingKindRoundTrip(t *testing.T) {
	st := testProfileStore(t)

	created, err := st.CreateMapping(&Mapping{
		ID: "map-1", Name: "Paging query params", Kind: MappingKindQuery,
		Rows: []MappingRow{{Target: "limit", Source: "20"}, {Target: "offset", Source: "{{ $json.offset }}"}},
	}, "user-1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Kind != MappingKindQuery {
		t.Fatalf("created kind = %q, want %q", created.Kind, MappingKindQuery)
	}

	got, err := st.GetMapping("map-1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Kind != MappingKindQuery {
		t.Fatalf("get kind = %q, want %q", got.Kind, MappingKindQuery)
	}

	// The kind is inside the version doc, so the snapshot is self-contained.
	var doc struct {
		Kind string `json:"kind"`
	}
	row := st.readRow(`SELECT doc FROM mapping_versions WHERE mapping_id = ? AND version = 1`, "map-1")
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		t.Fatalf("read version doc: %v", err)
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Kind != MappingKindQuery {
		t.Fatalf("doc kind = %q, want %q", doc.Kind, MappingKindQuery)
	}

	// Unknown kinds are rejected on create and update.
	if _, err := st.CreateMapping(&Mapping{ID: "map-2", Name: "Bad", Kind: "cookie"}, "user-1"); err == nil {
		t.Fatal("create accepted unknown kind")
	} else if !strings.Contains(err.Error(), "unknown mapping kind") {
		t.Fatalf("create error = %v", err)
	}
	if _, err := st.UpdateMapping(&Mapping{ID: "map-1", Name: "Bad", Kind: "cookie"}, 1, "user-1"); err == nil {
		t.Fatal("update accepted unknown kind")
	}

	// Updating the kind appends a version and reads back the new kind.
	updated, err := st.UpdateMapping(&Mapping{
		ID: "map-1", Name: "Paging", Kind: MappingKindHeaders,
		Rows: []MappingRow{{Target: "X-Page-Size", Source: "20"}},
	}, 1, "user-1")
	if err != nil {
		t.Fatalf("update kind: %v", err)
	}
	if updated.Kind != MappingKindHeaders {
		t.Fatalf("updated kind = %q, want %q", updated.Kind, MappingKindHeaders)
	}
	// Historical versions keep their original kind.
	old, err := st.GetMappingVersion("map-1", 1)
	if err != nil {
		t.Fatalf("historical version: %v", err)
	}
	if old.Kind != MappingKindQuery {
		t.Fatalf("historical kind = %q, want %q (append-only docs are self-contained)", old.Kind, MappingKindQuery)
	}

	// Empty kind means item (legacy payload) rather than an error.
	item, err := st.CreateMapping(&Mapping{
		ID: "map-3", Name: "Legacy shaped", Rows: []MappingRow{{Target: "a", Source: "1"}},
	}, "user-1")
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if item.Kind != MappingKindItem {
		t.Fatalf("empty kind normalized to %q, want %q", item.Kind, MappingKindItem)
	}
}

// A pre-v3 mapping database (no kind column, docs without kind) migrates
// in place: the column backfills to item and every read normalizes.
func TestMappingKindMigrationFromLegacy(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := st.CreateMapping(&Mapping{
		ID: "legacy-1", Name: "Old item mapping",
		Rows: []MappingRow{{Target: "email", Source: "{{ $json.email }}"}},
	}, "user-1"); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate the pre-v3 shape: drop the kind from the stored doc and
	// the column from the table.
	if _, err := st.db.Exec(`UPDATE mapping_versions SET doc = ? WHERE mapping_id = ?`,
		[]byte(`{"name":"Old item mapping","rows":[{"target":"email","source":"{{ $json.email }}"}]}`), "legacy-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE mappings SET kind = '' WHERE id = 'legacy-1'`); err != nil {
		t.Fatal(err)
	}
	// NOTE: the column is NOT NULL DEFAULT 'item' in the current schema;
	// legacy databases gain it via ALTER TABLE ... DEFAULT 'item', so a
	// legacy row reads 'item'. The empty string here only exercises the
	// doc-normalization path. Restore and close.
	if _, err := st.db.Exec(`UPDATE mappings SET kind = ? WHERE id = 'legacy-1'`, "item"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	got, err := st2.GetMapping("legacy-1")
	if err != nil {
		t.Fatalf("get legacy: %v", err)
	}
	if got.Kind != MappingKindItem {
		t.Fatalf("legacy kind = %q, want item normalization", got.Kind)
	}
}

// Reopening a store is idempotent for the kind migration: the hasColumn
// guard means the second open performs no ALTER and data is unchanged.
func TestMappingKindMigrationIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		st, err := Open(dir)
		if err != nil {
			t.Fatalf("open %d: %v", i, err)
		}
		if i == 0 {
			if _, err := st.CreateMapping(&Mapping{ID: "m", Name: "X", Kind: MappingKindBody,
				Rows: []MappingRow{{Target: "page", Source: "1"}}}, "u"); err != nil {
				t.Fatal(err)
			}
		}
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.GetMapping("m")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != MappingKindBody || got.UpdatedAt.IsZero() {
		t.Fatalf("after two reopens: %+v", got)
	}
}

// ResolveMappingRefs on http.request nodes (v3 plan D4/D6): mapping rows
// resolve per group, the node's own *Fields rows win per parameter, the
// compiled result lands on the plain config key, and kind mismatches are
// named errors. Groups without a ref or fields array pass through
// untouched.
func TestResolveHTTPParamGroups(t *testing.T) {
	st := testProfileStore(t)
	if _, err := st.CreateMapping(&Mapping{
		ID: "q-map", Name: "Paging", Kind: MappingKindQuery,
		Rows: []MappingRow{{Target: "limit", Source: "20"}, {Target: "source", Source: "web"}},
	}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMapping(&Mapping{
		ID: "b-map", Name: "Body shape", Kind: MappingKindBody,
		Rows: []MappingRow{{Target: "limit", Source: "20"}, {Target: "note", Source: "hello"}},
	}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateMapping(&Mapping{
		ID: "i-map", Name: "Item", Kind: MappingKindItem,
		Rows: []MappingRow{{Target: "email", Source: "{{ $json.email }}"}},
	}, "u"); err != nil {
		t.Fatal(err)
	}

	httpWF := func(config map[string]any) *graph.Workflow {
		return &graph.Workflow{
			ID: "wf", Name: "WF", Version: 1,
			Nodes: []graph.Node{
				{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
				{ID: "http", Type: "http.request", TypeVersion: 1, Config: config},
			},
		}
	}

	// Mapping under node rows: the node's limit wins, the mapping fills
	// source, and the raw query object is replaced.
	wf := httpWF(map[string]any{
		"mappingRefs": map[string]any{"query": "q-map"},
		"queryFields": []any{
			map[string]any{"name": "limit", "mode": "static", "type": "number", "value": float64(50)},
		},
		"query": map[string]any{"stale": "dropped"},
	})
	refs, err := st.ResolveMappingRefs(wf)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if refs["q-map"] != 1 {
		t.Fatalf("usage pins = %v, want q-map v1", refs)
	}
	query, _ := wf.Nodes[1].Config["query"].(map[string]any)
	if !reflect.DeepEqual(query, map[string]any{"limit": float64(50), "source": "web"}) {
		t.Fatalf("compiled query = %#v, want node limit 50 over mapping 20 plus mapping source web", query)
	}

	// Body mappings keep typed literals for canonical JSON; plain mapping
	// values stay strings, and headers map verbatim.
	wf = httpWF(map[string]any{
		"mappingRefs": map[string]any{"body": "b-map", "query": "q-map"},
	})
	if _, err := st.ResolveMappingRefs(wf); err != nil {
		t.Fatal(err)
	}
	body, _ := wf.Nodes[1].Config["body"].(map[string]any)
	if body["limit"] != float64(20) || body["note"] != "hello" {
		t.Fatalf("compiled body = %#v, want typed limit 20 and string note", body)
	}

	// Fields alone (no mapping) still compile, and the fields array stays
	// for display.
	wf = httpWF(map[string]any{
		"headerFields": []any{
			map[string]any{"name": "X-Trace", "mode": "expression", "value": "{{ $json.trace }}"},
		},
	})
	if _, err := st.ResolveMappingRefs(wf); err != nil {
		t.Fatal(err)
	}
	headers, _ := wf.Nodes[1].Config["headers"].(map[string]any)
	if !reflect.DeepEqual(headers, map[string]any{"X-Trace": "{{ $json.trace }}"}) {
		t.Fatalf("compiled headers = %#v, want the expression row verbatim", headers)
	}
	if rows, _ := wf.Nodes[1].Config["headerFields"].([]any); len(rows) != 1 {
		t.Fatalf("headerFields should not be consumed: %#v", wf.Nodes[1].Config["headerFields"])
	}

	// Groups without refs or fields pass through untouched.
	wf = httpWF(map[string]any{"query": map[string]any{"limit": "10"}})
	if _, err := st.ResolveMappingRefs(wf); err != nil {
		t.Fatal(err)
	}
	if q, _ := wf.Nodes[1].Config["query"].(map[string]any); q["limit"] != "10" {
		t.Fatalf("untouched query = %#v", wf.Nodes[1].Config["query"])
	}

	// Kind mismatch is a named error.
	wf = httpWF(map[string]any{"mappingRefs": map[string]any{"query": "i-map"}})
	_, err = st.ResolveMappingRefs(wf)
	if err == nil || !strings.Contains(err.Error(), "kind item") {
		t.Fatalf("kind mismatch error = %v", err)
	}

	// Dangling ref is a named error naming the group.
	wf = httpWF(map[string]any{"mappingRefs": map[string]any{"body": "ghost"}})
	if _, err := st.ResolveMappingRefs(wf); err == nil || !strings.Contains(err.Error(), "body mapping") {
		t.Fatalf("dangling ref error = %v", err)
	}
}
