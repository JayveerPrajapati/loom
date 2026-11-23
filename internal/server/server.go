package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"time"

	"loom/internal/auth"
	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/pyenv"
	"loom/internal/store"
)

type Deps struct {
	Version        string
	Store          *store.Store
	Engine         *engine.Engine
	Token          string
	Reload         func() error
	AllowAnonymous bool
	CookieSecure   bool
	// Prod forces Secure cookies regardless of the request transport so a
	// TLS terminator in front of plain HTTP still gets hardened sessions.
	Prod bool
	// TrustForwardedHost makes the login origin check accept the host named by
	// X-Forwarded-Host in addition to r.Host. Enable only behind a proxy that
	// overwrites the header; otherwise a client could spoof it.
	TrustForwardedHost bool
	// TrustedProxyCIDRs lets login throttling and auth audit events use the
	// client hop from X-Forwarded-For when r.RemoteAddr is one of these
	// operator-configured networks. Empty keeps the direct RemoteAddr.
	TrustedProxyCIDRs []*net.IPNet
	SessionTTL        time.Duration
	CSRFOverride      string
	// RejectWebhookQuerySecrets refuses webhook authentication via
	// ?secret= outright instead of accepting it with a deprecation warning.
	// Operators set this once their callers have migrated to header or
	// bearer authentication.
	RejectWebhookQuerySecrets bool
	// MaxTokenLifetime bounds API-token creation: a token minted without an
	// expiry gets exactly this lifetime, and longer explicit expiries are
	// rejected. 0 disables the cap (non-expiring tokens allowed) — an
	// operator opt-out, never the default the CLI sets (720h).
	MaxTokenLifetime time.Duration
	// BuildEnvs builds hash-keyed Python dependency environments at
	// publish time (the only phase allowed to install packages). nil means
	// the server runs without subprocess support: publishing a workflow
	// with code.python requirements then fails with a clear error.
	BuildEnvs func(requirements []string) error
	// ListPythonEnvs reports the built dependency environments (key,
	// requirements, size) for the operator status/publish surfaces
	// (Phase 5). nil when subprocess nodes are disabled — a simulated
	// host builds and serves nothing.
	ListPythonEnvs func() ([]pyenv.EnvInfo, error)
	// TestProfile runs Configuration Hub connection tests through the live
	// host's guarded machinery (same private-target guard, proxy and TLS
	// rules as real runs). nil means the server runs a simulated host and
	// profile testing fails with an actionable error.
	TestProfile ProfileTester
	// PythonExecutor runs python script tests through the live host's
	// subprocess machinery (Phase 4). nil means a simulated host: python
	// script tests answer 503. JavaScript tests run in-process on every
	// server and need no capability.
	PythonExecutor flow.Executor
	// SubprocessState describes code.python execution for operators:
	// enabled or disabled, the package policy path in force, and its
	// per-environment cap in MiB. Surfaced via /healthz so the isolation
	// posture is inspectable without shell access.
	SubprocessState SubprocessState
	// EgressState describes the live host's egress policy for operators
	// (the operational-trust campaign). A zero value reports nothing.
	EgressState EgressState
	// Capabilities is the admin-only capability/auth posture served at
	// /api/capabilities. /healthz keeps only the editor preflight fields.
	Capabilities CapabilitiesState
}

// SubprocessState is the operator-facing summary of code.python execution.
type SubprocessState struct {
	Enabled   bool   `json:"enabled"`
	Policy    string `json:"policy,omitempty"`
	CapMB     int    `json:"capMB,omitempty"`
	OSLevel   string `json:"osLevel"` // "container-recommended" on Linux, "sandbox-exec" on macOS, "" when disabled
	Container string `json:"containerRecommended,omitempty"`
	// Network is the code.python network posture (isolation-runner
	// campaign): "allow" (scripts may call out) or "deny" (the OS cuts
	// python's network — Linux namespaces / Seatbelt network-deny).
	Network string `json:"network,omitempty"` // "allow" | "deny"; empty when disabled
}

