package nodes

import "testing"

func TestValidateProfileConfig(t *testing.T) {
	httpDef, ok := Get("http.request")
	if !ok {
		t.Fatal("http.request missing from catalog")
	}

	if err := ValidateProfileConfig(httpDef, map[string]any{
		"method": "POST",
		"url":    "https://api.example.com/resource",
		"headers": map[string]any{
			"accept":          "application/json",
			"x-custom-header": "custom-value",
		},
		"timeout": 30.0,
	}); err != nil {
		t.Fatalf("valid http profile config rejected: %v", err)
	}

	cases := []struct {
		name   string
		config map[string]any
	}{
		{"non-profileable field", map[string]any{"mappingRefs": map[string]any{"query": "m-1"}}},
		{"unknown field", map[string]any{"nope": "x"}},
		{"wrong type", map[string]any{"method": 42.0}},
		{"bad enum", map[string]any{"method": "TELEPORT"}},
		{"below min", map[string]any{"timeout": 0.5}},
		{"above max", map[string]any{"timeout": 301.0}},
	}
	for _, tc := range cases {
		if err := ValidateProfileConfig(httpDef, tc.config); err == nil {
			t.Fatalf("%s: expected rejection, got nil", tc.name)
		}
	}
}

// The webhook node is the catalog's Secret-field carrier; its secret fields
// must be structurally impossible to profile even if someone flips the
// Profileable bit on a non-secret sibling.
func TestValidateProfileConfigRejectsSecretCarryingNodes(t *testing.T) {
	webhookDef, ok := Get("trigger.webhook")
	if !ok {
		t.Fatal("trigger.webhook missing from catalog")
	}
	for key, field := range webhookDef.Config {
		if field.Secret && field.Profileable {
			t.Fatalf("trigger.webhook.%q is Secret and Profileable — impossible by D1", key)
		}
	}
	// Every webhook config field is non-profileable today; anything sent is
	// rejected.
	if err := ValidateProfileConfig(webhookDef, map[string]any{"path": "x"}); err == nil {
		t.Fatal("non-profileable webhook field accepted")
	}
}

func TestProfileableCredentialSlots(t *testing.T) {
	httpDef, _ := Get("http.request")
	slots := ProfileableCredentialSlots(httpDef)
	if len(slots) == 0 {
		t.Fatal("http.request should expose credential slots")
	}

	waitDef, _ := Get("flow.wait")
	if got := ProfileableCredentialSlots(waitDef); len(got) != 0 {
		t.Fatalf("flow.wait has no credential slots, got %v", got)
	}
}
