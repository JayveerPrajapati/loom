package server

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

type Metrics struct {
	httpRequestsTotal   atomic.Uint64
	httpErrorsTotal     atomic.Uint64
	activeRequests      atomic.Int64
	workflowsRunsTotal  atomic.Uint64
	workflowErrorsTotal atomic.Uint64
	sseNotifWakes       atomic.Uint64
	ssePollWakes        atomic.Uint64
	sseActiveStreams    atomic.Int64
	startTime           time.Time
}

var globalMetrics = &Metrics{
	startTime: time.Now(),
}

// IncRequests increments total HTTP requests.
func IncRequests() {
	globalMetrics.httpRequestsTotal.Add(1)
	globalMetrics.activeRequests.Add(1)
}

// DecRequests decrements active requests.
func DecRequests(status int) {
	globalMetrics.activeRequests.Add(-1)
	if status >= 400 {
		globalMetrics.httpErrorsTotal.Add(1)
	}
}

// IncWorkflowRun tracks executed runs.
func IncWorkflowRun(failed bool) {
	globalMetrics.workflowsRunsTotal.Add(1)
	if failed {
		globalMetrics.workflowErrorsTotal.Add(1)
	}
}

// IncStreamWake counts one SSE stream wakeup, attributed to a run-change
// notification or to the fallback poll. The ratio shows whether streams run
// notification-driven; a growing poll share means wakeups are being missed.
func IncStreamWake(notified bool) {
	if notified {
		globalMetrics.sseNotifWakes.Add(1)
	} else {
		globalMetrics.ssePollWakes.Add(1)
	}
}

// IncStreams / DecStreams track how many SSE streams are currently open.
func IncStreams() { globalMetrics.sseActiveStreams.Add(1) }
func DecStreams() { globalMetrics.sseActiveStreams.Add(-1) }

func (s *api) prometheusMetrics(w http.ResponseWriter, _ *http.Request) {
	uptimeSeconds := time.Since(globalMetrics.startTime).Seconds()

	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	fmt.Fprintf(w, "# HELP loom_uptime_seconds Process uptime in seconds.\n")
	fmt.Fprintf(w, "# TYPE loom_uptime_seconds gauge\n")
	fmt.Fprintf(w, "loom_uptime_seconds %.2f\n\n", uptimeSeconds)

	fmt.Fprintf(w, "# HELP loom_http_requests_total Total HTTP requests handled.\n")
	fmt.Fprintf(w, "# TYPE loom_http_requests_total counter\n")
	fmt.Fprintf(w, "loom_http_requests_total %d\n\n", globalMetrics.httpRequestsTotal.Load())

	fmt.Fprintf(w, "# HELP loom_http_errors_total Total HTTP 4xx/5xx responses.\n")
	fmt.Fprintf(w, "# TYPE loom_http_errors_total counter\n")
	fmt.Fprintf(w, "loom_http_errors_total %d\n\n", globalMetrics.httpErrorsTotal.Load())

	fmt.Fprintf(w, "# HELP loom_http_active_requests Currently in-flight HTTP requests.\n")
	fmt.Fprintf(w, "# TYPE loom_http_active_requests gauge\n")
	fmt.Fprintf(w, "loom_http_active_requests %d\n\n", globalMetrics.activeRequests.Load())

	fmt.Fprintf(w, "# HELP loom_workflow_runs_total Total workflow runs initiated.\n")
	fmt.Fprintf(w, "# TYPE loom_workflow_runs_total counter\n")
	fmt.Fprintf(w, "loom_workflow_runs_total %d\n\n", globalMetrics.workflowsRunsTotal.Load())

	fmt.Fprintf(w, "# HELP loom_workflow_errors_total Total workflow runs failed.\n")
	fmt.Fprintf(w, "# TYPE loom_workflow_errors_total counter\n")
	fmt.Fprintf(w, "loom_workflow_errors_total %d\n\n", globalMetrics.workflowErrorsTotal.Load())

	fmt.Fprintf(w, "# HELP loom_sse_active_streams Currently open run event streams.\n")
	fmt.Fprintf(w, "# TYPE loom_sse_active_streams gauge\n")
	fmt.Fprintf(w, "loom_sse_active_streams %d\n\n", globalMetrics.sseActiveStreams.Load())

	fmt.Fprintf(w, "# HELP loom_sse_notification_wakes Total stream wakeups from run change notifications.\n")
	fmt.Fprintf(w, "# TYPE loom_sse_notification_wakes counter\n")
	fmt.Fprintf(w, "loom_sse_notification_wakes %d\n\n", globalMetrics.sseNotifWakes.Load())

	fmt.Fprintf(w, "# HELP loom_sse_poll_wakes Total stream wakeups from the fallback poll.\n")
	fmt.Fprintf(w, "# TYPE loom_sse_poll_wakes counter\n")
	fmt.Fprintf(w, "loom_sse_poll_wakes %d\n", globalMetrics.ssePollWakes.Load())

	fmt.Fprintf(w, "# HELP loom_db_read_pool_size Configured read-only database connection pool size.\n")
	fmt.Fprintf(w, "# TYPE loom_db_read_pool_size gauge\n")
	fmt.Fprintf(w, "loom_db_read_pool_size %d\n", s.deps.Store.ReadPoolSize())

	fmt.Fprintf(w, "# HELP loom_db_busy_errors_total Read queries that still hit SQLITE_BUSY-class errors after the busy timeout. Should stay near zero.\n")
	fmt.Fprintf(w, "# TYPE loom_db_busy_errors_total counter\n")
	fmt.Fprintf(w, "loom_db_busy_errors_total %d\n", s.deps.Store.BusyErrors())
}
