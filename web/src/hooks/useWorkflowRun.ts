import { useCallback, useEffect, useState } from 'react';
import { api } from '../api';
import { normalizeWorkflow } from '../appShared';
import { confirmAction } from '../confirm';
import { activePublishPhase, newPublishId } from '../publishProgress';
import { validateWorkflow } from '../validate';
import type {
  ConfigProfile,
  DataSchema,
  Mapping,
  NodeDef,
  Run,
  RunEvent,
  SubprocessNodes,
  Workflow,
} from '../types';

// The run/publish controls cluster (Phase 6 D6-1): save, autosave,
// publish, the profile-upgrade apply, run start (with the python
// subprocess preflight), retry-from-failed, cancel, resume, and the
// run-with-input dialog state. All the editor-flow actions that mutate
// the workflow doc go through accept/refresh callbacks from the host.
export interface WorkflowRunArgs {
  workflowRef: React.MutableRefObject<Workflow | null>;
  workflow: Workflow | null;
  dirty: boolean;
  online: boolean;
  subprocess: SubprocessNodes | null;
  defsByType: Record<string, NodeDef>;
  profilesById: Record<string, ConfigProfile>;
  schemasById: Record<string, DataSchema>;
  mappingsById: Record<string, Mapping>;
  notify: (message: string, tone?: 'info' | 'error') => void;
  refreshSummaries: () => Promise<void> | void;
  refreshWorkflowState: (id: string) => Promise<void> | void;
  acceptSaved: (next: Workflow) => void;
  setSelectedId: (id: string | null) => void;
  run: Run | null;
  setRun: (run: Run | null) => void;
  events: RunEvent[];
  setEvents: (events: RunEvent[]) => void;
  runId: string | null;
  setRunId: (id: string | null) => void;
}

