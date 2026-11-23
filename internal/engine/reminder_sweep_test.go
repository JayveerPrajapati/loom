package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/graph"
	"loom/internal/host"
	"loom/internal/store"
)

// The reminder sweep calls notifyApproval with a rebuilt exec state; it must
// carry the persisted effects so a replay run never notifies.
func TestReminderSweepSkipsReplayRuns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	live := &effectCountingHost{Host: host.NewSim()}
	eng := engine.New(st, live)

	wf := &graph.Workflow{
		ID: "reminders", Name: "Reminders", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{
				"message": "ok?", "notifyUrl": "https://notify.example.com/hook", "reminderMinutes": 1.0,
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
		},
	}
	snapshot, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"requestedAt":     time.Now().Add(-time.Hour).UTC().Format(time.RFC3339),
		"reminderMinutes": 1.0,
		"message":         "ok?",
	})
	if err := st.CreateRun(store.Run{
		ID: "run-replay", WorkflowID: wf.ID, WorkflowVersion: "1",
		Status: "waiting", Trigger: "replay", StartedAt: time.Now(),
		Snapshot: snapshot, Effects: "replay",
	}); err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := st.SetRunWaitingUntil("run-replay", "approve", "token", payload, time.Time{}, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("set waiting: %v", err)
	}

	eng.SweepApprovalReminders(context.Background())
	time.Sleep(150 * time.Millisecond)
	if live.callCount() != 0 {
		t.Fatalf("reminder sweep notified for a replay run (%d calls)", live.callCount())
	}
}

// An expired approval link cancels the waiting run exactly once; a live link
// leaves the run waiting.
func TestExpiredApprovalLinksCancelWaitingRuns(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()
	eng := engine.New(st, host.NewSim())

	wf := &graph.Workflow{
		ID: "expiry", Name: "Expiry", Version: 1,
		Nodes: []graph.Node{
			{ID: "start", Type: "trigger.manual", TypeVersion: 1, Name: "Start", Config: map[string]any{}},
			{ID: "approve", Type: "flow.approval", TypeVersion: 1, Name: "Approve", Config: map[string]any{
				"message": "ok?",
			}},
		},
		Edges: []graph.Edge{
			{From: graph.Endpoint{Node: "start", Port: "main"}, To: graph.Endpoint{Node: "approve", Port: "main"}},
		},
	}
	snapshot, err := json.Marshal(wf)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	now := time.Now().UTC()
	payload, _ := json.Marshal(map[string]any{
		"requestedAt": now.Add(-2 * time.Hour).Format(time.RFC3339),
		"message":     "ok?",
	})
	cases := []struct {
		id      string
		expires time.Time
	}{
		{"run-expired", now.Add(-time.Hour)},
		{"run-live", now.Add(time.Hour)},
	}
	for _, tc := range cases {
		if err := st.CreateRun(store.Run{
			ID: tc.id, WorkflowID: wf.ID, WorkflowVersion: "1",
			Status: "waiting", Trigger: "manual", StartedAt: now.Add(-2 * time.Hour),
			Snapshot: snapshot,
		}); err != nil {
			t.Fatalf("create %s: %v", tc.id, err)
		}
		if err := st.SetRunWaitingUntil(tc.id, "approve", "token-"+tc.id, payload, time.Time{}, tc.expires); err != nil {
			t.Fatalf("set waiting %s: %v", tc.id, err)
		}
	}

	eng.SweepApprovalReminders(context.Background())
	eng.SweepApprovalReminders(context.Background())

	expired, err := st.GetRun("run-expired")
	if err != nil {
		t.Fatalf("get expired: %v", err)
	}
	if expired.Status != "cancelled" {
		t.Fatalf("expired run status = %q, want cancelled", expired.Status)
	}
	if expired.Error != "approval link expired" {
		t.Fatalf("expired run error = %q, want approval link expired", expired.Error)
	}
	live, err := st.GetRun("run-live")
	if err != nil {
		t.Fatalf("get live: %v", err)
	}
	if live.Status != "waiting" {
		t.Fatalf("live run status = %q, want waiting", live.Status)
	}

	events, err := st.ListEvents("run-expired")
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	cancelled := 0
	for _, ev := range events {
		if ev.Type == "run.cancelled" {
			cancelled++
		}
	}
	if cancelled != 1 {
		t.Fatalf("run.cancelled events = %d, want exactly 1", cancelled)
	}
}
