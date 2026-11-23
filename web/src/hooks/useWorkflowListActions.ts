import { useCallback, useState } from 'react';
import { api } from '../api';
import { cloneWorkflow } from '../appShared';
import { confirmAction } from '../confirm';
import { makeNode } from '../catalog';
import type { Example, NodeDef, Workflow, WorkflowSummary } from '../types';

// Workflow-list actions (Phase 6 D6-1): create/switch/rename/delete plus
// the runbook download and run-navigation helpers. All of them funnel
// through adoptWorkflow (which resets editor + run state) and navigate.
export function useWorkflowListActions(args: {
  workflowRef: React.MutableRefObject<Workflow | null>;
  defsByType: Record<string, NodeDef>;
  dirty: boolean;
  summaries: WorkflowSummary[];
  adoptWorkflow: (next: Workflow, markDirty?: boolean) => void;
  navigate: (path: string, options?: { replace?: boolean; force?: boolean }) => boolean;
  notify: (message: string, tone?: 'info' | 'error') => void;
  refreshSummaries: () => Promise<void> | void;
  startRun: () => Promise<string | null>;
}) {
  const { workflowRef, defsByType, dirty, summaries, adoptWorkflow, navigate, notify, refreshSummaries, startRun } =
    args;
  const [busyNew, setBusyNew] = useState(false);

  const createBlank = useCallback(async () => {
    const triggerDef = defsByType['trigger.manual'];
    if (!triggerDef) {
      notify('The node catalog is still loading.', 'error');
      return;
    }
    if (
      dirty &&
      !(await confirmAction({
        title: 'Discard unsaved changes?',
        body: 'Creating a new workflow discards the current unsaved edits.',
        confirmLabel: 'Discard and create',
        cancelLabel: 'Keep editing',
      }))
    )
      return;
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
      navigate(`/workflows/${encodeURIComponent(created.id)}/edit`, { force: true });
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
      if (
        dirty &&
        !(await confirmAction({
          title: 'Discard unsaved changes?',
          body: 'Opening this example discards the current unsaved edits.',
          confirmLabel: 'Discard and open',
          cancelLabel: 'Keep editing',
        }))
      )
        return;
      setBusyNew(true);
      try {
        const clone = cloneWorkflow(example.workflow);
        if (summaries.some((summary) => summary.id === clone.id)) {
          clone.id = `${clone.id}-${Date.now().toString(36)}`;
          clone.name = `${clone.name} copy`;
        }
        const created = await api.createWorkflow(clone);
        adoptWorkflow(created);
        navigate(`/workflows/${encodeURIComponent(created.id)}/edit`, { force: true });
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
        navigate(`/workflows/${encodeURIComponent(id)}${mode === 'edit' ? '/edit' : ''}`);
        return;
      }
      if (
        dirty &&
        !(await confirmAction({
          title: 'Discard unsaved changes?',
          body: 'Switching workflows discards the current unsaved edits.',
          confirmLabel: 'Discard and switch',
          cancelLabel: 'Keep editing',
        }))
      )
        return;
      try {
        const next = await api.getWorkflow(id);
        adoptWorkflow(next);
        navigate(`/workflows/${encodeURIComponent(id)}${mode === 'edit' ? '/edit' : ''}`, {
          force: true,
        });
      } catch (error) {
        notify(error instanceof Error ? error.message : String(error), 'error');
      }
    },
    [adoptWorkflow, dirty, navigate, notify, workflowRef],
  );

  const renameTemplate = useCallback(
    async (id: string, name: string) => {
      const current = await api.getWorkflow(id);
      await api.saveWorkflow({ ...current, name });
      await refreshSummaries();
      notify(`Renamed workflow to “${name}”.`);
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

  const openRunById = useCallback(
    async (runID: string): Promise<boolean> => {
      return navigate(`/runs/${encodeURIComponent(runID)}`);
    },
    [navigate],
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
  }, [notify, workflowRef]);

  return {
    busyNew,
    createBlank,
    createFromExample,
    switchWorkflow,
    renameTemplate,
    deleteTemplate,
    openRunById,
    runFromView,
    openRunbook,
  };
}
