package store_test

import (
	"bytes"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"loom/internal/graph"
	"loom/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestCountRunsByStatusIgnoresListLimit(t *testing.T) {
	st := openStore(t)
	for i := 0; i < 3; i++ {
		run := store.Run{
			ID:              "run-count-" + string(rune('a'+i)),
			WorkflowID:      "wf-count",
			WorkflowVersion: "v1",
			Status:          "waiting",
			Trigger:         "manual",
			StartedAt:       time.Now().UTC(),
		}
		if err := st.CreateRun(run); err != nil {
			t.Fatalf("create run: %v", err)
		}
	}

	rows, err := st.ListRunsByStatus("waiting", 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}

	total, err := st.CountRunsByStatus("waiting")
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != 3 {
		t.Fatalf("count = %d, want 3 (the list limit must not cap it)", total)
	}
}

func TestLeaseOwnershipAndExpiry(t *testing.T) {
	st := openStore(t)

	ok, err := st.AcquireLease("scheduler", "instance-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire a: ok=%v err=%v", ok, err)
	}
	ok, err = st.AcquireLease("scheduler", "instance-b", time.Minute)
	if err != nil {
		t.Fatalf("acquire b: %v", err)
	}
	if ok {
		t.Fatal("instance-b acquired a live lease")
	}
	ok, err = st.AcquireLease("scheduler", "instance-a", time.Minute)
	if err != nil || !ok {
		t.Fatalf("renew a: ok=%v err=%v", ok, err)
	}
	if err := st.ReleaseLease("scheduler", "instance-a"); err != nil {
		t.Fatalf("release: %v", err)
	}
	ok, err = st.AcquireLease("scheduler", "instance-b", time.Minute)
	if err != nil || !ok {
		t.Fatalf("acquire after release: ok=%v err=%v", ok, err)
	}

	ok, err = st.AcquireLease("expired", "instance-a", -time.Second)
	if err != nil || !ok {
		t.Fatalf("expired acquire a: ok=%v err=%v", ok, err)
	}
	ok, err = st.AcquireLease("expired", "instance-b", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expired takeover: ok=%v err=%v", ok, err)
	}
}

func TestStaleRunsListsInFlightRuns(t *testing.T) {
	st := openStore(t)

	wf := &graph.Workflow{ID: "wf", Name: "WF", Version: 1, Nodes: []graph.Node{}}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}

	statuses := []string{"queued", "running", "waiting", "done"}
	for _, status := range statuses {
		err := st.CreateRun(store.Run{
			ID: "run-" + status, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: status, Trigger: "manual", StartedAt: time.Now(),
		})
		if err != nil {
			t.Fatalf("create %s run: %v", status, err)
		}
	}

	stale, err := st.StaleRuns()
	if err != nil {
		t.Fatalf("stale runs: %v", err)
	}
	if len(stale) != 2 {
		t.Fatalf("stale runs = %d, want 2", len(stale))
	}
	for _, run := range stale {
		if run.Status != "queued" && run.Status != "running" {
			t.Errorf("stale run %s has status %q", run.ID, run.Status)
		}
	}
	for _, status := range statuses {
		run, err := st.GetRun("run-" + status)
		if err != nil {
			t.Fatalf("get %s run: %v", status, err)
		}
		if run.Status != status {
			t.Errorf("%s run status = %q, want untouched %q", status, run.Status, status)
		}
	}
}

