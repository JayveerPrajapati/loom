import { formatTime, linkStateLabel } from '../runDetailHelpers';
import type { Job, Run } from '../../types';
import type { RefObject } from 'react';

// The run-detail header (Phase 6 D6-2): breadcrumb, run identity with
// copy-id, job actions, the approval-decision popover, and the
// waiting-link expiry chip. Props-only — no state of its own.
export function RunDetailHeader(props: {
  run: Run;
  job: Job | null;
  workflowName: string;
  busy: string;
  now: number;
  onBack: () => void;
  onRefresh: () => void;
  onCancel: () => void;
  onRetry: () => void;
  onOpenTemplate: (id: string) => void;
  onOpenJob: (id: string) => void;
  onCompare: () => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  retryOffered: boolean;
  decision: {
    panel: 'approved' | 'rejected' | null;
    note: string;
    error: string;
    noteRef: RefObject<HTMLTextAreaElement>;
    dialogRef: RefObject<HTMLDivElement>;
    onNoteChange: (value: string) => void;
    onOpen: (decision: 'approved' | 'rejected') => void;
    onClose: () => void;
    onSubmit: (decision?: 'approved' | 'rejected') => void;
  };
}) {
  const { run, job, workflowName, busy, now, decision } = props;
  const active = ['queued', 'running', 'waiting'].includes(run.status);
  const failedNodePresent = run.status === 'failed';

  return (
    <header className="page-head run-detail-head">
      <div>
        <button className="breadcrumb-button" onClick={props.onBack}>
          ← Runs
        </button>
        <h1>{workflowName}</h1>
        <p className="lede meta-list">
          <span>
            Run {run.id.split('_').pop()}
            <button
              className="icon-button copy-run-id"
              onClick={() => {
                navigator.clipboard
                  .writeText(run.id)
                  .then(() => props.onNotify?.('Full run id copied.', 'info'))
                  .catch(() => props.onNotify?.('Could not copy the run id.', 'error'));
              }}
              aria-label="Copy full run id"
              title="Copy full run id"
            >
              <svg
                viewBox="0 0 24 24"
                width="14"
                height="14"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.8"
                aria-hidden="true"
              >
                <rect x="9" y="9" width="11" height="11" rx="2" />
                <path d="M5 15V5a2 2 0 0 1 2-2h8" strokeLinecap="round" />
              </svg>
            </button>
          </span>
          <span>{run.trigger}</span>
          <span>{formatTime(run.startedAt)}</span>
          {job && <span>{job.name}</span>}
        </p>
      </div>
      <div className="job-actions">
        <button className="button subtle" onClick={props.onRefresh} disabled={Boolean(busy)}>
          Refresh
        </button>
        {run.status === 'waiting' && run.resumeTokenExpiresAt && (
          <span
            className={`link-expiry${
              Date.parse(run.resumeTokenExpiresAt) - now <= 0 ? ' is-expired' : ''
            }`}
            title="When the approval link stops working"
          >
            {linkStateLabel(run.resumeTokenExpiresAt, now)}
          </span>
        )}
        {run.status === 'waiting' && (
          <span className="decision-anchor">
            <button
              className="button subtle"
              onClick={() => decision.onOpen('rejected')}
              disabled={Boolean(busy)}
            >
              Reject
            </button>
            <button
              className="button primary"
              onClick={() => decision.onSubmit('approved')}
              disabled={Boolean(busy)}
            >
              Approve
            </button>
            {decision.panel && (
              <>
                <div className="decision-backdrop" onClick={decision.onClose} />
                <div
                  ref={decision.dialogRef}
                  className="decision-popover"
                  role="dialog"
                  aria-modal="true"
                  aria-label={decision.panel === 'rejected' ? 'Confirm rejection' : 'Confirm approval'}
                >
                  <span className="field-label">
                    {decision.panel === 'rejected' ? 'Rejection reason (required)' : 'Decision note'}
                  </span>
                  <textarea
                    ref={decision.noteRef}
                    data-autofocus
                    className="decision-note-input"
                    value={decision.note}
                    maxLength={500}
                    rows={3}
                    spellCheck={false}
                    aria-label={decision.panel === 'rejected' ? 'Rejection reason' : 'Decision note'}
                    placeholder={
                      decision.panel === 'rejected' ? 'Why this run is rejected' : 'Optional note'
                    }
                    onChange={(event) => decision.onNoteChange(event.target.value)}
                  />
                  {decision.error && (
                    <p className="field-error" role="alert">
                      {decision.error}
                    </p>
                  )}
                  <div className="decision-popover-actions">
                    <button type="button" className="button subtle" onClick={decision.onClose}>
                      Cancel
                    </button>
                    <button
                      type="button"
                      className="button primary"
                      onClick={() => decision.onSubmit()}
                      disabled={Boolean(busy)}
                    >
                      {decision.panel === 'rejected' ? 'Confirm rejection' : 'Confirm approval'}
                    </button>
                  </div>
                </div>
              </>
            )}
          </span>
        )}
        {active && run.status !== 'waiting' && (
          <button className="button subtle" onClick={props.onCancel} disabled={Boolean(busy)}>
            Cancel
          </button>
        )}
        {failedNodePresent && props.retryOffered && (
          <button className="button primary" onClick={props.onRetry} disabled={Boolean(busy)}>
            Retry failed node
          </button>
        )}
        <button className="button subtle" onClick={() => props.onOpenTemplate(run.workflowId)}>
          Open workflow
        </button>
        <button className="button subtle" onClick={props.onCompare}>
          Compare
        </button>
        {run.jobId && (
          <button className="button subtle" onClick={() => props.onOpenJob(run.jobId!)}>
            Open job
          </button>
        )}
      </div>
    </header>
  );
}
