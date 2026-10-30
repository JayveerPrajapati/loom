import { useEffect, useMemo, useState } from 'react';
import { api } from '../../api';
import type {
  Example,
  Run,
  RunStats,
  WorkflowSummary,
} from '../../types';
import { duration, formatTime, nodeCount, triggerName } from './shared';

interface HomeProps {
  summaries: WorkflowSummary[];
  examples: Example[];
  onNew: () => void;
  onOpenTemplate: (id: string) => void;
  onUseExample: (example: Example) => void;
  onOpenTemplates: () => void;
  onOpenRuns: () => void;
  onOpenRun: (runId: string) => void;
}

export function HomePage({
  summaries,
  examples,
  onNew,
  onOpenTemplate,
  onUseExample,
  onOpenTemplates,
  onOpenRuns,
  onOpenRun,
}: HomeProps) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [stats, setStats] = useState<RunStats | null>(null);
  const [loading, setLoading] = useState(true);
  const names = useMemo(
    () =>
      new Map<string, string>(
        summaries.map((summary): [string, string] => [summary.id, summary.name]),
      ),
    [summaries],
  );

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    Promise.all([api.listAllRuns(10), api.stats()])
      .then(([result, summary]) => {
        if (cancelled) return;
        setRuns((result.runs ?? []).filter((run) => !run.parentRunId));
        setStats(summary);
      })
      .catch(() => {
        if (!cancelled) {
          setRuns([]);
          setStats(null);
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, []);

  const finished = (stats?.done ?? 0) + (stats?.failed ?? 0);
  const successRate =
    finished === 0 ? '—' : `${Math.round(((stats?.done ?? 0) / finished) * 100)}%`;

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <section className="home-hero">
        <div>
          <p className="eyebrow">Home</p>
          <h1>Build once. Run it as a job.</h1>
          <p className="lede">
            Design reusable templates, then run them manually, on a schedule, or as a batch.
          </p>
        </div>
        <div className="home-hero-actions">
          <button className="button primary" onClick={onNew}>
            Blank workflow
          </button>
          <button className="button subtle" onClick={onOpenTemplates}>
            Browse templates
          </button>
        </div>
      </section>

      <section className="dashboard-metrics" aria-label="Workspace summary">
        <div className="dashboard-metric">
          <span>Templates</span>
          <strong>{summaries.length}</strong>
          <em>Reusable flows</em>
        </div>
        <div className="dashboard-metric">
          <span>Runs</span>
          <strong>{stats?.total ?? runs.length}</strong>
          <em>All recorded runs</em>
        </div>
        <div className="dashboard-metric">
          <span>Active</span>
          <strong>{(stats?.active ?? 0) + (stats?.waiting ?? 0)}</strong>
          <em>Queued, running or waiting</em>
        </div>
        <div className="dashboard-metric">
          <span>Success</span>
          <strong className={finished > 0 && successRate === '100%' ? 'metric-strong-good' : undefined}>
            {successRate}
          </strong>
          <em>Of finished runs</em>
        </div>
      </section>

      <div className="dashboard-grid">
        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Execution</p>
              <h2>Recent runs</h2>
            </div>
            <button className="button subtle" onClick={onOpenRuns}>
              View all
            </button>
          </header>
          {loading && (
            <div className="skel-list" aria-busy="true" aria-label="Loading recent runs">
              {Array.from({ length: 4 }, (_, index) => (
                <div className="skel-row" key={index} />
              ))}
            </div>
          )}
          {!loading && runs.length === 0 && (
            <p className="muted small pad">No runs yet. Open a template and press Run.</p>
          )}
          {!loading && runs.length > 0 && (
            <div className="dashboard-run-list">
              {runs.slice(0, 6).map((run) => (
                <button className="dashboard-run-row" key={run.id} onClick={() => onOpenRun(run.id)}>
                  <span className={`status-dot status-${run.status}`} aria-hidden="true" />
                  <span className="dashboard-run-main">
                    <strong>{names.get(run.workflowId) ?? run.workflowId}</strong>
                    <span>
                      {run.status} · {run.trigger} · {formatTime(run.startedAt)}
                    </span>
                  </span>
                  <span className="dashboard-run-meta">
                    <code>{run.id.split('_').pop()}</code>
                    <em>{duration(run)}</em>
                  </span>
                </button>
              ))}
            </div>
          )}
        </section>

        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Library</p>
              <h2>Your templates</h2>
            </div>
            <button className="button subtle" onClick={onOpenTemplates}>
              Manage
            </button>
          </header>
          {summaries.length === 0 && (
            <p className="muted small pad">No templates yet. Start from an example below.</p>
          )}
          <div className="dashboard-template-list">
            {summaries.slice(0, 5).map((summary) => (
              <button
                className="dashboard-template-row"
                key={summary.id}
                onClick={() => onOpenTemplate(summary.id)}
              >
                <span>
                  <strong>{summary.name}</strong>
                  <em>
                    {summary.publishedVersion ? `v${summary.publishedVersion}` : 'Draft'}
                    {summary.draft && summary.publishedVersion ? ' · draft' : ''} ·{' '}
                    {summary.jobCount ?? 0} job{summary.jobCount === 1 ? '' : 's'}
                    {summary.activeJobCount ? ` (${summary.activeJobCount} active)` : ''} · updated{' '}
                    {formatTime(summary.updatedAt)}
                  </em>
                </span>
                <span aria-hidden="true">→</span>
              </button>
            ))}
          </div>
        </section>
      </div>

      <section className="home-examples">
        <header className="dashboard-panel-head">
          <div>
            <p className="eyebrow">Start here</p>
            <h2>Example templates</h2>
          </div>
        </header>
        <div className="example-grid">
          {examples.map((example) => (
            <button
              key={example.id}
              className="example-card"
              onClick={() => onUseExample(example)}
            >
              <span className="eyebrow">Example</span>
              <strong>{example.name}</strong>
              <span className="example-meta">
                {nodeCount(example)} · {example.workflow.edges.length} edges
              </span>
              <span className="example-trigger">{triggerName(example)}</span>
            </button>
          ))}
        </div>
      </section>
    </main>
  );
}
