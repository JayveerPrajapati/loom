package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"loom/internal/engine"
	"loom/internal/flow"
	"loom/internal/graph"
	"loom/internal/schedule"
	"loom/internal/store"
)

type Scheduler struct {
	ctx    context.Context
	cron   *cron.Cron
	store  *store.Store
	engine *engine.Engine

	mu      sync.Mutex
	ids     []cron.EntryID
	timers  []*time.Timer
	running sync.Map
}

func NewScheduler(ctx context.Context, st *store.Store, eng *engine.Engine) *Scheduler {
	return &Scheduler{
		ctx:    ctx,
		cron:   cron.New(),
		store:  st,
		engine: eng,
	}
}

func (s *Scheduler) Start() {
	s.cron.Start()
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, timer := range s.timers {
		timer.Stop()
	}
	s.timers = nil
}

func (s *Scheduler) Reload() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, id := range s.ids {
		s.cron.Remove(id)
	}
	s.ids = s.ids[:0]
	for _, timer := range s.timers {
		timer.Stop()
	}
	s.timers = nil

	jobs, err := s.store.ListJobs()
	if err != nil {
		return err
	}
	cronTemplates := map[string]bool{}
	for i := range jobs {
		job := jobs[i]
		if job.TriggerType == "cron" {
			// An explicit cron job owns the template's schedule; the
			// workflow-level trigger is a shortcut, not a second schedule.
			// This applies even while the job is paused so the workflow
			// trigger cannot silently resume it.
			cronTemplates[job.TemplateID] = true
		}
		if !job.Enabled {
			continue
		}
		switch job.TriggerType {
		case "cron":
			s.addCron(job)
		case "once":
			s.addOnce(job)
		}
	}
	s.addWorkflowCrons(cronTemplates)
	return nil
}

// addWorkflowCrons schedules legacy workflow-level trigger.cron nodes. This
// runs on every Reload, so editing a workflow's schedule takes effect without
// a restart. Templates with an explicit cron job are skipped above.
func (s *Scheduler) addWorkflowCrons(scheduled map[string]bool) {
	summaries, err := s.store.ListWorkflows()
	if err != nil {
		log.Printf("scheduler: list workflows: %v", err)
		return
	}
	for _, summary := range summaries {
		if scheduled[summary.ID] {
			continue
		}
		wf, err := s.store.GetWorkflow(summary.ID)
		if err != nil {
			continue
		}
		var expr, zone string
		for _, node := range wf.Nodes {
			if node.Type != "trigger.cron" {
				continue
			}
			expr, _ = node.Config["schedule"].(string)
			zone, _ = node.Config["timezone"].(string)
			if strings.TrimSpace(expr) != "" {
				break
			}
		}
		if strings.TrimSpace(expr) == "" {
			continue
		}
		config := map[string]any{"schedule": expr}
		if strings.TrimSpace(zone) != "" {
			config["timezone"] = zone
		}
		cronExpr := schedule.Expression(config)
		wfCopy := wf
		entry, err := s.cron.AddFunc(cronExpr, func() {
			key := "workflow:" + wfCopy.ID
			if _, loaded := s.running.LoadOrStore(key, struct{}{}); loaded {
				log.Printf("scheduler: workflow %s: previous run still active, skipping", wfCopy.ID)
				return
			}
			defer s.running.Delete(key)
			s.runWorkflow(wfCopy, "cron")
		})
		if err != nil {
			log.Printf("scheduler: workflow %s: invalid schedule %q: %v", wf.ID, cronExpr, err)
			continue
		}
		log.Printf("scheduler: workflow %s: scheduled %q", wf.ID, cronExpr)
		s.ids = append(s.ids, entry)
	}
}

func (s *Scheduler) runWorkflow(wf *graph.Workflow, trigger string) {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()
	if _, err := s.engine.Start(ctx, wf, engine.Options{Trigger: trigger, Snapshot: wf}); err != nil {
		log.Printf("scheduler: workflow %s: %v", wf.ID, err)
	}
}

