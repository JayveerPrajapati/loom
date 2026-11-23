package graph

import "testing"

func paginationWorkflow(pagination map[string]any) *Workflow {
	return &Workflow{
		ID: "http-pagination", Name: "HTTP pagination", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("call", "http.request", map[string]any{
				"method":     "GET",
				"url":        "https://api.example.com/things",
				"pagination": pagination,
			}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "call", "main")},
	}
}

func TestValidateHTTPPagination(t *testing.T) {
	cases := []struct {
		name       string
		pagination map[string]any
		code       string
	}{
		{"offset without limit", map[string]any{"mode": "offset"}, "invalid_pagination_limit"},
		{"cursor without path", map[string]any{"mode": "cursor"}, "invalid_pagination_cursor_path"},
		{"bad mode", map[string]any{"mode": "scroll"}, "invalid_pagination_mode"},
		{"missing mode", map[string]any{"maxPages": 5}, "invalid_pagination_mode"},
		{"maxPages over cap", map[string]any{"mode": "page", "maxPages": 5000}, "invalid_pagination_max_pages"},
		{"maxItems over cap", map[string]any{"mode": "page", "maxItems": 500000}, "invalid_pagination_max_items"},
	}
	for _, tc := range cases {
		report := Validate(paginationWorkflow(tc.pagination))
		if !hasCode(report, tc.code) {
			t.Errorf("%s: expected issue %s, got: %v", tc.name, tc.code, report.Issues)
		}
	}

	valid := Validate(paginationWorkflow(map[string]any{
		"mode": "cursor", "cursorPath": "nextCursor", "maxPages": 10,
	}))
	if valid.HasErrors() {
		t.Errorf("valid cursor pagination rejected: %v", valid.Issues)
	}
}

func TestValidateHTTPOAuthAndGraphQL(t *testing.T) {
	// oauth2 auth without the oauth2 credential must fail at save time.
	oauthMissing := &Workflow{
		ID: "http-oauth", Name: "HTTP oauth", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("call", "http.request", map[string]any{
				"method": "GET", "url": "https://api.example.com/me", "auth": "oauth2",
			}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "call", "main")},
	}
	if report := Validate(oauthMissing); !hasCode(report, "missing_oauth2_credential") {
		t.Errorf("expected missing_oauth2_credential, got: %v", report.Issues)
	}

	// With the credential declared it validates clean.
	oauthOK := &Workflow{
		ID: "http-oauth", Name: "HTTP oauth", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("call", "http.request", map[string]any{
				"method": "GET", "url": "https://api.example.com/me", "auth": "oauth2",
			}, map[string]string{"oauth2": "my-oauth"}),
		},
		Edges: []Edge{mkEdge("start", "main", "call", "main")},
	}
	if report := Validate(oauthOK); hasCode(report, "missing_oauth2_credential") || hasCode(report, "unknown_credential") {
		t.Errorf("declared oauth2 credential flagged: %v", report.Issues)
	}

	// GraphQL without a query must fail at save time.
	gqlMissing := &Workflow{
		ID: "http-gql", Name: "HTTP graphql", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("call", "http.request", map[string]any{
				"method": "POST", "url": "https://api.example.com/graphql",
				"bodyType": "graphql",
			}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "call", "main")},
	}
	if report := Validate(gqlMissing); !hasCode(report, "missing_graphql_query") {
		t.Errorf("expected missing_graphql_query, got: %v", report.Issues)
	}

	// With a query it validates clean.
	gqlOK := &Workflow{
		ID: "http-gql", Name: "HTTP graphql", Version: 1,
		Nodes: []Node{
			mkNode("start", "trigger.manual", map[string]any{}, nil),
			mkNode("call", "http.request", map[string]any{
				"method": "POST", "url": "https://api.example.com/graphql",
				"bodyType": "graphql", "graphqlQuery": "query { viewer }",
			}, nil),
		},
		Edges: []Edge{mkEdge("start", "main", "call", "main")},
	}
	if report := Validate(gqlOK); hasCode(report, "missing_graphql_query") {
		t.Errorf("valid graphql flagged: %v", report.Issues)
	}
}

func TestValidatePythonRequirements(t *testing.T) {
	pythonWorkflow := func(requirements []any) *Workflow {
		return &Workflow{
			ID: "py-req", Name: "Python requirements", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				mkNode("py", "code.python", map[string]any{
					"source": "return 1", "requirements": requirements,
				}, nil),
			},
			Edges: []Edge{mkEdge("start", "main", "py", "main")},
		}
	}
	// Unpinned requirements must fail at save time.
	if report := Validate(pythonWorkflow([]any{"httpx", "pandas==2.2.0"})); !hasCode(report, "invalid_python_requirement") {
		t.Errorf("expected invalid_python_requirement, got: %v", report.Issues)
	}
	// Pinned requirements validate clean.
	if report := Validate(pythonWorkflow([]any{"httpx==0.27.0", "pandas==2.2.0"})); hasCode(report, "invalid_python_requirement") {
		t.Errorf("pinned requirements flagged: %v", report.Issues)
	}
	// No requirements at all is fine (system python).
	if report := Validate(pythonWorkflow(nil)); report.HasErrors() {
		t.Errorf("requirement-less python node flagged: %v", report.Issues)
	}
}

func TestPythonMemoryCapRange(t *testing.T) {
	wf := func(memoryMB any) *Workflow {
		return &Workflow{
			ID: "py-mem", Name: "Python memory cap", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				mkNode("py", "code.python", map[string]any{
					"source": "return 1", "maxMemoryMB": memoryMB,
				}, nil),
			},
			Edges: []Edge{mkEdge("start", "main", "py", "main")},
		}
	}
	if report := Validate(wf(4)); !hasCode(report, "invalid_config") {
		t.Errorf("maxMemoryMB below the floor must be invalid, got: %v", report.Issues)
	}
	if report := Validate(wf(90000)); !hasCode(report, "invalid_config") {
		t.Errorf("maxMemoryMB above the ceiling must be invalid, got: %v", report.Issues)
	}
	if report := Validate(wf(float64(256))); hasCode(report, "invalid_config") {
		t.Errorf("maxMemoryMB in range must validate, got: %v", report.Issues)
	}
}
