import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../api';
import { Canvas } from './Canvas';
import { deriveNodeStatuses } from '../runState';
import type {
  Job,
  NodeDef,
  ReplayResult,
  Run,
  RunDiff,
  RunEvent,
  Workflow,
  WorkflowSummary,
} from '../types';

interface Props {
  runId: string;
  summaries: WorkflowSummary[];
  defs: Record<string, NodeDef>;
  onBack: () => void;
  onOpenRun: (runId: string) => void;
  onOpenTemplate: (id: string) => void;
  onOpenJob: (id: string) => void;
}

type Filter = 'all' | 'errors' | 'warnings';
type DockTab = 'timeline' | 'data' | 'raw';

function formatTime(value?: string) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function duration(run: Run) {
  const start = Date.parse(run.startedAt);
  const end = run.finishedAt ? Date.parse(run.finishedAt) : 0;
  if (!start || !end || end < start) return '—';
  const ms = end - start;
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

function formatDurationMs(ms?: number) {
  if (!ms || ms < 0) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

function pretty(value: unknown) {
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function durationLabel(value?: number) {
  return value === undefined ? '—' : formatDurationMs(value);
}

function traceData(event: RunEvent | null, key: 'request' | 'response') {
  const value = event?.data?.[key];
  return value === undefined ? undefined : value;
}

function isTerminal(status: string) {
  return ['done', 'failed', 'cancelled'].includes(status);
}

function latestNodeEvent(events: RunEvent[], nodeId: string): RunEvent | null {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    if (events[index].nodeId === nodeId) return events[index];
  }
  return null;
}

function collectPaths(value: unknown, prefix = '$json', out: string[] = []): string[] {
  if (out.length >= 60 || value === null || typeof value !== 'object') return out;
  const entries = Array.isArray(value)
    ? value.slice(0, 10).map((item, index) => [String(index), item] as const)
    : Object.entries(value);
  for (const [key, child] of entries) {
    const path = Array.isArray(value)
      ? `${prefix}[${key}]`
      : /^[A-Za-z_$][\w$]*$/.test(key)
        ? `${prefix}.${key}`
        : `${prefix}[${JSON.stringify(key)}]`;
    out.push(path);
    collectPaths(child, path, out);
    if (out.length >= 60) break;
  }
  return out;
}

function JsonPaths({ value }: { value: unknown }) {
  const paths = useMemo(() => collectPaths(value), [value]);
  if (paths.length === 0) return null;
  const copy = (path: string) => {
    void navigator.clipboard?.writeText(`{{ ${path} }}`);
  };
  return (
    <details className="json-paths">
      <summary>{paths.length} expression paths</summary>
      <ul>
        {paths.map((path) => (
          <li key={path}>
            <code>{path}</code>
            <button
              type="button"
              className="icon-button"
              onClick={() => copy(path)}
              title="Copy expression"
            >
              Copy
            </button>
          </li>
        ))}
      </ul>
    </details>
  );
}

type TimelineStatus = 'running' | 'done' | 'failed' | 'waiting';

interface TimelineRow {
  nodeId: string;
  name: string;
  status: TimelineStatus;
  durationMs: number;
  error?: string;
  attempts: number;
  start: number;
}

function buildTimeline(events: RunEvent[], workflow: Workflow | null): TimelineRow[] {
  const rows = new Map<string, TimelineRow>();
  const ensure = (nodeId: string) => {
    let row = rows.get(nodeId);
    if (!row) {
      row = {
        nodeId,
        name: workflow?.nodes.find((node) => node.id === nodeId)?.name ?? nodeId,
        status: 'running',
        durationMs: 0,
        attempts: 1,
        start: 0,
      };
      rows.set(nodeId, row);
    }
    return row;
  };
  for (const event of events) {
    if (!event.nodeId || event.type.startsWith('item.')) continue;
    const row = ensure(event.nodeId);
    const at = Date.parse(event.at);
    if (Number.isFinite(at) && (!row.start || at < row.start)) row.start = at;
    switch (event.type) {
      case 'node.started':
        row.status = 'running';
        break;
      case 'node.retried':
        row.status = 'running';
        row.attempts = Math.max(row.attempts, event.attempt ?? row.attempts + 1);
        break;
      case 'node.finished':
        row.status = event.error ? 'failed' : 'done';
        row.error = event.error;
        row.durationMs = event.durationMs ?? row.durationMs;
        break;
      case 'node.failed':
        row.status = 'failed';
        row.error = event.error;
        row.durationMs = event.durationMs ?? row.durationMs;
        break;
      case 'run.waiting':
        row.status = 'waiting';
        break;
      default:
        break;
    }
  }
  return [...rows.values()].sort((a, b) => a.start - b.start || a.nodeId.localeCompare(b.nodeId));
}

const STATUS_LABEL: Record<string, string> = {
  queued: 'Queued',
  running: 'Running',
  waiting: 'Waiting',
  done: 'Complete',
  failed: 'Failed',
  cancelled: 'Cancelled',
  skipped: 'Skipped',
};

function readRunView(): { node: string | null; tab: DockTab } {
  const params = new URLSearchParams(window.location.search);
  const tab = params.get('tab');
  return {
    node: params.get('node'),
    tab: tab === 'data' || tab === 'raw' || tab === 'timeline' ? tab : 'raw',
  };
}

export function RunDetailPage({
  runId,
  summaries,
  defs,
  onBack,
  onOpenRun,
  onOpenTemplate,
  onOpenJob,
}: Props) {
  const [run, setRun] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [children, setChildren] = useState<Run[]>([]);
  const [job, setJob] = useState<Job | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState('');
  const [filter, setFilter] = useState<Filter>('all');
  const [query, setQuery] = useState('');
  const [dock, setDock] = useState<DockTab>(() => readRunView().tab);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(
    () => readRunView().node,
  );
  const [follow, setFollow] = useState(true);
  const [playback, setPlayback] = useState<number | null>(null);
  const [playing, setPlaying] = useState(false);
  const [now, setNow] = useState(Date.now());
  const [copied, setCopied] = useState(false);
  const [compare, setCompare] = useState<{
    open: boolean;
    loading: boolean;
    runs: Run[];
    against: string;
    diff: RunDiff | null;
    error: string;
  }>({ open: false, loading: false, runs: [], against: '', diff: null, error: '' });
  const timelineEndRef = useRef<HTMLDivElement | null>(null);
  const [selectedEvent, setSelectedEvent] = useState<RunEvent | null>(null);
  const [detail, setDetail] = useState<{
    loading: boolean;
    input?: unknown;
    output?: unknown;
    error?: string;
  }>({ loading: false });
  const [replay, setReplay] = useState<{ busy: boolean; result?: ReplayResult; error?: string }>({
    busy: false,
  });

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [nextRun, eventResult, childResult, nextWorkflow] = await Promise.all([
        api.getRun(runId),
        api.listEvents(runId),
        api.listChildRuns(runId, 20),
        api.getRunWorkflow(runId).catch(() => null),
      ]);
      setRun(nextRun);
      setEvents(eventResult.events ?? []);
      setChildren(childResult.runs ?? []);
      setWorkflow(nextWorkflow);
      if (nextRun.jobId) {
        try {
          const jobs = await api.listJobs();
          setJob((jobs.jobs ?? []).find((item) => item.id === nextRun.jobId) ?? null);
        } catch {
          setJob(null);
        }
      } else {
        setJob(null);
      }
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setLoading(false);
    }
  }, [runId]);

  useEffect(() => {
    const view = readRunView();
    setSelectedEvent(null);
    setSelectedNodeId(view.node);
    setDock(view.tab);
    setPlayback(null);
    setPlaying(false);
    setReplay({ busy: false });
    void load();
  }, [load]);

  useEffect(() => {
    if (!run || isTerminal(run.status)) return;
    const controller = new AbortController();
    let attempt = 0;
    const connect = async (): Promise<void> => {
      try {
        await api.streamRun(
          runId,
          (event) => {
            setEvents((previous) =>
              previous.some((item) => item.seq === event.seq && item.type === event.type)
                ? previous
                : [...previous, event],
            );
            if (['run.finished', 'run.failed', 'run.cancelled', 'run.waiting'].includes(event.type)) {
              void load();
            }
          },
          controller.signal,
        );
        attempt = 0;
      } catch (reason) {
        if (controller.signal.aborted) return;
        attempt += 1;
        if (attempt > 3) {
          setError(reason instanceof Error ? reason.message : String(reason));
          return;
        }
        await new Promise((resolve) =>
          window.setTimeout(resolve, Math.min(4000, 500 * 2 ** attempt)),
        );
        if (!controller.signal.aborted) await connect();
      }
    };
    void connect();
    return () => controller.abort();
  }, [load, run, runId]);

  const runStart = run ? Date.parse(run.startedAt) : 0;
  const runEnd = run
    ? run.finishedAt
      ? Date.parse(run.finishedAt)
      : events.length > 0
        ? Date.parse(events[events.length - 1].at)
        : runStart
    : 0;
  const totalMs = Math.max(runEnd - runStart, 0);
  const playbackEvents = useMemo(() => {
    if (playback === null || !runStart) return events;
    return events.filter((event) => {
      const at = Date.parse(event.at);
      return !Number.isFinite(at) || at <= runStart + playback;
    });
  }, [events, playback, runStart]);
  const viewStatus =
    playback === null ? run?.status ?? '' : playback >= totalMs ? run?.status ?? 'running' : 'running';
  const statuses = useMemo(
    () =>
      deriveNodeStatuses(
        playbackEvents,
        workflow?.nodes.map((node) => node.id),
        viewStatus,
      ),
    [playbackEvents, viewStatus, workflow],
  );
  const timeline = useMemo(() => buildTimeline(playbackEvents, workflow), [playbackEvents, workflow]);
  const fullTimeline = useMemo(() => buildTimeline(events, workflow), [events, workflow]);
  const playhead = playback ?? totalMs;
  const playbackActive = isTerminal(run?.status ?? '') && totalMs > 0 && fullTimeline.length > 0;

  useEffect(() => {
    if (!playing) return;
    const timer = window.setInterval(() => {
      setPlayback((current) => Math.min(totalMs, (current ?? 0) + Math.max(totalMs / 100, 1)));
    }, 80);
    return () => window.clearInterval(timer);
  }, [playing, totalMs]);

  useEffect(() => {
    if (playing && playback !== null && playback >= totalMs) setPlaying(false);
  }, [playback, playing, totalMs]);

  useEffect(() => {
    if (!run || isTerminal(run.status)) return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [run]);
  const selectedNodeEvent = useMemo(
    () => (selectedNodeId ? latestNodeEvent(events, selectedNodeId) : null),
    [events, selectedNodeId],
  );
  const activeEvent = selectedEvent ?? selectedNodeEvent;
  const selectedNode = workflow?.nodes.find((node) => node.id === selectedNodeId) ?? null;

  useEffect(() => {
    if (!selectedNodeId || selectedEvent?.nodeId === selectedNodeId) return;
    const event = latestNodeEvent(events, selectedNodeId);
    if (event) setSelectedEvent(event);
  }, [events, selectedEvent, selectedNodeId]);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (selectedNodeId) params.set('node', selectedNodeId);
    else params.delete('node');
    if (dock === 'timeline') params.delete('tab');
    else params.set('tab', dock);
    const queryString = params.toString();
    const next = `${window.location.pathname}${queryString ? `?${queryString}` : ''}`;
    if (`${window.location.pathname}${window.location.search}` !== next) {
      window.history.replaceState({}, '', next);
    }
  }, [dock, selectedNodeId]);

  useEffect(() => {
    if (!follow || dock !== 'timeline') return;
    timelineEndRef.current?.scrollIntoView({ block: 'nearest' });
  }, [dock, events.length, follow]);

  useEffect(() => {
    const event = activeEvent;
    if (!event) {
      setDetail({ loading: false });
      return;
    }
    let cancelled = false;
    setDetail({ loading: true });
    Promise.all([
      event.inputHash ? api.getBlob(event.inputHash) : Promise.resolve(undefined),
      event.outputHash ? api.getBlob(event.outputHash) : Promise.resolve(undefined),
    ])
      .then(([input, output]) => {
        if (!cancelled) setDetail({ loading: false, input, output });
      })
      .catch((reason: Error) => {
        if (!cancelled) setDetail({ loading: false, error: reason.message });
      });
    return () => {
      cancelled = true;
    };
  }, [activeEvent]);

  const workflowName = useMemo(() => {
    if (!run) return '';
    return summaries.find((summary) => summary.id === run.workflowId)?.name ?? run.workflowId;
  }, [run, summaries]);

  const visibleEvents = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return events.filter((event) => {
      if (filter === 'errors' && !event.error && !event.type.includes('failed')) return false;
      if (filter === 'warnings' && event.type !== 'run.waiting') return false;
      if (!needle) return true;
      return (
        event.type.toLowerCase().includes(needle) ||
        (event.nodeId ?? '').toLowerCase().includes(needle) ||
        (event.error ?? '').toLowerCase().includes(needle)
      );
    });
  }, [events, filter, query]);

  const failedNode = useMemo(
    () => [...events].reverse().find((event) => event.type === 'node.failed' && event.nodeId),
    [events],
  );

  const nodeName = useCallback(
    (nodeId?: string) =>
      workflow?.nodes.find((node) => node.id === nodeId)?.name ?? nodeId ?? 'Node',
    [workflow],
  );

  const selectNode = useCallback(
    (nodeId: string | null) => {
      setSelectedNodeId(nodeId);
      if (nodeId) {
        setSelectedEvent(latestNodeEvent(events, nodeId));
        setDock('data');
      } else {
        setSelectedEvent(null);
      }
    },
    [events],
  );

  const selectEvent = (event: RunEvent) => {
    setSelectedEvent(event);
    setSelectedNodeId(event.nodeId ?? null);
    setDock('data');
  };

  const runAction = async (label: string, action: () => Promise<void>) => {
    if (busy) return;
    setBusy(label);
    setError('');
    try {
      await action();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const togglePlayback = () => {
    if (playing) {
      setPlaying(false);
      return;
    }
    setPlayback(playhead >= totalMs ? 0 : playhead);
    setPlaying(true);
  };

  const loadCompare = async (against: string) => {
    if (!against) {
      setCompare((current) => ({
        ...current,
        against: '',
        diff: null,
        loading: false,
        error: '',
      }));
      return;
    }
    setCompare((current) => ({
      ...current,
      against,
      diff: null,
      loading: true,
      error: '',
    }));
    try {
      const diff = await api.diffRuns(runId, against);
      setCompare((current) => ({ ...current, diff, loading: false }));
    } catch (reason) {
      setCompare((current) => ({
        ...current,
        loading: false,
        error: reason instanceof Error ? reason.message : String(reason),
      }));
    }
  };

  const openCompare = async () => {
    if (!run) return;
    setCompare({
      open: true,
      loading: true,
      runs: [],
      against: '',
      diff: null,
      error: '',
    });
    try {
      const result = await api.listRuns(run.workflowId, 50);
      const candidates = (result.runs ?? []).filter(
        (candidate) =>
          candidate.id !== run.id &&
          !candidate.parentRunId &&
          candidate.status !== 'running' &&
          candidate.status !== 'waiting',
      );
      const against = candidates[0]?.id ?? '';
      setCompare((current) => ({
        ...current,
        loading: false,
        runs: candidates,
        against,
      }));
      if (against) void loadCompare(against);
    } catch (reason) {
      setCompare((current) => ({
        ...current,
        loading: false,
        error: reason instanceof Error ? reason.message : String(reason),
      }));
    }
  };

  const copyLog = async () => {
    try {
      await navigator.clipboard.writeText(
        visibleEvents.map((event) => JSON.stringify(event)).join('\n'),
      );
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  };

  const downloadLog = () => {
    const blob = new Blob(
      [visibleEvents.map((event) => JSON.stringify(event)).join('\n')],
      { type: 'application/x-ndjson;charset=utf-8' },
    );
    const url = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = url;
    link.download = `${runId}-events.ndjson`;
    document.body.appendChild(link);
    link.click();
    link.remove();
    window.setTimeout(() => URL.revokeObjectURL(url), 10000);
  };

  const cancel = () =>
    runAction('cancel', async () => {
      if (!run) return;
      await api.cancelRun(run.id);
      await load();
    });

  const resume = (decision: 'approved' | 'rejected') =>
    runAction(decision, async () => {
      if (!run) return;
      await api.resumeRun(run.id, decision);
      await load();
    });

  const retry = () =>
    runAction('retry', async () => {
      if (!run || !failedNode?.nodeId) return;
      const result = await api.replayRun(run.id, 'from', failedNode.nodeId);
      onOpenRun(result.runId);
    });

  const replayFrom = (event: RunEvent) =>
    runAction('replay', async () => {
      if (!run || !event.nodeId) return;
      if (
        !window.confirm(
          `Re-run ${event.nodeId} and everything downstream as a new run? Side effects will repeat.`,
        )
      ) {
        return;
      }
      const result = await api.replayRun(run.id, 'from', event.nodeId);
      setReplay({ busy: false, result });
      onOpenRun(result.runId);
    });

  const replayStep = (event: RunEvent) =>
    runAction('replay', async () => {
      if (!run || !event.nodeId) return;
      if (
        !window.confirm(
          `Re-run ${event.nodeId} in place? Side effects will repeat and this run log is unchanged.`,
        )
      ) {
        return;
      }
      const result = await api.replayRun(run.id, 'step', event.nodeId);
      setReplay({ busy: false, result });
    });

  if (loading && !run) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <p className="muted pad">Loading run…</p>
      </main>
    );
  }

  if (!run) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <header className="page-head">
          <div>
            <button className="breadcrumb-button" onClick={onBack}>
              ← Runs
            </button>
            <p className="eyebrow">Execution</p>
            <h1>Run not found</h1>
          </div>
        </header>
        {error && <p className="banner error">{error}</p>}
      </main>
    );
  }

  const request = traceData(activeEvent, 'request');
  const response = traceData(activeEvent, 'response');
  const finished = new Set(
    events.filter((event) => event.type === 'node.finished').map((event) => event.nodeId),
  ).size;
  const failed = new Set(
    events.filter((event) => event.type === 'node.failed').map((event) => event.nodeId),
  ).size;
  const active = ['queued', 'running', 'waiting'].includes(run.status);
  const slowest = fullTimeline.reduce((max, row) => Math.max(max, row.durationMs), 0);
  const retries = fullTimeline.reduce((total, row) => total + Math.max(0, row.attempts - 1), 0);
  const activeStatus = selectedNodeId ? statuses[selectedNodeId] : undefined;

  return (
    <main className="page-shell run-detail-page" id="main-stage" tabIndex={-1}>
      <header className="page-head run-detail-head">
        <div>
          <button className="breadcrumb-button" onClick={onBack}>
            ← Runs
          </button>
          <p className="eyebrow">Execution</p>
          <h1>
            Run <code>{run.id.split('_').pop()}</code>
          </h1>
          <p className="lede">
            {workflowName} · {run.trigger} · {formatTime(run.startedAt)}
            {job ? ` · ${job.name}` : ''}
          </p>
        </div>
        <div className="job-actions">
          <button className="button subtle" onClick={() => void load()} disabled={Boolean(busy)}>
            Refresh
          </button>
          {run.status === 'waiting' && (
            <>
              <button
                className="button subtle"
                onClick={() => void resume('rejected')}
                disabled={Boolean(busy)}
              >
                Reject
              </button>
              <button
                className="button primary"
                onClick={() => void resume('approved')}
                disabled={Boolean(busy)}
              >
                Approve
              </button>
            </>
          )}
          {active && run.status !== 'waiting' && (
            <button className="button subtle" onClick={() => void cancel()} disabled={Boolean(busy)}>
              Cancel
            </button>
          )}
          {run.status === 'failed' && failedNode && (
            <button className="button primary" onClick={() => void retry()} disabled={Boolean(busy)}>
              Retry failed node
            </button>
          )}
          <button className="button subtle" onClick={() => onOpenTemplate(run.workflowId)}>
            Open template
          </button>
          <button className="button subtle" onClick={() => void openCompare()}>
            Compare
          </button>
          {run.jobId && (
            <button className="button subtle" onClick={() => onOpenJob(run.jobId!)}>
              Open job
            </button>
          )}
        </div>
      </header>

      {error && <p className="banner error">{error}</p>}

      {failedNode && (
        <section className="run-failure-card" role="alert">
          <div className="run-failure-main">
            <span className="status-dot status-failed" aria-hidden="true" />
            <div>
              <p className="eyebrow">Failure</p>
              <strong>{nodeName(failedNode.nodeId)} failed</strong>
              <p>{failedNode.error || run.error || 'The run stopped at this node.'}</p>
            </div>
          </div>
          <div className="run-failure-actions">
            <button
              className="button subtle"
              onClick={() => selectNode(failedNode.nodeId ?? null)}
            >
              View data
            </button>
            <button className="button primary" onClick={() => void retry()} disabled={Boolean(busy)}>
              Retry from here
            </button>
          </div>
        </section>
      )}

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
            {finished} finished{failed ? ` · ${failed} failed` : ''}
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
          <strong>{run.templateVersion ? `v${run.templateVersion}` : run.workflowVersion || '—'}</strong>
        </div>
      </section>

      <div className="run-view">
        <section className="run-canvas-panel" aria-label="Execution canvas">
          <header className="run-canvas-head">
            <div>
              <p className="eyebrow">Execution</p>
              <h2>{workflow?.name ?? workflowName}</h2>
            </div>
            <div className="run-canvas-meta">
              <span className="muted small">
                {workflow ? `${workflow.nodes.length} nodes` : 'Loading graph…'}
              </span>
              <span className="version-chip">
                {run.templateVersion ? `v${run.templateVersion}` : 'snapshot'}
              </span>
            </div>
          </header>
          <div className="run-canvas-body">
            {workflow ? (
              <Canvas
                workflow={workflow}
                defs={defs}
                statuses={statuses}
                issues={[]}
                selectedId={selectedNodeId}
                onSelect={selectNode}
                onMove={() => undefined}
                onDeleteNodes={() => undefined}
                onDeleteEdges={() => undefined}
                onConnect={() => undefined}
                onAddNode={() => undefined}
                readOnly
              />
            ) : (
              <p className="muted small pad">The workflow snapshot for this run is unavailable.</p>
            )}
          </div>
          {playbackActive && (
            <div className="run-playback">
              <button
                type="button"
                className="icon-button"
                onClick={togglePlayback}
                aria-label={playing ? 'Pause playback' : 'Play run'}
              >
                {playing ? 'Pause' : 'Play'}
              </button>
              <input
                type="range"
                min={0}
                max={totalMs}
                step={Math.max(1, Math.round(totalMs / 200))}
                value={Math.min(playhead, totalMs)}
                aria-label="Playback position"
                onChange={(event) => {
                  setPlaying(false);
                  setPlayback(Number(event.target.value));
                }}
              />
              <span className="run-playback-time">
                {formatDurationMs(playhead)} / {formatDurationMs(totalMs)}
              </span>
              {playback !== null && (
                <button
                  type="button"
                  className="text-button"
                  onClick={() => {
                    setPlaying(false);
                    setPlayback(null);
                  }}
                >
                  Full run
                </button>
              )}
            </div>
          )}
        </section>

        <aside className="run-dock" aria-label="Run details">
          <div className="run-dock-tabs" role="tablist" aria-label="Run details">
            <button
              type="button"
              role="tab"
              aria-selected={dock === 'timeline'}
              className={dock === 'timeline' ? 'active' : ''}
              onClick={() => setDock('timeline')}
            >
              Timeline
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={dock === 'data'}
              className={dock === 'data' ? 'active' : ''}
              onClick={() => setDock('data')}
            >
              Data
            </button>
            <button
              type="button"
              role="tab"
              aria-selected={dock === 'raw'}
              className={dock === 'raw' ? 'active' : ''}
              onClick={() => setDock('raw')}
            >
              Raw events <span>{events.length}</span>
            </button>
          </div>

          <div className="run-dock-body">
            {dock === 'timeline' && (
              <>
                <div className="run-dock-toolbar">
                  <label className="run-follow">
                    <input
                      type="checkbox"
                      checked={follow}
                      onChange={(event) => setFollow(event.target.checked)}
                    />
                    Follow live
                  </label>
                  <span className="muted small">{timeline.length} nodes</span>
                </div>
                {timeline.length === 0 && (
                  <p className="muted small">Waiting for node events…</p>
                )}
                {timeline.map((row) => (
                  <button
                    type="button"
                    className={`timeline-row status-${row.status} ${
                      selectedNodeId === row.nodeId ? 'active' : ''
                    }`}
                    key={row.nodeId}
                    onClick={() => selectNode(row.nodeId)}
                  >
                    <span className={`status-dot status-${row.status}`} aria-hidden="true" />
                    <span className="timeline-main">
                      <strong>{row.name}</strong>
                      <em>
                        {row.nodeId}
                        {row.attempts > 1 ? ` · ${row.attempts} attempts` : ''}
                      </em>
                      {row.error && <span className="timeline-error">{row.error}</span>}
                    </span>
                    <span className="timeline-duration">
                      {formatDurationMs(
                        row.status === 'running' && row.start
                          ? Math.max(0, now - row.start)
                          : row.durationMs,
                      )}
                    </span>
                  </button>
                ))}
                <div ref={timelineEndRef} />
                <details className="run-meta-details">
                  <summary>Run details</summary>
                  <dl className="job-facts">
                    <div>
                      <dt>Run id</dt>
                      <dd>
                        <code>{run.id}</code>
                      </dd>
                    </div>
                    <div>
                      <dt>Template</dt>
                      <dd>
                        <button className="text-link" onClick={() => onOpenTemplate(run.workflowId)}>
                          {workflowName}
                        </button>
                      </dd>
                    </div>
                    {job && (
                      <div>
                        <dt>Job</dt>
                        <dd>
                          <button className="text-link" onClick={() => onOpenJob(job.id)}>
                            {job.name}
                          </button>
                        </dd>
                      </div>
                    )}
                    <div>
                      <dt>Started</dt>
                      <dd>{formatTime(run.startedAt)}</dd>
                    </div>
                    <div>
                      <dt>Finished</dt>
                      <dd>{formatTime(run.finishedAt)}</dd>
                    </div>
                    <div>
                      <dt>Duration</dt>
                      <dd>{duration(run)}</dd>
                    </div>
                  </dl>
                  {run.parentRunId && (
                    <button className="button subtle full" onClick={() => onOpenRun(run.parentRunId!)}>
                      Open parent run
                    </button>
                  )}
                  <div className="batch-aggregate">
                    <div className="batch-aggregate-head">
                      <span className="field-label">Batch aggregate · {children.length} runs</span>
                      <span className="muted small">
                        {children.filter((child) => child.status === 'done').length} done ·{' '}
                        {children.filter((child) => child.status === 'failed').length} failed ·{' '}
                        {children.filter((child) => child.status === 'running').length} running
                      </span>
                    </div>
                    <div className="batch-grid">
                      {children.map((child) => (
                        <button
                          type="button"
                          key={child.id}
                          className={`batch-cell status-${child.status}`}
                          title={`${child.status} · ${child.id}`}
                          onClick={() => onOpenRun(child.id)}
                        />
                      ))}
                    </div>
                  </div>
                  <div className="run-children">
                    <span className="field-label">Child runs · {children.length}</span>
                    {children.length === 0 && <p className="muted small">No child runs.</p>}
                    {children.map((child) => (
                      <button className="runs-row" key={child.id} onClick={() => onOpenRun(child.id)}>
                        <span className={`status-dot status-${child.status}`} aria-hidden="true" />
                        <span className="runs-row-main">
                          <strong>{child.status}</strong>
                          <span>
                            {child.parentNodeId || 'subflow'} · {formatTime(child.startedAt)}
                          </span>
                        </span>
                        <span className="runs-row-meta">
                          <code>{child.id.split('_').pop()}</code>
                        </span>
                      </button>
                    ))}
                  </div>
                </details>
              </>
            )}

            {dock === 'data' && (
              <>
                {!activeEvent && !selectedNode && (
                  <p className="muted small">
                    Select a node on the canvas or a step in the timeline to inspect its data.
                  </p>
                )}
                {(activeEvent || selectedNode) && (
                  <div className="event-detail" role="region" aria-label="Step data">
                    <div className="run-node-head">
                      <div>
                        <p className="eyebrow">Node</p>
                        <h3>{selectedNode?.name ?? nodeName(activeEvent?.nodeId)}</h3>
                        <code>{selectedNode?.type ?? activeEvent?.type ?? 'run'}</code>
                      </div>
                      <div className="run-node-head-actions">
                        {activeStatus && (
                          <span className={`status-text status-${activeStatus}`}>
                            {STATUS_LABEL[activeStatus] ?? activeStatus}
                          </span>
                        )}
                        <button
                          type="button"
                          className="icon-button"
                          onClick={() => {
                            setSelectedEvent(null);
                            setSelectedNodeId(null);
                            setDock('raw');
                          }}
                          aria-label="Close step data"
                        >
                          ×
                        </button>
                      </div>
                    </div>
                    {activeEvent?.error && <p className="banner error">{activeEvent.error}</p>}
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
                        <JsonPaths value={detail.output} />
                      </div>
                    )}
                    {activeEvent &&
                      (activeEvent.type === 'node.finished' || activeEvent.type === 'node.failed') &&
                      activeEvent.nodeId && (
                        <div className="event-detail-actions">
                          {activeEvent.type === 'node.finished' && (
                            <button
                              className="button subtle"
                              onClick={() => void replayStep(activeEvent)}
                              disabled={replay.busy || Boolean(busy)}
                            >
                              Replay step
                            </button>
                          )}
                          <button
                            className={`button ${
                              activeEvent.type === 'node.failed' ? 'primary' : 'subtle'
                            }`}
                            onClick={() => void replayFrom(activeEvent)}
                            disabled={replay.busy || Boolean(busy)}
                          >
                            {activeEvent.type === 'node.failed'
                              ? 'Retry from failed node'
                              : 'Replay from here'}
                          </button>
                        </div>
                      )}
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
                            : `Replay started as run ${replay.result.runId.split('_').pop()}.`}
                        </strong>
                      </div>
                    )}
                  </div>
                )}
              </>
            )}

            {dock === 'raw' && (
              <>
                <div className="run-log-toolbar">
                  <label className="field inline-field" htmlFor="run-log-filter">
                    <span className="field-label">Filter</span>
                    <select
                      id="run-log-filter"
                      className="text-input"
                      value={filter}
                      onChange={(event) => setFilter(event.target.value as Filter)}
                    >
                      <option value="all">All events</option>
                      <option value="errors">Errors</option>
                      <option value="warnings">Waiting</option>
                    </select>
                  </label>
                  <label className="field inline-field run-log-search" htmlFor="run-log-search">
                    <span className="field-label">Search</span>
                    <input
                      id="run-log-search"
                      className="text-input"
                      value={query}
                      placeholder="Node or message"
                      onChange={(event) => setQuery(event.target.value)}
                    />
                  </label>
                  <div className="run-log-actions">
                    <button
                      type="button"
                      className="text-button"
                      onClick={() => void copyLog()}
                      disabled={visibleEvents.length === 0}
                    >
                      {copied ? 'Copied' : 'Copy'}
                    </button>
                    <button
                      type="button"
                      className="text-button"
                      onClick={downloadLog}
                      disabled={visibleEvents.length === 0}
                    >
                      Download
                    </button>
                  </div>
                </div>
                {run.error && <p className="banner error">{run.error}</p>}
                <div className="event-list run-event-list">
                  {visibleEvents.length === 0 && (
                    <p className="muted small">No events match this view.</p>
                  )}
                  {visibleEvents.map((event) => (
                    <div
                      role="button"
                      tabIndex={0}
                      className={`event-row event-${event.type.replace('.', '-')} ${
                        selectedEvent?.seq === event.seq ? 'active' : ''
                      }`}
                      key={`${event.seq}-${event.type}`}
                      onClick={() => selectEvent(event)}
                      onKeyDown={(e) => {
                        if (e.key === 'Enter' || e.key === ' ') {
                          e.preventDefault();
                          selectEvent(event);
                        }
                      }}
                    >
                      <span className="event-seq">{event.seq}</span>
                      <span>
                        <strong>{event.type}</strong>
                        {event.nodeId && <span className="event-node">{event.nodeId}</span>}
                        {event.error && <span className="event-error">{event.error}</span>}
                      </span>
                      <span className="event-time">
                        {event.durationMs != null ? `${event.durationMs} ms` : formatTime(event.at)}
                      </span>
                      {(event.inputHash || event.outputHash) && (
                        <button
                          type="button"
                          className={`event-inspect ${selectedEvent?.seq === event.seq ? 'active' : ''}`}
                          onClick={(e) => {
                            e.stopPropagation();
                            selectEvent(event);
                          }}
                          aria-expanded={selectedEvent?.seq === event.seq}
                          aria-label={`Inspect ${event.type}${event.nodeId ? ` for ${event.nodeId}` : ''}`}
                        >
                          Inspect
                        </button>
                      )}
                    </div>
                  ))}
                </div>
              </>
            )}
          </div>
        </aside>
      </div>

      {compare.open && (
        <div
          className="overlay"
          onMouseDown={(event) => {
            if (event.target === event.currentTarget) {
              setCompare((current) => ({ ...current, open: false }));
            }
          }}
        >
          <div
            className="dialog run-compare-dialog"
            role="dialog"
            aria-modal="true"
            aria-label="Compare runs"
          >
            <header className="run-compare-head">
              <div>
                <p className="eyebrow">Compare</p>
                <h2>Run differences</h2>
              </div>
              <button
                type="button"
                className="icon-button"
                onClick={() => setCompare((current) => ({ ...current, open: false }))}
              >
                Close
              </button>
            </header>
            <p className="muted small">
              Compare this run against another completed run of the same workflow.
            </p>
            {compare.loading && <p className="muted small">Loading runs…</p>}
            {compare.error && <p className="banner error">{compare.error}</p>}
            {!compare.loading && compare.runs.length === 0 && (
              <p className="muted small">No other completed runs yet.</p>
            )}
            {compare.runs.length > 0 && (
              <label className="field" htmlFor="compare-run">
                <span className="field-label">Against</span>
                <select
                  id="compare-run"
                  className="text-input"
                  value={compare.against}
                  onChange={(event) => void loadCompare(event.target.value)}
                >
                  {compare.runs.map((candidate) => (
                    <option key={candidate.id} value={candidate.id}>
                      {candidate.id} · {candidate.status} · {formatTime(candidate.startedAt)}
                    </option>
                  ))}
                </select>
              </label>
            )}
            {compare.diff && (
              <div className="run-diff" role="region" aria-label="Run diff">
                <div className="run-diff-head">
                  <strong>
                    {compare.diff.changed
                      ? `${compare.diff.changed} changed`
                      : 'No differences'}
                  </strong>
                  <span className="muted small">{compare.diff.nodes.length} nodes compared</span>
                </div>
                <div className="diff-rows">
                  {compare.diff.nodes.map((node) => {
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
          </div>
        </div>
      )}
    </main>
  );
}
