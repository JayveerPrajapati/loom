import type { Run, RunEvent } from '../types';

const STATUS_LABEL: Record<string, string> = {
  queued: 'Queued',
  running: 'Running',
  waiting: 'Waiting',
  done: 'Complete',
  failed: 'Failed',
  cancelled: 'Cancelled',
};

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

function eventLine(event: RunEvent | undefined): string {
  if (!event) return 'Run to see live execution.';
  switch (event.type) {
    case 'run.started':
      return 'Run started';
    case 'node.started':
      return `${event.nodeId} started`;
    case 'node.finished':
      return `${event.nodeId} finished in ${formatDuration(event.durationMs ?? 0)}`;
    case 'node.failed':
      return `${event.nodeId} failed: ${event.error ?? 'unknown error'}`;
    case 'node.retried':
      return `${event.nodeId} retrying (attempt ${event.attempt ?? 2})`;
    case 'run.waiting':
      return `${event.nodeId} is waiting for approval`;
    case 'run.failed':
      return `Run failed: ${event.error ?? 'unknown error'}`;
    case 'run.finished':
      return 'Run finished';
    case 'run.cancelled':
      return 'Run cancelled';
    default:
      return event.type;
  }
}

interface Props {
  run: Run | null;
  status: string;
  events: RunEvent[];
  nodeCount: number;
  busy: boolean;
  online: boolean;
  runBlocked?: string;
  onRun: () => void;
  onInput: () => void;
  onRetry: () => void;
  onCancel: () => void;
  onResume: (decision: 'approved' | 'rejected') => void;
  onOpenRun: () => void;
  onOpenHistory?: () => void;
}

export function RunBar({
  run,
  status,
  events,
  nodeCount,
  busy,
  online,
  runBlocked,
  onRun,
  onInput,
  onRetry,
  onCancel,
  onResume,
  onOpenRun,
  onOpenHistory,
}: Props) {
  const done = new Set(events.filter((event) => event.type === 'node.finished').map((event) => event.nodeId));
  const active = status === 'queued' || status === 'running' || status === 'waiting';
  const waiting = status === 'waiting';
  const last = events[events.length - 1];
  const failure = [...events].reverse().find((event) => event.type === 'run.failed');
  const failedNode = [...events].reverse().find((event) => event.type === 'node.failed' && event.nodeId);
  const waitingEvent = [...events].reverse().find((event) => event.type === 'run.waiting');
  const approvalMessage =
    typeof waitingEvent?.data?.message === 'string' ? waitingEvent.data.message.trim() : '';
  const line =
    failure?.error ||
    run?.error ||
    (waiting && approvalMessage ? approvalMessage : eventLine(last));
  const progress = nodeCount === 0 ? 0 : Math.round((done.size / nodeCount) * 100);

  let started = 0;
  let ended = 0;
  if (run) {
    started = Date.parse(run.startedAt);
    ended = run.finishedAt ? Date.parse(run.finishedAt) : last ? Date.parse(last.at) : 0;
  }
  const elapsed = started && ended ? Math.max(0, ended - started) : 0;

  return (
    <div
      className={`runbar ${active ? 'is-active' : ''} ${waiting ? 'is-waiting' : ''} ${
        status === 'failed' ? 'is-failed' : ''
      }`}
      aria-live="polite"
    >
      <div className="runbar-state">
        <span className={`status-dot status-${status || 'idle'}`} aria-hidden="true" />
        <div>
          <strong>{run ? STATUS_LABEL[status] ?? status : 'Ready'}</strong>
          <span className="runbar-line" title={line}>
            {line}
          </span>
        </div>
      </div>

      {run && (
        <div className="runbar-progress">
          <div className="progress-track">
            <span style={{ width: `${progress}%` }} />
          </div>
          <span className="runbar-meta">
            {done.size}/{nodeCount} nodes
            {elapsed > 0 && ` · ${formatDuration(elapsed)}`}
          </span>
        </div>
      )}

      <div className="runbar-actions">
        {waiting && (
          <>
            {run?.resumeToken && (
              <button
                className="button subtle"
                onClick={() => {
                  const link = `${window.location.origin}/hook/approval/${run.resumeToken}`;
                  void navigator.clipboard.writeText(link);
                }}
                title="Copy a link that opens an approval page"
              >
                Copy approval link
              </button>
            )}
            <button
              className="button subtle"
              onClick={() => onResume('rejected')}
              disabled={!online}
              title={!online ? 'Loom server is unreachable' : undefined}
            >
              Reject
            </button>
            <button
              className="button primary"
              onClick={() => onResume('approved')}
              disabled={!online}
              title={!online ? 'Loom server is unreachable' : undefined}
            >
              Approve
            </button>
          </>
        )}
        {active && !waiting && (
          <button
            className="button subtle"
            onClick={onCancel}
            disabled={!online}
            title={!online ? 'Loom server is unreachable' : undefined}
          >
            Cancel
          </button>
        )}
        {status === 'failed' && failedNode && (
          <button
            className="button primary"
            onClick={onRetry}
            disabled={busy || !online}
            title={!online ? 'Loom server is unreachable' : `Retry from ${failedNode.nodeId}`}
          >
            Retry failed node
          </button>
        )}
        <details className="action-menu runbar-menu">
          <summary className="button subtle">More</summary>
          <div className="action-menu-list">
            <button
              onClick={(e) => {
                e.currentTarget.closest('details')?.removeAttribute('open');
                onInput();
              }}
              disabled={busy || !online}
            >
              Run with input…
            </button>
            {onOpenHistory && (
              <button
                onClick={(e) => {
                  e.currentTarget.closest('details')?.removeAttribute('open');
                  onOpenHistory();
                }}
                disabled={!online}
              >
                Run history
              </button>
            )}
            {run && (
              <button
                onClick={(e) => {
                  e.currentTarget.closest('details')?.removeAttribute('open');
                  onOpenRun();
                }}
                disabled={!online}
              >
                Open run log
              </button>
            )}
          </div>
        </details>
        <button
          className="button primary"
          onClick={onRun}
          disabled={busy || !online || Boolean(runBlocked)}
          title={runBlocked || (!online ? 'Loom server is unreachable' : undefined)}
        >
          {busy ? 'Starting…' : run ? 'Run again' : 'Run workflow'}
        </button>
      </div>
    </div>
  );
}
