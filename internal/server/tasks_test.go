package server_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

func approvalInboxWorkflow(id string) map[string]any {
	return map[string]any{
		"id": id, "name": "Approval " + id, "version": 1,
		"nodes": []any{
			map[string]any{"id": "start", "type": "trigger.manual", "typeVersion": 1, "name": "Start", "position": map[string]any{"x": 0, "y": 0}, "config": map[string]any{}},
			map[string]any{"id": "approve", "type": "flow.approval", "typeVersion": 1, "name": "Approve", "position": map[string]any{"x": 260, "y": 0}, "config": map[string]any{"message": "ship it?"}},
			map[string]any{"id": "yes", "type": "data.set", "typeVersion": 1, "name": "Yes", "position": map[string]any{"x": 520, "y": 0}, "config": map[string]any{"values": map[string]any{"ok": true}}},
		},
		"edges": []any{
			map[string]any{"from": map[string]any{"node": "start", "port": "main"}, "to": map[string]any{"node": "approve", "port": "main"}},
			map[string]any{"from": map[string]any{"node": "approve", "port": "approved"}, "to": map[string]any{"node": "yes", "port": "main"}},
		},
	}
}

// TestRunsStatusFilterAndApprovalNote covers the Tasks inbox backend: the
// status filter on the runs list (with rejection of unknown statuses) and
// the decision note recorded on the run.resumed event.
func TestRunsStatusFilterAndApprovalNote(t *testing.T) {
	srv, _ := newTestServer(t, "")

	if status, body := call(t, srv, http.MethodPost, "/api/workflows", "", approvalInboxWorkflow("approval-wf")); status != http.StatusCreated {
		t.Fatalf("create approval workflow: %d %s", status, body)
	}

	if status, _ := call(t, srv, http.MethodGet, "/api/runs?status=bogus", "", nil); status != http.StatusBadRequest {
		t.Fatalf("bogus status = %d, want 400", status)
	}

	status, data := call(t, srv, http.MethodPost, "/api/workflows/approval-wf/runs", "", map[string]any{"input": map[string]any{}})
	if status != http.StatusOK {
		t.Fatalf("start run: %d %s", status, data)
	}
	var started struct {
		RunID  string `json:"runId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &started); err != nil {
		t.Fatalf("start response: %v (%s)", err, data)
	}
	if started.Status != "waiting" {
		t.Fatalf("status = %q, want waiting", started.Status)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs?status=waiting&limit=10", "", nil)
	if status != http.StatusOK {
		t.Fatalf("list waiting: %d", status)
	}
	var list struct {
		Runs []map[string]any `json:"runs"`
	}
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("list decode: %v", err)
	}
	found := false
	for _, r := range list.Runs {
		if r["id"] == started.RunID {
			found = true
		}
	}
	if !found {
		t.Fatalf("waiting run %s not listed (%d runs)", started.RunID, len(list.Runs))
	}

	status, data = call(t, srv, http.MethodPost, "/api/runs/"+started.RunID+"/resume", "", map[string]any{"decision": "approved", "note": "  looks good  "})
	if status != http.StatusOK {
		t.Fatalf("resume: %d %s", status, data)
	}

	status, data = call(t, srv, http.MethodGet, "/api/runs/"+started.RunID+"/events?format=json", "", nil)
	if status != http.StatusOK {
		t.Fatalf("events: %d", status)
	}
	var evs []map[string]any
	if err := json.Unmarshal(data, &evs); err != nil {
		var wrapped struct {
			Events []map[string]any `json:"events"`
		}
		if err2 := json.Unmarshal(data, &wrapped); err2 != nil {
			t.Fatalf("events decode: %v / %v", err, err2)
		}
		evs = wrapped.Events
	}
	resumed := false
	for _, ev := range evs {
		if ev["type"] == "run.resumed" {
			resumed = true
			dataMap, _ := ev["data"].(map[string]any)
			if dataMap["decision"] != "approved" {
				t.Fatalf("decision = %v, want approved", dataMap["decision"])
			}
			if dataMap["note"] != "looks good" {
				t.Fatalf("note = %q, want trimmed 'looks good'", dataMap["note"])
			}
			if actor, _ := dataMap["actor"].(string); actor == "" || actor == "unknown" {
				t.Fatalf("decision actor = %q, want a resolved principal", dataMap["actor"])
			}
			if _, ok := dataMap["decidedAt"].(string); !ok {
				t.Fatalf("decidedAt missing from run.resumed data")
			}
		}
	}
	if !resumed {
		t.Fatal("run.resumed event missing")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		status, data = call(t, srv, http.MethodGet, "/api/runs/"+started.RunID, "", nil)
		var run map[string]any
		_ = json.Unmarshal(data, &run)
		if status == http.StatusOK && run["status"] == "done" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run never finished: %v", run["status"])
		}
		time.Sleep(50 * time.Millisecond)
	}
}