// EgressState is the /healthz view of the live host's egress policy
// (the operational-trust campaign). Mode "allow" is the default
// allow-all posture; "deny" restricts outbound destinations to the
// allowlist count reported alongside.
type EgressState struct {
	Mode      string `json:"mode"`
	Allowlist int    `json:"allowlist,omitempty"`
}

// CapabilitiesState is the admin-only summary of the deployment's auth and
// execution posture. It is deliberately separate from /healthz: the public
// health probe keeps only the fields the editor preflight needs.
type CapabilitiesState struct {
	Prod         bool            `json:"prod"`
	Anonymous    bool            `json:"anonymous"`
	ServiceToken bool            `json:"serviceToken"`
	Subprocess   SubprocessState `json:"subprocess"`
	Egress       EgressState     `json:"egress"`
	Warnings     []string        `json:"warnings"`
}

func Handler(deps Deps) http.Handler {
	s := &api{deps: deps, webhookSem: make(chan struct{}, 256)}
	wireEventBus(deps, s)
	s.rebuildEventTriggers()
	deps.Engine.OnRunFinished = s.shadowRun
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		body := map[string]any{"status": "ok"}
		if deps.SubprocessState.Enabled {
			body["subprocessNodes"] = map[string]any{
				"enabled":              true,
				"policy":               deps.SubprocessState.Policy,
				"capMB":                deps.SubprocessState.CapMB,
				"osLevel":              deps.SubprocessState.OSLevel,
				"containerRecommended": deps.SubprocessState.Container,
				"network":              deps.SubprocessState.Network,
			}
		} else {
			body["subprocessNodes"] = map[string]any{"enabled": false}
		}
		if deps.EgressState.Mode != "" {
			body["egress"] = map[string]any{
				"mode":      deps.EgressState.Mode,
				"allowlist": deps.EgressState.Allowlist,
			}
		}
		writeJSON(w, http.StatusOK, body)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := deps.Store.Ping(ctx); err != nil {
			// The body is public; keep driver paths and SQL errors in the log.
			log.Printf("readyz: store ping failed: %v", err)
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	// /metrics is intentionally unauthenticated so a sidecar Prometheus can
	// scrape it without the API token. Put it behind a proxy on public hosts.
	mux.HandleFunc("GET /metrics", s.prometheusMetrics)
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"name": "loom", "version": deps.Version})
	})

	s.route(mux, "GET /api/stats", auth.RoleViewer, s.stats)

	mux.HandleFunc("POST /api/auth/login", s.login)
	mux.HandleFunc("POST /api/auth/logout", s.logout)
	s.route(mux, "GET /api/auth/me", auth.RoleViewer, s.me)
	s.route(mux, "POST /api/auth/password", auth.RoleViewer, s.changePassword)
	s.route(mux, "GET /api/auth/events", auth.RoleAdmin, s.listAuthEvents)
	s.route(mux, "GET /api/capabilities", auth.RoleAdmin, s.capabilities)

	s.route(mux, "GET /api/users", auth.RoleAdmin, s.listUsers)
	s.route(mux, "POST /api/users", auth.RoleAdmin, s.createUser)
	s.route(mux, "PUT /api/users/{id}", auth.RoleAdmin, s.updateUser)
	s.route(mux, "POST /api/users/{id}/password", auth.RoleAdmin, s.resetUserPassword)

	s.route(mux, "GET /api/tokens", auth.RoleViewer, s.listTokens)
	s.route(mux, "POST /api/tokens", auth.RoleViewer, s.createToken)
	s.route(mux, "DELETE /api/tokens/{id}", auth.RoleViewer, s.revokeToken)

	s.route(mux, "GET /api/node-types", auth.RoleViewer, s.nodeTypes)
	s.route(mux, "GET /api/examples", auth.RoleViewer, s.listExamples)

	// Configuration Hub (v3 plan D1): node-type-centric views. Read-only
	// composition over the entity stores; writes keep going through the
	// per-entity endpoints above.
	s.route(mux, "GET /api/hub", auth.RoleViewer, s.hubOverview)
	s.route(mux, "GET /api/hub/{nodeType}", auth.RoleViewer, s.hubNodeType)
	s.route(mux, "GET /api/credentials", auth.RoleAdmin, s.listCredentials)
	s.route(mux, "POST /api/credentials", auth.RoleAdmin, s.createCredential)
	s.route(mux, "GET /api/credentials/{id}/usage", auth.RoleAdmin, s.credentialUsage)
	s.route(mux, "POST /api/credentials/{id}/rotate", auth.RoleAdmin, s.rotateCredential)
	s.route(mux, "DELETE /api/credentials/{id}", auth.RoleAdmin, s.deleteCredential)

	s.route(mux, "GET /api/config-profiles", auth.RoleViewer, s.listProfilesFiltered)
	s.route(mux, "POST /api/config-profiles", auth.RoleEditor, s.createProfile)
	s.route(mux, "GET /api/config-profiles/{id}", auth.RoleViewer, s.getProfile)
	s.route(mux, "PUT /api/config-profiles/{id}", auth.RoleEditor, s.updateProfile)
	s.route(mux, "DELETE /api/config-profiles/{id}", auth.RoleAdmin, s.deleteProfile)
	s.route(mux, "GET /api/config-profiles/{id}/versions", auth.RoleViewer, s.listProfileVersions)
	s.route(mux, "GET /api/config-profiles/{id}/versions/{version}", auth.RoleViewer, s.getProfileVersion)
	s.route(mux, "GET /api/config-profiles/{id}/usage", auth.RoleViewer, s.profileUsage)
	s.route(mux, "POST /api/config-profiles/{id}/duplicate", auth.RoleEditor, s.duplicateProfile)
	s.route(mux, "POST /api/config-profiles/{id}/test", auth.RoleEditor, s.testProfile)
	s.route(mux, "POST /api/config-profiles/{id}/share", auth.RoleEditor, s.shareProfile(store.VisibilityShared))
	s.route(mux, "POST /api/config-profiles/{id}/unshare", auth.RoleEditor, s.shareProfile(store.VisibilityPrivate))

	s.route(mux, "GET /api/data-schemas", auth.RoleViewer, s.listSchemas)
	s.route(mux, "POST /api/data-schemas", auth.RoleEditor, s.createSchema)
	s.route(mux, "GET /api/data-schemas/{id}", auth.RoleViewer, s.getSchema)
	s.route(mux, "PUT /api/data-schemas/{id}", auth.RoleEditor, s.updateSchema)
	s.route(mux, "DELETE /api/data-schemas/{id}", auth.RoleAdmin, s.deleteSchema)
	s.route(mux, "GET /api/data-schemas/{id}/versions", auth.RoleViewer, s.listSchemaVersions)
	s.route(mux, "GET /api/data-schemas/{id}/versions/{version}", auth.RoleViewer, s.getSchemaVersion)
	s.route(mux, "GET /api/data-schemas/{id}/usage", auth.RoleViewer, s.schemaUsage)
	s.route(mux, "POST /api/data-schemas/{id}/duplicate", auth.RoleEditor, s.duplicateSchema)
	s.route(mux, "POST /api/data-schemas/{id}/test", auth.RoleEditor, s.testSchema)
	s.route(mux, "POST /api/data-schemas/{id}/share", auth.RoleEditor, s.shareSchema(store.VisibilityShared))
	s.route(mux, "POST /api/data-schemas/{id}/unshare", auth.RoleEditor, s.shareSchema(store.VisibilityPrivate))

	s.route(mux, "GET /api/mappings", auth.RoleViewer, s.listMappingsFiltered)
	s.route(mux, "POST /api/mappings", auth.RoleEditor, s.createMapping)
	s.route(mux, "GET /api/mappings/{id}", auth.RoleViewer, s.getMapping)
	s.route(mux, "PUT /api/mappings/{id}", auth.RoleEditor, s.updateMapping)
	s.route(mux, "DELETE /api/mappings/{id}", auth.RoleAdmin, s.deleteMapping)
	s.route(mux, "GET /api/mappings/{id}/versions", auth.RoleViewer, s.listMappingVersions)
	s.route(mux, "GET /api/mappings/{id}/versions/{version}", auth.RoleViewer, s.getMappingVersion)
	s.route(mux, "GET /api/mappings/{id}/usage", auth.RoleViewer, s.mappingUsage)
	s.route(mux, "POST /api/mappings/{id}/duplicate", auth.RoleEditor, s.duplicateMapping)
	s.route(mux, "POST /api/mappings/{id}/share", auth.RoleEditor, s.shareMapping(store.VisibilityShared))
	s.route(mux, "POST /api/mappings/{id}/unshare", auth.RoleEditor, s.shareMapping(store.VisibilityPrivate))

	s.route(mux, "GET /api/scripts", auth.RoleViewer, s.listScripts)
	// Phase 5: the built dependency environments are operator-facing
	// publish status (hash, requirements, size); editors see them beside
	// the scripts they run.
	s.route(mux, "GET /api/python-envs", auth.RoleEditor, s.listPythonEnvs)
	s.route(mux, "GET /api/ops/storage", auth.RoleEditor, s.listStorageOps)
	s.route(mux, "GET /api/scripts/{id}/recorded-test", auth.RoleViewer, s.recordedScriptTest)
	s.route(mux, "POST /api/scripts", auth.RoleEditor, s.createScript)
	s.route(mux, "GET /api/scripts/{id}", auth.RoleViewer, s.getScript)
	s.route(mux, "PUT /api/scripts/{id}", auth.RoleEditor, s.updateScript)
	s.route(mux, "DELETE /api/scripts/{id}", auth.RoleAdmin, s.deleteScript)
	s.route(mux, "GET /api/scripts/{id}/versions", auth.RoleViewer, s.listScriptVersions)
	s.route(mux, "GET /api/scripts/{id}/versions/{version}", auth.RoleViewer, s.getScriptVersion)
	s.route(mux, "GET /api/scripts/{id}/usage", auth.RoleViewer, s.scriptUsage)
	s.route(mux, "POST /api/scripts/{id}/duplicate", auth.RoleEditor, s.duplicateScript)
	s.route(mux, "POST /api/scripts/{id}/share", auth.RoleEditor, s.shareScript(store.VisibilityShared))
	s.route(mux, "POST /api/scripts/{id}/unshare", auth.RoleEditor, s.shareScript(store.VisibilityPrivate))
	s.route(mux, "POST /api/scripts/{id}/test", auth.RoleEditor, s.testScript)

	s.route(mux, "GET /api/connectors", auth.RoleViewer, s.listConnectors)
	s.route(mux, "POST /api/connectors", auth.RoleEditor, s.createConnector)
	s.route(mux, "POST /api/connectors/{id}/duplicate", auth.RoleEditor, s.duplicateConnector)
	s.route(mux, "GET /api/connectors/{id}", auth.RoleViewer, s.getConnector)
	s.route(mux, "PUT /api/connectors/{id}", auth.RoleEditor, s.updateConnector)
	s.route(mux, "DELETE /api/connectors/{id}", auth.RoleAdmin, s.deleteConnector)
	s.route(mux, "GET /api/connectors/{id}/versions", auth.RoleViewer, s.listConnectorVersions)
	s.route(mux, "GET /api/connectors/{id}/usage", auth.RoleViewer, s.connectorUsage)
	s.route(mux, "POST /api/connectors/{id}/share", auth.RoleEditor, s.shareConnector(store.VisibilityShared))
	s.route(mux, "POST /api/connectors/{id}/unshare", auth.RoleEditor, s.shareConnector(store.VisibilityPrivate))

	s.route(mux, "POST /api/expressions/eval", auth.RoleEditor, s.evalExpression)

	s.route(mux, "GET /api/workflows", auth.RoleViewer, s.listWorkflows)
	s.route(mux, "POST /api/workflows", auth.RoleEditor, s.createWorkflow)
	s.route(mux, "GET /api/workflows/{id}", auth.RoleViewer, s.getWorkflow)
	s.route(mux, "GET /api/workflows/{id}/state", auth.RoleViewer, s.workflowState)
	s.route(mux, "GET /api/workflows/{id}/versions", auth.RoleViewer, s.listWorkflowVersions)
	s.route(mux, "GET /api/workflows/{id}/versions/{version}", auth.RoleViewer, s.getWorkflowVersion)
	s.route(mux, "POST /api/workflows/{id}/publish", auth.RoleEditor, s.publishWorkflow)
	s.route(mux, "GET /api/workflows/{id}/publish/status", auth.RoleEditor, s.publishStatus)
	s.route(mux, "POST /api/workflows/{id}/upgrade-preview", auth.RoleEditor, s.upgradePreviewWorkflow)
	s.route(mux, "POST /api/workflows/{id}/verify", auth.RoleEditor, s.verifyWorkflow)
	s.route(mux, "GET /api/workflows/{id}/runbook", auth.RoleViewer, s.workflowRunbook)
	s.route(mux, "PUT /api/workflows/{id}", auth.RoleEditor, s.putWorkflow)
	s.route(mux, "DELETE /api/workflows/{id}", auth.RoleEditor, s.deleteWorkflow)
	s.route(mux, "POST /api/workflows/{id}/runs", auth.RoleOperator, s.startRun)
	s.route(mux, "POST /api/workflows/{id}/preview", auth.RoleEditor, s.previewExpression)
	s.route(mux, "GET /api/jobs", auth.RoleViewer, s.listJobs)
	s.route(mux, "POST /api/jobs", auth.RoleEditor, s.createJob)
	s.route(mux, "POST /api/schedules/preview", auth.RoleViewer, s.previewSchedule)
	s.route(mux, "GET /api/jobs/{id}", auth.RoleViewer, s.getJob)
	s.route(mux, "PUT /api/jobs/{id}", auth.RoleEditor, s.updateJob)
	s.route(mux, "DELETE /api/jobs/{id}", auth.RoleEditor, s.deleteJob)
	s.route(mux, "POST /api/jobs/{id}/duplicate", auth.RoleEditor, s.duplicateJob)
	s.route(mux, "POST /api/jobs/{id}/run", auth.RoleOperator, s.runJob)
	s.route(mux, "POST /api/jobs/{id}/batch", auth.RoleOperator, s.startBatch)
	s.route(mux, "POST /api/jobs/{id}/enabled", auth.RoleOperator, s.setJobEnabled)
	s.route(mux, "POST /api/jobs/{id}/upgrade", auth.RoleEditor, s.upgradeJob)

	s.route(mux, "GET /api/runs", auth.RoleViewer, s.listRuns)
	s.route(mux, "GET /api/runs/{id}", auth.RoleViewer, s.getRun)
	s.route(mux, "GET /api/runs/{id}/workflow", auth.RoleViewer, s.runWorkflow)
	s.route(mux, "GET /api/runs/{id}/events", auth.RoleViewer, s.streamEvents)
	s.route(mux, "GET /api/runs/{id}/data-context", auth.RoleViewer, s.runDataContext)
	s.route(mux, "GET /api/runs/{id}/diff", auth.RoleViewer, s.diffRuns)
	s.route(mux, "GET /api/blobs/{hash}", auth.RoleViewer, s.getBlob)
	s.route(mux, "POST /api/runs/{id}/replay", auth.RoleOperator, s.replayRun)
	s.route(mux, "POST /api/runs/{id}/retry", auth.RoleOperator, s.retryRun)
	s.route(mux, "POST /api/runs/{id}/cancel", auth.RoleOperator, s.cancelRun)
	s.route(mux, "POST /api/runs/{id}/resume", auth.RoleOperator, s.resumeRun)
	s.route(mux, "POST /api/runs/{id}/revoke-approval", auth.RoleOperator, s.revokeRunApproval)
	s.route(mux, "GET /api/decisions", auth.RoleViewer, s.listDecisions)
	s.route(mux, "GET /api/metrics/decisions", auth.RoleViewer, s.decisionMetrics)

	mux.HandleFunc("GET /hook/approval/{token}", s.approvalCallback)
	mux.HandleFunc("POST /hook/approval/{token}", s.approvalCallback)
	mux.HandleFunc("POST /hook/{path...}", s.webhook)
	mux.HandleFunc("GET /hook/{path...}", s.webhook)

	mux.Handle("/", http.HandlerFunc(s.ui))

	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		IncRequests()
		rw := &responseObserver{ResponseWriter: w, statusCode: http.StatusOK}
		defer func() {
			DecRequests(rw.statusCode)
		}()

		rw.Header().Set("X-Content-Type-Options", "nosniff")
		rw.Header().Set("X-Frame-Options", "DENY")
		rw.Header().Set("Referrer-Policy", "no-referrer")
		rw.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; connect-src 'self'; font-src 'self' data:; "+
				"frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(rw, r)
	})
}

