import { useEffect, useState } from 'react';
import { api } from '../../api';
import type { WorkflowSummary, WorkflowVersion } from '../../types';
import {
  COMMON_TIMEZONES,
  CRON_PRESETS,
  SchedulePreviewPanel,
  cronPresetFor,
  localTimezone,
  parseJSONObject,
  schedulePreviewConfig,
} from './shared';

interface CreateJobProps {
  summaries: WorkflowSummary[];
  onCreated: (id: string) => void;
  onCancel: () => void;
  onOpenTemplates: () => void;
}

export function CreateJobPage({ summaries, onCreated, onCancel, onOpenTemplates }: CreateJobProps) {
  const published = summaries.filter((summary) => summary.publishedVersion > 0);
  const [templateId, setTemplateId] = useState(published[0]?.id ?? '');
  const [versions, setVersions] = useState<WorkflowVersion[]>([]);
  const [version, setVersion] = useState(0);
  const [name, setName] = useState('');
  const [inputText, setInputText] = useState('{}');
  const [triggerType, setTriggerType] = useState<'manual' | 'cron' | 'once'>('manual');
  const [schedule, setSchedule] = useState('0 9 * * *');
  const [cronPreset, setCronPreset] = useState(cronPresetFor('0 9 * * *'));
  const [timezone, setTimezone] = useState(localTimezone());
  const [runAt, setRunAt] = useState('');
  const [maxConcurrency, setMaxConcurrency] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!templateId && published[0]) setTemplateId(published[0].id);
  }, [published, templateId]);

  useEffect(() => {
    if (!templateId) {
      setVersions([]);
      setVersion(0);
      return;
    }
    let cancelled = false;
    api
      .listWorkflowVersions(templateId)
      .then((result) => {
        if (cancelled) return;
        const list = result.versions ?? [];
        setVersions(list);
        setVersion(list[0]?.version ?? 0);
      })
      .catch((reason) => {
        if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => {
      cancelled = true;
    };
  }, [templateId]);

  const template = published.find((summary) => summary.id === templateId);
  const previewConfig = schedulePreviewConfig(triggerType, schedule, timezone, runAt);

  const create = async () => {
    if (!template || version <= 0) {
      setError('Choose a published template version.');
      return;
    }
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
    setBusy(true);
    setError('');
    try {
      const job = await api.createJob({
        name: name.trim() || `${template.name} v${version}`,
        templateId: template.id,
        templateVersion: version,
        inputDefaults,
        triggerType,
        triggerConfig,
      });
      onCreated(job.id);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  if (published.length === 0) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <header className="page-head">
          <div>
            <p className="eyebrow">Deploy</p>
            <h1>Create a job</h1>
            <p className="lede">A job needs a published template version.</p>
          </div>
        </header>
        <div className="empty-panel">
          <h2>No published templates yet</h2>
          <p className="muted">Publish a template before deploying a job.</p>
          <button className="button primary" onClick={onOpenTemplates}>Browse templates</button>
        </div>
      </main>
    );
  }

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <p className="eyebrow">Deploy</p>
          <h1>Create a job</h1>
          <p className="lede">Pin a published version, then run it manually, on a schedule, or once.</p>
        </div>
        <button className="button subtle" onClick={onCancel}>Cancel</button>
      </header>

      {error && <p className="banner error">{error}</p>}

      <div className="job-create-layout">
        <section className="dashboard-panel job-form">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Step 1</p>
              <h2>Template version</h2>
            </div>
          </header>
          <div className="field-stack">
            <label className="field" htmlFor="job-template">
              <span className="field-label">Published template</span>
              <select
                id="job-template"
                className="text-input"
                value={templateId}
                onChange={(event) => setTemplateId(event.target.value)}
              >
                {published.map((summary) => (
                  <option key={summary.id} value={summary.id}>
                    {summary.name} · v{summary.publishedVersion}
                  </option>
                ))}
              </select>
            </label>
            <label className="field" htmlFor="job-version">
              <span className="field-label">Version to pin</span>
              <select
                id="job-version"
                className="text-input"
                value={version}
                onChange={(event) => setVersion(Number(event.target.value))}
              >
                {versions.map((item) => (
                  <option key={item.version} value={item.version}>
                    v{item.version} · {new Date(item.publishedAt).toLocaleString()}
                  </option>
                ))}
              </select>
            </label>
            <p className="muted small">
              This job keeps running v{version || '—'} until you upgrade it.
            </p>
          </div>
        </section>

        <section className="dashboard-panel job-form">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Step 2</p>
              <h2>Configuration</h2>
            </div>
          </header>
          <div className="field-stack">
            <label className="field" htmlFor="job-name">
              <span className="field-label">Job name</span>
              <input
                id="job-name"
                className="text-input"
                value={name}
                onChange={(event) => setName(event.target.value)}
                placeholder={template ? `${template.name} v${version}` : 'Daily refund sync'}
              />
            </label>
            <label className="field" htmlFor="job-trigger">
              <span className="field-label">Trigger</span>
              <select
                id="job-trigger"
                className="text-input"
                value={triggerType}
                onChange={(event) => setTriggerType(event.target.value as 'manual' | 'cron' | 'once')}
              >
                <option value="manual">Manual</option>
                <option value="cron">Schedule (cron)</option>
                <option value="once">One time</option>
              </select>
            </label>
            {triggerType === 'cron' && (
              <>
                <label className="field" htmlFor="job-cron-preset">
                  <span className="field-label">Schedule preset</span>
                  <select
                    id="job-cron-preset"
                    className="text-input"
                    value={cronPreset}
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
                <label className="field" htmlFor="job-schedule">
                  <span className="field-label">Cron schedule</span>
                  <input
                    id="job-schedule"
                    className="text-input"
                    value={schedule}
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
                <label className="field" htmlFor="job-timezone">
                  <span className="field-label">Timezone</span>
                  <input
                    id="job-timezone"
                    className="text-input"
                    list="job-timezone-options"
                    value={timezone}
                    onChange={(event) => setTimezone(event.target.value)}
                    placeholder="UTC"
                    spellCheck={false}
                  />
                  <datalist id="job-timezone-options">
                    {COMMON_TIMEZONES.map((zone) => (
                      <option key={zone} value={zone} />
                    ))}
                  </datalist>
                </label>
              </>
            )}
            {triggerType === 'once' && (
              <label className="field" htmlFor="job-run-at">
                <span className="field-label">Run at</span>
                <input
                  id="job-run-at"
                  className="text-input"
                  type="datetime-local"
                  value={runAt}
                  onChange={(event) => setRunAt(event.target.value)}
                />
              </label>
            )}
            <SchedulePreviewPanel triggerType={triggerType} config={previewConfig} />
            <label className="field" htmlFor="job-max-concurrency">
              <span className="field-label">Max concurrency</span>
              <input
                id="job-max-concurrency"
                className="text-input"
                type="number"
                min={1}
                value={maxConcurrency}
                onChange={(event) => setMaxConcurrency(event.target.value)}
                placeholder="Engine default"
              />
              <span className="field-hint">Maximum nodes running at once for runs of this job.</span>
            </label>
            <label className="field" htmlFor="job-input">
              <span className="field-label">Input defaults (JSON)</span>
              <textarea
                id="job-input"
                className="text-input code-input"
                value={inputText}
                onChange={(event) => setInputText(event.target.value)}
                rows={7}
                spellCheck={false}
              />
            </label>
            <p className="muted small">Run now always uses these input defaults.</p>
          </div>
        </section>
      </div>

      <div className="job-create-actions">
        <button className="button primary" onClick={() => void create()} disabled={busy || !templateId || version <= 0}>
          {busy ? 'Creating…' : 'Create job'}
        </button>
      </div>
    </main>
  );
}
