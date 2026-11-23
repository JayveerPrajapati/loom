import { useCallback, useEffect, useMemo, useState } from 'react';
import { api } from '../../api';
import { usePagedList } from '../../usePagedList';
import type { PageParams } from '../../paging';
import type { Job, Run, WorkflowSummary } from '../../types';
import { AssetErrorState, AssetFilteredEmptyState } from '../assets/AssetState';
import { duration, formatTime } from './shared';

interface RunsProps {
  summaries: WorkflowSummary[];
  onOpenRun: (runId: string) => void;
}

export function RunsPage({ summaries, onOpenRun }: RunsProps) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [status, setStatus] = useState('all');
  const [trigger, setTrigger] = useState('all');
  const [jobFilter, setJobFilter] = useState('');
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

  const loadRuns = useCallback(
    (params: PageParams) =>
      api
        .listRuns(null, params.limit ?? limit, {
          q: params.q,
          status: status === 'all' ? undefined : status,
          trigger: trigger === 'all' ? undefined : trigger,
          job: jobFilter || undefined,
          roots: true,
          cursor: params.cursor,
        })
        .then((result) => ({
          items: result.runs ?? [],
          count: result.count,
          nextCursor: result.nextCursor,
        })),
    [jobFilter, limit, status, trigger],
  );
  const list = usePagedList<Run>(loadRuns, { limit });

  useEffect(() => {
    api
      .listJobs()
      .then((result) => setJobs(result.jobs ?? []))
      .catch(() => setJobs([]));
  }, []);

  useEffect(() => {
    if (list.loaded) setUpdatedAt(new Date().toISOString());
  }, [list.loaded, list.items]);

  useEffect(() => {
    const refresh = () => {
      if (document.visibilityState === 'visible') list.reload();
    };
    const timer = window.setInterval(refresh, 5000);
    window.addEventListener('focus', refresh);
    return () => {
      window.clearInterval(timer);
      window.removeEventListener('focus', refresh);
    };
  }, [list.reload]);

  const filtersActive =
    list.query.trim() !== '' || status !== 'all' || trigger !== 'all' || jobFilter !== '';
  const recentFailures = list.items.filter((run) => {
    if (run.status !== 'failed') return false;
    const started = Date.parse(run.startedAt);
    return Number.isFinite(started) && Date.now() - started < 24 * 60 * 60 * 1000;
  }).length;

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Runs</h1>
          <p className="lede">The most recent executions across every template, newest first.</p>
        </div>
        <button className="button subtle" onClick={list.reload} disabled={list.loading}>
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
            value={list.query}
            placeholder="Run id, workflow, job, error"
            onChange={(event) => list.setQuery(event.target.value)}
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
            <option value="queued">Queued</option>
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
          <span className="field-label">Rows</span>
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
          <strong>
            {list.loaded
              ? `${list.count} run${list.count === 1 ? '' : 's'}`
              : 'Loading runs…'}
          </strong>
          <em>{updatedAt ? `Live · updated ${formatTime(updatedAt)}` : 'Live'}</em>
        </span>
      </div>

      {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
      {list.loaded && recentFailures > 0 && status !== 'failed' && (
        <div className="run-alert" role="status">
          <span>
            <strong>{recentFailures}</strong> failed run{recentFailures === 1 ? '' : 's'} in the
            loaded runs.
          </span>
          <button className="text-button" onClick={() => setStatus('failed')}>
            Review failures
          </button>
        </div>
      )}
      {list.loading && !list.loaded && (
        <div className="skel-list" aria-busy="true" aria-label="Loading runs">
          {Array.from({ length: 6 }, (_, index) => (
            <div className="skel-row" key={index} />
          ))}
        </div>
      )}
      {!list.loading &&
        !list.failure &&
        list.loaded &&
        list.count === 0 &&
        (filtersActive ? (
          <AssetFilteredEmptyState
            title="No matching runs"
            onClear={() => {
              list.setQuery('');
              setStatus('all');
              setTrigger('all');
              setJobFilter('');
            }}
          >
            Try a different search or clear the filters.
          </AssetFilteredEmptyState>
        ) : (
          <div className="empty-panel">
            <h2>No runs yet</h2>
            <p className="muted">Open a workflow and press Run to create the first run.</p>
          </div>
        ))}

      {!list.failure && list.items.length > 0 && (
        <div className="runs-table">
          <div className="runs-row-head" aria-hidden="true">
            <span className="runs-col-main">Workflow</span>
            <span className="runs-col-id">Run</span>
            <span className="runs-col-duration">Duration</span>
          </div>
          {list.items.map((run) => (
            <button
              className="runs-row"
              key={run.id}
              onClick={() => onOpenRun(run.id)}
            >
              <span className="runs-row-main">
                <strong>{names.get(run.workflowId) ?? run.workflowId}</strong>
                <span className="meta-list">
                  <span className={`status-text status-${run.status}`}>{run.status}</span>
                  <span>{run.trigger}</span>
                  <span>{formatTime(run.startedAt)}</span>
                  {run.jobId && <span>{jobNames.get(run.jobId) ?? run.jobId}</span>}
                </span>
              </span>
              <span className="runs-row-meta">
                <code title={run.id}>{run.id.split('_').pop()}</code>
                <em>{duration(run)}</em>
              </span>
            </button>
          ))}
        </div>
      )}
      {!list.failure && list.hasMore && (
        <div className="list-load-more">
          <button
            type="button"
            className="button"
            onClick={list.loadMore}
            disabled={list.loadingMore}
            data-testid="runs-load-more"
          >
            {list.loadingMore ? 'Loading more…' : 'Load more'}
          </button>
        </div>
      )}
    </main>
  );
}
