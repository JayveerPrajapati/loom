package server_test

import (
	"context"
	"testing"
	"time"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/host"
	"loom/internal/server"
	"loom/internal/store"
)

func TestSchedulerRunsCronJob(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	wf := linearWorkflow()
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	version, err := st.PublishWorkflow(wf)
	if err != nil {
		t.Fatal(err)
	}
	job := &store.Job{
		ID: flow.NewID("job"), Name: "cron", TemplateID: wf.ID,
		TemplateVersion: version.Version, Enabled: true, TriggerType: "cron",
		TriggerConfig: map[string]any{"schedule": "@every 1s"},
	}
	if err := st.CreateJob(job); err != nil {
		t.Fatal(err)
	}

	scheduler := server.NewScheduler(context.Background(), st, engine.New(st, host.NewSim()))
	if err := scheduler.Reload(); err != nil {
		t.Fatal(err)
	}
	scheduler.Start()
	defer scheduler.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for {
		runs, err := st.ListRunsByJob(job.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(runs) > 0 {
			if runs[0].Trigger != "cron" {
				t.Fatalf("run trigger = %q, want cron", runs[0].Trigger)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("cron job did not run before deadline")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSchedulerRunsOneTimeJobAndPausesIt(t *testing.T) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	wf := linearWorkflow()
	if err := st.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	version, err := st.PublishWorkflow(wf)
	if err != nil {
		t.Fatal(err)
	}

	job := &store.Job{
		ID: flow.NewID("job"), Name: "one time", TemplateID: wf.ID,
		TemplateVersion: version.Version, Enabled: true, TriggerType: "once",
		TriggerConfig: map[string]any{"runAt": time.Now().Add(100 * time.Millisecond).Format(time.RFC3339)},
	}
	if err := st.CreateJob(job); err != nil {
		t.Fatal(err)
	}

	scheduler := server.NewScheduler(context.Background(), st, engine.New(st, host.NewSim()))
	if err := scheduler.Reload(); err != nil {
		t.Fatal(err)
	}
	scheduler.Start()
	defer scheduler.Stop()

	deadline := time.Now().Add(5 * time.Second)
	for {
		current, err := st.GetJob(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		runs, err := st.ListRunsByJob(job.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if !current.Enabled && len(runs) == 1 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job enabled=%v runs=%d after deadline", current.Enabled, len(runs))
		}
		time.Sleep(25 * time.Millisecond)
	}
}
