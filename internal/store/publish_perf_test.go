package store

import (
	"fmt"
	"testing"

	"loom/internal/graph"
	"loom/internal/schemas"
)

// largePublishReadBudget is the deterministic publish gate. The fixture
// references 13 distinct assets (one profile, ten contracts, one mapping,
// one script) from 1,000 nodes; one request-scoped LookupCache serves the
// whole resolution, so duplicate refs must not add reads. The allowance
// leaves room for small resolver bookkeeping without hiding an N+1.
const largePublishReadBudget = 20

const (
	largePublishNodes     = 1000
	largeProfileRefs      = 500
	largeSchemaRefs       = 300
	largeContractCount    = 10
	largeContractFields   = 100
	largeMappingRefs      = 100
	largeMappingRows      = 200
	largeScriptRefs       = 100
	largePublishProfileID = "perf-profile"
	largePublishMappingID = "perf-mapping"
	largePublishScriptID  = "perf-script"
)

// seedLargePublishAssets creates the distinct assets the large workflow
// references. Creation is outside the measured window.
func seedLargePublishAssets(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.CreateConfigProfile(&ConfigProfile{
		ID: largePublishProfileID, Name: "Large profile", NodeType: "http.request",
		Visibility: VisibilityShared,
		Config: map[string]any{
			"method": "GET",
			"url":    "https://api.example.com/large",
		},
	}, "test"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	fields := make([]schemas.Field, 0, largeContractFields)
	for i := 0; i < largeContractFields; i++ {
		fields = append(fields, schemas.Field{Name: fmt.Sprintf("field_%03d", i), Type: "string"})
	}
	for i := 0; i < largeContractCount; i++ {
		if _, err := s.CreateDataSchema(&DataSchema{
			ID: fmt.Sprintf("perf-schema-%02d", i), Name: fmt.Sprintf("Large contract %02d", i),
			Visibility: VisibilityShared, Fields: fields,
		}, "test"); err != nil {
			t.Fatalf("create schema %d: %v", i, err)
		}
	}
	rows := make([]MappingRow, 0, largeMappingRows)
	for i := 0; i < largeMappingRows; i++ {
		rows = append(rows, MappingRow{
			Target: fmt.Sprintf("target_%03d", i),
			Source: fmt.Sprintf("{{ $json.source_%03d }}", i),
		})
	}
	if _, err := s.CreateMapping(&Mapping{
		ID: largePublishMappingID, Name: "Large mapping", Kind: "item",
		Visibility: VisibilityShared, Rows: rows,
	}, "test"); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if _, err := s.CreateScript(&Script{
		ID: largePublishScriptID, Name: "Large script", Language: ScriptLanguageJavaScript,
		Visibility: VisibilityShared, Source: "return $json;",
	}, "test"); err != nil {
		t.Fatalf("create script: %v", err)
	}
}

func largeSchemaIDs() []string {
	ids := make([]string, 0, largeContractCount)
	for i := 0; i < largeContractCount; i++ {
		ids = append(ids, fmt.Sprintf("perf-schema-%02d", i))
	}
	return ids
}

// buildLargePublishWorkflow mirrors the publish fixture: 1,000 asset
// references across four resolver families, with 500 profile refs and 100
// duplicate mapping/script refs.
func buildLargePublishWorkflow() *graph.Workflow {
	nodes := make([]graph.Node, 0, largePublishNodes)
	for i := 0; i < largeProfileRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("profile-%03d", i), Type: "http.request",
			ProfileRef: largePublishProfileID, Config: map[string]any{},
		})
	}
	schemaIDs := largeSchemaIDs()
	for i := 0; i < largeSchemaRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("schema-%03d", i), Type: "http.request",
			InputSchemaRef: schemaIDs[i%len(schemaIDs)],
		})
	}
	for i := 0; i < largeMappingRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("mapping-%03d", i), Type: "data.map",
			Config: map[string]any{"mappingRef": largePublishMappingID},
		})
	}
	for i := 0; i < largeScriptRefs; i++ {
		nodes = append(nodes, graph.Node{
			ID: fmt.Sprintf("script-%03d", i), Type: "code.run",
			Config: map[string]any{"scriptRef": largePublishScriptID},
		})
	}
	return &graph.Workflow{Nodes: nodes}
}

