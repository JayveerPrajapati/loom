import { useEffect, useMemo, useState } from 'react';
import { api } from '../../api';
import type { Job, Run, WorkflowSummary } from '../../types';
import { duration, formatTime } from './shared';

interface RunsProps {
  summaries: WorkflowSummary[];
  onOpenRun: (runId: string) => void;
}

export function RunsPage({ summaries, onOpenRun }: RunsProps) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('all');
  const [trigger, setTrigger] = useState('all');
  const [jobFilter, setJobFilter] = useState('');
  const [query, setQuery] = useState('');
  const [limit, setLimit] = useState(100);
  const [updatedAt, setUpdatedAt] = useState('');
  const names = useMemo(
    () =>
      new Map<string, string>(
        summaries.map((summary): [string, string] => [summary.id, summary.name]),
      ),
    [summaries],
  );
  const jobNames = useMemo(
    () =>
      new Map<string, string>(jobs.map((job): [string, string] => [job.id, job.name])),
    [jobs],
  );

  const load = (silent = false) => {
    if (!silent) setLoading(true);
    setError('');
    api
      .listAllRuns(limit, jobFilter || undefined)
      .then((result) => {
        setRuns(result.runs ?? []);
        setUpdatedAt(new Date().toISOString());
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    api
      .listJobs()
      .then((result) => setJobs(result.jobs ?? []))
      .catch(() => setJobs([]));
  }, []);

  useEffect(() => {
    load();
  }, [jobFilter, limit]);

  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState === 'visible') load(true);
    };
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener('focus', refresh);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', refresh);
    };
  }, [jobFilter, limit]);

  const term = query.trim().toLowerCase();
  const filtered = (status === 'all' ? runs : runs.filter((run) => run.status === status))
    .filter((run) => trigger === 'all' || run.trigger === trigger)
    .filter((run) => !run.parentRunId)
    .filter((run) => {
      if (!term) return true;
      const workflow = names.get(run.workflowId) ?? '';
      const job = run.jobId ? jobNames.get(run.jobId) ?? '' : '';
      return [run.id, run.workflowId, workflow, job, run.trigger, run.status, run.error ?? '']
        .some((value) => value.toLowerCase().includes(term));
    });
  const recentFailures = runs.filter((run) => {
    if (run.status !== 'failed' || run.parentRunId) return false;
    const started = Date.parse(run.startedAt);
    return Number.isFinite(started) && Date.now() - started < 24 * 60 * 60 * 1000;
  }).length;

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <p className="eyebrow">Execution</p>
          <h1>Runs</h1>
          <p className="lede">The most recent executions across every template, newest first.</p>
        </div>
        <button className="button subtle" onClick={() => load()} disabled={loading}>
          Refresh
        </button>
      </header>

      <div className="page-toolbar">
        <label className="field inline-field" htmlFor="run-search">
          <span className="field-label">Search</span>
          <input
            id="run-search"
            className="text-input"
            type="search"
            value={query}
            placeholder="Run id, workflow, job, error"
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <label className="field inline-field" htmlFor="run-status">
          <span className="field-label">Status</span>
          <select
            id="run-status"
            className="text-input"
            value={status}
            onChange={(event) => setStatus(event.target.value)}
          >
            <option value="all">All statuses</option>
            <option value="running">Running</option>
            <option value="waiting">Waiting</option>
            <option value="done">Complete</option>
            <option value="failed">Failed</option>
            <option value="cancelled">Cancelled</option>
          </select>
        </label>
        <label className="field inline-field" htmlFor="run-trigger">
          <span className="field-label">Source</span>
          <select
            id="run-trigger"
            className="text-input"
            value={trigger}
            onChange={(event) => setTrigger(event.target.value)}
          >
            <option value="all">All sources</option>
            <option value="manual">Manual</option>
            <option value="job">Job</option>
            <option value="cron">Cron</option>
            <option value="once">One time</option>
            <option value="batch">Batch</option>
            <option value="webhook">Webhook</option>
          </select>
        </label>
        <label className="field inline-field" htmlFor="run-job">
          <span className="field-label">Job</span>
          <select
            id="run-job"
            className="text-input"
            value={jobFilter}
            onChange={(event) => setJobFilter(event.target.value)}
          >
            <option value="">All jobs</option>
            {jobs.map((job) => (
              <option key={job.id} value={job.id}>{job.name}</option>
            ))}
          </select>
        </label>
        <label className="field inline-field" htmlFor="run-limit">
          <span className="field-label">Show</span>
          <select
            id="run-limit"
            className="text-input"
            value={limit}
            onChange={(event) => setLimit(Number(event.target.value))}
          >
            <option value={100}>Latest 100</option>
            <option value={250}>Latest 250</option>
            <option value={500}>Latest 500</option>
          </select>
        </label>
        <span className="page-toolbar-status">
          <strong>{filtered.length} runs</strong>
          <em>{updatedAt ? `Live · updated ${formatTime(updatedAt)}` : 'Live'}</em>
        </span>
      </div>

      {error && <p className="banner error">{error}</p>}
      {!loading && recentFailures > 0 && status !== 'failed' && (
        <div className="run-alert" role="status">
          <span>
            <strong>{recentFailures}</strong> failed run{recentFailures === 1 ? '' : 's'} in the
            last 24 hours.
          </span>
          <button className="text-button" onClick={() => setStatus('failed')}>
            Review failures
          </button>
        </div>
      )}
      {loading && (
        <div className="skel-list" aria-busy="true" aria-label="Loading runs">
          {Array.from({ length: 6 }, (_, index) => (
            <div className="skel-row" key={index} />
          ))}
        </div>
      )}
      {!loading && !error && filtered.length === 0 && (
        <div className="empty-panel">
          <h2>{runs.length === 0 ? 'No runs yet' : 'No matching runs'}</h2>
          <p className="muted">
            {runs.length === 0
              ? 'Open a template and press Run to create the first run.'
              : 'Try a different search or clear the filters.'}
          </p>
        </div>
      )}

      {!loading && filtered.length > 0 && (
        <div className="runs-table">
          {filtered.map((run) => (
            <button
              className="runs-row"
              key={run.id}
              onClick={() => onOpenRun(run.id)}
            >
              <span className={`status-dot status-${run.status}`} aria-hidden="true" />
              <span className="runs-row-main">
                <strong>{names.get(run.workflowId) ?? run.workflowId}</strong>
                <span>
                  {run.status} · {run.trigger} · {formatTime(run.startedAt)}
                  {run.jobId ? ` · ${jobNames.get(run.jobId) ?? run.jobId}` : ''}
                </span>
              </span>
              <span className="runs-row-meta">
                <code>{run.id.split('_').pop()}</code>
                <em>{duration(run)}</em>
              </span>
            </button>
          ))}
        </div>
      )}
    </main>
  );
}
