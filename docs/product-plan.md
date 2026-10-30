# Loom Product Plan: Home, Templates, Jobs, and Runs

Status: planning approved; Phases 0–4 implemented. Phase 5 (polish and migration)
is in progress: dashboard metrics and run totals are implemented; the template
gallery polish, import/export UI, and workflow backfill remain.

## 1. Baseline before this plan

This section records the pre-plan editor so the rationale is clear; Phases 0–4
have since addressed the navigation, versioning, job and scheduling gaps below.

The editor was a single-page workflow studio with no persistent navigation.

- There is no Home or dashboard route.
- The Loom mark in the header is a static `<div>`, not a link or button
  (`web/src/App.tsx:974`).
- The app switches between onboarding and the editor based on whether a
  workflow is open. There is no `/templates`, `/jobs`, or `/runs` navigation.
- Onboarding is not Home. It only appears when no workflow is open.
- Workflows are mutable documents. Saving upserts the same workflow row
  (`internal/store/store.go:191`).
- Runs store a workflow version, but replay loads the current workflow
  (`internal/engine/engine.go:517`), so old runs are not guaranteed to replay
  the exact published flow.
- The pre-plan editor had no template, version, job, or instance layer.
- Jobs now carry their own manual, cron, one-time, batch, or webhook trigger.
  Startup migrates legacy workflow `trigger.cron` nodes into cron jobs; the
  scheduler still scans workflows as a fallback for templates without a cron
  job (`internal/server/scheduler.go`).
- The global runs dashboard, per-job schedules, one-time runs, and batch runs
  are implemented. Run history is no longer workflow-only.

## 2. Target model

Use four separate concepts.

| Concept | Meaning |
|---|---|
| **Template** | The reusable flow design. Has a name, description, tags, and versions. |
| **Template Version** | An immutable published snapshot. `v1`, `v2`, etc. A job always pins one version. |
| **Job** | A configured instance of a template version: use case, input defaults, trigger, schedule, enabled/paused state. |
| **Run** | One execution of a job. Batch jobs create a parent batch plus one child run per item. |

Lifecycle:

```text
Edit draft -> Validate -> Publish version -> Create job from version
-> Run manually / schedule / one-time / batch / webhook
-> Each execution is a Run
```

Editing a published template creates a new draft. Publishing creates `v2`.
Existing jobs stay on `v1` until explicitly upgraded.

### Naming decision

Use **Job** for the configured instance. Avoid calling the job itself an
"instance" in the UI; reserve "run" and "execution" for actual executions.

## 3. Navigation

Add real routes:

| Route | Screen |
|---|---|
| `/` | Home dashboard |
| `/templates` | Template gallery/list |
| `/templates/:id` | Read-only template overview and recent runs |
| `/templates/:id/edit` | Existing canvas editor |
| `/jobs` | Job instances |
| `/jobs/new?template=...` | Create job wizard |
| `/jobs/:id` | Job detail, schedule, runs (read-only settings) |
| `/jobs/:id/edit` | Edit job settings |
| `/runs` | Global run history |
| `/runs/:id` | Run detail/timeline |
| `/settings` | Tokens, credentials, retention |

Header changes:

- Make the Loom logo a link to `/`.
- Add a visible **Home** navigation item.
- Add **Templates**, **Jobs**, and **Runs** navigation.
- Keep the current workflow selector inside the editor, not as global
  navigation.
- Keep editor-only controls (undo/redo, save, run, publish, validation) inside
  the editor header; move credentials, theme, API token, and the command
  palette into a global Settings menu.
- Open templates and jobs read-only by default, with explicit **Edit** actions.
- Move full run logs to `/runs/:id`; keep only a compact live status bar in the
  editor.

Routing implementation options:

1. Add `react-router-dom` and route-aware layout.
2. Use a small local history router (`pushState` + `popstate`) with no new
   dependency.

The server already serves `index.html` for unknown paths, so clean URLs work.
Prefer option 1 unless adding the dependency is undesirable.

**Phase 0 decision:** option 2 was used to avoid a new dependency. Revisit when
job detail/settings layouts need nested or parameterized routes.

## 4. Home dashboard

The dashboard should answer "what is happening?".

- Template count, active jobs, runs today, success rate.
- Recent runs with status and duration.
- Failed runs needing attention.
- Upcoming scheduled/one-time jobs.
- Recently edited templates.
- Quick actions: **New template**, **Create job**, **Run now**.
- Empty state that links to the template gallery/examples.

