package store

import (
	"errors"
	"strings"
	"testing"

	"loom/internal/graph"
)

// Profile-Centric UX Phase 1 (P0-2/P0-4): bundle resolution composes the
// three existing resolvers with the plan's five-layer precedence, and the
// profile_bundle_refs index guards deletes.

func bundleTestStore(t *testing.T) *Store {
	t.Helper()
	st := testProfileStore(t)
	for _, m := range []Mapping{
		{ID: "bundle-q", Name: "Bundle query", Kind: MappingKindQuery,
			Rows: []MappingRow{{Target: "fromBundle", Source: "bundle"}, {Target: "shared", Source: "bundle"}}},
		{ID: "node-q", Name: "Node query", Kind: MappingKindQuery,
			Rows: []MappingRow{{Target: "fromNode", Source: "node"}}},
		{ID: "bundle-item", Name: "Bundle item", Kind: MappingKindItem,
			Rows: []MappingRow{{Target: "email", Source: "{{ $json.email }}"}}},
	} {
		if _, err := st.CreateMapping(&m, "u"); err != nil {
			t.Fatalf("create mapping %s: %v", m.ID, err)
		}
	}
	if _, err := st.CreateDataSchema(&DataSchema{ID: "bundle-schema", Name: "Bundle schema"}, "u"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	return st
}

func TestResolveProfileBundlePrecedence(t *testing.T) {
	st := bundleTestStore(t)
	created, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "p-bundle", Name: "Bundled", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
		Bundle: &ProfileBundle{
			Mappings: map[string]string{"query": "bundle-q"},
			Schemas:  map[string]string{"input": "bundle-schema"},
		},
	}, "u")
	if err != nil {
		t.Fatalf("create profile: %v", err)
	}

	httpWF := func(nodeID string, config map[string]any, schemaIn string) *graph.Workflow {
		n := graph.Node{ID: nodeID, Type: "http.request", TypeVersion: 1, Config: config, ProfileRef: "p-bundle"}
		n.InputSchemaRef = schemaIn
		return &graph.Workflow{
			ID: "wf-" + nodeID, Name: "WF", Version: 1,
			Nodes: []graph.Node{
				{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
				n,
			},
		}
	}

	// Layer 5 alone: a bare node inherits the bundle mapping and schema.
	wf := httpWF("bare", map[string]any{}, "")
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	refs, err := st.ResolveMappingRefs(wf)
	if err != nil {
		t.Fatalf("resolve mappings: %v", err)
	}
	if refs["bundle-q"] != 1 {
		t.Fatalf("bundle mapping not pinned: %v", refs)
	}
	query, _ := wf.Nodes[1].Config["query"].(map[string]any)
	if query["fromBundle"] != "bundle" {
		t.Fatalf("bare bundle query = %#v", query)
	}
	if _, err := st.ResolveSchemaRefs(wf); err != nil {
		t.Fatalf("resolve schemas: %v", err)
	}
	if wf.Nodes[1].InputSchemaRef != "bundle-schema" || wf.Nodes[1].InputSchemaFields == nil {
		t.Fatalf("bundle input schema = %q fields=%v", wf.Nodes[1].InputSchemaRef, wf.Nodes[1].InputSchemaFields)
	}

	// Layer 3 beats 5: the node's own mappingRef wins and the bundle
	// mapping is not even pinned.
	wf = httpWF("node-ref", map[string]any{"mappingRefs": map[string]any{"query": "node-q"}}, "")
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	refs, err = st.ResolveMappingRefs(wf)
	if err != nil {
		t.Fatalf("resolve mappings: %v", err)
	}
	if refs["node-q"] != 1 || refs["bundle-q"] != 0 {
		t.Fatalf("node ref pins = %v, want node-q only", refs)
	}
	query, _ = wf.Nodes[1].Config["query"].(map[string]any)
	if _, exists := query["fromNode"]; !exists || query["fromBundle"] != nil {
		t.Fatalf("node-ref query = %#v, want node mapping only", query)
	}

	// Layer 1 beats 5: the node's plain target object drops the bundle
	// group entirely.
	wf = httpWF("plain", map[string]any{"query": map[string]any{"mine": true}}, "")
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	if _, err := st.ResolveMappingRefs(wf); err != nil {
		t.Fatalf("resolve mappings: %v", err)
	}
	query, _ = wf.Nodes[1].Config["query"].(map[string]any)
	if len(query) != 1 || query["mine"] != true {
		t.Fatalf("plain-object query = %#v, want node object untouched", query)
	}

	// Layer 2 beats 5: node parameter rows overlay the bundle mapping
	// rows per name.
	wf = httpWF("rows", map[string]any{
		"queryFields": []any{
			map[string]any{"name": "shared", "mode": "static", "type": "string", "value": "node-row"},
		},
	}, "")
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	if _, err := st.ResolveMappingRefs(wf); err != nil {
		t.Fatalf("resolve mappings: %v", err)
	}
	query, _ = wf.Nodes[1].Config["query"].(map[string]any)
	if query["shared"] != "node-row" || query["fromBundle"] != "bundle" {
		t.Fatalf("rows-over-bundle query = %#v, want node row to win per name", query)
	}

	// Schema precedence: the node's own input schema ref wins.
	wf = httpWF("schema", map[string]any{}, "other-schema")
	if _, err := st.CreateDataSchema(&DataSchema{ID: "other-schema", Name: "Other"}, "u"); err != nil {
		t.Fatalf("create other schema: %v", err)
	}
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	if wf.Nodes[1].InputSchemaRef != "other-schema" {
		t.Fatalf("node input schema = %q, want the node's own ref", wf.Nodes[1].InputSchemaRef)
	}
	_ = created
}