func TestPublishLargeWorkflowStaysWithinReadBudget(t *testing.T) {
	s := newOpsStore(t)
	seedLargePublishAssets(t, s)
	wf := buildLargePublishWorkflow()

	refs := 0
	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		if n.ProfileRef != "" {
			refs++
		}
		if n.InputSchemaRef != "" {
			refs++
		}
		if _, ok := n.Config["mappingRef"]; ok {
			refs++
		}
		if _, ok := n.Config["scriptRef"]; ok {
			refs++
		}
	}
	if len(wf.Nodes) != largePublishNodes || refs != largePublishNodes {
		t.Fatalf("fixture = %d nodes / %d refs, want %d/%d",
			len(wf.Nodes), refs, largePublishNodes, largePublishNodes)
	}

	cache := NewLookupCache()
	s.ResetReadQueryCount()
	profileRefs, err := s.ResolveProfileRefsWith(wf, cache)
	if err != nil {
		t.Fatalf("resolve profiles: %v", err)
	}
	schemaRefs, err := s.ResolveSchemaRefsWith(wf, cache)
	if err != nil {
		t.Fatalf("resolve schemas: %v", err)
	}
	mappingRefs, err := s.ResolveMappingRefsWith(wf, cache)
	if err != nil {
		t.Fatalf("resolve mappings: %v", err)
	}
	scriptRefs, err := s.ResolveScriptRefsWith(wf, cache)
	if err != nil {
		t.Fatalf("resolve scripts: %v", err)
	}
	reads := s.ReadQueryCount()
	if reads > largePublishReadBudget {
		t.Fatalf("large publish issued %d reads, budget is %d (distinct assets: %d)",
			reads, largePublishReadBudget, 1+largeContractCount+1+1)
	}
	t.Logf("large publish: %d nodes, %d refs, %d reads (budget %d)",
		len(wf.Nodes), refs, reads, largePublishReadBudget)

	if len(profileRefs) != 1 || profileRefs[largePublishProfileID] != 1 {
		t.Fatalf("profile pins = %#v, want one v1 pin", profileRefs)
	}
	if len(schemaRefs) != largeContractCount {
		t.Fatalf("schema pins = %d, want %d", len(schemaRefs), largeContractCount)
	}
	if len(mappingRefs) != 1 || len(scriptRefs) != 1 {
		t.Fatalf("mapping/script pins = %d/%d, want 1/1", len(mappingRefs), len(scriptRefs))
	}

	profileNode := &wf.Nodes[0]
	if got := profileNode.Config["url"]; got != "https://api.example.com/large" {
		t.Fatalf("profile value not merged: url = %#v", got)
	}
	schemaNode := &wf.Nodes[largeProfileRefs]
	if len(schemaNode.InputSchemaFields) != largeContractFields {
		t.Fatalf("contract fields = %d, want %d", len(schemaNode.InputSchemaFields), largeContractFields)
	}
	mappingNode := &wf.Nodes[largeProfileRefs+largeSchemaRefs]
	rows, ok := mappingNode.Config["mappingRows"].([]map[string]any)
	if !ok || len(rows) != largeMappingRows {
		t.Fatalf("mapping rows = %#v (ok=%v), want %d", mappingNode.Config["mappingRows"], ok, largeMappingRows)
	}
	scriptNode := &wf.Nodes[largeProfileRefs+largeSchemaRefs+largeMappingRefs]
	if got, _ := scriptNode.Config["source"].(string); got != "return $json;" {
		t.Fatalf("script source not inlined: %#v", scriptNode.Config["source"])
	}
}

// BenchmarkResolveLargePublishWorkflow measures the resolver work on the
// same fixture. It includes building the 1,000-node workflow but no DB
// writes or validation, so it isolates asset resolution cost.
func BenchmarkResolveLargePublishWorkflow(b *testing.B) {
	s, err := Open(b.TempDir())
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()
	seedLargePublishAssetsBench(b, s)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wf := buildLargePublishWorkflow()
		cache := NewLookupCache()
		if _, err := s.ResolveProfileRefsWith(wf, cache); err != nil {
			b.Fatal(err)
		}
		if _, err := s.ResolveSchemaRefsWith(wf, cache); err != nil {
			b.Fatal(err)
		}
		if _, err := s.ResolveMappingRefsWith(wf, cache); err != nil {
			b.Fatal(err)
		}
		if _, err := s.ResolveScriptRefsWith(wf, cache); err != nil {
			b.Fatal(err)
		}
	}
}

// seedLargePublishAssetsBench is seedLargePublishAssets with benchmark
// failures instead of test failures.
func seedLargePublishAssetsBench(b *testing.B, s *Store) {
	b.Helper()
	if _, err := s.CreateConfigProfile(&ConfigProfile{
		ID: largePublishProfileID, Name: "Large profile", NodeType: "http.request",
		Visibility: VisibilityShared,
		Config:     map[string]any{"method": "GET", "url": "https://api.example.com/large"},
	}, "bench"); err != nil {
		b.Fatalf("create profile: %v", err)
	}
	fields := make([]schemas.Field, 0, largeContractFields)
	for i := 0; i < largeContractFields; i++ {
		fields = append(fields, schemas.Field{Name: fmt.Sprintf("field_%03d", i), Type: "string"})
	}
	for i := 0; i < largeContractCount; i++ {
		if _, err := s.CreateDataSchema(&DataSchema{
			ID: fmt.Sprintf("perf-schema-%02d", i), Name: fmt.Sprintf("Large contract %02d", i),
			Visibility: VisibilityShared, Fields: fields,
		}, "bench"); err != nil {
			b.Fatalf("create schema %d: %v", i, err)
		}
	}
	rows := make([]MappingRow, 0, largeMappingRows)
	for i := 0; i < largeMappingRows; i++ {
		rows = append(rows, MappingRow{
			Target: fmt.Sprintf("target_%03d", i),
			Source: fmt.Sprintf("{{ $json.source_%03d }}", i),
		})
	}
	if _, err := s.CreateMapping(&Mapping{
		ID: largePublishMappingID, Name: "Large mapping", Kind: "item",
		Visibility: VisibilityShared, Rows: rows,
	}, "bench"); err != nil {
		b.Fatalf("create mapping: %v", err)
	}
	if _, err := s.CreateScript(&Script{
		ID: largePublishScriptID, Name: "Large script", Language: ScriptLanguageJavaScript,
		Visibility: VisibilityShared, Source: "return $json;",
	}, "bench"); err != nil {
		b.Fatalf("create script: %v", err)
	}
}