type responseObserver struct {
	http.ResponseWriter
	statusCode int
}

func (ro *responseObserver) WriteHeader(code int) {
	ro.statusCode = code
	ro.ResponseWriter.WriteHeader(code)
}

func (ro *responseObserver) Flush() {
	if flusher, ok := ro.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (ro *responseObserver) Unwrap() http.ResponseWriter {
	return ro.ResponseWriter
}

func Run(ctx context.Context, addr string, deps Deps) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           Handler(deps),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const indexHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Loom</title>
<style>
  :root {
    --bg: #fbfbfd; --fg: #1d1d1f; --muted: #6e6e73;
    --line: #d2d2d7; --accent: #0071e3; --card: #ffffff;
    --radius: 12px; --gap: 16px;
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", sans-serif;
  }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#000; --fg:#f5f5f7; --muted:#86868b; --line:#2d2d2f; --card:#1c1c1e; }
  }
  * { box-sizing: border-box; }
  body { margin:0; background:var(--bg); color:var(--fg); min-height:100vh;
         display:grid; place-items:center; padding:32px; }
  main { width:min(680px, 100%); }
  .brand { display:flex; align-items:center; gap:10px; margin-bottom:8px; }
  .dot { width:12px; height:12px; border-radius:50%; background:var(--accent); }
  h1 { font-size:34px; line-height:1.1; margin:0 0 8px; letter-spacing:-0.02em; }
  p { color:var(--muted); font-size:16px; line-height:1.5; margin:0 0 24px; }
  .card { background:var(--card); border:1px solid var(--line); border-radius:var(--radius);
          padding:20px; display:grid; gap:12px; }
  .row { display:flex; justify-content:space-between; gap:16px; font-size:14px; }
  .row span:first-child { color:var(--muted); }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size:13px; }
  .pill { display:inline-block; padding:2px 10px; border-radius:999px; border:1px solid var(--line);
          color:var(--muted); font-size:12px; }
</style>
</head>
<body>
<main>
  <div class="brand"><div class="dot"></div><span class="pill">engine running</span></div>
  <h1>Workflows you can trust.</h1>
  <p>The editor is not built into this binary. Run <code>make ui</code> (or use a release image) and rebuild.</p>
  <div class="card">
    <div class="row"><span>Health</span><a href="/healthz"><code>/healthz</code></a></div>
    <div class="row"><span>Version</span><a href="/api/version"><code>/api/version</code></a></div>
    <div class="row"><span>Workflows</span><a href="/api/workflows"><code>/api/workflows</code></a></div>
    <div class="row"><span>Run an example</span><code>loom run examples/daily-digest.flow.json</code></div>
    <div class="row"><span>Replay a run</span><code>loom replay &lt;runID&gt;</code></div>
  </div>
</main>
</body>
</html>
`
