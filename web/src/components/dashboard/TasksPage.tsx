import { AssetLoadingState } from '../assets/AssetState';
import { useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../../api';
import type { Run, WorkflowSummary } from '../../types';
import { formatTime } from './shared';

interface TasksProps {
  summaries: WorkflowSummary[];
  canApprove: boolean;
  onOpenRun: (runId: string) => void;
  onNotify: (message: string, tone: 'info' | 'error') => void;
  onWaitingChanged?: () => void;
}

interface DecisionFailure {
  id: string;
  message: string;
}

// waitAge renders how long an approval has been waiting. It states elapsed
// time only — never a promise about when someone will look at it.
function waitAgeLabel(requestedAt: string | undefined, now: number): string {
  if (!requestedAt) return '';
  const at = Date.parse(requestedAt);
  if (!Number.isFinite(at)) return '';
  const ms = Math.max(0, now - at);
  const minutes = Math.floor(ms / 60_000);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  if (days >= 1) return `waiting ${days}d`;
  if (hours >= 1) return `waiting ${hours}h${minutes % 60 ? ` ${minutes % 60}m` : ''}`;
  if (minutes >= 1) return `waiting ${minutes}m`;
  return `waiting ${Math.floor(ms / 1000)}s`;
}

function waitStartedAt(run: Run): number {
  const requested = run.resumeRequestedAt ? Date.parse(run.resumeRequestedAt) : NaN;
  if (Number.isFinite(requested)) return requested;
  const started = Date.parse(run.startedAt);
  return Number.isFinite(started) ? started : 0;
}

export function TasksPage({ summaries, canApprove, onOpenRun, onNotify, onWaitingChanged }: TasksProps) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [note, setNote] = useState('');
  const [noteError, setNoteError] = useState('');
  const [busy, setBusy] = useState(false);
  const [updatedAt, setUpdatedAt] = useState('');
  const [failures, setFailures] = useState<DecisionFailure[]>([]);
  const [failedDecision, setFailedDecision] = useState<'approved' | 'rejected'>('approved');
  const [failedNote, setFailedNote] = useState('');
  const [now, setNow] = useState(() => Date.now());
  const noteRef = useRef<HTMLInputElement | null>(null);
  const names = useMemo(
    () =>
      new Map<string, string>(
        summaries.map((summary): [string, string] => [summary.id, summary.name]),
      ),
    [summaries],
  );

  // Oldest wait first so the longest-standing approval is the first thing
  // the inbox shows, whatever order the server happened to return.
  const ordered = useMemo(
    () => [...runs].sort((a, b) => waitStartedAt(a) - waitStartedAt(b)),
    [runs],
  );

  const load = (silent = false) => {
    if (!silent) setLoading(true);
    setError('');
    api
      .waitingRuns()
      .then((result) => {
        const listed = result.runs ?? [];
        setRuns(listed);
        setUpdatedAt(new Date().toISOString());
        setSelected((prev) => new Set([...prev].filter((id) => listed.some((run) => run.id === id))));
        // A failed id that is no longer waiting was decided elsewhere; its
        // failure row has nothing left to retry.
        setFailures((prev) => prev.filter((failure) => listed.some((run) => run.id === failure.id)));
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
    const timer = window.setInterval(() => load(true), 30000);
    return () => window.clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Keep the wait ages ticking while the inbox is on screen.
  useEffect(() => {
    if (runs.length === 0) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [runs.length]);

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const decide = async (
    ids: string[],
    decision: 'approved' | 'rejected',
    noteOverride?: string,
  ) => {
    if (busy || ids.length === 0) return;
    const trimmed = (noteOverride ?? note).trim();
    if (decision === 'rejected' && trimmed.length < 3) {
      setNoteError('Add a reason of at least 3 characters.');
      noteRef.current?.focus();
      return;
    }
    setNoteError('');
    setBusy(true);
    try {
      const results = await Promise.allSettled(
        ids.map((id) => api.resumeRun(id, decision, trimmed || undefined)),
      );
      const nextFailures: DecisionFailure[] = [];
      ids.forEach((id, index) => {
        const result = results[index];
        if (result.status !== 'rejected') return;
        const reason = result.reason;
        nextFailures.push({
          id,
          message: reason instanceof Error ? reason.message : String(reason),
        });
      });
      const failed = nextFailures.length;
      const ok = ids.length - failed;
      const verb = decision === 'approved' ? 'approved' : 'rejected';
      if (failed === 0) {
        onNotify(`${ok} run${ok === 1 ? '' : 's'} ${verb}.`, 'info');
        setFailures([]);
        setNote('');
      } else {
        setFailures(nextFailures);
        setFailedDecision(decision);
        setFailedNote(trimmed);
        onNotify(`Failed to ${verb} ${failed} of ${ids.length} run${ids.length === 1 ? '' : 's'}.`, 'error');
      }
      load(true);
      onWaitingChanged?.();
    } finally {
      setBusy(false);
    }
  };

  const retryFailed = () => {
    if (failures.length === 0) return;
    void decide(
      failures.map((failure) => failure.id),
      failedDecision,
      failedNote,
    );
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Tasks</h1>
          <p className="muted">
            Waiting approvals across all workflows, longest wait first.
            {updatedAt && ` Updated ${formatTime(updatedAt)}.`}
          </p>
        </div>
      </header>

      {error && (
        <div className="run-alert" role="alert">
          {error}
        </div>
      )}

      {loading && <AssetLoadingState label="Loading waiting approvals…" className="muted" />}

      {!loading && !error && runs.length === 0 && (
        <p className="muted">
          Nothing is waiting for approval. Runs that reach an approval step appear here.
        </p>
      )}

      {canApprove && runs.length > 0 && (
        <div className="tasks-bar">
          <span className="muted">{selected.size} selected</span>
          <input
            ref={noteRef}
            className="task-note"
            type="text"
            placeholder="Decision note (required to reject)"
            aria-label="Decision note"
            value={note}
            maxLength={500}
            onChange={(event) => {
              setNote(event.target.value);
              setNoteError('');
            }}
          />
          <button
            className="button subtle"
            disabled={busy || selected.size === 0}
            onClick={() => void decide([...selected], 'rejected')}
          >
            Reject selected
          </button>
          <button
            className="button primary"
            disabled={busy || selected.size === 0}
            onClick={() => void decide([...selected], 'approved')}
          >
            Approve selected
          </button>
        </div>
      )}

      {canApprove && noteError && (
        <p className="field-error task-note-error" role="alert">
          {noteError}
        </p>
      )}

      {failures.length > 0 && (
        <div className="task-failures" role="alert">
          <div className="task-failures-head">
            <span className="field-label">
              {failedDecision === 'approved' ? 'Approval' : 'Rejection'} failed for{' '}
              {failures.length} run{failures.length === 1 ? '' : 's'}
            </span>
          </div>
          <ul className="task-failure-list">
            {failures.map((failure) => (
              <li className="task-failure-item" key={failure.id}>
                <code>{failure.id.split('_').pop()}</code>
                <span>{failure.message}</span>
              </li>
            ))}
          </ul>
          <div className="task-failure-actions">
            <button
              className="button primary"
              disabled={busy}
              onClick={retryFailed}
            >
              Retry failed
            </button>
            <button type="button" className="text-button" onClick={() => setFailures([])}>
              Dismiss
            </button>
          </div>
        </div>
      )}

      {!loading && runs.length > 0 && (
        <div className="tasks-list">
          {ordered.map((run) => (
            <div className="task-row" key={run.id}>
              {canApprove && (
                <input
                  className="task-select"
                  type="checkbox"
                  aria-label={`Select ${names.get(run.workflowId) ?? run.workflowId} run ${run.id.split('_').pop() ?? run.id}`}
                  checked={selected.has(run.id)}
                  disabled={Boolean(busy)}
                  onChange={() => toggle(run.id)}
                />
              )}
              <span className="runs-row-main">
                <strong>{names.get(run.workflowId) ?? run.workflowId}</strong>
                <span className="meta-list">
                  <span className="status-text status-waiting">waiting</span>
                  <span className="task-wait-age">{waitAgeLabel(run.resumeRequestedAt, now)}</span>
                  <span>{run.trigger}</span>
                  <span>{formatTime(run.startedAt)}</span>
                  {run.actor && <span>{run.actor}</span>}
                </span>
              </span>
              <span className="runs-row-meta">
                <code title={run.id}>{run.id.split('_').pop()}</code>
              </span>
              <span className="task-actions">
                <button className="button subtle" onClick={() => onOpenRun(run.id)}>
                  Open
                </button>
                {canApprove && (
                  <>
                    <button
                      className="button subtle"
                      disabled={Boolean(busy)}
                      onClick={() => void decide([run.id], 'rejected')}
                    >
                      Reject
                    </button>
                    <button
                      className="button primary"
                      disabled={Boolean(busy)}
                      onClick={() => void decide([run.id], 'approved')}
                    >
                      Approve
                    </button>
                  </>
                )}
              </span>
            </div>
          ))}
        </div>
      )}
    </main>
  );
}
