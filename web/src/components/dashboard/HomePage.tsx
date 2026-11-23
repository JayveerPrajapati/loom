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
  onOpenWorkflows: () => void;
  onOpenRuns: () => void;
  onOpenRun: (runId: string) => void;
}

export function HomePage({
  summaries,
  examples,
  onNew,
  onOpenTemplate,
  onUseExample,
  onOpenWorkflows,
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
  const firstRun = !loading && (stats?.total ?? 0) === 0 && summaries.length === 0;

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <section className="home-hero">
        <div>
          <h1>Build once. Run it as a job.</h1>
          <p className="lede">
            Design reusable workflows, then run them manually, on a schedule, or as a batch.
          </p>
        </div>
        <div className="home-hero-actions">
          <button className="button primary" onClick={onNew}>
            Blank workflow
          </button>
          <button className="button subtle" onClick={onOpenWorkflows}>
            Browse workflows
          </button>
        </div>
      </section>

      {firstRun ? (
        <section className="home-get-started" aria-label="Get started">
          <div className="get-started-step">
            <span className="get-started-num" aria-hidden="true">1</span>
            <h2>Start from an example</h2>
            <p>Open a working workflow, or begin from a blank canvas.</p>
            <a className="button subtle" href="#home-examples">
              See examples below
            </a>
          </div>
          <div className="get-started-step">
            <span className="get-started-num" aria-hidden="true">2</span>
            <h2>Run it and watch</h2>
            <p>Every run is recorded. Press Run in the editor and follow each step.</p>
          </div>
          <div className="get-started-step">
            <span className="get-started-num" aria-hidden="true">3</span>
            <h2>Make it a job</h2>
            <p>Publish the workflow, then schedule it or run it as a batch.</p>
          </div>
        </section>
      ) : (
        <>
          <section className="dashboard-metrics" aria-label="Workspace summary">
            <div className="dashboard-metric">
              <span>Workflows</span>
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
                <span className="dashboard-run-main">
                  <strong>{names.get(run.workflowId) ?? run.workflowId}</strong>
                  <span className="meta-list">
                    <span className={`status-text status-${run.status}`}>{run.status}</span>
                    <span>{run.trigger}</span>
                    <span>{formatTime(run.startedAt)}</span>
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
              <h2>Your templates</h2>
            </div>
            <button className="button subtle" onClick={onOpenWorkflows}>
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
                  <em className="meta-list">
                    <span>
                      {summary.publishedVersion ? `v${summary.publishedVersion}` : 'Draft'}
                      {summary.draft && summary.publishedVersion ? ' draft' : ''}
                    </span>
                    <span>
                      {summary.jobCount ?? 0} job{summary.jobCount === 1 ? '' : 's'}
                      {summary.activeJobCount ? ` (${summary.activeJobCount} active)` : ''}
                    </span>
                    <span>Updated {formatTime(summary.updatedAt)}</span>
                  </em>
                </span>
              </button>
            ))}
          </div>
        </section>
      </div>
        </>
      )}

      <section className="home-examples" id="home-examples">
        <header className="dashboard-panel-head">
          <div>
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
              <strong>{example.name}</strong>
              <span className="example-meta">
                {nodeCount(example)}, {example.workflow.edges.length} edges
              </span>
              <span className="example-trigger">{triggerName(example)}</span>
            </button>
          ))}
        </div>
      </section>
    </main>
  );
}
