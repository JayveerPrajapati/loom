package server

import (
	"fmt"
	"log"

	"loom/internal/flow"
	"loom/internal/store"
)

// MigrateCronWorkflows turns legacy workflow-level trigger.cron nodes into
// scheduled jobs. The job scheduler no longer runs workflow cron triggers
// directly, so migrating once is what prevents a migrated workflow from
// running twice.
func MigrateCronWorkflows(st *store.Store) (int, error) {
	summaries, err := st.ListWorkflows()
	if err != nil {
		return 0, err
	}
	jobs, err := st.ListJobs()
	if err != nil {
		return 0, err
	}
	scheduled := map[string]bool{}
	for _, job := range jobs {
		if job.TriggerType == "cron" {
			scheduled[job.TemplateID] = true
		}
	}

	migrated := 0
	for _, summary := range summaries {
		if scheduled[summary.ID] {
			continue
		}
		wf, err := st.GetWorkflow(summary.ID)
		if err != nil {
			log.Printf("cron migration: load %s: %v", summary.ID, err)
			continue
		}
		var scheduleExpr, timezone string
		for _, node := range wf.Nodes {
			if node.Type != "trigger.cron" {
				continue
			}
			scheduleExpr, _ = node.Config["schedule"].(string)
			timezone, _ = node.Config["timezone"].(string)
			if scheduleExpr != "" {
				break
			}
		}
		if scheduleExpr == "" {
			continue
		}

		version := summary.PublishedVersion
		if version == 0 {
			published, err := st.PublishWorkflow(wf)
			if err != nil {
				log.Printf("cron migration: publish %s: %v", summary.ID, err)
				continue
			}
			version = published.Version
		} else if _, err := st.GetWorkflowVersion(summary.ID, version); err != nil {
			log.Printf("cron migration: load %s v%d: %v", summary.ID, version, err)
			continue
		}

		config := map[string]any{"schedule": scheduleExpr}
		if timezone != "" {
			config["timezone"] = timezone
		}
		job := &store.Job{
			ID:              flow.NewID("job"),
			Name:            wf.Name + " schedule",
			TemplateID:      summary.ID,
			TemplateVersion: version,
			Enabled:         true,
			TriggerType:     "cron",
			TriggerConfig:   config,
		}
		if err := st.CreateJob(job); err != nil {
			return migrated, fmt.Errorf("create migrated job for %s: %w", summary.ID, err)
		}
		migrated++
	}
	return migrated, nil
}
