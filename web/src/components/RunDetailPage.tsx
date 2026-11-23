import { AssetLoadingState } from './assets/AssetState';
import { ActionError } from './assets/ActionError';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api } from '../api';
import { RunCompareDialog } from './RunCompareDialog';
import { RunDock } from './RunDock';
import { RunSummaryStrip } from './RunSummaryStrip';
import { deriveNodeStatuses } from '../runState';
import {
  Filter,
  DockTab,
  pretty,
  traceData,
  readDecisionData,
  isTerminal,
  latestNodeEvent,
  buildTimeline,
  readRunView,
} from './runDetailHelpers';
import { useRunDetailData } from './runDetail/useRunDetailData';
import { useRunCompare } from './runDetail/useRunCompare';
import { useDecisionPanel } from './runDetail/useDecisionPanel';
import { useEventDetail } from './runDetail/useEventDetail';
import { useReplayActions } from './runDetail/useReplayActions';
import { RunDetailHeader } from './runDetail/RunDetailHeader';
import { RunFailureCard } from './runDetail/RunFailureCard';
import { RunCanvasPanel } from './runDetail/RunCanvasPanel';
import { RunDecisionSummary } from './runDetail/RunDecisionSummary';
import type { NodeDef, RunEvent, WorkflowSummary } from '../types';

interface Props {
  runId: string;
  summaries: WorkflowSummary[];
  defs: Record<string, NodeDef>;
  onBack: () => void;
  onOpenRun: (runId: string) => void;
  onOpenTemplate: (id: string) => void;
  onOpenJob: (id: string) => void;
  onWaitingChanged?: () => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}

