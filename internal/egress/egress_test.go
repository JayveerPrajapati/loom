package egress

import (
	"strings"
	"testing"
)

func TestPolicyDefaultAllowsEverything(t *testing.T) {
	p := Policy{}
	if err := p.Check("https://anything.example.com"); err != nil {
		t.Fatalf("zero-value policy should allow: %v", err)
	}
}

func TestPolicyModeAllowAllowsEverything(t *testing.T) {
	p := Policy{Mode: ModeAllow}
	if err := p.Check("http://10.0.0.1/"); err != nil {
		t.Fatalf("mode allow should not restrict: %v", err)
	}
}

func TestPolicyDenyBlocksUnlisted(t *testing.T) {
	p := Policy{Mode: ModeDeny, Allowlist: []string{"https://api.example.com"}}
	err := p.Check("https://evil.example.net")
	if err == nil || !strings.Contains(err.Error(), "not in the global allowlist") {
		t.Fatalf("want global-allowlist denial, got %v", err)
	}
}

func TestPolicyDenyAllowsListed(t *testing.T) {
	p := Policy{Mode: ModeDeny, Allowlist: []string{"https://api.example.com"}}
	if err := p.Check("https://api.example.com/v1/users"); err != nil {
		t.Fatalf("listed destination should pass: %v", err)
	}
}

func TestPolicyDenySchemeMatters(t *testing.T) {
	p := Policy{Mode: ModeDeny, Allowlist: []string{"https://api.example.com"}}
	if err := p.Check("http://api.example.com"); err == nil {
		t.Fatal("http should not match an https-only entry")
	}
}

func TestPolicyPortMatters(t *testing.T) {
	p := Policy{Mode: ModeDeny, Allowlist: []string{"https://api.example.com:8443"}}
	if err := p.Check("https://api.example.com:8443"); err != nil {
		t.Fatalf("exact port should pass: %v", err)
	}
	if err := p.Check("https://api.example.com"); err == nil {
		t.Fatal("default port should not match a 8443 entry")
	}
	// An entry without a port matches any port.
	p2 := Policy{Mode: ModeDeny, Allowlist: []string{"https://api.example.com"}}
	if err := p2.Check("https://api.example.com:9943"); err != nil {
		t.Fatalf("portless entry should match any port: %v", err)
	}
}

func TestPolicyWildcardSubdomains(t *testing.T) {
	p := Policy{Mode: ModeDeny, Allowlist: []string{"https://*.github.com"}}
	for _, dest := range []string{
		"https://api.github.com",
		"https://uploads.github.com",
		"https://deep.sub.github.com",
	} {
		if err := p.Check(dest); err != nil {
			t.Fatalf("%s should match *.github.com: %v", dest, err)
		}
	}
	if err := p.Check("https://github.com.evil.net"); err == nil {
		t.Fatal("suffix trick must not match")
	}
	if err := p.Check("https://github.com"); err == nil {
		t.Fatal("bare domain must not match *.domain (explicit entry required)")
	}
}

func TestPolicyInvalidMode(t *testing.T) {
	p := Policy{Mode: "open"}
	if err := p.Check("https://x.example.com"); err == nil || !strings.Contains(err.Error(), "invalid policy mode") {
		t.Fatalf("want invalid-mode error, got %v", err)
	}
}

func TestWorkflowPolicyNilAndEmptyAllowAll(t *testing.T) {
	var w *WorkflowPolicy
	if err := w.Check("https://anything.example.com"); err != nil {
		t.Fatalf("nil workflow policy should not restrict: %v", err)
	}
	if err := (&WorkflowPolicy{}).Check("https://anything.example.com"); err != nil {
		t.Fatalf("empty workflow policy should not restrict: %v", err)
	}
}

func TestWorkflowPolicyRestricts(t *testing.T) {
	w := &WorkflowPolicy{Allowlist: []string{"https://api.github.com"}}
	if err := w.Check("https://api.github.com/graphql"); err != nil {
		t.Fatalf("listed destination should pass: %v", err)
	}
	err := w.Check("https://api.gitlab.com")
	if err == nil || !strings.Contains(err.Error(), "workflow's egress allowlist") {
		t.Fatalf("want workflow denial naming the workflow, got %v", err)
	}
}

func TestCredentialAllows(t *testing.T) {
	if err := CredentialAllows("github", nil, "api.github.com:443"); err != nil {
		t.Fatalf("absent restriction should not limit: %v", err)
	}
	allowed := []string{"api.github.com", "*.api.slack.com"}
	if err := CredentialAllows("github", allowed, "api.github.com:443"); err != nil {
		t.Fatalf("host match should pass: %v", err)
	}
	if err := CredentialAllows("github", allowed, "files.api.slack.com:443"); err != nil {
		t.Fatalf("wildcard host should pass: %v", err)
	}
	err := CredentialAllows("github", allowed, "evil.example.net:443")
	if err == nil || !strings.Contains(err.Error(), `credential "github"`) {
		t.Fatalf("want credential-named denial, got %v", err)
	}
	if err := CredentialAllows("github", []string{"api.github.com:443"}, "api.github.com:8443"); err == nil {
		t.Fatal("port-bearing restriction should enforce the port")
	}
}

func TestParseDestination(t *testing.T) {
	cases := map[string]string{
		"https://api.example.com/v1/x?y=1": "https://api.example.com",
		"http://Example.COM:8080/path":     "http://example.com:8080",
		"https://[2001:db8::1]:9443/x":     "https://[2001:db8::1]:9443",
	}
	for in, want := range cases {
		got, err := ParseDestination(in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Fatalf("%s: got %q want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "not a url", "https://"} {
		if _, err := ParseDestination(bad); err == nil {
			t.Fatalf("%q should be rejected", bad)
		}
	}
}

func TestHostPortFromAddress(t *testing.T) {
	cases := map[string]string{
		"db.internal:5432":   "db.internal",
		"db.internal":        "db.internal",
		"[2001:db8::5]:5432": "[2001:db8::5]",
		"Host.EXAMPLE:443":   "host.example",
	}
	for in, want := range cases {
		if got := HostPortFromAddress(in); got != want {
			t.Fatalf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestValidateEntry(t *testing.T) {
	good := []string{
		"https://api.example.com",
		"https://api.example.com:8443",
		"http://intranet.corp:8080",
		"https://*.github.com",
		"https://*.github.com:443",
		"postgres://db.internal:5432",
		"postgres://db.internal",
	}
	for _, entry := range good {
		if err := ValidateEntry(entry); err != nil {
			t.Fatalf("%q should validate: %v", entry, err)
		}
	}
	bad := []string{
		"api.example.com",            // no scheme
		"ftp://api.example.com",      // scheme
		"https://",                   // no host
		"https://api.example.com/v1", // path
		"https://*.*.example.com",    // nested wildcard
		"https://api.*.com",          // mid wildcard
	}
	for _, entry := range bad {
		if err := ValidateEntry(entry); err == nil {
			t.Fatalf("%q should be rejected", entry)
		}
	}
}
