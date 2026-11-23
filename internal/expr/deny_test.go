package expr

import "testing"

func TestEnvDeniedCoversCommonSecrets(t *testing.T) {
	for _, name := range []string{
		"LOOM_TOKEN", "LOOM_SECRET", "LOOM_SECRETS_DIR", "AWS_ACCESS_KEY_ID",
		"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GITHUB_TOKEN", "GITLAB_TOKEN",
		"DATABASE_URL", "POSTGRES_PASSWORD", "MYSQL_PASSWORD", "REDIS_URL", "MONGO_URI",
	} {
		if !envDenied(name) {
			t.Errorf("envDenied(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"HOME", "PATH", "TZ"} {
		if envDenied(name) {
			t.Errorf("envDenied(%q) = true, want false", name)
		}
	}
}

func TestEnvDeniedStaysDeniedWhenAllowListed(t *testing.T) {
	// A matching allow-list entry must never re-admit a denied secret, whether
	// it is an exact name or a wildcard prefix.
	names := []string{
		"LOOM_BOOTSTRAP_ADMIN_PASSWORD", "LOOM_E2E_CSRF", "PGPASSWORD",
		"VAULT_TOKEN", "TWILIO_AUTH_TOKEN", "AWS_SECRET_ACCESS_KEY",
		"AZURE_CLIENT_SECRET", "GOOGLE_APPLICATION_CREDENTIALS",
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "canary")
			for _, allow := range []string{name, name + "*"} {
				t.Setenv(envAllowVar, allow)
				if got := envScope()[name]; got != "" {
					t.Fatalf("allow-list %q exposed denied env %q = %q", allow, name, got)
				}
			}
		})
	}
}