export function RunDetailPage({
  runId,
  summaries,
  defs,
  onBack,
  onOpenRun,
  onOpenTemplate,
  onOpenJob,
  onWaitingChanged,
  onNotify,
}: Props) {
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
  const timelineEndRef = useRef<HTMLDivElement | null>(null);
  const [selectedEvent, setSelectedEvent] = useState<RunEvent | null>(null);

  // L3: below 720px the run graph becomes a step list; the same query
  // width drives the swap so CSS and markup can never disagree.
  const [phoneViewport, setPhoneViewport] = useState(
    () => typeof window !== 'undefined' && window.matchMedia('(max-width: 720px)').matches,
  );
  useEffect(() => {
    const query = window.matchMedia('(max-width: 720px)');
    const onChange = (event: MediaQueryListEvent) => setPhoneViewport(event.matches);
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);

  const resetView = useCallback(() => {
    const view = readRunView();
    setSelectedEvent(null);
    setSelectedNodeId(view.node);
    setDock(view.tab);
    setPlayback(null);
    setPlaying(false);
  }, []);

  const {
    run,
    events,
    workflow,
    children,
    job,
    loading,
    error,
    setError,
    load,
  } = useRunDetailData(runId, resetView);

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

  const { detail, inputDraft, setInputDraft } = useEventDetail(runId, activeEvent);

  const workflowName = useMemo(() => {
    if (!run) return '';
    return summaries.find((summary) => summary.id === run.workflowId)?.name ?? run.workflowId;
  }, [run, summaries]);

  const visibleEvents = useMemo(() => {
    const needle = query.trim().toLowerCase();
    return events.filter((event) => {
      if (filter === 'errors' && !event.error && !event.type.includes('failed')) return false;
      if (
        filter === 'warnings' &&
        event.type !== 'run.waiting' &&
        event.type !== 'node.effects.drift'
      )
        return false;
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

  const { compare, setCompare, compareRef, loadCompare, openCompare } = useRunCompare(runId, run);

  const decision = useDecisionPanel({ run, busy, runAction, reload: load, onWaitingChanged });

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

  const retry = () =>
    runAction('retry', async () => {
      if (!run || !failedNode?.nodeId) return;
      const result = await api.replayRun(run.id, 'from', failedNode.nodeId);
      onOpenRun(result.runId);
    });

  // currentInput resolves the edited input for replay actions: undefined
  // means "use the recorded input" (no override), null means invalid JSON.
  const currentInput = (): Record<string, unknown> | undefined | null => {
    if (detail.input === undefined) return undefined;
    const text = inputDraft !== null ? inputDraft : pretty(detail.input);
    if (!text.trim()) return undefined;
    try {
      const parsed = JSON.parse(text) as unknown;
      if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) return null;
      return parsed as Record<string, unknown>;
    } catch {
      return null;
    }
  };

  const { replay, hermetic, setHermetic, replayFrom, replayStep, replayStepLive } =
    useReplayActions({ run, runAction, onOpenRun, currentInput, resetKey: runId });

  if (loading && !run) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <AssetLoadingState label="Loading run…" className="muted pad" />
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
            <h1>Run not found</h1>
          </div>
        </header>
        {error && (
          <ActionError testId="run-detail-error">{error}</ActionError>
        )}
      </main>
    );
  }

  const request = traceData(activeEvent, 'request');
  const response = traceData(activeEvent, 'response');
  const replayIsStep =
    replay.result?.mode === 'step' || replay.result?.mode === 'step-execute';
  const finished = new Set(
    events.filter((event) => event.type === 'node.finished').map((event) => event.nodeId),
  ).size;
  const failed = new Set(
    events.filter((event) => event.type === 'node.failed').map((event) => event.nodeId),
  ).size;
  const slowest = fullTimeline.reduce((max, row) => Math.max(max, row.durationMs), 0);
  const retries = fullTimeline.reduce((total, row) => total + Math.max(0, row.attempts - 1), 0);
  const activeStatus = selectedNodeId ? statuses[selectedNodeId] : undefined;
  const decisionEvents = events
    .map((event) => ({ event, data: readDecisionData(event) }))
    .filter(
      (item): item is {
        event: RunEvent;
        data: { decision: string; note: string; actor: string; source: string };
      } => item.data !== null,
    );
  const latestDecision =
    decisionEvents.length > 0 ? decisionEvents[decisionEvents.length - 1] : null;

  return (
    <main className="page-shell run-detail-page" id="main-stage" tabIndex={-1}>
      <RunDetailHeader
        run={run}
        job={job}
        workflowName={workflowName}
        busy={busy}
        now={now}
        onBack={onBack}
        onRefresh={() => void load()}
        onCancel={() => void cancel()}
        onRetry={() => void retry()}
        onOpenTemplate={onOpenTemplate}
        onOpenJob={onOpenJob}
        onCompare={() => void openCompare()}
        onNotify={onNotify}
        retryOffered={run.status === 'failed' && Boolean(failedNode)}
        decision={{
          panel: decision.decisionPanel,
          note: decision.decisionNote,
          error: decision.decisionError,
          noteRef: decision.decisionNoteRef,
          dialogRef: decision.decisionRef,
          onNoteChange: (value) => {
            decision.setDecisionNote(value);
            decision.setDecisionError('');
          },
          onOpen: decision.openDecision,
          onClose: decision.closeDecision,
          onSubmit: (next) => decision.submitDecision(next),
        }}
      />

      {latestDecision && <RunDecisionSummary decision={latestDecision} />}

      {error && <ActionError>{error}</ActionError>}

      {failedNode && (
        <RunFailureCard
          nodeName={nodeName(failedNode.nodeId)}
          error={failedNode.error || run.error || 'The run stopped at this node.'}
          busy={busy}
          onViewData={() => selectNode(failedNode.nodeId ?? null)}
          onRetry={() => void retry()}
        />
      )}

      <RunSummaryStrip
        run={run}
        finished={finished}
        failed={failed}
        slowest={slowest}
        retries={retries}
      />

      <div className="run-view">
        <RunCanvasPanel
          workflow={workflow}
          workflowName={workflowName}
          defs={defs}
          statuses={statuses}
          selectedNodeId={selectedNodeId}
          templateVersion={run.templateVersion}
          phone={phoneViewport}
          timeline={timeline}
          now={now}
          onSelect={selectNode}
          playback={{
            active: playbackActive,
            playing,
            playhead,
            totalMs,
            scrubbing: playback !== null,
            onToggle: togglePlayback,
            onScrub: (value) => {
              setPlaying(false);
              setPlayback(value);
            },
            onFullRun: () => {
              setPlaying(false);
              setPlayback(null);
            },
          }}
        />

        <RunDock
          dock={dock}
          setDock={setDock}
          events={events}
          follow={follow}
          setFollow={setFollow}
          timeline={timeline}
          decisionEvents={decisionEvents}
          selectedNodeId={selectedNodeId}
          selectNode={selectNode}
          timelineEndRef={timelineEndRef}
          run={run}
          workflowName={workflowName}
          job={job}
          children={children}
          now={now}
          onOpenTemplate={onOpenTemplate}
          onOpenJob={onOpenJob}
          onOpenRun={onOpenRun}
          selectedEvent={selectedEvent}
          setSelectedEvent={setSelectedEvent}
          setSelectedNodeId={setSelectedNodeId}
          activeEvent={activeEvent}
          selectedNode={selectedNode}
          nodeName={nodeName}
          activeStatus={activeStatus}
          detail={detail}
          request={request}
          response={response}
          inputDraft={inputDraft}
          setInputDraft={setInputDraft}
          replay={replay}
          busy={busy}
          hermetic={hermetic}
          setHermetic={setHermetic}
          replayStep={replayStep}
          replayStepLive={replayStepLive}
          replayFrom={replayFrom}
          replayIsStep={replayIsStep}
          filter={filter}
          setFilter={setFilter}
          query={query}
          setQuery={setQuery}
          copyLog={copyLog}
          copied={copied}
          downloadLog={downloadLog}
          visibleEvents={visibleEvents}
          selectEvent={selectEvent}
        />
      </div>

      <RunCompareDialog
        compare={compare}
        setCompare={setCompare}
        compareRef={compareRef}
        loadCompare={loadCompare}
      />
    </main>
  );
}