The current onboarding examples become **system templates** in the template
gallery. "Use template" creates a user template or a job from a published
version.

## 5. Data model

Add tables rather than overloading `workflows`:

```text
templates
  id, name, description, tags, created_at, updated_at, archived

template_versions
  template_id, version, doc, content_hash, status,
  published_at, created_at
  PK (template_id, version)

jobs
  id, template_id, template_version,
  name, description, use_case, tags,
  trigger_type, trigger_config,
  input_defaults, enabled,
  timezone, next_run_at, last_run_at,
  concurrency_policy, created_at, updated_at

runs
  add job_id, batch_id, template_version,
      trigger_type, input_hash
```

`trigger_config` examples:

```json
{ "type": "manual" }
{ "type": "cron", "schedule": "0 9 * * 1-5", "timezone": "Asia/Kolkata" }
{ "type": "one_time", "run_at": "2026-11-01T09:00:00+05:30" }
{ "type": "webhook", "path": "refund-sync" }
{ "type": "batch", "input": "blob_or_upload", "concurrency": 3 }
```

For v1, keep **one trigger per job**. Multiple schedules per job can come later.

## 6. Backend API plan

### Templates

```text
GET    /api/templates
POST   /api/templates
GET    /api/templates/:id
PUT    /api/templates/:id/draft
POST   /api/templates/:id/publish
POST   /api/templates/:id/duplicate
POST   /api/templates/:id/archive
GET    /api/templates/:id/versions
```

### Jobs

```text
GET    /api/jobs
POST   /api/jobs
GET    /api/jobs/:id
PUT    /api/jobs/:id
POST   /api/jobs/:id/enable
POST   /api/jobs/:id/disable
POST   /api/jobs/:id/run
POST   /api/jobs/:id/batch
POST   /api/jobs/:id/upgrade
GET    /api/jobs/:id/runs
POST   /api/schedules/preview
```

### Runs and dashboard

```text
GET /api/stats
GET /api/runs?job=&template=&status=&trigger=&from=&to=
GET /api/runs/:id
POST /api/runs/:id/replay
POST /api/runs/:id/retry
```

## 7. Scheduling

Move scheduling from `trigger.cron` nodes to **jobs**.

- Manual: Run now.
- Cron: expression + timezone, next-run calculation.
- One-time: run at a timestamp, then auto-complete.
- Batch: upload/paste a JSON array or CSV; create a parent batch and one child
  run per item, with a concurrency limit.
- Webhook: per-job secret URL.
- Pause/resume without deleting history.
- Concurrency policy: allow, queue, or skip if the previous run is still active.
- Persist next run and reload after restart.
- Prevent duplicate scheduling during migration from legacy `trigger.cron`.

## 8. UI phases

### Phase 0 — App shell and Home

**Status: complete.** Implemented with the local history router. Existing
workflows are listed as templates. The Phase 1 template/version tables and a
minimal jobs bridge now back `/jobs`.

- Add routing and a route-aware layout.
- Make the Loom logo and Home navigation work.
- Build the Home dashboard.
- Build the Templates list and global Runs list.
- Keep the existing editor mostly unchanged.
- Add empty states and an unsaved-change guard.

Exit criteria:

- Clicking the logo or Home opens `/`.
- `/templates` lists existing workflows as templates.
- `/runs` lists runs across all workflows.
- Existing editor, run, replay, and approval flows still work.
- Playwright covers Home, logo navigation, template open, and global runs.

### Phase 1 — Template versioning

**Status: complete.** The editor shows published/draft state, publishes
validated snapshots, and exposes version history with node/edge/name diffs.
Jobs pin a published version; runs store the pinned snapshot and job metadata,
and replay/resume fall back to the snapshot instead of the latest draft.

- Draft/published states.
- Publish action with validation and version number.
- Immutable version snapshots.
- Jobs pin a version.
- Replay and resume use the run's exact template version.
- Version history and upgrade diff.

Exit criteria:

- Editing a published template creates a draft.
- Publishing creates a new immutable version.
- A job keeps running its pinned version.
- Replay uses the pinned version, not the latest draft.

### Phase 2 — Jobs/instances

