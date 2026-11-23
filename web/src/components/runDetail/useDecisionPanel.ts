import { useCallback, useEffect, useRef, useState } from 'react';
import { ApiError, api } from '../../api';
import { useDialogFocus } from '../../useDialogFocus';
import type { Run } from '../../types';

// Approval-decision cluster (Phase 6 D6-2): the reject-reason popover
// state, focus handling, and the submit path (approve stays one click;
// rejection requires a reason of at least 3 characters).
export function useDecisionPanel(args: {
  run: Run | null;
  busy: string;
  runAction: (label: string, action: () => Promise<void>) => Promise<void> | void;
  reload: () => Promise<void>;
  onWaitingChanged?: () => void;
}) {
  const { run, busy, runAction, reload, onWaitingChanged } = args;
  const [decisionPanel, setDecisionPanel] = useState<'approved' | 'rejected' | null>(null);
  const [decisionNote, setDecisionNote] = useState('');
  const [decisionError, setDecisionError] = useState('');
  const decisionNoteRef = useRef<HTMLTextAreaElement | null>(null);

  useEffect(() => {
    if (decisionPanel) decisionNoteRef.current?.focus();
  }, [decisionPanel]);

  const openDecision = useCallback((decision: 'approved' | 'rejected') => {
    setDecisionError('');
    setDecisionPanel(decision);
  }, []);

  const closeDecision = useCallback(() => {
    setDecisionPanel(null);
    setDecisionError('');
  }, []);

  const decisionRef = useDialogFocus<HTMLDivElement>(decisionPanel !== null, closeDecision);

  const submitDecision = useCallback(
    (decision: 'approved' | 'rejected' = decisionPanel ?? 'approved') =>
      runAction(decision, async () => {
        if (!run) return;
        const trimmed = decisionNote.trim();
        if (decision === 'rejected' && trimmed.length < 3) {
          setDecisionError('Add a reason of at least 3 characters.');
          decisionNoteRef.current?.focus();
          return;
        }
        try {
          await api.resumeRun(run.id, decision, trimmed || undefined);
        } catch (reason) {
          // Another tab or a retried callback may have resumed first. The CAS
          // guarantees the run moved on; reload instead of surfacing a 409.
          if (!(reason instanceof ApiError) || reason.status !== 409) {
            setDecisionError(reason instanceof Error ? reason.message : String(reason));
            decisionNoteRef.current?.focus();
            return;
          }
        }
        setDecisionPanel(null);
        setDecisionNote('');
        setDecisionError('');
        await reload();
        onWaitingChanged?.();
      }),
    [decisionNote, decisionPanel, onWaitingChanged, reload, run, runAction],
  );

  return {
    decisionPanel,
    decisionNote,
    decisionError,
    decisionNoteRef,
    decisionRef,
    openDecision,
    closeDecision,
    setDecisionNote,
    setDecisionError,
    submitDecision,
    decisionBusy: Boolean(busy),
  };
}
