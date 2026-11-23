import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import { useEffect, useState } from 'react';
import { api } from '../../api';
import { confirmAction } from '../../confirm';
import type { Job, WorkflowSummary } from '../../types';
import { formatTime, nextRunLabel, triggerLabel } from './shared';

interface JobsProps {
  summaries: WorkflowSummary[];
  onOpenJob: (id: string) => void;
  onCreateJob: () => void;
  onOpenWorkflows: () => void;
  onOpenRun: (runId: string) => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}

export function JobsPage({ summaries, onOpenJob, onCreateJob, onOpenWorkflows, onOpenRun, onNotify }: JobsProps) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const published = summaries.filter((summary) => summary.publishedVersion > 0);

  const load = () => {
    setLoading(true);
    api
      .listJobs()
      .then((result) => {
        setJobs(result.jobs ?? []);
        setError('');
      })
      .catch((reason) => setError(reason instanceof Error ? reason.message : String(reason)))
      .finally(() => setLoading(false));
  };

  useEffect(load, []);

  const run = async (job: Job) => {
    setBusy(job.id);
    setError('');
    try {
      const started = await api.runJob(job.id);
      onNotify?.('Run started.');
      onOpenRun(started.runId);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const toggle = async (job: Job) => {
    setBusy(job.id);
    setError('');
    try {
      const updated = await api.setJobEnabled(job.id, !job.enabled);
      setJobs((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      onNotify?.(updated.enabled ? 'Job resumed.' : 'Job paused.');
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const upgrade = async (job: Job, version: number) => {
    setBusy(job.id);
    setError('');
    try {
      const updated = await api.upgradeJob(job.id, version);
      setJobs((current) => current.map((item) => (item.id === updated.id ? updated : item)));
      onNotify?.(`Job pinned to v${updated.templateVersion}.`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const remove = async (job: Job) => {
    await confirmAction({
      title: `Delete “${job.name}”?`,
      body: 'The job stops scheduling. Its run history is kept.',
      confirmLabel: 'Delete job',
      onConfirm: async () => {
        setBusy(job.id);
        setError('');
        try {
          await api.deleteJob(job.id);
          setJobs((current) => current.filter((item) => item.id !== job.id));
          onNotify?.('Job deleted.');
        } catch (reason) {
          return reason instanceof Error ? reason.message : String(reason);
        } finally {
          setBusy('');
        }
      },
    });
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Jobs</h1>
          <p className="lede">
            Jobs pin a published workflow version and keep running it until you upgrade them.
          </p>
        </div>
        {published.length > 0 && (
          <button className="button primary" onClick={onCreateJob}>
            Create job
          </button>
        )}
      </header>

      {error && (
        <ActionError testId="jobs-error">{error}</ActionError>
      )}

      {!error && published.length === 0 && (
        <div className="empty-panel">
          <h2>No published workflows yet</h2>
          <p className="muted">Publish a workflow version before creating a job.</p>
          <button className="button primary" onClick={onOpenWorkflows}>Browse workflows</button>
        </div>
      )}

      {loading && <AssetLoadingState label="Loading jobs…" />}
      {!loading && !error && jobs.length === 0 && published.length > 0 && (
        <div className="empty-panel">
          <h2>No jobs yet</h2>
          <p className="muted">Create a job to pin a published workflow version.</p>
          <button className="button primary" onClick={onCreateJob}>Create job</button>
        </div>
      )}
      {!loading && jobs.length > 0 && (
        <div className="runs-table job-table">
          {jobs.map((job) => {
            const template = summaries.find((summary) => summary.id === job.templateId);
            const latest = template?.publishedVersion ?? job.templateVersion;
            return (
            <div className="job-row" key={job.id}>
              <button className="runs-row-main job-row-main" onClick={() => onOpenJob(job.id)}>
                  <strong>{job.name}</strong>
                  <span>
                    {job.templateName || job.templateId}, v{job.templateVersion},{' '}
                    {triggerLabel(job.triggerType)}
                  </span>
                  <span>
                    {job.lastRunAt ? `Last run ${formatTime(job.lastRunAt)}` : 'Never run'}. Next run{' '}
                    {nextRunLabel(job)}
                  </span>
                </button>
                <span className={`job-state ${job.enabled ? '' : 'paused'}`}>
                  {job.enabled ? 'Active' : 'Paused'}
                </span>
                <details className="action-menu job-actions">
                  <summary className="button subtle">More</summary>
                  <div className="action-menu-list">
                    <button onClick={() => void toggle(job)} disabled={busy === job.id}>
                      {job.enabled ? 'Pause' : 'Resume'}
                    </button>
                    {latest > job.templateVersion && (
                      <button onClick={() => void upgrade(job, latest)} disabled={busy === job.id}>
                        Upgrade to v{latest}
                      </button>
                    )}
                    <button
                      className="danger"
                      onClick={() => void remove(job)}
                      disabled={busy === job.id}
                    >
                      Delete job
                    </button>
                  </div>
                </details>
                <button className="button primary" onClick={() => void run(job)} disabled={busy === job.id || !job.enabled}>
                  {busy === job.id ? 'Starting…' : 'Run now'}
                </button>
              </div>
            );
          })}
        </div>
      )}
    </main>
  );
}
