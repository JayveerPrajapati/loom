import { useEffect, useState } from 'react';
import { api } from '../../api';
import type {
  Job,
  Run,
  Workflow,
  WorkflowSummary,
  WorkflowVersion,
} from '../../types';
import {
  BatchRunRow,
  COMMON_TIMEZONES,
  CRON_PRESETS,
  SchedulePreviewPanel,
  WorkflowDiffBadges,
  cronPresetFor,
  duration,
  formatTime,
  localTimezone,
  nextRunLabel,
  parseJSONObject,
  schedulePreviewConfig,
  toDateTimeLocal,
  triggerLabel,
} from './shared';

interface JobDetailProps {
  jobId: string;
  summaries: WorkflowSummary[];
  editing: boolean;
  onBack: () => void;
  onEdit: () => void;
  onDone: () => void;
  onOpenTemplate: (id: string) => void;
  onOpenRun: (runId: string) => void;
  onOpenJob: (id: string) => void;
}

export function JobDetailPage({
  jobId,
  summaries,
  editing,
  onBack,
  onEdit,
  onDone,
  onOpenTemplate,
  onOpenRun,
  onOpenJob,
}: JobDetailProps) {
  const [job, setJob] = useState<Job | null>(null);
  const [versions, setVersions] = useState<WorkflowVersion[]>([]);
  const [templateWorkflow, setTemplateWorkflow] = useState<Workflow | null>(null);
  const [latestWorkflow, setLatestWorkflow] = useState<Workflow | null>(null);
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const [name, setName] = useState('');
  const [inputText, setInputText] = useState('{}');
  const [triggerType, setTriggerType] = useState('manual');
  const [schedule, setSchedule] = useState('0 9 * * *');
  const [cronPreset, setCronPreset] = useState(cronPresetFor('0 9 * * *'));
  const [timezone, setTimezone] = useState(localTimezone());
  const [runAt, setRunAt] = useState('');
  const [maxConcurrency, setMaxConcurrency] = useState('');
  const [showBatch, setShowBatch] = useState(false);
  const [batchText, setBatchText] = useState('');
  const [batchConcurrency, setBatchConcurrency] = useState(3);
  const [batchBusy, setBatchBusy] = useState(false);
  const [batchError, setBatchError] = useState('');

  const load = async () => {
    setLoading(true);
    setError('');
    try {
      const nextJob = await api.getJob(jobId);
      const [runResult, versionResult, templateResult] = await Promise.all([
        api.listJobRuns(jobId, 50),
        api.listWorkflowVersions(nextJob.templateId),
        api
          .getWorkflowVersion(nextJob.templateId, nextJob.templateVersion)
          .catch(() => api.getWorkflow(nextJob.templateId))
          .catch(() => null),
      ]);
      setJob(nextJob);
      setRuns(runResult.runs ?? []);
      setVersions(versionResult.versions ?? []);
      setTemplateWorkflow(templateResult);
      const latestVersion = Math.max(
        nextJob.templateVersion,
        ...(versionResult.versions ?? []).map((item) => item.version),
      );
      setLatestWorkflow(
        latestVersion > nextJob.templateVersion
          ? await api
              .getWorkflowVersion(nextJob.templateId, latestVersion)
              .catch(() => null)
          : templateResult,
      );
      setName(nextJob.name);
      setInputText(JSON.stringify(nextJob.inputDefaults ?? {}, null, 2));
      setTriggerType(nextJob.triggerType || 'manual');
      const nextSchedule = String(nextJob.triggerConfig?.schedule ?? '0 9 * * *');
      setSchedule(nextSchedule);
      setCronPreset(cronPresetFor(nextSchedule));
      setTimezone(String(nextJob.triggerConfig?.timezone ?? localTimezone()));
      setRunAt(toDateTimeLocal(String(nextJob.triggerConfig?.runAt ?? '')));
      setMaxConcurrency(String(nextJob.triggerConfig?.maxConcurrency ?? ''));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, [editing, jobId]);

  const run = async () => {
    if (!job) return;
    setBusy('run');
    setError('');
    try {
      const started = await api.runJob(job.id);
      onOpenRun(started.runId);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const startBatch = async () => {
    if (!job) return;
    const text = batchText.trim();
    if (!text) {
      setBatchError('Paste a JSON array or CSV with a header row.');
      return;
    }
    let payload: { items?: unknown[]; csv?: string; concurrency: number };
    if (text.startsWith('[')) {
      try {
        const parsed: unknown = JSON.parse(text);
        if (!Array.isArray(parsed) || parsed.length === 0) {
          throw new Error('Expected a non-empty JSON array.');
        }
        payload = { items: parsed, concurrency: batchConcurrency };
      } catch (reason) {
        setBatchError(reason instanceof Error ? reason.message : String(reason));
        return;
      }
    } else {
      payload = { csv: text, concurrency: batchConcurrency };
    }
    setBatchBusy(true);
    setBatchError('');
    try {
      await api.startBatch(job.id, payload);
      setShowBatch(false);
      setBatchText('');
      await load();
    } catch (reason) {
      setBatchError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBatchBusy(false);
    }
  };

  const toggle = async () => {
    if (!job) return;
    setBusy('toggle');
    setError('');
    try {
      setJob(await api.setJobEnabled(job.id, !job.enabled));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const duplicate = async () => {
    if (!job) return;
    setBusy('duplicate');
    setError('');
    try {
      const copy = await api.duplicateJob(job.id);
      onOpenJob(copy.id);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const remove = async () => {
    if (!job) return;
    if (!window.confirm(`Delete “${job.name}”? Its run history is kept.`)) return;
    setBusy('delete');
    setError('');
    try {
      await api.deleteJob(job.id);
      onBack();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const upgrade = async (version: number) => {
    if (!job) return;
    setBusy('upgrade');
    setError('');
    try {
      await api.upgradeJob(job.id, version);
      await load();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const save = async () => {
    if (!job) return;
    let inputDefaults: Record<string, unknown>;
    try {
      inputDefaults = parseJSONObject(inputText);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      return;
    }
    let triggerConfig: Record<string, unknown> | undefined;
    if (triggerType === 'cron') {
      if (!schedule.trim()) {
        setError('Enter a cron schedule.');
        return;
      }
      triggerConfig = { schedule: schedule.trim(), timezone: timezone.trim() || 'UTC' };
    } else if (triggerType === 'once') {
      if (!runAt) {
        setError('Choose a date and time for the one-time run.');
        return;
      }
      triggerConfig = { runAt: new Date(runAt).toISOString() };
    }
    const concurrency = maxConcurrency.trim() ? Number(maxConcurrency) : 0;
    if (concurrency && (!Number.isInteger(concurrency) || concurrency < 1)) {
      setError('Max concurrency must be a positive whole number.');
      return;
    }
    if (concurrency > 0) {
      triggerConfig = { ...(triggerConfig ?? {}), maxConcurrency: concurrency };
    }
    setBusy('save');
    setError('');
    try {
      const updated = await api.updateJob(job.id, {
        name: name.trim() || job.name,
        inputDefaults,
        triggerType,
        triggerConfig,
      });
      setJob(updated);
      setName(updated.name);
      setTriggerType(updated.triggerType || 'manual');
      const updatedSchedule = String(updated.triggerConfig?.schedule ?? '0 9 * * *');
      setSchedule(updatedSchedule);
      setCronPreset(cronPresetFor(updatedSchedule));
      setTimezone(String(updated.triggerConfig?.timezone ?? localTimezone()));
      setRunAt(toDateTimeLocal(String(updated.triggerConfig?.runAt ?? '')));
      setMaxConcurrency(String(updated.triggerConfig?.maxConcurrency ?? ''));
      onDone();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  if (loading) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <p className="muted pad">Loading job…</p>
      </main>
    );
  }

  if (!job) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <header className="page-head">
          <div>
            <p className="eyebrow">Job</p>
            <h1>Job not found</h1>
          </div>
          <button className="button subtle" onClick={onBack}>Back to jobs</button>
        </header>
        {error && <p className="banner error">{error}</p>}
      </main>
    );
  }

  const template = summaries.find((summary) => summary.id === job.templateId);
  const latest = versions[0]?.version ?? template?.publishedVersion ?? job.templateVersion;
  const doneRuns = runs.filter((item) => item.status === 'done').length;
  const failedRuns = runs.filter((item) => item.status === 'failed').length;
  const activeRuns = runs.filter((item) =>
    ['queued', 'running', 'waiting'].includes(item.status),
  ).length;
  const previewConfig = schedulePreviewConfig(triggerType, schedule, timezone, runAt);

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head job-detail-head">
        <div>
          <button className="breadcrumb-button" onClick={onBack}>← Jobs</button>
          <p className="eyebrow">Job</p>
          <h1>{job.name}</h1>
          <p className="lede">
            {job.templateName || job.templateId} · pinned to v{job.templateVersion} · {triggerLabel(job.triggerType)} trigger
          </p>
        </div>
        <div className="job-actions">
          {editing ? (
            <>
              <button className="button subtle" onClick={onDone} disabled={Boolean(busy)}>
                Cancel
              </button>
              <button className="button primary" onClick={() => void save()} disabled={Boolean(busy)}>
                {busy === 'save' ? 'Saving…' : 'Save settings'}
              </button>
            </>
          ) : (
            <>
              <button className="button subtle" onClick={() => void toggle()} disabled={Boolean(busy)}>
                {job.enabled ? 'Pause' : 'Resume'}
              </button>
              <button
                className="button subtle"
                onClick={() => {
                  setBatchError('');
                  setShowBatch(true);
                }}
                disabled={Boolean(busy) || !job.enabled}
              >
                Run batch
              </button>
              <button className="button primary" onClick={() => void run()} disabled={Boolean(busy) || !job.enabled}>
                {busy === 'run' ? 'Starting…' : 'Run now'}
              </button>
              <button className="button subtle" onClick={onEdit} disabled={Boolean(busy)}>
                Edit settings
              </button>
              <details className="action-menu">
                <summary className="button subtle">More</summary>
                <div className="action-menu-list">
                  <button onClick={() => void duplicate()} disabled={Boolean(busy)}>
                    Duplicate
                  </button>
                  <button className="danger" onClick={() => void remove()} disabled={Boolean(busy)}>
                    Delete
                  </button>
                </div>
              </details>
            </>
          )}
        </div>
      </header>

      {error && <p className="banner error">{error}</p>}

      <section className="job-summary-strip" aria-label="Job summary">
        <div>
          <span>Status</span>
          <strong>{job.enabled ? 'Active' : 'Paused'}</strong>
        </div>
        <div>
          <span>Runs</span>
          <strong>{runs.length}</strong>
        </div>
        <div>
          <span>Completed</span>
          <strong>{doneRuns}</strong>
        </div>
        <div>
          <span>Failed</span>
          <strong>{failedRuns}</strong>
        </div>
        <div>
          <span>Active</span>
          <strong>{activeRuns}</strong>
        </div>
      </section>

      <div className="job-detail-grid">
        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Configuration</p>
              <h2>Job settings</h2>
            </div>
            <span className={`job-state ${job.enabled ? '' : 'paused'}`}>
              {job.enabled ? 'Active' : 'Paused'}
            </span>
          </header>
          <div className="field-stack">
            <label className="field" htmlFor="detail-job-name">
              <span className="field-label">Name</span>
              <input
                id="detail-job-name"
                className="text-input"
                value={name}
                disabled={!editing}
                onChange={(event) => setName(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="detail-job-trigger">
              <span className="field-label">Trigger</span>
              <select
                id="detail-job-trigger"
                className="text-input"
                value={triggerType}
                disabled={!editing}
                onChange={(event) => setTriggerType(event.target.value)}
              >
                <option value="manual">Manual</option>
                <option value="cron">Schedule (cron)</option>
                <option value="once">One time</option>
              </select>
            </label>
            {triggerType === 'cron' && (
              <>
                <label className="field" htmlFor="detail-job-cron-preset">
                  <span className="field-label">Schedule preset</span>
                  <select
                    id="detail-job-cron-preset"
                    className="text-input"
                    value={cronPreset}
                    disabled={!editing}
                    onChange={(event) => {
                      const nextPreset = event.target.value;
                      setCronPreset(nextPreset);
                      const preset = CRON_PRESETS.find((item) => item.id === nextPreset);
                      if (preset?.expression) setSchedule(preset.expression);
                    }}
                  >
                    {CRON_PRESETS.map((preset) => (
                      <option key={preset.id} value={preset.id}>{preset.label}</option>
                    ))}
                  </select>
                </label>
                <label className="field" htmlFor="detail-job-schedule">
                  <span className="field-label">Cron schedule</span>
                  <input
                    id="detail-job-schedule"
                    className="text-input"
                    value={schedule}
                    disabled={!editing}
                    onChange={(event) => {
                      const value = event.target.value;
                      setSchedule(value);
                      setCronPreset(cronPresetFor(value));
                    }}
                    placeholder="0 9 * * *"
                    spellCheck={false}
                  />
                  <span className="field-hint">
                    Five fields: minute hour day-of-month month day-of-week. Use <code>*/10</code> for every 10 minutes.
                  </span>
                </label>
                <label className="field" htmlFor="detail-job-timezone">
                  <span className="field-label">Timezone</span>
                  <input
                    id="detail-job-timezone"
                    className="text-input"
                    list="detail-job-timezone-options"
                    value={timezone}
                    disabled={!editing}
                    onChange={(event) => setTimezone(event.target.value)}
                    placeholder="UTC"
                    spellCheck={false}
                  />
                  <datalist id="detail-job-timezone-options">
                    {COMMON_TIMEZONES.map((zone) => (
                      <option key={zone} value={zone} />
                    ))}
                  </datalist>
                </label>
              </>
            )}
            {triggerType === 'once' && (
              <label className="field" htmlFor="detail-job-run-at">
                <span className="field-label">Run at</span>
                <input
                  id="detail-job-run-at"
                  className="text-input"
                  type="datetime-local"
                  value={runAt}
                  disabled={!editing}
                  onChange={(event) => setRunAt(event.target.value)}
                />
              </label>
            )}
            <SchedulePreviewPanel triggerType={triggerType} config={previewConfig} />
            <label className="field" htmlFor="detail-job-max-concurrency">
              <span className="field-label">Max concurrency</span>
              <input
                id="detail-job-max-concurrency"
                className="text-input"
                type="number"
                min={1}
                value={maxConcurrency}
                disabled={!editing}
                onChange={(event) => setMaxConcurrency(event.target.value)}
                placeholder="Engine default"
              />
            </label>
            <label className="field" htmlFor="detail-job-input">
              <span className="field-label">Input defaults (JSON)</span>
              <textarea
                id="detail-job-input"
                className="text-input code-input"
                value={inputText}
                disabled={!editing}
                onChange={(event) => setInputText(event.target.value)}
                rows={8}
                spellCheck={false}
              />
            </label>
            <div className="job-inline-actions">
              <span className="muted small">Trigger: {triggerLabel(job.triggerType)}</span>
            </div>
          </div>
        </section>

        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Deployment</p>
              <h2>Pinned version</h2>
            </div>
          </header>
          <dl className="job-facts">
            <div>
              <dt>Template</dt>
              <dd>
                <button className="text-link" onClick={() => onOpenTemplate(job.templateId)}>
                  {job.templateName || job.templateId}
                </button>
              </dd>
            </div>
            <div>
              <dt>Version</dt>
              <dd>v{job.templateVersion}</dd>
            </div>
            <div>
              <dt>Last run</dt>
              <dd>{job.lastRunAt ? formatTime(job.lastRunAt) : 'Never'}</dd>
            </div>
            <div>
              <dt>Next run</dt>
              <dd>{nextRunLabel(job)}</dd>
            </div>
            <div>
              <dt>Created</dt>
              <dd>{formatTime(job.createdAt)}</dd>
            </div>
          </dl>
          {latest > job.templateVersion && (
            <>
              <button className="button subtle full" onClick={() => void upgrade(latest)} disabled={Boolean(busy)}>
                Upgrade to v{latest}
              </button>
              {templateWorkflow && latestWorkflow && (
                <WorkflowDiffBadges from={templateWorkflow} to={latestWorkflow} />
              )}
            </>
          )}
          {templateWorkflow && (
            <div className="job-template-steps">
              <div className="job-template-steps-head">
                <h3>Template steps</h3>
                <span className="muted small">
                  {templateWorkflow.nodes.length} node{templateWorkflow.nodes.length === 1 ? '' : 's'}
                </span>
              </div>
              <ol>
                {templateWorkflow.nodes.map((node) => (
                  <li key={node.id}>
                    <span className="step-index" aria-hidden="true" />
                    <span>
                      <strong>{node.name || node.id}</strong>
                      <em>{node.type}</em>
                    </span>
                  </li>
                ))}
              </ol>
            </div>
          )}
        </section>
      </div>

      <section className="dashboard-panel job-runs-panel">
        <header className="dashboard-panel-head">
          <div>
            <p className="eyebrow">History</p>
            <h2>Runs from this job</h2>
          </div>
          <span className="muted small">{runs.length} runs</span>
        </header>
        {runs.length === 0 ? (
          <p className="muted small pad">No runs yet. Use Run now to create the first one.</p>
        ) : (
          <div className="runs-table" role="list">
            {runs.map((item) =>
              item.trigger === 'batch' ? (
                <BatchRunRow
                  key={item.id}
                  run={item}
                  onOpenRun={onOpenRun}
                  onRetried={() => void load()}
                />
              ) : (
                <button
                  className="runs-row"
                  key={item.id}
                  role="listitem"
                  onClick={() => onOpenRun(item.id)}
                >
                  <span className={`status-dot status-${item.status}`} aria-hidden="true" />
                  <span className="runs-row-main">
                    <strong>{item.status}</strong>
                    <span>{formatTime(item.startedAt)} · v{item.templateVersion ?? job.templateVersion}</span>
                  </span>
                  <span className="runs-row-meta">
                    <code>{item.id.split('_').pop()}</code>
                    <em>{duration(item)}</em>
                  </span>
                </button>
              ),
            )}
          </div>
        )}
      </section>

      {showBatch && (
        <div className="batch-modal-backdrop" role="presentation">
          <section
            className="batch-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="batch-modal-title"
          >
            <header className="batch-modal-head">
              <div>
                <p className="eyebrow">Batch</p>
                <h2 id="batch-modal-title">Run {job.name} over many items</h2>
              </div>
              <button className="button subtle" onClick={() => setShowBatch(false)}>
                Close
              </button>
            </header>
            <p className="muted small">
              Paste a JSON array of objects, or CSV with a header row. Each item runs as a child
              run; job input defaults are merged into every item.
            </p>
            <label className="field" htmlFor="batch-file">
              <span className="field-label">Upload JSON or CSV</span>
              <input
                id="batch-file"
                className="text-input"
                type="file"
                accept=".json,.csv,application/json,text/csv"
                onChange={(event) => {
                  const file = event.target.files?.[0];
                  if (!file) return;
                  file
                    .text()
                    .then((text) => {
                      setBatchText(text);
                      setBatchError('');
                    })
                    .catch((reason) => setBatchError(reason instanceof Error ? reason.message : String(reason)));
                }}
              />
            </label>
            <label className="field" htmlFor="batch-text">
              <span className="field-label">Items</span>
              <textarea
                id="batch-text"
                className="text-input code-input"
                value={batchText}
                onChange={(event) => setBatchText(event.target.value)}
                rows={10}
                spellCheck={false}
                placeholder={'[{"name":"Ada"},{"name":"Grace"}]\n\nor\n\nname,city\nAda,London\nGrace,Paris'}
              />
            </label>
            <label className="field batch-concurrency" htmlFor="batch-concurrency">
              <span className="field-label">Concurrency</span>
              <input
                id="batch-concurrency"
                className="text-input"
                type="number"
                min={1}
                max={20}
                value={batchConcurrency}
                onChange={(event) => setBatchConcurrency(Number(event.target.value) || 1)}
              />
              <span className="muted small">At most 20 items run at once.</span>
            </label>
            {batchError && <p className="banner error">{batchError}</p>}
            <footer className="batch-modal-actions">
              <button className="button subtle" onClick={() => setShowBatch(false)}>
                Cancel
              </button>
              <button className="button primary" onClick={() => void startBatch()} disabled={batchBusy}>
                {batchBusy ? 'Starting…' : 'Start batch'}
              </button>
            </footer>
          </section>
        </div>
      )}
    </main>
  );
}
