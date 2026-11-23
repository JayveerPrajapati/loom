package server_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"loom/internal/store"
)

// openStream connects to the SSE endpoint and returns a scanner over its
// body plus a close func. The client has no timeout: the test decides when
// a line is late.
func openStream(t *testing.T, srvURL, runID string) (*bufio.Scanner, func()) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, srvURL+"/api/runs/"+runID+"/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	return sc, func() { _ = resp.Body.Close() }
}

// waitForLine reads until the predicate matches or the deadline passes.
func waitForLine(t *testing.T, sc *bufio.Scanner, within time.Duration, want func(line string) bool, what string) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if !sc.Scan() {
			t.Fatalf("stream closed while waiting for %s", what)
		}
		if want(sc.Text()) {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func isEventData(line string) bool { return strings.HasPrefix(line, "data: ") }

func newStreamRun(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := st.CreateRun(store.Run{
		ID: id, WorkflowID: "wf-stream", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// waitForEOF consumes the remaining stream body and reports when the server
// closed it. Takes exclusive ownership of the scanner.
func waitForEOF(sc *bufio.Scanner) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for sc.Scan() {
		}
	}()
	return done
}

// Events appended after the stream opens must arrive promptly — fast enough
// that only the notification path (not the 5s fallback) can explain it —
// and a terminal status must close the stream.
func TestSSEStreamDeliversEventsAndClosesOnTerminal(t *testing.T) {
	srv, st := newTestServer(t, "")
	newStreamRun(t, st, "run-sse-1")

	sc, closeStream := openStream(t, srv.URL, "run-sse-1")
	defer closeStream()

	if _, err := st.AppendEvent("run-sse-1", store.Event{Type: "node.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, sc, time.Second, func(line string) bool {
		return isEventData(line) && strings.Contains(line, `"node.started"`)
	}, "first event")

	if _, err := st.AppendEvent("run-sse-1", store.Event{Type: "node.finished", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	waitForLine(t, sc, time.Second, func(line string) bool {
		return isEventData(line) && strings.Contains(line, `"node.finished"`)
	}, "second event")

	// The stream must close by itself once the run is terminal.
	fin := time.Now()
	if err := st.UpdateRunStatus("run-sse-1", "done", "", &fin); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waitForEOF(sc):
	case <-time.After(2 * time.Second):
		t.Fatal("terminal run left the stream open")
	}
}

// An idle stream with no events and no notifications must stay silent: no
// database-driven traffic, and no keepalive before the 15s quiet threshold.
func TestSSEStreamIdleIsQuiet(t *testing.T) {
	srv, st := newTestServer(t, "")
	newStreamRun(t, st, "run-sse-4")

	sc, closeStream := openStream(t, srv.URL, "run-sse-4")
	defer closeStream()

	lines := make(chan string)
	go func() {
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("idle stream closed unexpectedly")
			}
			if strings.TrimSpace(line) == "" {
				continue // SSE frame separator
			}
			t.Fatalf("idle stream sent %q within 3s", line)
		case <-deadline:
			return
		}
	}
}

// A second store over the same data directory stands in for a second
// process: its mutations reach the database but its notifications cannot
// wake the first process's streams. The fallback poll must still deliver
// the events — notification loss must never strand data.
func TestSSEStreamFallbackPollCatchesMissedNotification(t *testing.T) {
	srv, st, dir := newTestServerAt(t, "")
	newStreamRun(t, st, "run-sse-2")
	other, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	defer other.Close()

	sc, closeStream := openStream(t, srv.URL, "run-sse-2")
	defer closeStream()

	if _, err := other.AppendEvent("run-sse-2", store.Event{Type: "node.started", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// The fallback poll is 5s; the event must appear well inside 10s.
	waitForLine(t, sc, 10*time.Second, func(line string) bool {
		return isEventData(line) && strings.Contains(line, `"node.started"`)
	}, "event written by another process via fallback poll")
}

// A run pruned away by a second process closes its stream: the fallback wake
// finds the run row gone and ends the response instead of streaming
// keepalives forever.
func TestSSEStreamClosesWhenRunPrunedByAnotherProcess(t *testing.T) {
	srv, st, dir := newTestServerAt(t, "")
	newStreamRun(t, st, "run-sse-3")
	other, err := store.Open(dir)
	if err != nil {
		t.Fatalf("open second store: %v", err)
	}
	defer other.Close()

	sc, closeStream := openStream(t, srv.URL, "run-sse-3")
	defer closeStream()

	// Terminal with an old finish time so the second process's prune takes
	// it: notifications from `other` never reach this server's stream.
	fin := time.Now().Add(-48 * time.Hour)
	if err := other.UpdateRunStatus("run-sse-3", "done", "", &fin); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Prune(time.Now().Add(-24 * time.Hour)); err != nil {
		t.Fatal(err)
	}

	// The stream must end once the fallback poll discovers the deletion.
	eof := waitForEOF(sc)
	select {
	case <-eof:
	case <-time.After(10 * time.Second):
		t.Fatal("pruned run left the stream open")
	}
}

// A stream outlives the login that opened it only until the session dies:
// logging out must close the stream even though nothing about the run
// itself changed (no notifications fire, so the fallback poll's auth
// recheck is what ends it).
func TestSSEStreamClosesOnSessionRevocation(t *testing.T) {
	srv, st, _ := newAuthServer(t)
	addUser(t, st, "sse@example.com", "admin", "a-password-123")
	session := loginAs(t, srv, "sse@example.com", "a-password-123")
	newStreamRun(t, st, "run-sse-5")

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/api/runs/run-sse-5/events", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, cookie := range session.cookies {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("stream status = %d", resp.StatusCode)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	if status, _ := session.do(t, srv, http.MethodPost, "/api/auth/logout", nil, true); status != http.StatusOK && status != http.StatusNoContent {
		t.Fatalf("logout status = %d", status)
	}

	eof := waitForEOF(sc)
	select {
	case <-eof:
	case <-time.After(7 * time.Second):
		t.Fatal("revoked session left the stream open")
	}
}

// TestSSEStreamAlwaysDeliversTerminalEvent guards the read-ordering fix in
// streamEvents: a stream opened while a run completes must always deliver
// run.finished before it closes. The engine flips the run row and appends
// the terminal event in one transaction; if the handler read the event log
// first and the run row second, the row could report terminal while the
// earlier event read had not yet seen run.finished — the stream then closed
// without delivering it and the browser stayed on "Running" forever.
// Each cycle mirrors what the editor UI does: start an async run, connect
// the stream immediately, and read to EOF. Repeated because the race window
// is the run's own execution (~tens of ms), which the connect reliably
// lands inside; the ordering fix makes every cycle deliver the event, the
// previous ordering dropped it in most cycles.
func TestSSEStreamAlwaysDeliversTerminalEvent(t *testing.T) {
	srv, _ := newTestServer(t, "")
	wf := linearWorkflow()
	wf.ID = "sse-terminal-race"
	status, data := call(t, srv, http.MethodPost, "/api/workflows", "", wf)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d: %s", status, data)
	}

	for cycle := 0; cycle < 25; cycle++ {
		status, data := call(t, srv, http.MethodPost, "/api/workflows/sse-terminal-race/runs?async=1", "", map[string]any{"input": map[string]any{}})
		if status != http.StatusAccepted {
			t.Fatalf("cycle %d: start status = %d: %s", cycle, status, data)
		}
		var started struct {
			RunID string `json:"runId"`
		}
		if err := json.Unmarshal(data, &started); err != nil {
			t.Fatalf("cycle %d: decode start: %v", cycle, err)
		}

		sc, closeStream := openStream(t, srv.URL, started.RunID)
		sawTerminal := false
		for sc.Scan() {
			if isEventData(sc.Text()) && strings.Contains(sc.Text(), `"run.finished"`) {
				sawTerminal = true
			}
		}
		closeStream()
		if !sawTerminal {
			t.Fatalf("cycle %d: stream closed on run %s without delivering run.finished", cycle, started.RunID)
		}
	}
}

// TestSSEStreamTenStreamsFanOutViaNotifications (P2-10): ten concurrent
// streams on ten runs must each deliver an appended event well inside the
// 5s fallback poll — sub-second delivery across the fan proves the
// notification path scales with streams without proportional polling.
func TestSSEStreamTenStreamsFanOutViaNotifications(t *testing.T) {
	srv, st := newTestServer(t, "")
	scanners := make([]*bufio.Scanner, 0, 10)
	closers := make([]func(), 0, 10)
	for i := 0; i < 10; i++ {
		id := fmt.Sprintf("run-fan-%02d", i)
		newStreamRun(t, st, id)
		sc, closeStream := openStream(t, srv.URL, id)
		scanners = append(scanners, sc)
		closers = append(closers, closeStream)
	}
	for _, closeStream := range closers {
		defer closeStream()
	}

	begin := time.Now()
	for i := 0; i < 10; i++ {
		if _, err := st.AppendEvent(fmt.Sprintf("run-fan-%02d", i), store.Event{Type: "node.started", At: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	for i, sc := range scanners {
		waitForLine(t, sc, time.Second, func(line string) bool {
			return isEventData(line) && strings.Contains(line, `"node.started"`)
		}, fmt.Sprintf("stream %d event", i))
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("fan-out to 10 streams took %s; the fallback poll (5s), not notifications, must be the delivery path", elapsed)
	}
}