func TestEventsAreOrdered(t *testing.T) {
	st := openStore(t)
	if err := st.CreateRun(store.Run{
		ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "running", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	const n = 50
	for i := 0; i < n; i++ {
		if _, err := st.AppendEvent("run", store.Event{Type: "test", NodeID: "node"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	events, err := st.ListEvents("run")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != n {
		t.Fatalf("got %d events, want %d", len(events), n)
	}
	for i, ev := range events {
		if ev.Seq != int64(i+1) {
			t.Fatalf("event %d seq = %d, want %d", i, ev.Seq, i+1)
		}
	}
}

func TestPruneOldRunsAndOrphanBlobs(t *testing.T) {
	st := openStore(t)
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	recent := now

	for _, item := range []struct {
		id       string
		finished time.Time
	}{{"old", old}, {"recent", recent}} {
		if err := st.CreateRun(store.Run{
			ID: item.id, WorkflowID: "wf", WorkflowVersion: "v1",
			Status: "done", Trigger: "manual", StartedAt: item.finished,
		}); err != nil {
			t.Fatalf("create %s: %v", item.id, err)
		}
		if err := st.UpdateRunStatus(item.id, "done", "", &item.finished); err != nil {
			t.Fatalf("finish %s: %v", item.id, err)
		}
	}
	hash, err := st.SaveBlob([]byte(`{"old":true}`))
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	if _, err := st.AppendEvent("old", store.Event{Type: "node.finished", NodeID: "n", OutputHash: hash}); err != nil {
		t.Fatalf("append event: %v", err)
	}

	result, err := st.Prune(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if result.Runs != 1 {
		t.Fatalf("pruned %d runs, want 1", result.Runs)
	}
	if _, err := st.GetRun("old"); err == nil {
		t.Fatal("old run still exists")
	}
	if _, err := st.GetRun("recent"); err != nil {
		t.Fatalf("recent run removed: %v", err)
	}

	orphan, err := st.SaveBlob([]byte(`{"orphan":true}`))
	if err != nil {
		t.Fatalf("save orphan: %v", err)
	}
	result, err = st.Prune(now.Add(time.Hour))
	if err != nil {
		t.Fatalf("prune blobs: %v", err)
	}
	if result.Blobs == 0 {
		t.Fatal("no blobs pruned")
	}
	if _, err := st.LoadBlob(orphan); err == nil {
		t.Fatal("orphan blob still exists")
	}
	if _, err := st.LoadBlob(hash); err == nil {
		t.Fatal("old run blob still exists")
	}
}

func TestBlobsAndCredentials(t *testing.T) {
	st := openStore(t)

	hash, err := st.SaveBlob([]byte(`{"hello":"world"}`))
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	data, err := st.LoadBlob(hash)
	if err != nil {
		t.Fatalf("load blob: %v", err)
	}
	if string(data) != `{"hello":"world"}` {
		t.Fatalf("blob = %s", data)
	}

	plain, err := json.Marshal(map[string]any{"token": "secret", "region": "eu"})
	if err != nil {
		t.Fatalf("marshal credential: %v", err)
	}
	if err := st.SaveCredential("cred", "http", plain); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	typ, sealed, err := st.GetCredential("cred")
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}
	if typ != "http" {
		t.Fatalf("credential type = %q, want http", typ)
	}
	var got map[string]any
	if err := json.Unmarshal(sealed, &got); err != nil {
		t.Fatalf("decode credential: %v", err)
	}
	if got["token"] != "secret" || got["region"] != "eu" {
		t.Fatalf("credential round-trip = %#v", got)
	}
}

func TestResumeWaitingIsAtomic(t *testing.T) {
	st := openStore(t)
	wf := &graph.Workflow{ID: "wf", Name: "WF", Version: 1, Nodes: []graph.Node{}}
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatalf("save workflow: %v", err)
	}
	if err := st.CreateRun(store.Run{
		ID: "run", WorkflowID: "wf", WorkflowVersion: "v1",
		Status: "waiting", Trigger: "manual", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			ok, err := st.ResumeWaiting("run")
			if err != nil {
				t.Errorf("resume: %v", err)
				return
			}
			if ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("resume claims = %d, want 1", wins)
	}
}

func TestBlobStoresRawAndDisplayRedacts(t *testing.T) {
	st := openStore(t)
	raw := []byte(`{"author":"Ada","password":"hunter2","tokenCount":3}`)

	hash, err := st.SaveBlob(raw)
	if err != nil {
		t.Fatalf("save blob: %v", err)
	}
	got, err := st.LoadBlob(hash)
	if err != nil {
		t.Fatalf("load blob: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("raw blob = %s, want %s", got, raw)
	}

	display, err := st.LoadBlobForDisplay(hash)
	if err != nil {
		t.Fatalf("load display blob: %v", err)
	}
	var view map[string]any
	if err := json.Unmarshal(display, &view); err != nil {
		t.Fatalf("decode display blob: %v", err)
	}
	if view["author"] != "Ada" || view["tokenCount"] != float64(3) {
		t.Fatalf("display over-redacted: %#v", view)
	}
	if view["password"] != "[redacted]" {
		t.Fatalf("display password = %v, want [redacted]", view["password"])
	}
}

func TestSensitiveKeyMatcherAvoidsFalsePositives(t *testing.T) {
	cases := map[string]bool{
		"author":               false,
		"authorName":           false,
		"tokenCount":           false,
		"authenticationStatus": false,
		"secretary":            false,
		"password":             true,
		"api_key":              true,
		"apiKey":               true,
		"Authorization":        true,
		"clientSecret":         true,
		"accessToken":          true,
		"private_key":          true,
	}
	for key, want := range cases {
		out, ok := store.RedactValue(map[string]any{key: "v"}).(map[string]any)
		if !ok {
			t.Fatalf("%s: unexpected redaction type", key)
		}
		got := out[key] == "[redacted]"
		if got != want {
			t.Errorf("isSensitiveKey(%q) = %v, want %v", key, got, want)
		}
	}
}
