import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { Connection } from '@xyflow/react';
import { api, ApiError, getToken, setToken } from './api';
import {
  defaultNodeName,
  edgeId,
  makeNode,
  nodeDefMap,
  outputType,
  parseHandle,
  portCompatibility,
} from './catalog';
import { Canvas } from './components/Canvas';
import { CommandPalette, type Command } from './components/CommandPalette';
import { CredentialsPage } from './components/CredentialsPage';
import {
  CreateJobPage,
  HomePage,
  JobDetailPage,
  JobsPage,
  RunsPage,
  TemplatesPage,
} from './components/DashboardPages';
import { Inspector } from './components/Inspector';
import { NodePanel } from './components/NodePanel';
import { RunBar } from './components/RunBar';
import { RunDetailPage } from './components/RunDetailPage';
import { TemplateViewPage } from './components/TemplateViewPage';
import { VersionHistory } from './components/VersionHistory';
import { RunHistory } from './components/RunHistory';
import type {
  Example,
  NodeDef,
  Run,
  RunEvent,
  ValidationIssue,
  Workflow,
  WorkflowState,
  WorkflowSummary,
} from './types';
import { validateWorkflow } from './validate';
import { deriveNodeStatuses, deriveRunStatus } from './runState';

type Phase = 'loading' | 'ready' | 'error';
type Toast = { message: string; tone: 'info' | 'error' };

function cloneWorkflow(workflow: Workflow): Workflow {
  return JSON.parse(JSON.stringify(workflow)) as Workflow;
}

function normalizeWorkflow(workflow: Workflow): Workflow {
  return {
    ...workflow,
    nodes: workflow.nodes ?? [],
    edges: workflow.edges ?? [],
    settings: workflow.settings ?? {},
  };
}

function formatCount(count: number, singular: string): string {
  return `${count} ${singular}${count === 1 ? '' : 's'}`;
}

type Route = {
  name:
    | 'home'
    | 'templates'
    | 'template'
    | 'jobs'
    | 'job-new'
    | 'job'
    | 'job-edit'
    | 'runs'
    | 'run'
    | 'credentials'
    | 'editor';
  id?: string;
};

function parseRoute(pathname = window.location.pathname): Route {
  if (pathname === '/' || pathname === '') return { name: 'home' };
  if (pathname === '/templates') return { name: 'templates' };
  if (pathname === '/jobs') return { name: 'jobs' };
  if (pathname === '/jobs/new') return { name: 'job-new' };
  if (pathname === '/credentials') return { name: 'credentials' };
  const jobEdit = pathname.match(/^\/jobs\/([^/]+)\/edit\/?$/);
  if (jobEdit) return { name: 'job-edit', id: decodeURIComponent(jobEdit[1]) };
  const job = pathname.match(/^\/jobs\/([^/]+)\/?$/);
  if (job) return { name: 'job', id: decodeURIComponent(job[1]) };
  if (pathname === '/runs') return { name: 'runs' };
  const run = pathname.match(/^\/runs\/([^/]+)\/?$/);
  if (run) return { name: 'run', id: decodeURIComponent(run[1]) };
  const editor = pathname.match(/^\/templates\/([^/]+)\/edit\/?$/);
  if (editor) return { name: 'editor', id: decodeURIComponent(editor[1]) };
  const template = pathname.match(/^\/templates\/([^/]+)\/?$/);
  if (template) return { name: 'template', id: decodeURIComponent(template[1]) };
  return { name: 'home' };
}