func TestResolveDataMapBundleItem(t *testing.T) {
	st := bundleTestStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "p-datamap", Name: "Map via bundle", NodeType: "data.map",
		Bundle: &ProfileBundle{Mappings: map[string]string{"item": "bundle-item"}},
	}, "u"); err != nil {
		t.Fatalf("create data.map profile: %v", err)
	}

	wf := &graph.Workflow{
		ID: "wf-map", Name: "Map", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{ID: "map", Type: "data.map", TypeVersion: 1, Config: map[string]any{}, ProfileRef: "p-datamap"},
		},
	}
	if _, err := st.ResolveProfileRefs(wf); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	if got, _ := wf.Nodes[1].Config["mappingRef"].(string); got != "bundle-item" {
		t.Fatalf("injected mappingRef = %q, want bundle-item", got)
	}
	refs, err := st.ResolveMappingRefs(wf)
	if err != nil {
		t.Fatalf("resolve mappings: %v", err)
	}
	if refs["bundle-item"] != 1 {
		t.Fatalf("pins = %v, want bundle-item", refs)
	}
	rows, _ := wf.Nodes[1].Config["mappingRows"].([]map[string]any)
	if len(rows) != 1 || rows[0]["target"] != "email" {
		t.Fatalf("mappingRows = %#v", wf.Nodes[1].Config["mappingRows"])
	}

	// A node that set its own mappingRef keeps it.
	wf2 := &graph.Workflow{
		ID: "wf-map-2", Name: "Map 2", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{ID: "map", Type: "data.map", TypeVersion: 1,
				Config: map[string]any{"mappingRef": "node-item"}, ProfileRef: "p-datamap"},
		},
	}
	if _, err := st.CreateMapping(&Mapping{ID: "node-item", Name: "Node item", Kind: MappingKindItem,
		Rows: []MappingRow{{Target: "x", Source: "1"}}}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ResolveProfileRefs(wf2); err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	if got, _ := wf2.Nodes[1].Config["mappingRef"].(string); got != "node-item" {
		t.Fatalf("node mappingRef replaced = %q, want node-item", got)
	}
}

