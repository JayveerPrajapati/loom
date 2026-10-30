import { useEffect, useState } from 'react';
import { api } from '../api';
import type { ReplayResult, Run, RunDiff, RunEvent } from '../types';

function formatTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(date);
}

function duration(run: Run): string {
  if (!run.finishedAt) return '—';
  const ms = Date.parse(run.finishedAt) - Date.parse(run.startedAt);
  if (!Number.isFinite(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

function durationLabel(value?: number): string {
  if (value == null) return '—';
  if (value < 1000) return `${value} ms`;
  return `${(value / 1000).toFixed(1)} s`;
}

function pretty(value: unknown): string {
  if (value === undefined) return '—';
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}

function traceData(event: RunEvent | null, key: 'request' | 'response'): unknown {
  const data = event?.data;
  if (!data || typeof data !== 'object') return undefined;
  return (data as Record<string, unknown>)[key];
}

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
  const [runs, setRuns] = useState<Run[]>([]);
  const [loading, setLoading] = useState(false);
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

  const load = () => {
    setLoading(true);
    setError('');
    api
      .listRuns(workflowId, 50)
      .then((result) => {
        setRuns(result.runs ?? []);
        setSelected((current) => current ?? result.runs?.[0]?.id ?? null);
      })
      .catch((err: Error) => setError(err.message))
      .finally(() => setLoading(false));
  };

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
    const prompt =
      mode === 'from'
        ? 'Re-run this node and every downstream node as a new run? Side effects will repeat.'
        : 'Re-run this node in place? Side effects will repeat and the original run log is unchanged.';
    if (!window.confirm(prompt)) return;
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
  const current = runs.find((run) => run.id === selected) ?? null;
  const finishedNodes = new Set(events.filter((event) => event.type === 'node.finished').map((event) => event.nodeId)).size;
  const failedNodes = new Set(events.filter((event) => event.type === 'node.failed').map((event) => event.nodeId)).size;
  const request = traceData(detailEvent, 'request');
  const response = traceData(detailEvent, 'response');

  return (
    <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div className="history-panel" role="dialog" aria-modal="true" aria-label="Run history">
        <header className="history-head">
          <div>
            <p className="eyebrow">Execution</p>
            <h2>Run history</h2>
          </div>
          <div className="row-actions">
            <button className="button subtle" onClick={load} disabled={loading}>
              Refresh
            </button>
            <button className="icon-button" onClick={onClose} aria-label="Close run history">
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M6 6l12 12M18 6L6 18" strokeLinecap="round" />
              </svg>
            </button>
          </div>
        </header>

        {error && <p className="banner error">{error}</p>}

        <div className="history-body">
          <div className="history-list">
            {loading && <p className="muted small pad">Loading runs…</p>}
            {!loading && runs.length === 0 && <p className="muted small pad">No runs yet. Press Run to start one.</p>}
            {runs.map((run) => (
              <button
                key={run.id}
                className={`history-row ${run.id === selected ? 'selected' : ''} ${
                  run.id === currentRunId ? 'current' : ''
                }`}
                onClick={() => setSelected(run.id)}
              >
                <span className={`status-dot status-${run.status}`} aria-hidden="true" />
                <span className="history-row-main">
                  <strong>{run.status}</strong>
                  <span>
                    {run.trigger} · {formatTime(run.startedAt)}
                  </span>
                </span>
                <span className="history-row-meta">
                  <code>{run.id.split('_').pop()}</code>
                  <em>{duration(run)}</em>
                </span>
              </button>
            ))}
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
                  <div>
                    <span className="field-label">Run</span>
                    <strong><code>{current.id}</code></strong>
                  </div>
                </div>
                {current.error && <p className="banner error">{current.error}</p>}
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
                {childrenLoading && <p className="muted small">Loading child runs…</p>}
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
                        <span className={`status-dot status-${child.status}`} aria-hidden="true" />
                        <span className="child-run-main">
                          <strong>{child.workflowId}</strong>
                          <span className="muted small">
                            {child.parentNodeId || 'subflow'} · {formatTime(child.startedAt)}
                          </span>
                        </span>
                        <span className="child-run-meta">
                          <em>{child.status}</em>
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
                {diffError && <p className="banner error">{diffError}</p>}
                {diff && (
                  <div className="run-diff" role="region" aria-label="Run diff">
                    <div className="run-diff-head">
                      <strong>{diff.changed ? `${diff.changed} changed` : 'No differences'}</strong>
                      <span className="muted small">{diff.nodes.length} nodes compared</span>
                    </div>
                    <div className="diff-rows">
                      {diff.nodes.map((node) => {
                        const changed =
                          !node.inputMatch ||
                          !node.outputMatch ||
                          node.baseStatus !== node.againstStatus;
                        return (
                          <details
                            key={node.nodeId}
                            className={`diff-row ${changed ? 'diff-changed' : ''}`}
                          >
                            <summary>
                              <span className="diff-node">
                                <strong>{node.name || node.nodeId}</strong>
                                <code>{node.nodeId}</code>
                              </span>
                              <span className="diff-statuses">
                                {node.baseStatus || 'missing'} → {node.againstStatus || 'missing'}
                              </span>
                              <span className="diff-durations">
                                {durationLabel(node.baseDurationMs)} →{' '}
                                {durationLabel(node.againstDurationMs)}
                              </span>
                              <span className={`diff-badge ${node.inputMatch ? 'same' : 'changed'}`}>
                                input {node.inputMatch ? 'same' : 'changed'}
                              </span>
                              <span className={`diff-badge ${node.outputMatch ? 'same' : 'changed'}`}>
                                output {node.outputMatch ? 'same' : 'changed'}
                              </span>
                            </summary>
                            <div className="diff-detail">
                              <div className="diff-outputs">
                                <div>
                                  <span className="field-label">Base output</span>
                                  <pre className="json-view">{pretty(node.baseOutput)}</pre>
                                </div>
                                <div>
                                  <span className="field-label">Compared output</span>
                                  <pre className="json-view">{pretty(node.againstOutput)}</pre>
                                </div>
                              </div>
                              <div className="diff-hashes">
                                <span>
                                  input: <code>{node.baseInputHash || '—'}</code> →{' '}
                                  <code>{node.againstInputHash || '—'}</code>
                                </span>
                                <span>
                                  output: <code>{node.baseOutputHash || '—'}</code> →{' '}
                                  <code>{node.againstOutputHash || '—'}</code>
                                </span>
                              </div>
                            </div>
                          </details>
                        );
                      })}
                    </div>
                  </div>
                )}
                <div className="event-list">
                  {eventsLoading && <p className="muted small">Loading events…</p>}
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
                  <div className="event-detail" role="region" aria-label="Step data">
                    <div className="event-detail-head">
                      <div>
                        <strong>{detailEvent.type}</strong>
                        {detailEvent.nodeId && <span className="event-node">{detailEvent.nodeId}</span>}
                      </div>
                      <button
                        className="icon-button"
                        onClick={() => setDetailEvent(null)}
                        aria-label="Close step data"
                      >
                        ×
                      </button>
                    </div>
                    {detail.loading && <p className="muted small">Loading data…</p>}
                    {detail.error && <p className="banner error">{detail.error}</p>}
                    {!detail.loading &&
                      !detail.error &&
                      detail.input === undefined &&
                      detail.output === undefined &&
                      request === undefined &&
                      response === undefined && (
                        <p className="muted small">No data recorded for this step.</p>
                      )}
                    {request !== undefined && (
                      <div className="event-detail-block">
                        <span className="field-label">Request</span>
                        <pre className="json-view">{pretty(request)}</pre>
                      </div>
                    )}
                    {response !== undefined && (
                      <div className="event-detail-block">
                        <span className="field-label">Response</span>
                        <pre className="json-view">{pretty(response)}</pre>
                      </div>
                    )}
                    {detail.input !== undefined && (
                      <div className="event-detail-block">
                        <span className="field-label">Input item</span>
                        <pre className="json-view">{pretty(detail.input)}</pre>
                      </div>
                    )}
                    {detail.output !== undefined && (
                      <div className="event-detail-block">
                        <span className="field-label">Output item</span>
                        <pre className="json-view">{pretty(detail.output)}</pre>
                      </div>
                    )}
                    {(detailEvent.type === 'node.finished' || detailEvent.type === 'node.failed') &&
                      detailEvent.nodeId && (
                        <div className="event-detail-actions">
                          {detailEvent.type === 'node.finished' && (
                            <button
                              type="button"
                              className="button subtle"
                              onClick={() => void replayNode('step')}
                              disabled={replay.busy}
                            >
                              Replay step
                            </button>
                          )}
                          <button
                            type="button"
                            className={`button ${detailEvent.type === 'node.failed' ? 'primary' : 'subtle'}`}
                            onClick={() => void replayNode('from')}
                            disabled={replay.busy}
                          >
                            {detailEvent.type === 'node.failed' ? 'Retry from failed node' : 'Replay from here'}
                          </button>
                        </div>
                      )}
                    {replay.busy && <p className="muted small">Replaying…</p>}
                    {replay.error && <p className="banner error">{replay.error}</p>}
                    {replay.result && (
                      <div
                        className={`replay-result ${
                          replay.result.mode === 'step'
                            ? replay.result.match
                              ? 'match'
                              : 'diverge'
                            : ''
                        }`}
                      >
                        <strong>
                          {replay.result.mode === 'step'
                            ? replay.result.match
                              ? 'Step replay matched the recorded output.'
                              : 'Step replay diverged from the recorded output.'
                            : `Replay from ${replay.result.nodeId ?? 'node'}: ${replay.result.status} · new run ${replay.result.runId.split('_').pop()}`}
                        </strong>
                        {replay.result.mode === 'step' && (
                          <span className="replay-hashes">
                            <code>{replay.result.recordedHash ?? '—'}</code>
                            {' → '}
                            <code>{replay.result.actualHash ?? '—'}</code>
                          </span>
                        )}
                      </div>
                    )}
                  </div>
                )}
                <div className="history-footer">
                  <button className="button primary" onClick={() => onView(current, events)}>
                    View this run on the canvas
                  </button>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
