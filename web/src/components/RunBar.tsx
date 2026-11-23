import { useEffect, useState } from 'react';
import { ApiError, api } from '../api';
import type { Run, RunEvent } from '../types';
import { useDialogFocus } from '../useDialogFocus';

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

// linkStateLabel reports the health of the approval link: plenty of life
// left, running low, or gone. It describes the link, never an SLA.
function linkStateLabel(expiresAt: string, now: number): string {
  const remaining = Date.parse(expiresAt) - now;
  if (!Number.isFinite(remaining)) return '';
  if (remaining <= 0) return 'Link expired';
  const minutes = Math.floor(remaining / 60_000);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  if (days >= 1) return `Link valid for ${days}d${hours % 24 ? ` ${hours % 24}h` : ''}`;
  if (hours >= 1) return `Link expires in ${hours}h${minutes % 60 ? ` ${minutes % 60}m` : ''}`;
  if (minutes >= 1) return `Link expires in ${minutes}m`;
  return `Link expires in ${Math.ceil(remaining / 1000)}s`;
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
  onResume: (decision: 'approved' | 'rejected', note?: string) => void;
  onOpenRun: () => void;
  onOpenHistory?: () => void;
  onNotify?: (message: string, tone: 'info' | 'error') => void;
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
  onNotify,
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
  const [decisionPanel, setDecisionPanel] = useState<'approved' | 'rejected' | null>(null);
  const [decisionNote, setDecisionNote] = useState('');
  const [decisionError, setDecisionError] = useState('');
  const [confirmRevoke, setConfirmRevoke] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [revoked, setRevoked] = useState(false);
  const [now, setNow] = useState(() => Date.now());

  // Link state belongs to the current run only; a new run in the editor
  // starts with a fresh link.
  useEffect(() => {
    setRevoked(false);
    setConfirmRevoke(false);
    setRevoking(false);
  }, [run?.id]);

  // Keep the countdown honest while a link can still be used.
  useEffect(() => {
    if (!waiting || !run?.resumeTokenExpiresAt || revoked) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [waiting, run?.resumeTokenExpiresAt, revoked]);

  const closeDecision = () => {
    setDecisionPanel(null);
    setDecisionNote('');
    setDecisionError('');
  };
  const decisionRef = useDialogFocus<HTMLDivElement>(decisionPanel !== null, closeDecision);

  const openDecision = (decision: 'approved' | 'rejected') => {
    setDecisionPanel(decision);
    setDecisionNote('');
    setDecisionError('');
  };

  const confirmDecision = () => {
    if (!decisionPanel) return;
    const note = decisionNote.trim();
    if (decisionPanel === 'rejected' && note.length < 3) {
      setDecisionError('Add a reason of at least 3 characters.');
      return;
    }
    onResume(decisionPanel, note || undefined);
    closeDecision();
  };

  const doRevoke = () => {
    if (!run || revoking || revoked) return;
    setRevoking(true);
    api
      .revokeApproval(run.id)
      .then(() => {
        setRevoked(true);
        setConfirmRevoke(false);
        onNotify?.('Approval link revoked. The run still waits for a decision.', 'info');
      })
      .catch((reason: unknown) => {
        if (reason instanceof ApiError && reason.status === 409) {
          // Already revoked or decided elsewhere; treat the link as gone.
          setRevoked(true);
          setConfirmRevoke(false);
          onNotify?.('The approval link was already revoked.', 'info');
          return;
        }
        onNotify?.(
          reason instanceof Error ? reason.message : 'Could not revoke the approval link.',
          'error',
        );
      })
      .finally(() => setRevoking(false));
  };

  let started = 0;
  let ended = 0;
  if (run) {
    started = Date.parse(run.startedAt);
    ended = run.finishedAt ? Date.parse(run.finishedAt) : last ? Date.parse(last.at) : 0;
  }
  const elapsed = started && ended ? Math.max(0, ended - started) : 0;
  const linkExpires = run?.resumeTokenExpiresAt;
  const linkText = linkExpires && !revoked ? linkStateLabel(linkExpires, now) : '';
  const linkExpired = linkExpires ? Date.parse(linkExpires) - now <= 0 : false;

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
            <span style={{ transform: `scaleX(${progress / 100})` }} />
          </div>
          <span className="runbar-meta meta-list">
            <span>
              {done.size}/{nodeCount} nodes
            </span>
            {elapsed > 0 && <span>{formatDuration(elapsed)}</span>}
          </span>
        </div>
      )}

      <div className="runbar-actions">
        {waiting && (
          <>
            {(linkText || revoked) && (
              <span
                className={`runbar-link-state${linkExpired && !revoked ? ' is-expired' : ''}${
                  revoked ? ' is-revoked' : ''
                }`}
                title={
                  revoked
                    ? 'The approval link no longer works; decide from here or the inbox.'
                    : 'When the approval link stops working'
                }
              >
                {revoked ? 'Link revoked' : linkText}
              </span>
            )}
            {run?.resumeToken && !revoked && (
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
            {run?.resumeToken && !revoked && !confirmRevoke && (
              <button
                className="button subtle"
                onClick={() => setConfirmRevoke(true)}
                disabled={revoking}
                title="Stop the approval link from working; the run keeps waiting"
              >
                Revoke link
              </button>
            )}
            {run?.resumeToken && !revoked && confirmRevoke && (
              <span className="revoke-confirm" role="group" aria-label="Confirm revoking the approval link">
                <span className="revoke-confirm-text">Revoke this link?</span>
                <button
                  className="button subtle"
                  onClick={() => setConfirmRevoke(false)}
                  disabled={revoking}
                >
                  Keep link
                </button>
                <button className="button primary" onClick={doRevoke} disabled={revoking}>
                  {revoking ? 'Revoking…' : 'Confirm revoke'}
                </button>
              </span>
            )}
            <span className="decision-anchor">
              <button
                className="button subtle"
                onClick={() => openDecision('rejected')}
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
              {decisionPanel && (
                <>
                  <div className="decision-backdrop" onClick={closeDecision} />
                  <div
                    ref={decisionRef}
                    className="decision-popover up"
                    role="dialog"
                    aria-modal="true"
                    aria-label={
                      decisionPanel === 'rejected' ? 'Confirm rejection' : 'Confirm approval'
                    }
                  >
                    <span className="field-label">
                      {decisionPanel === 'rejected' ? 'Rejection reason (required)' : 'Decision note'}
                    </span>
                    <textarea
                      className="decision-note-input"
                      value={decisionNote}
                      rows={3}
                      maxLength={500}
                      spellCheck={false}
                      data-autofocus
                      aria-label={
                        decisionPanel === 'rejected' ? 'Rejection reason' : 'Decision note'
                      }
                      placeholder={
                        decisionPanel === 'rejected' ? 'Why this run is rejected' : 'Optional note'
                      }
                      onChange={(event) => {
                        setDecisionNote(event.target.value);
                        setDecisionError('');
                      }}
                    />
                    {decisionError && (
                      <p className="field-error" role="alert">
                        {decisionError}
                      </p>
                    )}
                    <div className="decision-popover-actions">
                      <button className="button subtle" onClick={closeDecision}>
                        Cancel
                      </button>
                      <button className="button primary" onClick={confirmDecision}>
                        {decisionPanel === 'rejected' ? 'Confirm reject' : 'Confirm approve'}
                      </button>
                    </div>
                  </div>
                </>
              )}
            </span>
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