func TestBundleRefsDeleteGuards(t *testing.T) {
	st := bundleTestStore(t)
	if _, err := st.CreateConfigProfile(&ConfigProfile{
		ID: "p-guard", Name: "Guard profile", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
		Bundle: &ProfileBundle{
			Mappings: map[string]string{"query": "bundle-q"},
			Schemas:  map[string]string{"input": "bundle-schema"},
		},
	}, "u"); err != nil {
		t.Fatalf("create profile: %v", err)
	}

	// The mapping is guarded by the bundle reference, with the profile
	// named in the error.
	err := st.DeleteMapping("bundle-q")
	if err == nil || !errors.Is(err, ErrMappingInUse) || !strings.Contains(err.Error(), "Guard profile") {
		t.Fatalf("delete guarded mapping err = %v", err)
	}

	// The schema too.
	err = st.DeleteDataSchema("bundle-schema")
	if err == nil || !errors.Is(err, ErrDataSchemaInUse) || !strings.Contains(err.Error(), "Guard profile") {
		t.Fatalf("delete guarded schema err = %v", err)
	}

	// ProfilesReferencingAsset lists it.
	profiles, err := st.ProfilesReferencingAsset("mapping", "bundle-q")
	if err != nil || len(profiles) != 1 || profiles[0].ID != "p-guard" {
		t.Fatalf("referencing profiles = %v (%v)", profiles, err)
	}

	// Removing the bundle from the profile releases both assets.
	if _, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "p-guard", Name: "Guard profile", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
	}, 1, "u"); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if err := st.DeleteMapping("bundle-q"); err != nil {
		t.Fatalf("delete mapping after bundle removal: %v", err)
	}
	if err := st.DeleteDataSchema("bundle-schema"); err != nil {
		t.Fatalf("delete schema after bundle removal: %v", err)
	}

	// Deleting the profile clears its index rows: re-create the assets,
	// reference them, delete the profile, then the assets go free.
	if _, err := st.CreateMapping(&Mapping{ID: "m-2", Name: "Again", Kind: MappingKindQuery,
		Rows: []MappingRow{{Target: "a", Source: "1"}}}, "u"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateConfigProfile(&ConfigProfile{
		ID: "p-guard", Name: "Guard profile", NodeType: "http.request",
		Config: map[string]any{"method": "GET"},
		Bundle: &ProfileBundle{Mappings: map[string]string{"query": "m-2"}},
	}, 2, "u"); err != nil {
		t.Fatalf("update profile with bundle: %v", err)
	}
	if err := st.DeleteConfigProfile("p-guard"); err != nil {
		t.Fatalf("delete profile: %v", err)
	}
	if err := st.DeleteMapping("m-2"); err != nil {
		t.Fatalf("delete mapping after profile delete: %v", err)
	}
}

// Deleting a workflow releases its usage pins: the delete guards guard
// live references, not dead rows (pre-existing defect surfaced by e2e
// residue while wiring bundles).
func TestDeleteWorkflowReleasesUsagePins(t *testing.T) {
	st := bundleTestStore(t)

	prof, err := st.CreateConfigProfile(&ConfigProfile{
		Name: "Pinner", NodeType: "http.request", Config: map[string]any{"method": "GET"},
	}, "tester")
	if err != nil {
		t.Fatal(err)
	}
	wf := &graph.Workflow{
		ID: "wf-pins", Name: "Pins", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Config: map[string]any{}},
			{ID: "http", Type: "http.request", TypeVersion: 1, ProfileRef: prof.ID,
				Config: map[string]any{"method": "GET", "url": "https://example.com"}},
		},
	}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	if _, err := st.PublishWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	if err := st.RecordConfigProfileUsage(prof.ID, prof.Version, wf.ID, 1); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteConfigProfile(prof.ID); err == nil {
		t.Fatal("profile delete while workflow lives: expected guard error")
	}
	if err := st.DeleteWorkflow(wf.ID); err != nil {
		t.Fatalf("delete workflow: %v", err)
	}
	if err := st.DeleteConfigProfile(prof.ID); err != nil {
		t.Fatalf("profile delete after workflow deleted: %v (pins not released)", err)
	}
}