**Status: complete.** Jobs deploy through a two-step wizard that pins any
published version and accepts JSON input defaults. The list shows status,
trigger, last run, and manual next run. The detail page edits settings, shows
job runs, and supports run-now, pause/resume, duplicate, and version upgrades.
Global runs can be filtered by job. Runs carry `job_id` and `template_version`.

- "Create job" wizard from a published version.
- Job list with enabled state, last run, next run, and trigger.
- Job detail page with config, runs, run-now, pause, and duplicate.
- Global run filters by job.

Exit criteria:

- A published template can be deployed as a job.
- Run now creates a run linked to the job.
- Pausing a job does not delete history.
- Jobs page shows status and last/next run.

### Phase 3 — Scheduling

**Status: complete.** Jobs can run on a cron schedule with an explicit
timezone, or once at a timestamp and then auto-pause. Next-run times are
returned by the API and shown in the jobs list and detail page. The scheduler
reloads enabled jobs from the store on startup and after every job change.
Workflow-level `trigger.cron` nodes also keep running for templates without a
cron job; the scheduler skips a workflow cron when an explicit cron job pins
that template, so a flow cannot be scheduled twice. Cron runs that overlap a
still-active run are skipped with a log entry, and the job form previews the
next three fire times via `/api/schedules/preview`.

- Cron and one-time scheduling per job.
- Timezone and next-run display.
- Scheduler reload on restart.
- Pause/resume.
- Keep legacy `trigger.cron` workflows running without double-scheduling when a
  job pins the same template.

Exit criteria:

- A cron job runs on schedule and shows the next run.
- A one-time job runs once and auto-completes.
- Restarting the server does not duplicate or lose schedules.

### Phase 4 — Batch

**Status: complete.** Jobs accept a pasted JSON array or CSV in the job detail
page. A batch creates one parent run and one child run per item, caps child
concurrency, shows aggregate progress and failures, and can retry the failed
items as a new batch.

- Batch input upload/paste.
- Parent batch plus child runs.
- Concurrency limit.
- Aggregate progress and failures.
- Retry failed items.

Exit criteria:

- A batch of N items creates N child runs under one parent.
- Concurrency is capped.
- Batch progress and failures are visible and retryable.

### Phase 5 — Polish and migration

**Status: in progress.** Home now shows workspace run totals, active/waiting
counts, success rate, and recent runs from `/api/stats`; credentials have a
dedicated page. Templates and jobs now open read-only with explicit Edit
actions, and runs have a dedicated `/runs/:id` log page. Template gallery
polish, import/export UI, and backfilling existing workflows into
templates/jobs are still open.

- Dashboard metrics.
- Template gallery with system examples.
- Import/export.
- Documentation and E2E coverage.
- Backfill existing workflows into templates and jobs.

## 9. Migration strategy

1. Add new tables without removing `workflows`.
2. Backfill each existing workflow into a template with version `1`.
3. Create a manual job for each existing workflow so current behavior remains
   available.
4. **Done:** migrate `trigger.cron` nodes into cron jobs at startup, then skip
   the legacy scheduler entry when an explicit cron job owns that template.
   The fallback remains for workflows created after startup.
5. Keep old APIs temporarily; move the UI to the new APIs.
6. Store template snapshots for all future published versions. Existing
   historical runs cannot be perfectly reconstructed beyond the current
   document, so document that limitation.
7. Replay must resolve `run.template_version`, never the latest template.

## 10. Risks and decisions

- **Immutability:** published versions must be stored as full snapshots; a
  version number alone is not enough.
- **Migration:** existing cron workflows must not be scheduled twice during
  cutover.
- **Batch scale:** cap batch size, child concurrency, and stored events.
- **Timezones:** use IANA timezones and define DST/misfire behavior explicitly.
- **Unsaved edits:** route changes from the editor need a confirmation guard.
- **Auth:** job webhooks need per-job secrets; the existing approval capability
  URL pattern is a good reference.
- **Terminology:** "Template" is the design; "Job" is the configured instance;
  "Run" is one execution.

## 11. Recommended order

1. Phase 0: Home, routing, templates list, global runs.
2. Phase 1: immutable template versions.
3. Phase 2: jobs/instances.
4. Phase 3: scheduling.
5. Phase 4: batch.
6. Phase 5: polish and migration.

Phase 0 first fixes the immediate navigation gap and creates the shell for the
template/job model. Phase 1 must precede scheduling so "fix once and run
instances from it" is actually guaranteed.
