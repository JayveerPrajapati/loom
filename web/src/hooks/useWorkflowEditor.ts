import { useCallback, useRef, useState } from 'react';
import type { Workflow } from '../types';
import { normalizeWorkflow } from '../appShared';

// The workflow editor state cluster (Phase 6 D6-1): the open draft,
// selection, and the undo/redo stacks. commit/apply preserve the
// coalescing semantics — one Backspace or one drag is one undo step.
export function useWorkflowEditor() {
  const [workflow, setWorkflowState] = useState<Workflow | null>(null);
  const workflowRef = useRef<Workflow | null>(null);
  const [dirty, setDirty] = useState(false);
  const [selectedId, setSelectedId] = useState<string | null>(null);

  const [undoStack, setUndoStack] = useState<Workflow[]>([]);
  const [redoStack, setRedoStack] = useState<Workflow[]>([]);
  const undoRef = useRef<Workflow[]>([]);
  const redoRef = useRef<Workflow[]>([]);
  const lastCommitRef = useRef<{ key: string; at: number } | null>(null);

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

  // adopt resets the editor to a freshly opened workflow: selection,
  // undo history and the coalescing clock start over.
  const adopt = useCallback((next: Workflow, markDirty = false) => {
    const normalized = normalizeWorkflow(next);
    workflowRef.current = normalized;
    setWorkflowState(normalized);
    setSelectedId(null);
    setDirty(markDirty);
    undoRef.current = [];
    redoRef.current = [];
    setUndoStack([]);
    setRedoStack([]);
    lastCommitRef.current = null;
  }, []);

  // clear empties the editor without a replacement doc.
  const clear = useCallback(() => {
    workflowRef.current = null;
    setWorkflowState(null);
    setDirty(false);
  }, []);

  // acceptSaved swaps in a freshly saved/published doc from the server:
  // selection and undo history stay; only the draft-clean flag clears.
  const acceptSaved = useCallback((next: Workflow) => {
    workflowRef.current = next;
    setWorkflowState(next);
    setDirty(false);
  }, []);

  return {
    workflow,
    setWorkflowState,
    workflowRef,
    dirty,
    setDirty,
    selectedId,
    setSelectedId,
    undoStack,
    redoStack,
    commit,
    apply,
    undo,
    redo,
    adopt,
    clear,
    acceptSaved,
  };
}
