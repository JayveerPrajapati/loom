package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"loom/internal/engine"
	"loom/internal/store"
)

type Deps struct {
	Version string
	Store   *store.Store
	Engine  *engine.Engine
	Token   string
	Reload  func() error
}

func Handler(deps Deps) http.Handler {
	s := &api{deps: deps, webhookSem: make(chan struct{}, 256)}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := deps.Store.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable", "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("GET /metrics", s.prometheusMetrics)
	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"name": "loom", "version": deps.Version})
	})

	mux.HandleFunc("GET /api/stats", s.auth(s.stats))
	mux.HandleFunc("GET /api/node-types", s.auth(s.nodeTypes))
	mux.HandleFunc("GET /api/examples", s.auth(s.listExamples))
	mux.HandleFunc("GET /api/credentials", s.auth(s.listCredentials))
	mux.HandleFunc("POST /api/credentials", s.auth(s.createCredential))
	mux.HandleFunc("DELETE /api/credentials/{id}", s.auth(s.deleteCredential))

	mux.HandleFunc("GET /api/workflows", s.auth(s.listWorkflows))
	mux.HandleFunc("POST /api/workflows", s.auth(s.createWorkflow))
	mux.HandleFunc("GET /api/workflows/{id}", s.auth(s.getWorkflow))
	mux.HandleFunc("GET /api/workflows/{id}/state", s.auth(s.workflowState))
	mux.HandleFunc("GET /api/workflows/{id}/versions", s.auth(s.listWorkflowVersions))
	mux.HandleFunc("GET /api/workflows/{id}/versions/{version}", s.auth(s.getWorkflowVersion))
	mux.HandleFunc("POST /api/workflows/{id}/publish", s.auth(s.publishWorkflow))
	mux.HandleFunc("GET /api/workflows/{id}/runbook", s.auth(s.workflowRunbook))
	mux.HandleFunc("PUT /api/workflows/{id}", s.auth(s.putWorkflow))
	mux.HandleFunc("DELETE /api/workflows/{id}", s.auth(s.deleteWorkflow))
	mux.HandleFunc("POST /api/workflows/{id}/runs", s.auth(s.startRun))
	mux.HandleFunc("POST /api/workflows/{id}/preview", s.auth(s.previewExpression))
	mux.HandleFunc("GET /api/jobs", s.auth(s.listJobs))
	mux.HandleFunc("POST /api/jobs", s.auth(s.createJob))
	mux.HandleFunc("POST /api/schedules/preview", s.auth(s.previewSchedule))
	mux.HandleFunc("GET /api/jobs/{id}", s.auth(s.getJob))
	mux.HandleFunc("PUT /api/jobs/{id}", s.auth(s.updateJob))
	mux.HandleFunc("DELETE /api/jobs/{id}", s.auth(s.deleteJob))
	mux.HandleFunc("POST /api/jobs/{id}/duplicate", s.auth(s.duplicateJob))
	mux.HandleFunc("POST /api/jobs/{id}/run", s.auth(s.runJob))
	mux.HandleFunc("POST /api/jobs/{id}/batch", s.auth(s.startBatch))
	mux.HandleFunc("POST /api/jobs/{id}/enabled", s.auth(s.setJobEnabled))
	mux.HandleFunc("POST /api/jobs/{id}/upgrade", s.auth(s.upgradeJob))

	mux.HandleFunc("GET /api/runs", s.auth(s.listRuns))
	mux.HandleFunc("GET /api/runs/{id}", s.auth(s.getRun))
	mux.HandleFunc("GET /api/runs/{id}/workflow", s.auth(s.runWorkflow))
	mux.HandleFunc("GET /api/runs/{id}/events", s.auth(s.streamEvents))
	mux.HandleFunc("GET /api/runs/{id}/data-context", s.auth(s.runDataContext))
	mux.HandleFunc("GET /api/runs/{id}/diff", s.auth(s.diffRuns))
	mux.HandleFunc("GET /api/blobs/{hash}", s.auth(s.getBlob))
	mux.HandleFunc("POST /api/runs/{id}/replay", s.auth(s.replayRun))
	mux.HandleFunc("POST /api/runs/{id}/retry", s.auth(s.retryRun))
	mux.HandleFunc("POST /api/runs/{id}/cancel", s.auth(s.cancelRun))
	mux.HandleFunc("POST /api/runs/{id}/resume", s.auth(s.resumeRun))

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
  <div class="brand"><div class="dot"></div><span class="pill">M1 engine</span></div>
  <h1>Workflows you can trust.</h1>
  <p>Typed edges, deterministic replay, one binary. The editor lands in M2.</p>
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
