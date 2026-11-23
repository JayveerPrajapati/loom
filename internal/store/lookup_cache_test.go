package store

import (
	"fmt"
	"testing"

	"loom/internal/graph"
	"loom/internal/schemas"
)

func TestLookupCacheDeduplicatesResolverReads(t *testing.T) {
	s := newOpsStore(t)
	if _, err := s.CreateConfigProfile(&ConfigProfile{
		ID: "p-cache", Name: "Cache", NodeType: "http.request",
		Config: map[string]any{"method": "GET", "url": "https://example.com"},
	}, "test"); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	if _, err := s.CreateDataSchema(&DataSchema{
		ID: "s-cache", Name: "Cache",
		Fields: []schemas.Field{{Name: "email", Type: "string"}},
	}, "test"); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := s.CreateMapping(&Mapping{
		ID: "m-cache", Name: "Cache",
		Rows: []MappingRow{{Target: "email", Source: "{{ $json.email }}"}},
	}, "test"); err != nil {
		t.Fatalf("create mapping: %v", err)
	}
	if _, err := s.CreateScript(&Script{
		ID: "sc-cache", Name: "Cache", Language: ScriptLanguageJavaScript,
		Source: "return $json;",
	}, "test"); err != nil {
		t.Fatalf("create script: %v", err)
	}
	connector, err := s.CreateConnector(&Connector{
		ID: "c-cache", Name: "Cache",
		Operations: []ConnectorOperation{{
			Op: "list", Method: "GET", URL: "https://example.com/things",
		}},
	}, "test")
	if err != nil {
		t.Fatalf("create connector: %v", err)
	}

	cases := []struct {
		name    string
		build   func(n int) *graph.Workflow
		resolve func(*graph.Workflow, *LookupCache) error
	}{
		{
			name: "profile",
			build: func(n int) *graph.Workflow {
				nodes := make([]graph.Node, 0, n)
				for i := 0; i < n; i++ {
					nodes = append(nodes, graph.Node{
						ID: fmt.Sprintf("p%d", i), Type: "http.request", ProfileRef: "p-cache",
						Config: map[string]any{},
					})
				}
				return &graph.Workflow{Nodes: nodes}
			},
			resolve: func(wf *graph.Workflow, cache *LookupCache) error {
				_, err := s.ResolveProfileRefsWith(wf, cache)
				return err
			},
		},
		{
			name: "schema",
			build: func(n int) *graph.Workflow {
				nodes := make([]graph.Node, 0, n)
				for i := 0; i < n; i++ {
					nodes = append(nodes, graph.Node{
						ID: fmt.Sprintf("s%d", i), Type: "http.request", InputSchemaRef: "s-cache",
					})
				}
				return &graph.Workflow{Nodes: nodes}
			},
			resolve: func(wf *graph.Workflow, cache *LookupCache) error {
				_, err := s.ResolveSchemaRefsWith(wf, cache)
				return err
			},
		},
		{
			name: "mapping",
			build: func(n int) *graph.Workflow {
				nodes := make([]graph.Node, 0, n)
				for i := 0; i < n; i++ {
					nodes = append(nodes, graph.Node{
						ID: fmt.Sprintf("m%d", i), Type: "data.map",
						Config: map[string]any{"mappingRef": "m-cache"},
					})
				}
				return &graph.Workflow{Nodes: nodes}
			},
			resolve: func(wf *graph.Workflow, cache *LookupCache) error {
				_, err := s.ResolveMappingRefsWith(wf, cache)
				return err
			},
		},
		{
			name: "script",
			build: func(n int) *graph.Workflow {
				nodes := make([]graph.Node, 0, n)
				for i := 0; i < n; i++ {
					nodes = append(nodes, graph.Node{
						ID: fmt.Sprintf("sc%d", i), Type: "code.run",
						Config: map[string]any{"scriptRef": "sc-cache"},
					})
				}
				return &graph.Workflow{Nodes: nodes}
			},
			resolve: func(wf *graph.Workflow, cache *LookupCache) error {
				_, err := s.ResolveScriptRefsWith(wf, cache)
				return err
			},
		},
		{
			name: "connector",
			build: func(n int) *graph.Workflow {
				nodes := make([]graph.Node, 0, n)
				for i := 0; i < n; i++ {
					nodes = append(nodes, graph.Node{
						ID:   fmt.Sprintf("c%d", i),
						Type: ConnectorTypePrefix + connector.ID + ".list",
					})
				}
				return &graph.Workflow{Nodes: nodes}
			},
			resolve: func(wf *graph.Workflow, cache *LookupCache) error {
				_, err := s.ResolveConnectorRefsWith(wf, cache)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			one := resolverReads(t, s, tc.build, tc.resolve, 1)
			many := resolverReads(t, s, tc.build, tc.resolve, 12)
			if one == 0 {
				t.Fatalf("%s resolver issued no reads", tc.name)
			}
			if many != one {
				t.Fatalf("%s: 1 node = %d reads, 12 nodes = %d reads; cache did not dedupe",
					tc.name, one, many)
			}
		})
	}
}

func resolverReads(
	t *testing.T,
	s *Store,
	build func(n int) *graph.Workflow,
	resolve func(*graph.Workflow, *LookupCache) error,
	n int,
) int64 {
	t.Helper()
	wf := build(n)
	s.ResetReadQueryCount()
	if err := resolve(wf, NewLookupCache()); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return s.ReadQueryCount()
}