export default function App() {
  const [phase, setPhase] = useState<Phase>('loading');
  const [authRequired, setAuthRequired] = useState(false);
  const [authError, setAuthError] = useState('');
  const [bootError, setBootError] = useState('');
  const [defs, setDefs] = useState<NodeDef[]>([]);
  const [examples, setExamples] = useState<Example[]>([]);
  const [summaries, setSummaries] = useState<WorkflowSummary[]>([]);

  const [workflow, setWorkflowState] = useState<Workflow | null>(null);
  const workflowRef = useRef<Workflow | null>(null);
  const [dirty, setDirty] = useState(false);
  const [autosaving, setAutosaving] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const [undoStack, setUndoStack] = useState<Workflow[]>([]);
  const [redoStack, setRedoStack] = useState<Workflow[]>([]);
  const undoRef = useRef<Workflow[]>([]);
  const redoRef = useRef<Workflow[]>([]);
  const lastCommitRef = useRef<{ key: string; at: number } | null>(null);
  const skipPopRef = useRef(false);

  const [run, setRun] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [runId, setRunId] = useState<string | null>(null);

  const [saving, setSaving] = useState(false);
  const [starting, setStarting] = useState(false);
  const [busyNew, setBusyNew] = useState(false);
  const [runInputOpen, setRunInputOpen] = useState(false);
  const [runInput, setRunInput] = useState<Record<string, unknown>>({});
  const [runInputDraft, setRunInputDraft] = useState('{}');
  const [runInputError, setRunInputError] = useState('');

  const [paletteOpen, setPaletteOpen] = useState(false);
  const [versionsOpen, setVersionsOpen] = useState(false);
  const [historyOpen, setHistoryOpen] = useState(false);
  const [versionState, setVersionState] = useState<WorkflowState | null>(null);
  const [publishing, setPublishing] = useState(false);
  const [tokenOpen, setTokenOpen] = useState(false);
  const [tokenDraft, setTokenDraft] = useState(() => getToken());

  const [theme, setTheme] = useState<'light' | 'dark'>(() => {
    const saved = localStorage.getItem('loom.theme');
    if (saved === 'light' || saved === 'dark') return saved;
    return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
  });
  const [toast, setToast] = useState<Toast | null>(null);
  const [online, setOnline] = useState(() => typeof navigator === 'undefined' || navigator.onLine);
  const [route, setRoute] = useState<Route>(() => parseRoute());

  const defsByType = useMemo(() => nodeDefMap(defs), [defs]);

  const notify = useCallback((message: string, tone: 'info' | 'error' = 'info') => {
    setToast({ message, tone });
  }, []);

  const discardEditor = useCallback(() => {
    workflowRef.current = null;
    setWorkflowState(null);
    setSelectedId(null);
    setDirty(false);
    setRun(null);
    setEvents([]);
    setRunId(null);
    setVersionsOpen(false);
  }, []);

  const navigate = useCallback(
    (path: string, options?: { replace?: boolean; force?: boolean }) => {
      if (!options?.force && route.name === 'editor' && dirty) {
        if (!window.confirm('Discard unsaved changes and leave the editor?')) return false;
        discardEditor();
      }
      if (options?.replace) window.history.replaceState({}, '', path);
      else window.history.pushState({}, '', path);
      setRoute(parseRoute(path));
      return true;
    },
    [discardEditor, dirty, route.name],
  );

  useEffect(() => {
    const onPopState = () => {
      if (skipPopRef.current) {
        skipPopRef.current = false;
        setRoute(parseRoute());
        return;
      }
      if (route.name === 'editor' && dirty) {
        if (!window.confirm('Discard unsaved changes and leave the editor?')) {
          skipPopRef.current = true;
          window.history.go(1);
          return;
        }
        discardEditor();
      }
      setRoute(parseRoute());
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, [discardEditor, dirty, route.name]);

  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    return () => window.removeEventListener('beforeunload', onBeforeUnload);
  }, [dirty]);

  const checkConnection = useCallback(async () => {
    try {
      await api.healthz();
      setOnline(true);
    } catch {
      setOnline(false);
    }
  }, []);

  const refreshSummaries = useCallback(async () => {
    try {
      const result = await api.listWorkflows();
      setSummaries(result.workflows ?? []);
    } catch {
      // A stale switcher is harmless; the next boot or save retries.
    }
  }, []);

  const renameTemplate = useCallback(
    async (id: string, name: string) => {
      const current = await api.getWorkflow(id);
      await api.saveWorkflow({ ...current, name });
      await refreshSummaries();
      notify(`Renamed template to “${name}”.`);
    },
    [notify, refreshSummaries],
  );

  const deleteTemplate = useCallback(
    async (id: string) => {
      await api.deleteWorkflow(id);
      await refreshSummaries();
      notify('Template deleted.');
    },
    [notify, refreshSummaries],
  );

  const adoptWorkflow = useCallback((next: Workflow, markDirty = false) => {
    const normalized = normalizeWorkflow(next);
    workflowRef.current = normalized;
    setWorkflowState(normalized);
    setSelectedId(null);
    setDirty(markDirty);
    undoRef.current = [];
    redoRef.current = [];
    setUndoStack([]);
    setRedoStack([]);
    setRun(null);
    setEvents([]);
    setRunId(null);
    setVersionsOpen(false);
    lastCommitRef.current = null;
  }, []);

  useEffect(() => {
    if ((route.name !== 'editor' && route.name !== 'template') || !route.id) return;
    if (workflowRef.current?.id === route.id) return;
    let cancelled = false;
    api
      .getWorkflow(route.id)
      .then((next) => {
        if (!cancelled) adoptWorkflow(next);
      })
      .catch((error: Error) => {
        if (!cancelled) notify(error.message, 'error');
      });
    return () => {
      cancelled = true;
    };
  }, [adoptWorkflow, notify, route]);

  const refreshWorkflowState = useCallback(async (id: string) => {
    try {
      setVersionState(await api.getWorkflowState(id));
    } catch {
      setVersionState(null);
    }
  }, []);

  useEffect(() => {
    if (!workflow?.id) {
      setVersionState(null);
      return;
    }
    let cancelled = false;
    api
      .getWorkflowState(workflow.id)
      .then((state) => {
        if (!cancelled) setVersionState(state);
      })
      .catch(() => {
        if (!cancelled) setVersionState(null);
      });
    return () => {
      cancelled = true;
    };
  }, [workflow?.id]);

  const loadInitial = useCallback(async () => {
    setPhase('loading');
    setBootError('');
    try {
      const [nodeTypes, exampleList, workflowList] = await Promise.all([
        api.nodeTypes(),
        api.examples(),
        api.listWorkflows(),
      ]);
      setDefs(nodeTypes.nodeTypes ?? []);
      setExamples(exampleList.examples ?? []);
      setSummaries(workflowList.workflows ?? []);
      setAuthRequired(false);
      setAuthError('');
      const initialRoute = parseRoute();
      const requestedId =
        initialRoute.name === 'editor' || initialRoute.name === 'template'
          ? initialRoute.id
          : undefined;
      const workflowId = requestedId ?? workflowList.workflows?.[0]?.id;
      if ((initialRoute.name === 'editor' || initialRoute.name === 'template') && workflowId) {
        const selected = await api.getWorkflow(workflowId);
        adoptWorkflow(selected);
      } else {
        workflowRef.current = null;
        setWorkflowState(null);
        setDirty(false);
      }
      setPhase('ready');
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setAuthRequired(true);
        setAuthError('That token was rejected. Check the server token and try again.');
        setPhase('ready');
        return;
      }
      setBootError(error instanceof Error ? error.message : String(error));
      setPhase('error');
    }
  }, [adoptWorkflow]);

  useEffect(() => {
    const onConnectivity = (event: Event) => {
      const detail = (event as CustomEvent<{ online?: boolean }>).detail;
      if (typeof detail?.online === 'boolean') setOnline(detail.online);
    };
    const onAuthRequired = (event: Event) => {
      const detail = (event as CustomEvent<{ message?: string }>).detail;
      setAuthRequired(true);
      setAuthError(detail?.message || 'Authentication required. Please enter API token.');
      setPhase('ready');
    };
    const onOffline = () => setOnline(false);
    const onOnline = () => void checkConnection();
    window.addEventListener('loom:connectivity', onConnectivity);
    window.addEventListener('loom:auth-required', onAuthRequired);
    window.addEventListener('offline', onOffline);
    window.addEventListener('online', onOnline);
    return () => {
      window.removeEventListener('loom:connectivity', onConnectivity);
      window.removeEventListener('loom:auth-required', onAuthRequired);
      window.removeEventListener('offline', onOffline);
      window.removeEventListener('online', onOnline);
    };
  }, [checkConnection]);

  useEffect(() => {
    if (online) return;
    const timer = window.setInterval(() => void checkConnection(), 4000);
    return () => window.clearInterval(timer);
  }, [checkConnection, online]);

  useEffect(() => {
    void loadInitial();
  }, [loadInitial]);

  useEffect(() => {
    if (route.name === 'home' || route.name === 'templates') void refreshSummaries();
  }, [refreshSummaries, route.name]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('loom.theme', theme);
  }, [theme]);

  useEffect(() => {
    if (!toast) return;
    const timer = window.setTimeout(() => setToast(null), 3600);
    return () => window.clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    if (!tokenOpen) return;
    setTokenDraft(getToken());
  }, [tokenOpen]);

  const commit = useCallback((next: Workflow, record = true, coalesceKey?: string) => {
    const current = workflowRef.current;
    if (!current) return;
    const now = Date.now();
    const last = lastCommitRef.current;
    const coalesce =
      record && !!coalesceKey && !!last && last.key === coalesceKey && now - last.at < 700;
    if (record && !coalesce) {
      undoRef.current = [...undoRef.current.slice(-49), current];
      setUndoStack(undoRef.current);
    }
    lastCommitRef.current = coalesceKey ? { key: coalesceKey, at: now } : null;
    redoRef.current = [];
    setRedoStack([]);
    workflowRef.current = next;
    setWorkflowState(next);
    setDirty(true);
  }, []);

  const apply = useCallback(
    (mutator: (current: Workflow) => Workflow, record = true, coalesceKey?: string) => {
      const current = workflowRef.current;
      if (!current) return;
      commit(mutator(current), record, coalesceKey);
    },
    [commit],
  );

  const undo = useCallback(() => {
    const previous = undoRef.current[undoRef.current.length - 1];
    if (!previous) return;
    undoRef.current = undoRef.current.slice(0, -1);
    setUndoStack(undoRef.current);
    const current = workflowRef.current;
    if (current) {
      redoRef.current = [...redoRef.current, current];
      setRedoStack(redoRef.current);
    }
    workflowRef.current = previous;
    setWorkflowState(previous);
    setDirty(true);
    lastCommitRef.current = null;
    setSelectedId((id) => (id && previous.nodes.some((node) => node.id === id) ? id : null));
  }, []);

  const redo = useCallback(() => {
    const next = redoRef.current[redoRef.current.length - 1];
    if (!next) return;
    redoRef.current = redoRef.current.slice(0, -1);
    setRedoStack(redoRef.current);
    const current = workflowRef.current;
    if (current) {
      undoRef.current = [...undoRef.current, current];
      setUndoStack(undoRef.current);
    }
    workflowRef.current = next;
    setWorkflowState(next);
    setDirty(true);
    lastCommitRef.current = null;
    setSelectedId((id) => (id && next.nodes.some((node) => node.id === id) ? id : null));
  }, []);

  const save = useCallback(async (): Promise<boolean> => {
    const current = workflowRef.current;
    if (!current) return false;
    if (!online) {
      notify('Loom server is unreachable. Reconnect before saving.', 'error');
      return false;
    }
    const issues = validateWorkflow(current, defsByType);
    if (issues.some((issue) => issue.severity === 'error')) {
      setSelectedId(null);
      notify('Fix validation errors before saving.', 'error');
      return false;
    }
    setSaving(true);
    try {
      const saved = normalizeWorkflow(await api.saveWorkflow(current));
      workflowRef.current = saved;
      setWorkflowState(saved);
      setDirty(false);
      void refreshWorkflowState(saved.id);
      void refreshSummaries();
      notify('Workflow saved.');
      return true;
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
      return false;
    } finally {
      setSaving(false);
    }
  }, [defsByType, notify, online, refreshSummaries, refreshWorkflowState]);

  useEffect(() => {
    if (!dirty || !workflow || !online || saving) return;
    const current = workflow;
    if (validateWorkflow(current, defsByType).some((issue) => issue.severity === 'error')) return;
    const timer = window.setTimeout(() => {
      void (async () => {
        setAutosaving(true);
        try {
          const saved = normalizeWorkflow(await api.saveWorkflow(current));
          const latest = workflowRef.current;
          if (!latest || JSON.stringify(latest) !== JSON.stringify(current)) return;
          workflowRef.current = saved;
          setWorkflowState(saved);
          setDirty(false);
          void refreshWorkflowState(saved.id);
          void refreshSummaries();
        } catch {
          // Keep the draft dirty; a manual save will surface the error.
        } finally {
          setAutosaving(false);
        }
      })();
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [defsByType, dirty, online, refreshSummaries, refreshWorkflowState, saving, workflow]);

  const publish = useCallback(async () => {
    const current = workflowRef.current;
    if (!current || publishing) return;
    if (!online) {
      notify('Loom server is unreachable. Reconnect before publishing.', 'error');
      return;
    }
    const issues = validateWorkflow(current, defsByType);
    if (issues.some((issue) => issue.severity === 'error')) {
      setSelectedId(null);
      notify('Fix validation errors before publishing.', 'error');
      return;
    }
    setPublishing(true);
    try {
      if (dirty) {
        const saved = await save();
        if (!saved) return;
      }
      const result = await api.publishWorkflow(current.id);
      const published = normalizeWorkflow(result.workflow);
      workflowRef.current = published;
      setWorkflowState(published);
      setDirty(false);
      await refreshWorkflowState(published.id);
      void refreshSummaries();
      notify(`Published v${result.version.version}.`);
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setPublishing(false);
    }
  }, [
    defsByType,
    dirty,
    notify,
    online,
    publishing,
    refreshSummaries,
    refreshWorkflowState,
    save,
  ]);

  const startRun = useCallback(async (input: Record<string, unknown> = runInput) => {
    const current = workflowRef.current;
    if (!current || starting) return null;
    if (!online) {
      notify('Loom server is unreachable. Reconnect before running.', 'error');
      return null;
    }
    const issues = validateWorkflow(current, defsByType);
    if (issues.some((issue) => issue.severity === 'error')) {
      setSelectedId(null);
      notify('Fix validation errors before running.', 'error');
      return null;
    }
    setStarting(true);
    try {
      if (dirty) {
        const saved = await save();
        if (!saved) return null;
      }
      const started = await api.startRun(current.id, input);
      setEvents([]);
      setRun({
        id: started.runId,
        workflowId: current.id,
        workflowVersion: '',
        status: started.status,
        trigger: 'manual',
        startedAt: new Date().toISOString(),
      });
      setSelectedId(null);
      setRunId(started.runId);
      return started.runId;
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setStarting(false);
    }
    return null;
  }, [defsByType, dirty, notify, online, runInput, save, starting]);

  const retryFailedNode = useCallback(async () => {
    const failed = [...events].reverse().find((event) => event.type === 'node.failed' && event.nodeId);
    const sourceId = runId ?? run?.id ?? null;
    if (!sourceId || !failed?.nodeId || starting) return;
    if (
      !window.confirm(
        `Retry from failed node ${failed.nodeId}? This starts a new run; downstream side effects will repeat.`,
      )
    )
      return;
    setStarting(true);
    try {
      const result = await api.replayRun(sourceId, 'from', failed.nodeId);
      const retryId = result.runId;
      const [updatedRun, updatedEvents] = await Promise.all([
        api.getRun(retryId),
        api.listEvents(retryId),
      ]);
      setRun(updatedRun);
      const prevCompleted = events.filter((e) => e.type === 'node.finished' && !e.error);
      const retriedNodeIds = new Set((updatedEvents.events ?? []).map((e) => e.nodeId).filter(Boolean));
      const carried = prevCompleted.filter((e) => e.nodeId && !retriedNodeIds.has(e.nodeId));
      setEvents([...carried, ...(updatedEvents.events ?? [])]);
      setRunId(retryId);
      notify(
        result.status === 'done'
          ? `Retry completed from ${failed.nodeId} as run ${retryId}.`
          : `Retry ${result.status} as run ${retryId}.`,
        result.status === 'failed' ? 'error' : 'info',
      );
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setStarting(false);
    }
  }, [events, notify, run, runId, starting]);

  const openRunInput = useCallback(() => {
    setRunInputDraft(JSON.stringify(runInput, null, 2));
    setRunInputError('');
    setRunInputOpen(true);
  }, [runInput]);

  const submitRunInput = useCallback(() => {
    let parsed: unknown;
    try {
      parsed = JSON.parse(runInputDraft);
    } catch {
      setRunInputError('Enter a valid JSON object.');
      return;
    }
    if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
      setRunInputError('Run input must be a JSON object.');
      return;
    }
    const input = parsed as Record<string, unknown>;
    setRunInput(input);
    setRunInputOpen(false);
    void startRun(input);
  }, [runInputDraft, startRun]);

  useEffect(() => {
    if (!runId) return;
    const controller = new AbortController();
    let attempt = 0;
    const refresh = async () => {
      try {
        const latest = await api.getRun(runId);
        if (!controller.signal.aborted) setRun(latest);
      } catch {
        // Connectivity state and the offline banner cover this path.
      }
    };
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
              void refresh();
            }
          },
          controller.signal,
        );
        attempt = 0;
        await refresh();
      } catch (error) {
        if (controller.signal.aborted) return;
        if (error instanceof ApiError) {
          notify(`Live stream ended: ${error.message}`, 'error');
          await refresh();
          return;
        }
        attempt += 1;
        if (attempt === 1) notify('Live updates paused. Reconnecting…', 'error');
        if (attempt > 5) {
          notify('Live updates stopped. Reopen the run to retry.', 'error');
          await refresh();
          return;
        }
        await new Promise((resolve) =>
          window.setTimeout(resolve, Math.min(8000, 400 * 2 ** attempt)),
        );
        if (!controller.signal.aborted) await connect();
      }
    };
    void connect();
    return () => controller.abort();
  }, [notify, runId]);

  const cancelRun = useCallback(async () => {
    if (!run) return;
    try {
      await api.cancelRun(run.id);
      notify('Cancelling run…');
      if (!runId) {
        window.setTimeout(() => {
          Promise.all([api.getRun(run.id), api.listEvents(run.id)])
            .then(([latest, log]) => {
              setRun(latest);
              setEvents(log.events ?? []);
            })
            .catch(() => undefined);
        }, 600);
      }
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    }
  }, [notify, run, runId]);

  const resumeRun = useCallback(
    async (decision: 'approved' | 'rejected') => {
      if (!run) return;
      setStarting(true);
      try {
        const result = await api.resumeRun(run.id, decision);
        if (result.events) setEvents(result.events);
        const latest = await api.getRun(run.id);
        setRun(latest);
        notify(decision === 'approved' ? 'Approval recorded.' : 'Run rejected.');
      } catch (error) {
        notify(error instanceof Error ? error.message : String(error), 'error');
      } finally {
        setStarting(false);
      }
    },
    [notify, run],
  );

  const addNode = useCallback(
    (def: NodeDef, position?: { x: number; y: number }) => {
      const current = workflowRef.current;
      if (!current) return;
      const node = makeNode(def, current.nodes);
      if (position) node.position = position;
      commit({ ...current, nodes: [...current.nodes, node] });
      setSelectedId(node.id);
    },
    [commit],
  );

  const moveNode = useCallback(
    (id: string, position: { x: number; y: number }) => {
      const current = workflowRef.current;
      if (!current) return;
      const node = current.nodes.find((item) => item.id === id);
      if (!node || (node.position.x === position.x && node.position.y === position.y)) return;
      commit(
        {
          ...current,
          nodes: current.nodes.map((item) => (item.id === id ? { ...item, position } : item)),
        },
        true,
        `move:${id}`,
      );
    },
    [commit],
  );

  const deleteNodes = useCallback(
    (ids: string[]) => {
      const removed = new Set(ids);
      apply((current) => ({
        ...current,
        nodes: current.nodes.filter((node) => !removed.has(node.id)),
        edges: current.edges.filter(
          (edge) => !removed.has(edge.from.node) && !removed.has(edge.to.node),
        ),
      }));
      setSelectedId((id) => (id && removed.has(id) ? null : id));
    },
    [apply],
  );

  const deleteEdges = useCallback(
    (ids: string[]) => {
      const removed = new Set(ids);
      apply((current) => ({
        ...current,
        edges: current.edges.filter((edge) => !removed.has(edgeId(edge))),
      }));
    },
    [apply],
  );

  const connect = useCallback(
    (connection: Connection) => {
      const current = workflowRef.current;
      if (!current) return;
      const source = current.nodes.find((node) => node.id === connection.source);
      const target = current.nodes.find((node) => node.id === connection.target);
      if (!source || !target) return;
      if (source.id === target.id) {
        notify('A node cannot connect to itself.', 'error');
        return;
      }
      const sourceDef = defsByType[source.type];
      const targetDef = defsByType[target.type];
      if (!sourceDef || !targetDef) return;
      if (targetDef.category === 'trigger') {
        notify('Triggers cannot have incoming edges.', 'error');
        return;
      }
      const fromPort = parseHandle(connection.sourceHandle, 'out');
      const toPort = parseHandle(connection.targetHandle, 'in');
      if (!sourceDef.outputs.includes(fromPort) || !targetDef.inputs.includes(toPort)) {
        notify('That port is not available on this node.', 'error');
        return;
      }
      if (current.edges.some((edge) => edge.to.node === target.id && edge.to.port === toPort)) {
        notify(`${target.name || target.id}'s “${toPort}” input is already connected.`, 'error');
        return;
      }
      const sourceType = outputType(sourceDef, source.config ?? {});
      const compatibility = portCompatibility(sourceType, targetDef.inputType);
      if (!compatibility.ok) {
        notify(
          `${sourceDef.type} produces ${sourceType}, but ${targetDef.type} expects ${targetDef.inputType}.`,
          'error',
        );
        return;
      }
      if (compatibility.warning) notify(compatibility.warning);
      commit({
        ...current,
        edges: [
          ...current.edges,
          { from: { node: source.id, port: fromPort }, to: { node: target.id, port: toPort } },
        ],
      });
    },
    [commit, defsByType, notify],
  );

  const deleteSelected = useCallback(() => {
    if (!selectedId) return;
    deleteNodes([selectedId]);
  }, [deleteNodes, selectedId]);

  const createBlank = useCallback(async () => {
    const triggerDef = defsByType['trigger.manual'];
    if (!triggerDef) {
      notify('The node catalog is still loading.', 'error');
      return;
    }
    if (dirty && !window.confirm('Discard unsaved changes and create a new workflow?')) return;
    setBusyNew(true);
    try {
      const id = `workflow-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`;
      const trigger = makeNode(triggerDef, []);
      const created = await api.createWorkflow({
        id,
        name: 'Untitled workflow',
        version: 1,
        nodes: [trigger],
        edges: [],
      });
      adoptWorkflow(created);
      navigate(`/templates/${encodeURIComponent(created.id)}/edit`, { force: true });
      void refreshSummaries();
      notify('New workflow created.');
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      setBusyNew(false);
    }
  }, [adoptWorkflow, defsByType, dirty, navigate, notify, refreshSummaries]);

  const createFromExample = useCallback(
    async (example: Example) => {
      if (dirty && !window.confirm('Discard unsaved changes and open this example?')) return;
      setBusyNew(true);
      try {
        const clone = cloneWorkflow(example.workflow);
        if (summaries.some((summary) => summary.id === clone.id)) {
          clone.id = `${clone.id}-${Date.now().toString(36)}`;
          clone.name = `${clone.name} copy`;
        }
        const created = await api.createWorkflow(clone);
        adoptWorkflow(created);
        navigate(`/templates/${encodeURIComponent(created.id)}/edit`, { force: true });
        void refreshSummaries();
        notify(`Opened “${created.name}”.`);
      } catch (error) {
        notify(error instanceof Error ? error.message : String(error), 'error');
      } finally {
        setBusyNew(false);
      }
    },
    [adoptWorkflow, dirty, navigate, notify, refreshSummaries, summaries],
  );

  const switchWorkflow = useCallback(
    async (id: string, mode: 'view' | 'edit' = 'view') => {
      if (!id) return;
      if (id === workflowRef.current?.id) {
        navigate(`/templates/${encodeURIComponent(id)}${mode === 'edit' ? '/edit' : ''}`);
        return;
      }
      if (dirty && !window.confirm('Discard unsaved changes and switch workflows?')) return;
      try {
        const next = await api.getWorkflow(id);
        adoptWorkflow(next);
        navigate(`/templates/${encodeURIComponent(id)}${mode === 'edit' ? '/edit' : ''}`, {
          force: true,
        });
      } catch (error) {
        notify(error instanceof Error ? error.message : String(error), 'error');
      }
    },
    [adoptWorkflow, dirty, navigate, notify],
  );

  const openRunById = useCallback(
    async (runID: string): Promise<boolean> => {
      return navigate(`/runs/${encodeURIComponent(runID)}`);
    },
    [navigate],
  );

  const openRunFromRunsPage = useCallback(
    async (runID: string) => {
      await openRunById(runID);
    },
    [openRunById],
  );

  const runFromView = useCallback(async () => {
    const startedID = await startRun();
    if (startedID) await openRunById(startedID);
  }, [openRunById, startRun]);

  const openRunbook = useCallback(async () => {
    const current = workflowRef.current;
    if (!current) return;
    try {
      const markdown = await api.getRunbook(current.id);
      const blob = new Blob([markdown], { type: 'text/markdown;charset=utf-8' });
      const url = URL.createObjectURL(blob);
      const link = document.createElement('a');
      link.href = url;
      link.download = `${current.id}-runbook.md`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      window.setTimeout(() => URL.revokeObjectURL(url), 10000);
      notify('Runbook downloaded.');
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    }
  }, [notify]);

  const applyToken = useCallback(async () => {
    setToken(tokenDraft);
    setTokenOpen(false);
    setAuthRequired(false);
    setAuthError('');
    await loadInitial();
  }, [loadInitial, tokenDraft]);

  const issues = useMemo<ValidationIssue[]>(
    () => (workflow ? validateWorkflow(workflow, defsByType) : []),
    [workflow, defsByType],
  );
  const errorCount = issues.filter((issue) => issue.severity === 'error').length;
  const warningCount = issues.length - errorCount;
  const selectedNode = workflow?.nodes.find((node) => node.id === selectedId) ?? null;
  const status = deriveRunStatus(events, run?.status ?? '');
  const statuses = useMemo(
    () => deriveNodeStatuses(events, workflow?.nodes.map((node) => node.id), status),
    [events, status, workflow],
  );
  const commandDefs = defs;
  const runBlocked = !online
    ? 'Loom server is unreachable'
    : errorCount
      ? 'Fix validation errors before running'
      : undefined;

  const commands = useMemo<Command[]>(() => {
    const list: Command[] = [];
    if (route.name === 'editor' && workflow) {
      list.push({
        id: 'run',
        label: 'Run workflow',
        hint: '⌘↵',
        group: 'Workflow',
        run: () => void startRun(),
      });
      list.push({
        id: 'save',
        label: 'Save workflow',
        hint: '⌘S',
        group: 'Workflow',
        run: () => void save(),
      });
      list.push({
        id: 'new',
        label: 'New blank workflow',
        group: 'Workflow',
        run: () => void createBlank(),
      });
    }
    list.push({
      id: 'theme',
      label: `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`,
      group: 'Workspace',
      run: () => setTheme((value) => (value === 'dark' ? 'light' : 'dark')),
    });
    list.push({
      id: 'token',
      label: 'Set API token',
      group: 'Workspace',
      run: () => setTokenOpen(true),
    });
    for (const example of examples) {
      list.push({
        id: `example-${example.id}`,
        label: `Open example: ${example.name}`,
        hint: formatCount(example.workflow.nodes.length, 'node'),
        group: 'Examples',
        run: () => void createFromExample(example),
      });
    }
    if (route.name === 'editor' && workflow) {
      for (const def of commandDefs) {
        list.push({
          id: `node-${def.type}`,
          label: `Add ${defaultNodeName(def)}`,
          hint: def.category,
          group: 'Nodes',
          run: () => addNode(def),
        });
      }
    }
    return list;
  }, [
    addNode,
    commandDefs,
    createBlank,
    createFromExample,
    examples,
    route.name,
    save,
    startRun,
    theme,
    workflow,
  ]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const modifier = event.metaKey || event.ctrlKey;
      const target = event.target as HTMLElement | null;
      const editing =
        !!target &&
        (target.tagName === 'INPUT' ||
          target.tagName === 'TEXTAREA' ||
          target.tagName === 'SELECT' ||
          target.isContentEditable);

      if (modifier && event.key.toLowerCase() === 'k') {
        event.preventDefault();
        setPaletteOpen((open) => !open);
        return;
      }
      if (event.key === 'Escape') {
        if (paletteOpen) setPaletteOpen(false);
        else if (versionsOpen) setVersionsOpen(false);
        else if (tokenOpen) setTokenOpen(false);
        else if (runInputOpen) setRunInputOpen(false);
        return;
      }
      if (route.name !== 'editor') return;
      if (modifier && event.key.toLowerCase() === 's') {
        event.preventDefault();
        void save();
        return;
      }
      if (modifier && event.key === 'Enter') {
        event.preventDefault();
        void startRun();
        return;
      }
      if (modifier && event.key.toLowerCase() === 'z' && !editing) {
        event.preventDefault();
        if (event.shiftKey) redo();
        else undo();
        return;
      }
      if (!editing && !modifier && event.key.toLowerCase() === 'r') {
        event.preventDefault();
        void startRun();
        return;
      }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [
    paletteOpen,
    redo,
    route.name,
    runInputOpen,
    save,
    startRun,
    tokenOpen,
    undo,
    versionsOpen,
  ]);

  if (phase === 'loading' && !authRequired) {
    return (
      <div className="boot">
        <span className="brand-mark large">L</span>
        <p>Loading Loom…</p>
      </div>
    );
  }

  if (authRequired) {
    return (
      <div className="auth-screen">
        <div className="auth-card">
          <span className="brand-mark large">L</span>
          <p className="eyebrow">Loom</p>
          <h1>Connect to this server</h1>
          <p className="muted">
            This server requires an API token. It is stored in this browser only and sent as a bearer token.
          </p>
          {authError && <p className="banner error">{authError}</p>}
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void applyToken();
            }}
          >
            <label className="field" htmlFor="auth-token">
              <span className="field-label">API token</span>
              <input
                id="auth-token"
                className="text-input"
                type="password"
                autoFocus
                autoComplete="off"
                value={tokenDraft}
                placeholder="Paste token"
                onChange={(event) => setTokenDraft(event.target.value)}
              />
            </label>
            <button className="button primary full" type="submit">
              Connect
            </button>
          </form>
        </div>
      </div>
    );
  }

  if (phase === 'error') {
    return (
      <div className="auth-screen">
        <div className="auth-card">
          <span className="brand-mark large">L</span>
          <h1>Could not reach Loom</h1>
          <p className="banner error">{bootError}</p>
          <button className="button primary full" onClick={() => void loadInitial()}>
            Try again
          </button>
          <button className="button subtle full" onClick={() => setTokenOpen(true)}>
            Set API token
          </button>
        </div>
      </div>
    );
  }

  const publishedVersion = versionState?.publishedVersion ?? 0;
  const hasDraft = Boolean(workflow) && (dirty || versionState?.draft || publishedVersion === 0);
  const selectOptions = workflow && !summaries.some((summary) => summary.id === workflow.id)
    ? [{
        id: workflow.id,
        name: workflow.name,
        version: workflow.version,
        publishedVersion,
        draft: hasDraft,
        updatedAt: '',
      }, ...summaries]
    : summaries;

  return (
    <div className="app">
      <a className="skip-link" href="#main-stage">Skip to canvas</a>
      {!online && (
        <div className="connection-banner" role="status" aria-live="assertive">
          <span>
            <strong>Loom server unreachable.</strong> Edits stay in this tab, but saving, running and
            approvals are paused.
          </span>
          <button className="button subtle" onClick={() => void checkConnection()}>
            Retry
          </button>
        </div>
      )}
      <header className="topbar">
        <a
          className="brand"
          href="/"
          onClick={(event) => {
            event.preventDefault();
            navigate('/');
          }}
          aria-label="Loom home"
        >
          <span className="brand-mark">L</span>
          <div>
            <strong>Loom</strong>
            <span>workflow studio</span>
          </div>
        </a>

        <nav className="topnav" aria-label="Primary">
          <a
            className={route.name === 'home' ? 'active' : ''}
            href="/"
            onClick={(event) => {
              event.preventDefault();
              navigate('/');
            }}
          >
            Home
          </a>
          <a
            className={route.name === 'templates' || route.name === 'template' ? 'active' : ''}
            href="/templates"
            onClick={(event) => {
              event.preventDefault();
              navigate('/templates');
            }}
          >
            Templates
          </a>
          <a
            className={
              route.name === 'jobs' ||
              route.name === 'job' ||
              route.name === 'job-edit' ||
              route.name === 'job-new'
                ? 'active'
                : ''
            }
            href="/jobs"
            onClick={(event) => {
              event.preventDefault();
              navigate('/jobs');
            }}
          >
            Jobs
          </a>
          <a
            className={route.name === 'runs' || route.name === 'run' ? 'active' : ''}
            href="/runs"
            onClick={(event) => {
              event.preventDefault();
              navigate('/runs');
            }}
          >
            Runs
          </a>
        </nav>

        {route.name === 'editor' && workflow && (
          <div className="topbar-workflow">
            <select
              className="workflow-select"
              value={workflow.id}
              aria-label="Current workflow"
              onChange={(event) => void switchWorkflow(event.target.value, 'edit')}
            >
              {selectOptions.map((summary) => (
                <option key={summary.id} value={summary.id}>
                  {summary.name}
                </option>
              ))}
            </select>
            {dirty && <span className="dirty-badge">{autosaving ? 'Saving…' : 'Unsaved'}</span>}
            <button className="icon-button" onClick={() => void createBlank()} disabled={busyNew} title="New workflow">
              <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.9">
                <path d="M12 5v14M5 12h14" strokeLinecap="round" />
              </svg>
            </button>
          </div>
        )}
        {route.name === 'editor' && workflow && (
          <div className="publish-controls">
            <button
              className={`version-chip ${hasDraft ? 'draft' : ''}`}
              onClick={() => setVersionsOpen(true)}
              title="Version history"
            >
              {publishedVersion ? `v${publishedVersion}` : 'Draft'}
              {hasDraft && publishedVersion ? ' · draft' : ''}
            </button>
            <button
              className="button subtle"
              onClick={() => void publish()}
              disabled={publishing || saving || !online}
            >
              {publishing ? 'Publishing…' : 'Publish'}
            </button>
          </div>
        )}
        <div className="topbar-spacer" />

        {route.name === 'editor' && workflow && (
          <button
            className={`validation-chip ${errorCount ? 'bad' : 'good'}`}
            onClick={() => {
              const issue = issues.find(
                (item) => item.severity === 'error' && item.node,
              ) ?? issues.find((item) => item.node);
              setSelectedId(issue?.node ?? null);
            }}
            aria-label={
              errorCount
                ? `Show ${formatCount(errorCount, 'validation error')}`
                : warningCount
                  ? `Show ${formatCount(warningCount, 'validation warning')}`
                  : 'Workflow is valid'
            }
            title={issues.some((item) => item.node) ? 'Show the first issue' : 'Show validation details'}
          >
            {errorCount
              ? formatCount(errorCount, 'error')
              : warningCount
                ? formatCount(warningCount, 'warning')
                : 'Valid'}
          </button>
        )}

        {route.name === 'editor' && (
          <>
            <button
              className="icon-button"
              aria-label="Undo"
              title="Undo"
              onClick={undo}
              disabled={undoStack.length === 0}
            >
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M9 7L4 12l5 5M4 12h9a6 6 0 016 6" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
            <button
              className="icon-button"
              aria-label="Redo"
              title="Redo"
              onClick={redo}
              disabled={redoStack.length === 0}
            >
              <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
                <path d="M15 7l5 5-5 5M20 12h-9a6 6 0 00-6 6" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            </button>
          </>
        )}
        <details className="settings-menu">
          <summary className="button ghost">Settings</summary>
          <div className="settings-menu-list">
            <button onClick={() => navigate('/credentials')}>Credentials</button>
            <button onClick={() => setTheme((value) => (value === 'dark' ? 'light' : 'dark'))}>
              {theme === 'dark' ? 'Light theme' : 'Dark theme'}
            </button>
            <button onClick={() => setTokenOpen(true)}>API token</button>
            <button onClick={() => setPaletteOpen(true)}>Command palette</button>
          </div>
        </details>
        {route.name === 'editor' && workflow && (
          <>
            <button
              className="button subtle"
              onClick={() => void save()}
              disabled={saving || !online}
              title={!online ? 'Loom server is unreachable' : undefined}
            >
              {saving ? 'Saving…' : 'Save'}
            </button>
            <button
              className="button primary"
              onClick={() => void startRun()}
              disabled={starting || Boolean(runBlocked)}
              title={runBlocked}
            >
              {starting ? 'Starting…' : 'Run'}
            </button>
          </>
        )}
      </header>

      {route.name === 'home' && (
        <HomePage
          summaries={summaries}
          examples={examples}
          onNew={() => void createBlank()}
          onOpenTemplate={(id) => void switchWorkflow(id)}
          onUseExample={(example) => void createFromExample(example)}
          onOpenTemplates={() => navigate('/templates')}
          onOpenRuns={() => navigate('/runs')}
          onOpenRun={(runID) => void openRunFromRunsPage(runID)}
        />
      )}

      {route.name === 'templates' && (
        <TemplatesPage
          summaries={summaries}
          onNew={() => void createBlank()}
          onOpenTemplate={(id) => void switchWorkflow(id)}
          onRenameTemplate={renameTemplate}
          onDeleteTemplate={deleteTemplate}
        />
      )}

      {route.name === 'template' && workflow && workflow.id === route.id && (
        <TemplateViewPage
          workflow={workflow}
          defs={defsByType}
          issues={issues}
          statuses={statuses}
          selectedId={selectedId}
          online={online}
          onSelect={setSelectedId}
          onBack={() => navigate('/templates')}
          onEdit={() => navigate(`/templates/${encodeURIComponent(workflow.id)}/edit`)}
          onRun={() => void runFromView()}
          onOpenRun={(runID) => void openRunById(runID)}
        />
      )}

      {route.name === 'template' && !workflow && (
        <main className="page-shell" id="main-stage" tabIndex={-1}>
          <div className="empty-panel">
            <h2>Template not found</h2>
            <p className="muted">The template could not be loaded.</p>
            <button className="button primary" onClick={() => navigate('/templates')}>
              Browse templates
            </button>
          </div>
        </main>
      )}

      {route.name === 'jobs' && (
        <JobsPage
          summaries={summaries}
          onOpenJob={(id) => navigate(`/jobs/${encodeURIComponent(id)}`)}
          onCreateJob={() => navigate('/jobs/new')}
          onOpenTemplates={() => navigate('/templates')}
          onOpenRun={(runID) => void openRunFromRunsPage(runID)}
        />
      )}

      {route.name === 'job-new' && (
        <CreateJobPage
          summaries={summaries}
          onCreated={(id) => navigate(`/jobs/${encodeURIComponent(id)}`)}
          onCancel={() => navigate('/jobs')}
          onOpenTemplates={() => navigate('/templates')}
        />
      )}

      {(route.name === 'job' || route.name === 'job-edit') && (
        <JobDetailPage
          jobId={route.id ?? ''}
          summaries={summaries}
          editing={route.name === 'job-edit'}
          onBack={() => navigate('/jobs')}
          onEdit={() => navigate(`/jobs/${encodeURIComponent(route.id ?? '')}/edit`)}
          onDone={() => navigate(`/jobs/${encodeURIComponent(route.id ?? '')}`)}
          onOpenTemplate={(id) => void switchWorkflow(id)}
          onOpenRun={(runID) => void openRunFromRunsPage(runID)}
          onOpenJob={(id) => navigate(`/jobs/${encodeURIComponent(id)}`)}
        />
      )}

      {route.name === 'runs' && (
        <RunsPage summaries={summaries} onOpenRun={(runID) => void openRunFromRunsPage(runID)} />
      )}

      {route.name === 'run' && route.id && (
        <RunDetailPage
          key={route.id}
          runId={route.id}
          summaries={summaries}
          defs={defsByType}
          onBack={() => navigate('/runs')}
          onOpenRun={(runID) => void openRunById(runID)}
          onOpenTemplate={(id) => void switchWorkflow(id)}
          onOpenJob={(id) => navigate(`/jobs/${encodeURIComponent(id)}`)}
        />
      )}

      {route.name === 'credentials' && <CredentialsPage />}

      {route.name === 'editor' && workflow ? (
        <div className="workspace" id="main-stage" tabIndex={-1}>
          <NodePanel
            defs={defs}
            onAdd={(def) => addNode(def)}
            onDragStart={() => undefined}
          />
          <main className="stage">
            <Canvas
              workflow={workflow}
              defs={defsByType}
              statuses={statuses}
              issues={issues}
              selectedId={selectedId}
              onSelect={setSelectedId}
              onMove={moveNode}
              onDeleteNodes={deleteNodes}
              onDeleteEdges={deleteEdges}
              onConnect={connect}
              onAddNode={(type, position) => {
                const def = defsByType[type];
                if (def) addNode(def, position);
              }}
            />
            <RunBar
              run={run}
              status={status}
              events={events}
              nodeCount={workflow.nodes.length}
              busy={starting}
              online={online}
              runBlocked={runBlocked}
              onRun={() => void startRun()}
              onInput={openRunInput}
              onRetry={() => void retryFailedNode()}
              onCancel={() => void cancelRun()}
              onResume={(decision) => void resumeRun(decision)}
              onOpenHistory={() => setHistoryOpen(true)}
              onOpenRun={() => {
                if (run) void openRunById(run.id);
              }}
            />
          </main>
          <Inspector
            workflow={workflow}
            node={selectedNode}
            def={selectedNode ? defsByType[selectedNode.type] : undefined}
            issues={issues}
            runEvents={events}
            runId={run?.id ?? null}
            onWorkflowName={(name) =>
              apply((current) => ({ ...current, name }), true, 'workflow:name')
            }
            onOpenRunbook={() => void openRunbook()}
            onNodePatch={(patch) =>
              apply(
                (current) => ({
                  ...current,
                  nodes: current.nodes.map((node) =>
                    node.id === selectedId ? { ...node, ...patch } : node,
                  ),
                }),
                true,
                `node:${selectedId}`,
              )
            }
            onConfigChange={(key, value) =>
              apply(
                (current) => ({
                  ...current,
                  nodes: current.nodes.map((node) =>
                    node.id === selectedId
                      ? { ...node, config: { ...(node.config ?? {}), [key]: value } }
                      : node,
                  ),
                }),
                true,
                `config:${selectedId}:${key}`,
              )
            }
            onCredentialChange={(key, value) =>
              apply(
                (current) => ({
                  ...current,
                  nodes: current.nodes.map((node) =>
                    node.id === selectedId
                      ? { ...node, credentials: { ...(node.credentials ?? {}), [key]: value } }
                      : node,
                  ),
                }),
                true,
                `credential:${selectedId}:${key}`,
              )
            }
            onDeleteNode={deleteSelected}
            onSelectNode={setSelectedId}
          />
        </div>
      ) : route.name === 'editor' ? (
        <main className="page-shell" id="main-stage" tabIndex={-1}>
          <div className="empty-panel">
            <h2>No template open</h2>
            <p className="muted">Choose a template or create a blank workflow.</p>
            <button className="button primary" onClick={() => navigate('/templates')}>
              Browse templates
            </button>
          </div>
        </main>
      ) : null}

      <CommandPalette open={paletteOpen} commands={commands} onClose={() => setPaletteOpen(false)} />

      {workflow && (
        <VersionHistory
          open={versionsOpen}
          workflowId={workflow.id}
          current={workflow}
          state={versionState}
          onClose={() => setVersionsOpen(false)}
          onPublish={() => void publish()}
        />
      )}

      <RunHistory
        open={historyOpen}
        workflowId={workflow?.id ?? null}
        currentRunId={run?.id ?? null}
        onClose={() => setHistoryOpen(false)}
        onView={(historicalRun, historicalEvents) => {
          setRun(historicalRun);
          setEvents(historicalEvents);
          setRunId(historicalRun.id);
          setHistoryOpen(false);
        }}
        onUpdate={(historicalRun, historicalEvents) => {
          setRun(historicalRun);
          setEvents(historicalEvents);
          setRunId(historicalRun.id);
        }}
        onOpenRun={(historicalRunId) => {
          setHistoryOpen(false);
          void openRunById(historicalRunId);
        }}
      />

      {runInputOpen && (
        <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && setRunInputOpen(false)}>
          <div className="dialog" role="dialog" aria-modal="true" aria-label="Run input">
            <p className="eyebrow">Execution</p>
            <h2>Run input</h2>
            <p className="muted small">
              This object becomes the trigger item. Use fields here from <code>{'{{ $json.field }}'}</code> in node configs.
            </p>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                submitRunInput();
              }}
            >
              <label className="field" htmlFor="run-input">
                <span className="field-label">JSON object</span>
                <textarea
                  id="run-input"
                  className={`text-input code ${runInputError ? 'invalid' : ''}`}
                  rows={10}
                  autoFocus
                  spellCheck={false}
                  value={runInputDraft}
                  onChange={(event) => {
                    setRunInputDraft(event.target.value);
                    setRunInputError('');
                  }}
                />
              </label>
              {runInputError && <p className="field-error">{runInputError}</p>}
              <div className="dialog-actions">
                <button className="button subtle" type="button" onClick={() => setRunInputOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Run with input
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {tokenOpen && (
        <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && setTokenOpen(false)}>
          <div className="dialog" role="dialog" aria-modal="true" aria-label="API token">
            <p className="eyebrow">Connection</p>
            <h2>API token</h2>
            <p className="muted small">
              Sent as <code>Authorization: Bearer …</code>. Stored in this browser only.
            </p>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                void applyToken();
              }}
            >
              <label className="field" htmlFor="settings-token">
                <span className="field-label">Token</span>
                <input
                  id="settings-token"
                  className="text-input"
                  type="password"
                  autoFocus
                  autoComplete="off"
                  value={tokenDraft}
                  onChange={(event) => setTokenDraft(event.target.value)}
                />
              </label>
              <div className="dialog-actions">
                <button className="button subtle" type="button" onClick={() => setTokenOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" type="submit">
                  Save token
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {toast && (
        <div
          className={`toast toast-${toast.tone}`}
          role={toast.tone === 'error' ? 'alert' : 'status'}
          aria-live={toast.tone === 'error' ? 'assertive' : 'polite'}
        >
          {toast.message}
        </div>
      )}
    </div>
  );
}
