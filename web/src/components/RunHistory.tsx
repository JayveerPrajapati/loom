import { AssetLoadingState } from './assets/AssetState';
import { ActionError } from './assets/ActionError';
import { useEffect, useState } from 'react';
import { api } from '../api';
import { confirmAction } from '../confirm';
import type { ReplayResult, Run, RunDiff, RunEvent } from '../types';
import { useDialogFocus } from '../useDialogFocus';

import { formatTime, duration, traceData } from './runHistory/helpers';
import { HistoryEventDetail } from './runHistory/HistoryEventDetail';
import { RunDiffView } from './runHistory/RunDiffView';

interface Props {
  open: boolean;
  workflowId: string | null;
  currentRunId: string | null;
  onClose: () => void;
  onView: (run: Run, events: RunEvent[]) => void;
  onUpdate: (run: Run, events: RunEvent[]) => void;
  onOpenRun: (runId: string) => void;
}

export function RunHistory({ open, workflowId, currentRunId, onClose, onView, onUpdate, onOpenRun }: Props) {
  const dialogRef = useDialogFocus<HTMLDivElement>(open, onClose);
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [total, setTotal] = useState(0);
  const [nextCursor, setNextCursor] = useState('');
  const [search, setSearch] = useState('');
  const [statusFilter, setStatusFilter] = useState('');
  const [error, setError] = useState('');
  const [selected, setSelected] = useState<string | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [eventsLoading, setEventsLoading] = useState(false);
  const [detailEvent, setDetailEvent] = useState<RunEvent | null>(null);
  const [detail, setDetail] = useState<{
    loading: boolean;
    input?: unknown;
    output?: unknown;
    error?: string;
  }>({ loading: false });
  const [replay, setReplay] = useState<{ busy: boolean; result?: ReplayResult; error?: string }>({
    busy: false,
  });
  const [compare, setCompare] = useState('');
  const [diff, setDiff] = useState<RunDiff | null>(null);
  const [diffLoading, setDiffLoading] = useState(false);
  const [diffError, setDiffError] = useState('');
  const [children, setChildren] = useState<Run[]>([]);
  const [childrenLoading, setChildrenLoading] = useState(false);

  const loadRuns = (cursor = '') => {
    if (cursor === '') setLoading(true);
    else setLoadingMore(true);
    setError('');
    api
      .listRuns(workflowId, 50, {
        q: search.trim() || undefined,
        status: statusFilter || undefined,
        cursor: cursor || undefined,
      })
      .then((result) => {
        const next = result.runs ?? [];
        setTotal(result.count ?? next.length);
        setNextCursor(result.nextCursor ?? '');
        setRuns((current) =>
          cursor === ''
            ? next
            : [...current, ...next.filter((run) => !current.some((seen) => seen.id === run.id))],
        );
        if (cursor === '') {
          setSelected((current) =>
            current && next.some((run) => run.id === current) ? current : next[0]?.id ?? null,
          );
        }
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => {
        setLoading(false);
        setLoadingMore(false);
      });
  };

  const load = () => loadRuns();

  useEffect(() => {
    if (!open) return;
    const timer = window.setTimeout(() => loadRuns(), 250);
    return () => window.clearTimeout(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [search, statusFilter]);

  useEffect(() => {
    if (!open) return;
    setSelected(null);
    setEvents([]);
    setDetailEvent(null);
    setDetail({ loading: false });
    setReplay({ busy: false });
    setCompare('');
    setDiff(null);
    setDiffError('');
    setChildren([]);
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, workflowId]);

  useEffect(() => {
    if (!open || !selected) {
      setEvents([]);
      setDetailEvent(null);
      setCompare('');
      setDiff(null);
      return;
    }
    let cancelled = false;
    setEventsLoading(true);
    api
      .listEvents(selected)
      .then((result) => {
        if (!cancelled) setEvents(result.events ?? []);
      })
      .catch((err: Error) => {
        if (!cancelled) setError(err.message);
      })
      .finally(() => {
        if (!cancelled) setEventsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, selected]);

  useEffect(() => {
    if (!open || !selected) {
      setChildren([]);
      return;
    }
    let cancelled = false;
    setChildrenLoading(true);
    api
      .listChildRuns(selected, 20)
      .then((result) => {
        if (!cancelled) setChildren(result.runs ?? []);
      })
      .catch(() => {
        if (!cancelled) setChildren([]);
      })
      .finally(() => {
        if (!cancelled) setChildrenLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, selected]);

  useEffect(() => {
    if (!detailEvent) return;
    let cancelled = false;
    setReplay({ busy: false });
    setDetail({ loading: true });
    Promise.all([
      detailEvent.inputHash ? api.getBlob(detailEvent.inputHash) : Promise.resolve(undefined),
      detailEvent.outputHash ? api.getBlob(detailEvent.outputHash) : Promise.resolve(undefined),
    ])
      .then(([input, output]) => {
        if (!cancelled) setDetail({ loading: false, input, output });
      })
      .catch((err: Error) => {
        if (!cancelled) setDetail({ loading: false, error: err.message });
      });
    return () => {
      cancelled = true;
    };
  }, [detailEvent]);

  useEffect(() => {
    if (!open || !selected || !compare) {
      setDiff(null);
      setDiffError('');
      return;
    }
    let cancelled = false;
    setDiffLoading(true);
    setDiffError('');
    api
      .diffRuns(selected, compare)
      .then((result) => {
        if (!cancelled) setDiff(result);
      })
      .catch((err: Error) => {
        if (!cancelled) setDiffError(err.message);
      })
      .finally(() => {
        if (!cancelled) setDiffLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [compare, open, selected]);

  const replayNode = async (mode: 'step' | 'from') => {
    if (!selected || !detailEvent?.nodeId || replay.busy) return;
    const confirmed = await confirmAction({
      title: mode === 'from' ? 'Re-run from this node?' : 'Re-run this node?',
      body:
        mode === 'from'
          ? 'Re-runs this node and every downstream node as a new run. Side effects will repeat.'
          : 'Re-runs this node in place. Side effects will repeat and the original run log is unchanged.',
      confirmLabel: mode === 'from' ? 'Re-run downstream' : 'Re-run node',
      cancelLabel: 'Cancel',
      tone: 'primary',
    });
    if (!confirmed) return;
    setReplay({ busy: true });
    try {
      const result = await api.replayRun(selected, mode, detailEvent.nodeId);
      const targetId = result.runId ?? selected;
      if (workflowId) {
        const [runsResult, eventsResult, updatedRun] = await Promise.all([
          api.listRuns(workflowId, 50),
          api.listEvents(targetId),
          api.getRun(targetId),
        ]);
        setRuns(runsResult.runs ?? []);
        setSelected(targetId);
        setEvents(eventsResult.events ?? []);
        onUpdate(updatedRun, eventsResult.events ?? []);
      }
      setReplay({ busy: false, result });
    } catch (err) {
      setReplay({ busy: false, error: err instanceof Error ? err.message : String(err) });
    }
  };

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, onClose]);

  if (!open) return null;
  const current = runs.find((run) => run.id === selected) ?? null;  const finishedNodes = new Set(events.filter((event) => event.type === 'node.finished').map((event) => event.nodeId)).size;
  const failedNodes = new Set(events.filter((event) => event.type === 'node.failed').map((event) => event.nodeId)).size;
  const request = traceData(detailEvent, 'request');
  const response = traceData(detailEvent, 'response');

  return (
    <div className="overlay" onClick={onClose}>
      <section
        ref={dialogRef}
        className="dialog history-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Run history"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <header className="history-head">
          <div>
            <h2>Run history</h2>
          </div>
          <div className="history-head-actions">
            <button className="button subtle" onClick={load} disabled={loading} aria-label="Refresh runs">
              Refresh
            </button>
            <button className="icon-button" onClick={onClose} aria-label="Close run history">
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M6 6l12 12M18 6L6 18" strokeLinecap="round" />
              </svg>
            </button>
          </div>
        </header>

        {error && <ActionError>{error}</ActionError>}

        <div className="history-body">
          <div className="history-list">
            <div className="history-filters">
              <input
                className="text-input"
                type="search"
                placeholder="Search run id or error"
                aria-label="Search runs"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
              <select
                className="text-input"
                aria-label="Filter by status"
                value={statusFilter}
                onChange={(event) => setStatusFilter(event.target.value)}
              >
                <option value="">All statuses</option>
                <option value="waiting">Waiting</option>
                <option value="running">Running</option>
                <option value="done">Done</option>
                <option value="failed">Failed</option>
                <option value="cancelled">Cancelled</option>
              </select>
              <span className="muted small history-count">
                {total} run{total === 1 ? '' : 's'}
              </span>
            </div>
            <div className="history-scroll">
            {loading && <AssetLoadingState label="Loading runs…" />}
            {!loading && runs.length === 0 && <p className="muted small pad">No runs yet. Press Run to start one.</p>}
            {runs.map((run) => (
              <button
                key={run.id}
                className={`history-row ${run.id === selected ? 'selected' : ''} ${
                  run.id === currentRunId ? 'current' : ''
                }`}
                onClick={() => setSelected(run.id)}
              >
                <span className="history-row-main">
                  <strong className={`status-text status-${run.status}`}>{run.status}</strong>
                  <span className="meta-list">
                    <span>{run.trigger}</span>
                  </span>
                </span>
                <span className="history-row-meta">
                  <em>{formatTime(run.startedAt)}</em>
                  <code title={run.id.split('_').pop()}>{run.id.split('_').pop()}</code>
                  <em>{duration(run)}</em>
                </span>
              </button>
            ))}
            {!loading && runs.length < total && nextCursor !== '' && (
              <button
                className="button subtle history-more"
                onClick={() => loadRuns(nextCursor)}
                disabled={loadingMore}
              >
                {loadingMore ? 'Loading…' : `Load more (${total - runs.length} left)`}
              </button>
            )}
            </div>
          </div>

          <div className="history-detail">
            {!current && <p className="muted small pad">Select a run to inspect its event log.</p>}
            {current && (
              <>
                <div className="history-summary">
                  <div>
                    <span className="field-label">Status</span>
                    <strong className={`status-text status-${current.status}`}>{current.status}</strong>
                  </div>
                  <div>
                    <span className="field-label">Trigger</span>
                    <strong>{current.trigger}</strong>
                  </div>
                  <div>
                    <span className="field-label">Started</span>
                    <strong>{formatTime(current.startedAt)}</strong>
                  </div>
                  <div>
                    <span className="field-label">Duration</span>
                    <strong>{duration(current)}</strong>
                  </div>
                  <div>
                    <span className="field-label">Nodes</span>
                    <strong>
                      {finishedNodes} finished{failedNodes > 0 ? ` · ${failedNodes} failed` : ''}
                    </strong>
                  </div>
                  <div className="history-summary-run">
                    <span className="field-label">Run</span>
                    <strong title={current.id}>
                      <code>{current.id}</code>
                    </strong>
                  </div>
                </div>
                {current.error && <ActionError>{current.error}</ActionError>}
                {current.parentRunId && (
                  <div className="run-link-row">
                    <span className="field-label">Child run</span>
                    <span className="muted small">
                      of <code>{current.parentRunId}</code> via <code>{current.parentNodeId}</code>
                    </span>
                    <button className="button subtle" onClick={() => onOpenRun(current.parentRunId!)}>
                      Open parent
                    </button>
                  </div>
                )}
                {childrenLoading && <AssetLoadingState label="Loading child runs…" className="muted small" />}
                {children.length > 0 && (
                  <div className="child-runs" role="region" aria-label="Child runs">
                    <div className="child-runs-head">
                      <span className="field-label">Child runs</span>
                      <span className="muted small">{children.length}</span>
                    </div>
                    {children.map((child) => (
                      <button
                        key={child.id}
                        className="child-run"
                        onClick={() => onOpenRun(child.id)}
                        aria-label={`Open child run ${child.id}`}
                      >
              <span className="child-run-main">
                <strong>{child.workflowId}</strong>
                <span className="muted small meta-list">
                  <span>{child.parentNodeId || 'subflow'}</span>
                  <span>{formatTime(child.startedAt)}</span>
                </span>
              </span>
                        <span className="child-run-meta">
                          <em className={`status-text status-${child.status}`}>{child.status}</em>
                          <code>{child.id.split('_').pop()}</code>
                        </span>
                      </button>
                    ))}
                  </div>
                )}
                <div className="diff-controls">
                  <label className="diff-label">
                    Compare with
                    <select
                      className="diff-select"
                      value={compare}
                      onChange={(event) => setCompare(event.target.value)}
                    >
                      <option value="">Another run…</option>
                      {runs
                        .filter((run) => run.id !== selected)
                        .map((run) => (
                          <option key={run.id} value={run.id}>
                            {run.id.split('_').pop()} · {run.status} · {formatTime(run.startedAt)}
                          </option>
                        ))}
                    </select>
                  </label>
                  {diffLoading && <span className="muted small">Comparing…</span>}
                </div>
                {diffError && <ActionError>{diffError}</ActionError>}
                {diff && <RunDiffView diff={diff} />}
                <div className="event-list">
                  {eventsLoading && <AssetLoadingState label="Loading events…" className="muted small" />}
                  {!eventsLoading && events.length === 0 && <p className="muted small">No events recorded.</p>}
                  {events.map((event) => (
                    <div className={`event-row event-${event.type.replace('.', '-')}`} key={`${event.seq}-${event.type}`}>
                      <span className="event-seq">{event.seq}</span>
                      <div>
                        <strong>{event.type}</strong>
                        {event.nodeId && <span className="event-node">{event.nodeId}</span>}
                        {event.error && <p className="event-error">{event.error}</p>}
                      </div>
                      <span className="event-time">
                        {event.durationMs != null ? `${event.durationMs} ms` : formatTime(event.at)}
                      </span>
                      {(event.inputHash || event.outputHash) && (
                        <button
                          type="button"
                          className={`event-inspect ${detailEvent?.seq === event.seq ? 'active' : ''}`}
                          onClick={() => setDetailEvent(detailEvent?.seq === event.seq ? null : event)}
                          aria-expanded={detailEvent?.seq === event.seq}
                          aria-label={`Inspect ${event.type}${event.nodeId ? ` for ${event.nodeId}` : ''}`}
                        >
                          Inspect
                        </button>
                      )}
                    </div>
                  ))}
                </div>
                {detailEvent && (
                  <HistoryEventDetail
                    detailEvent={detailEvent}
                    detail={detail}
                    request={request}
                    response={response}
                    replay={replay}
                    onDetailEventChange={setDetailEvent}
                    onReplayNode={(mode) => void replayNode(mode)}
                  />
                )}
              </>
            )}
          </div>
        </div>

        <footer className="dialog-actions history-footer">
          {current ? (
            <button className="button primary" onClick={() => onView(current, events)}>
              Open on canvas
            </button>
          ) : (
            <span className="muted small">Select a run to open it on the canvas.</span>
          )}
        </footer>
      </section>
    </div>
  );
}
