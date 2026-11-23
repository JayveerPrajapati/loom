package graph

import "testing"

func TestHTTPConfigValidation(t *testing.T) {
	build := func(extra map[string]any) *Workflow {
		config := map[string]any{
			"method": "POST", "url": "https://example.com", "bodyType": "none",
		}
		for key, value := range extra {
			config[key] = value
		}
		return &Workflow{
			ID: "http-config", Name: "HTTP config", Version: 1,
			Nodes: []Node{
				mkNode("start", "trigger.manual", map[string]any{}, nil),
				mkNode("http", "http.request", config, nil),
			},
			Edges: []Edge{mkEdge("start", "main", "http", "main")},
		}
	}

	cases := []struct {
		name      string
		extra     map[string]any
		wantIssue bool
	}{
		{"array body accepted", map[string]any{
			"bodyType": "json", "body": []any{float64(1), float64(2), float64(3)},
		}, false},
		{"valid success codes", map[string]any{"successCodes": "2xx, 200-299, 404"}, false},
		{"invalid success codes", map[string]any{"successCodes": "200-"}, true},
		{"timeout below minimum", map[string]any{"timeout": float64(0)}, true},
		{"timeout above maximum", map[string]any{"timeout": float64(301)}, true},
		{"rate limit above maximum", map[string]any{"rateLimitPerMinute": float64(601)}, true},
	}
	for _, tc := range cases {
		report := Validate(build(tc.extra))
		got := hasCode(report, "invalid_config")
		if got != tc.wantIssue {
			t.Errorf("%s: invalid_config = %v, want %v (%v)", tc.name, got, tc.wantIssue, report.Issues)
		}
	}
}
