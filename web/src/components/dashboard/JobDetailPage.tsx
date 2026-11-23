import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import { useEffect, useRef, useState } from 'react';
import { api } from '../../api';
import { confirmAction } from '../../confirm';
import type {
  Job,
  Run,
  Workflow,
  WorkflowSummary,
  WorkflowVersion,
} from '../../types';
import { cronPresetFor, localTimezone, parseJSONObject, schedulePreviewConfig, toDateTimeLocal, triggerLabel } from './shared';
import { BatchRunDialog } from './jobDetail/BatchRunDialog';
import { JobPinnedVersionPanel } from './jobDetail/JobPinnedVersionPanel';
import { JobRunsPanel } from './jobDetail/JobRunsPanel';
import { JobSettingsPanel } from './jobDetail/JobSettingsPanel';

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
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
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
  onNotify,
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
  const dirtyRef = useRef(false);

  const applyForm = (source: Job) => {
    setName(source.name);
    setInputText(JSON.stringify(source.inputDefaults ?? {}, null, 2));
    setTriggerType(source.triggerType || 'manual');
    const nextSchedule = String(source.triggerConfig?.schedule ?? '0 9 * * *');
    setSchedule(nextSchedule);
    setCronPreset(cronPresetFor(nextSchedule));
    setTimezone(String(source.triggerConfig?.timezone ?? localTimezone()));
    setRunAt(toDateTimeLocal(String(source.triggerConfig?.runAt ?? '')));
    setMaxConcurrency(String(source.triggerConfig?.maxConcurrency ?? ''));
  };

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
      if (!dirtyRef.current) {
        applyForm(nextJob);
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    dirtyRef.current = false;
  }, [jobId]);

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

  const toggle = async () => {
    if (!job) return;
    setBusy('toggle');
    setError('');
    try {
      setJob(await api.setJobEnabled(job.id, !job.enabled));
      onNotify?.(job.enabled ? 'Job paused.' : 'Job resumed.');
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
    await confirmAction({
      title: `Delete “${job.name}”?`,
      body: 'The job stops scheduling. Its run history is kept.',
      confirmLabel: 'Delete job',
      onConfirm: async () => {
        setBusy('delete');
        setError('');
        try {
          await api.deleteJob(job.id);
          onBack();
        } catch (reason) {
          return reason instanceof Error ? reason.message : String(reason);
        } finally {
          setBusy('');
        }
      },
    });
  };

  const upgrade = async (version: number) => {
    if (!job) return;
    setBusy('upgrade');
    setError('');
    try {
      await api.upgradeJob(job.id, version);
      await load();
      onNotify?.(`Job pinned to v${version}.`);
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
      applyForm(updated);
      dirtyRef.current = false;
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
        <AssetLoadingState label="Loading job…" className="muted pad" />
      </main>
    );
  }

  if (!job) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <header className="page-head">
          <div>
            <h1>Job not found</h1>
          </div>
          <button className="button subtle" onClick={onBack}>Back to jobs</button>
        </header>
        {error && (
          <ActionError testId="job-detail-error">{error}</ActionError>
        )}
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
            <h1>{job.name}</h1>
            <p className="lede meta-list">
              <span>{job.templateName || job.templateId}</span>
              <span>Pinned to v{job.templateVersion}</span>
              <span>{triggerLabel(job.triggerType)} trigger</span>
            </p>
        </div>
        <div className="job-actions">
          {editing ? (
            <>
              <button
                className="button subtle"
                onClick={() => {
                  dirtyRef.current = false;
                  applyForm(job);
                  onDone();
                }}
                disabled={Boolean(busy)}
              >
                Cancel
              </button>
              <button className="button primary" onClick={() => void save()} disabled={Boolean(busy)}>
                {busy === 'save' ? 'Saving…' : 'Save settings'}
              </button>
            </>
          ) : (
            <>
              <button
                className="button subtle"
                onClick={() => setShowBatch(true)}
                disabled={Boolean(busy) || !job.enabled}
              >
                Run batch
              </button>
              <details className="action-menu">
                <summary className="button subtle">More</summary>
                <div className="action-menu-list">
                  <button onClick={() => void toggle()} disabled={Boolean(busy)}>
                    {job.enabled ? 'Pause' : 'Resume'}
                  </button>
                  <button onClick={onEdit} disabled={Boolean(busy)}>
                    Edit settings
                  </button>
                  <button onClick={() => void duplicate()} disabled={Boolean(busy)}>
                    Duplicate
                  </button>
                  <button className="danger" onClick={() => void remove()} disabled={Boolean(busy)}>
                    Delete
                  </button>
                </div>
              </details>
              <button className="button primary" onClick={() => void run()} disabled={Boolean(busy) || !job.enabled}>
                {busy === 'run' ? 'Starting…' : 'Run now'}
              </button>
            </>
          )}
        </div>
      </header>

      {error && <ActionError>{error}</ActionError>}

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
        <JobSettingsPanel
          job={job}
          editing={editing}
          onDirty={() => {
            dirtyRef.current = true;
          }}
          name={name}
          triggerType={triggerType}
          schedule={schedule}
          cronPreset={cronPreset}
          timezone={timezone}
          runAt={runAt}
          maxConcurrency={maxConcurrency}
          inputText={inputText}
          previewConfig={previewConfig}
          onNameChange={setName}
          onTriggerTypeChange={setTriggerType}
          onScheduleChange={setSchedule}
          onCronPresetChange={setCronPreset}
          onTimezoneChange={setTimezone}
          onRunAtChange={setRunAt}
          onMaxConcurrencyChange={setMaxConcurrency}
          onInputTextChange={setInputText}
        />

        <JobPinnedVersionPanel
          job={job}
          latest={latest}
          templateWorkflow={templateWorkflow}
          latestWorkflow={latestWorkflow}
          busy={busy}
          onOpenTemplate={onOpenTemplate}
          onUpgrade={(version) => void upgrade(version)}
        />
      </div>

      <JobRunsPanel
        job={job}
        runs={runs}
        onOpenRun={onOpenRun}
        onRetried={() => void load()}
      />

      {showBatch && (
        <BatchRunDialog
          job={job}
          onClose={() => setShowBatch(false)}
          onStarted={async () => {
            await load();
            onNotify?.('Batch started.');
          }}
        />
      )}
    </main>
  );
}