func (s *Scheduler) addCron(job store.Job) {
	expr := schedule.Expression(job.TriggerConfig)
	if expr == "" {
		log.Printf("scheduler: job %s: empty cron schedule", job.ID)
		return
	}
	jobCopy := job
	entry, err := s.cron.AddFunc(expr, func() {
		if _, loaded := s.running.LoadOrStore(jobCopy.ID, struct{}{}); loaded {
			log.Printf("scheduler: job %s: previous run still active, skipping", jobCopy.ID)
			return
		}
		defer s.running.Delete(jobCopy.ID)
		if err := s.run(jobCopy, "cron"); err != nil {
			log.Printf("scheduler: job %s: %v", jobCopy.ID, err)
		}
	})
	if err != nil {
		log.Printf("scheduler: job %s: invalid schedule %q: %v", job.ID, expr, err)
		return
	}
	log.Printf("scheduler: job %s: scheduled %q", job.ID, expr)
	s.ids = append(s.ids, entry)

	if job.LastRunAt != nil && !job.LastRunAt.IsZero() {
		now := time.Now()
		if nextAfterLast, ok, err := schedule.Next(job.TriggerType, job.TriggerConfig, *job.LastRunAt); err == nil && ok {
			if nextAfterLast.Before(now) && now.Sub(nextAfterLast) < 2*time.Hour {
				log.Printf("scheduler: job %s: recovered missed run scheduled for %s", job.ID, nextAfterLast.Format(time.RFC3339))
				go func(j store.Job) {
					if _, loaded := s.running.LoadOrStore(j.ID, struct{}{}); loaded {
						return
					}
					defer s.running.Delete(j.ID)
					if err := s.run(j, "cron-missed"); err != nil {
						log.Printf("scheduler: job %s: missed-run recovery failed: %v", j.ID, err)
					}
				}(jobCopy)
			}
		}
	}
}

func (s *Scheduler) addOnce(job store.Job) {
	next, ok, err := schedule.Next(job.TriggerType, job.TriggerConfig, time.Now())
	if err != nil {
		log.Printf("scheduler: job %s: %v", job.ID, err)
		return
	}
	if !ok {
		return
	}
	delay := time.Until(next)
	if delay < 0 {
		delay = 0
	}
	jobCopy := job
	s.timers = append(s.timers, time.AfterFunc(delay, func() {
		// Disable only after the run starts. Disabling first loses the run
		// forever if the process crashes before the engine records it.
		if err := s.run(jobCopy, "once"); err != nil {
			log.Printf("scheduler: job %s: one-time run failed: %v", jobCopy.ID, err)
			return
		}
		if err := s.store.SetJobEnabled(jobCopy.ID, false); err != nil {
			log.Printf("scheduler: job %s: disable after one-time fire: %v", jobCopy.ID, err)
		}
	}))
}

func (s *Scheduler) run(job store.Job, trigger string) error {
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
	defer cancel()

	wf, err := s.store.GetWorkflowVersion(job.TemplateID, job.TemplateVersion)
	if err != nil {
		return fmt.Errorf("load template v%d: %w", job.TemplateVersion, err)
	}
	input := flow.Item{}
	for key, value := range job.InputDefaults {
		input[key] = value
	}
	_, err = s.engine.Start(ctx, wf, engine.Options{
		Trigger:         trigger,
		Input:           input,
		Snapshot:        wf,
		JobID:           job.ID,
		TemplateVersion: job.TemplateVersion,
		MaxConcurrency:  jobConcurrency(job),
	})
	return err
}

// jobConcurrency reads the optional per-job node concurrency limit from the
// trigger config. Zero means "use the workflow/engine limit".
func jobConcurrency(job store.Job) int {
	switch v := job.TriggerConfig["maxConcurrency"].(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}
