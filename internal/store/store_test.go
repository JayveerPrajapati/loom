package store_test

import (
	"encoding/json"
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
