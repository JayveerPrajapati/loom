import { BatchRunRow, duration, formatTime } from '../shared';
import type { Job, Run } from '../../../types';

/**
 * JobRunsPanel: the runs this job produced — batch runs expand to their
 * children with retry affordances.
 */
export function JobRunsPanel({
  job,
  runs,
  onOpenRun,
  onRetried,
}: {
  job: Job;
  runs: Run[];
  onOpenRun: (runId: string) => void;
  onRetried: () => void;
}) {
  return (
    <section className="dashboard-panel job-runs-panel">
      <header className="dashboard-panel-head">
        <div>
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
              <BatchRunRow key={item.id} run={item} onOpenRun={onOpenRun} onRetried={onRetried} />
            ) : (
              <button
                className="runs-row"
                key={item.id}
                role="listitem"
                onClick={() => onOpenRun(item.id)}
              >
                <span className="runs-row-main">
                  <strong>{item.status}</strong>
                  <span className="meta-list">
                    <span>{formatTime(item.startedAt)}</span>
                    <span>v{item.templateVersion ?? job.templateVersion}</span>
                  </span>
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
  );
}