export function useWorkflowRun(args: WorkflowRunArgs) {
  const {
    workflowRef, workflow, dirty, online, subprocess,
    defsByType, profilesById, schemasById, mappingsById,
    notify, refreshSummaries, refreshWorkflowState,
    acceptSaved, setSelectedId,
    run, setRun, events, setEvents, runId, setRunId,
  } = args;

  const [saving, setSaving] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [publishPhase, setPublishPhase] = useState('');
  const [starting, setStarting] = useState(false);
  const [autosaving, setAutosaving] = useState(false);
  const [runInput, setRunInput] = useState<Record<string, unknown>>({});
  const [publishWarnings, setPublishWarnings] = useState<
    { node: string; message: string }[]
  >([]);

  const validate = useCallback(
    (current: Workflow) =>
      validateWorkflow(current, defsByType, profilesById, schemasById, mappingsById),
    [defsByType, profilesById, schemasById, mappingsById],
  );

  const save = useCallback(async (): Promise<boolean> => {
    const current = workflowRef.current;
    if (!current) return false;
    if (!online) {
      notify('Loom server is unreachable. Reconnect before saving.', 'error');
      return false;
    }
    const issues = validate(current);
    if (issues.some((issue) => issue.severity === 'error')) {
      setSelectedId(null);
      notify('Fix validation errors before saving.', 'error');
      return false;
    }
    setSaving(true);
    try {
      const saved = normalizeWorkflow(await api.saveWorkflow(current));
      acceptSaved(saved);
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
  }, [acceptSaved, notify, online, refreshSummaries, refreshWorkflowState, setSelectedId, validate, workflowRef]);

  // Autosave runs only when validation passes; a failed autosave keeps
  // the draft dirty so a manual save surfaces the error.
  useEffect(() => {
    if (!dirty || !workflow || !online || saving) return;
    const current = workflow;
    if (validate(current).some((issue) => issue.severity === 'error')) return;
    const timer = window.setTimeout(() => {
      void (async () => {
        setAutosaving(true);
        try {
          const saved = normalizeWorkflow(await api.saveWorkflow(current));
          const latest = workflowRef.current;
          if (!latest || JSON.stringify(latest) !== JSON.stringify(current)) return;
          acceptSaved(saved);
          void refreshWorkflowState(saved.id);
          void refreshSummaries();
        } catch (error) {
          // Keep the draft dirty; the toast makes the failure visible —
          // a silent catch here is how egress 400s used to vanish.
          notify(
            `Autosave failed: ${error instanceof Error ? error.message : String(error)} — your edits are still here, fix the problem and save.`,
            'error',
          );
        } finally {
          setAutosaving(false);
        }
      })();
    }, 1200);
    return () => window.clearTimeout(timer);
  }, [acceptSaved, dirty, notify, online, refreshSummaries, refreshWorkflowState, saving, validate, workflow, workflowRef]);

  const publish = useCallback(async () => {
    const current = workflowRef.current;
    if (!current || publishing) return;
    if (!online) {
      notify('Loom server is unreachable. Reconnect before publishing.', 'error');
      return;
    }
    const issues = validate(current);
    if (issues.some((issue) => issue.severity === 'error')) {
      setSelectedId(null);
      notify('Fix validation errors before publishing.', 'error');
      return;
    }
    setPublishing(true);
    // Audit 4E: the publish stays one synchronous POST; a short poll of
    // the server's in-memory progress registry turns it into a live phase
    // label. Poll failures are ignored — the POST itself decides success.
    const publishId = newPublishId();
    let cancelled = false;
    const poll = window.setInterval(() => {
      void (async () => {
        try {
          const status = await api.publishStatus(current.id, publishId);
          if (cancelled) return;
          const label = activePublishPhase(status.phases);
          if (label) setPublishPhase(label);
        } catch {
          // A missed poll must never surface as a publish failure.
        }
      })();
    }, 300);
    try {
      if (dirty) {
        const saved = await save();
        if (!saved) return;
      }
      const result = await api.publishWorkflow(current.id, publishId);
      const published = normalizeWorkflow(result.workflow);
      acceptSaved(published);
      await refreshWorkflowState(published.id);
      void refreshSummaries();
      // D6-4: mapping-contract warnings ride the response — recorded,
      // never blocking; the editor shows them until dismissed.
      setPublishWarnings(result.mappingWarnings ?? []);
      if (result.mappingWarnings?.length) {
        notify(
          `Published v${result.version.version} with ${result.mappingWarnings.length} mapping warning${result.mappingWarnings.length === 1 ? '' : 's'}.`,
        );
      } else {
        notify(`Published v${result.version.version}.`);
      };
    } catch (error) {
      notify(error instanceof Error ? error.message : String(error), 'error');
    } finally {
      cancelled = true;
      window.clearInterval(poll);
      setPublishPhase('');
      setPublishing(false);
    }
  }, [acceptSaved, dirty, notify, online, publishing, refreshSummaries, refreshWorkflowState, save, setSelectedId, validate, workflowRef]);

  // UX Phase 2 D2-6: apply the profile upgrade to the saved draft and
  // republish in one step. Unsaved editor changes are saved first so
  // nothing is lost — the apply works on the stored doc.
  const applyUpgrade = useCallback(
    async (onApplied: () => void) => {
      const current = workflowRef.current;
      if (!current) return;
      setStarting(true);
      try {
        if (dirty) {
          const saved = await save();
          if (!saved) return;
        }
        const result = await api.workflowUpgradePreview(current.id, true);
        const published = normalizeWorkflow(result.workflow as Workflow);
        acceptSaved(published);
        await refreshWorkflowState(published.id);
        void refreshSummaries();
        onApplied();
        notify(`Published v${result.version?.version ?? ''} with profile upgrades.`);
      } catch (error) {
        notify(error instanceof Error ? error.message : String(error), 'error');
      } finally {
        setStarting(false);
      }
    },
    [acceptSaved, dirty, notify, refreshSummaries, refreshWorkflowState, save, workflowRef],
  );

  const startRun = useCallback(
    async (input: Record<string, unknown> = runInput) => {
      const current = workflowRef.current;
      if (!current || starting) return null;
      if (!online) {
        notify('Loom server is unreachable. Reconnect before running.', 'error');
        return null;
      }
      const issues = validate(current);
      if (issues.some((issue) => issue.severity === 'error')) {
        setSelectedId(null);
        notify('Fix validation errors before running.', 'error');
        return null;
      }
      // Python preflight: warn before the run when the workflow uses
      // code.python but the server has subprocess nodes disabled, so the
      // gate is discovered before the failure, not after.
      if (
        current.nodes.some((node) => node.type === 'code.python') &&
        subprocess !== null &&
        !subprocess.enabled
      ) {
        const proceed = await confirmAction({
          title: 'Python nodes are disabled on this server',
          body: 'This workflow contains a Python node, but subprocess nodes are disabled, so the run will fail at that node. Ask the operator to start Loom with --enable-subprocess-nodes (or set LOOM_ENABLE_SUBPROCESS_NODES=1); see docs/security.md for the isolation model.',
          confirmLabel: 'Run anyway',
          cancelLabel: 'Cancel run',
          tone: 'primary',
        });
        if (!proceed) return null;
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
    },
    [dirty, notify, online, runInput, save, setSelectedId, setEvents, setRun, setRunId, starting, subprocess, validate, workflowRef],
  );

  const retryFailedNode = useCallback(async () => {
    const failed = [...events].reverse().find((event) => event.type === 'node.failed' && event.nodeId);
    const sourceId = runId ?? run?.id ?? null;
    if (!sourceId || !failed?.nodeId || starting) return;
    const confirmed = await confirmAction({
      title: 'Retry from the failed node?',
      body: `Retrying from ${failed.nodeId} starts a new run; downstream side effects will repeat.`,
      confirmLabel: 'Retry run',
      cancelLabel: 'Cancel',
      tone: 'primary',
    });
    if (!confirmed) return;
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
  }, [events, notify, run, runId, setEvents, setRun, setRunId, starting]);

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
  }, [notify, run, runId, setEvents, setRun]);

  const resumeRun = useCallback(
    async (decision: 'approved' | 'rejected', note?: string) => {
      if (!run) return;
      setStarting(true);
      try {
        const result = await api.resumeRun(run.id, decision, note);
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
    [notify, run, setEvents, setRun],
  );

  // Run-with-sample memory: the last input used for this workflow
  // pre-fills the dialog so re-running with the same sample is one click.
  const openRunInput = useCallback(
    (open: (draft: string) => void) => {
      let remembered = runInput;
      try {
        const stored = window.localStorage.getItem(`loom-run-sample:${workflowRef.current?.id ?? ''}`);
        if (stored) remembered = JSON.parse(stored) as Record<string, unknown>;
      } catch {
        // Corrupt stored sample: fall back to the current input.
      }
      open(JSON.stringify(remembered, null, 2));
    },
    [runInput, workflowRef],
  );

  const submitRunInput = useCallback(
    (draft: string, setError: (message: string) => void, close: () => void) => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(draft);
      } catch {
        setError('Enter a valid JSON object.');
        return;
      }
      if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
        setError('Run input must be a JSON object.');
        return;
      }
      const input = parsed as Record<string, unknown>;
      setRunInput(input);
      close();
      try {
        window.localStorage.setItem(
          `loom-run-sample:${workflowRef.current?.id ?? ''}`,
          JSON.stringify(input),
        );
      } catch {
        // Storage full or unavailable: the run still starts.
      }
      void startRun(input);
    },
    [startRun, workflowRef],
  );

  return {
    saving,
    publishing,
    publishPhase,
    starting,
    autosaving,
    publish,
    save,
    applyUpgrade,
    startRun,
    retryFailedNode,
    cancelRun,
    resumeRun,
    openRunInput,
    submitRunInput,
    publishWarnings,
    clearPublishWarnings: () => setPublishWarnings([]),
  };
}
