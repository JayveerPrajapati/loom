package engine_test

import (
	"context"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

func waitWorkflowDurable() *graph.Workflow {
	return &graph.Workflow{
		ID: "durable-wait", Name: "Durable wait", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "wait", Type: "flow.wait", TypeVersion: 1, Name: "Wait", Config: map[string]any{"seconds": 1.0}},
			{ID: "done", Type: "data.set", TypeVersion: 1, Name: "Done", Config: map[string]any{
				"values": map[string]any{"woke": true},
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "wait", Port: "main"}},
			{From: graph.Endpoint{Node: "wait", Port: "main"}, To: graph.Endpoint{Node: "done", Port: "main"}},
		},
	}
}

func TestDurableWaitParksAndResumes(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	eng := engine.New(st, host.NewSim())
	eng.DurableWaits = true

	res, err := eng.Start(context.Background(), waitWorkflowDurable(), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if res.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", res.Status)
	}
	run, err := st.GetRun(res.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "waiting" || run.ResumeNode != "wait" {
		t.Fatalf("run = %s at %q, want waiting at wait", run.Status, run.ResumeNode)
	}
	wakeAt, ok, err := st.RunWakeAt(res.RunID)
	if err != nil || !ok {
		t.Fatalf("wake time missing: ok=%v err=%v", ok, err)
	}
	if time.Until(wakeAt) <= 0 {
		t.Fatalf("wake time %s is already due", wakeAt)
	}
	if _, err := eng.ResumeTimer(context.Background(), res.RunID); err == nil {
		t.Fatal("resume before the deadline should fail")
	}
	if due, err := st.DueWakeups(time.Now()); err != nil || len(due) != 0 {
		t.Fatalf("due wakeups before deadline = %v (err=%v)", due, err)
	}

	time.Sleep(1100 * time.Millisecond)
	if resumed := eng.ResumeDue(context.Background()); resumed != 1 {
		t.Fatalf("resumed %d runs, want 1", resumed)
	}
	run, err = st.GetRun(res.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "done" {
		t.Fatalf("run = %s (%s), want done", run.Status, run.Error)
	}

	events, err := st.ListEvents(res.RunID)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var resumed, finished bool
	for _, ev := range events {
		if ev.Type == "run.resumed" {
			resumed = true
			if ev.Data["reason"] != "timer" {
				t.Fatalf("resume reason = %v, want timer", ev.Data["reason"])
			}
		}
		if ev.Type == "run.finished" {
			finished = true
		}
	}
	if !resumed || !finished {
		t.Fatalf("resumed=%v finished=%v, want both", resumed, finished)
	}
}

func TestDurableWaitSurvivesRestart(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	first := engine.New(st, host.NewSim())
	first.DurableWaits = true
	res, err := first.Start(context.Background(), waitWorkflowDurable(), engine.Options{
		Trigger: "manual", Input: flow.Item{},
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	// A new engine over the same store represents a process restart: the
	// suspended run lives entirely in the database.
	second := engine.New(st, host.NewSim())
	second.DurableWaits = true
	time.Sleep(1100 * time.Millisecond)
	if resumed := second.ResumeDue(context.Background()); resumed != 1 {
		t.Fatalf("resumed %d runs after restart, want 1", resumed)
	}
	run, err := st.GetRun(res.RunID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != "done" {
		t.Fatalf("run = %s (%s), want done", run.Status, run.Error)
	}
}
