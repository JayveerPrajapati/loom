import { duration, formatDurationMs } from './runDetailHelpers';
import type { Run } from '../types';

// The status/trigger/duration/nodes/slowest/retries/version strip on the run
// detail page, extracted verbatim from RunDetailPage.
export function RunSummaryStrip({
  run,
  finished,
  failed,
  slowest,
  retries,
}: {
  run: Run;
  finished: number;
  failed: number;
  slowest: number | undefined;
  retries: number;
}) {
  return (
    <section className="job-summary-strip" aria-label="Run summary">
      <div>
        <span>Status</span>
        <strong className={`status-text status-${run.status}`}>{run.status}</strong>
      </div>
      <div>
        <span>Trigger</span>
        <strong>{run.trigger}</strong>
      </div>
      <div>
        <span>Duration</span>
        <strong>{duration(run)}</strong>
      </div>
      <div>
        <span>Nodes</span>
        <strong>
          {finished} finished{failed ? `, ${failed} failed` : ''}
        </strong>
      </div>
      <div>
        <span>Slowest</span>
        <strong>{formatDurationMs(slowest)}</strong>
      </div>
      <div>
        <span>Retries</span>
        <strong>{retries}</strong>
      </div>
      <div>
        <span>Version</span>
        <strong title={!run.templateVersion && run.workflowVersion ? run.workflowVersion : undefined}>
          {run.templateVersion
            ? `v${run.templateVersion}`
            : run.workflowVersion
              ? run.workflowVersion.slice(0, 7)
              : '—'}
        </strong>
      </div>
    </section>
  );
}
