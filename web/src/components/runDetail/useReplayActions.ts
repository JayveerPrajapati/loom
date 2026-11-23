import { useEffect, useState } from 'react';
import { api } from '../../api';
import { confirmAction } from '../../confirm';
import type { ReplayResult, Run, RunEvent } from '../../types';

// Replay cluster (Phase 6 D6-2): hermetic toggle, replay result state,
// and the from/step/step-live actions. currentInput is supplied by the
// page (undefined means "recorded input", null means invalid JSON).
// resetKey (the run id) clears any previous replay result.
export function useReplayActions(args: {
  run: Run | null;
  runAction: (label: string, action: () => Promise<void>) => Promise<void>;
  onOpenRun: (runId: string) => void;
  currentInput: () => Record<string, unknown> | undefined | null;
  resetKey: string;
}) {
  const { run, runAction, onOpenRun, currentInput } = args;
  const [hermetic, setHermetic] = useState(false);
  const [replay, setReplay] = useState<{
    busy: boolean;
    result?: ReplayResult;
    error?: string;
    hermetic?: boolean;
  }>({ busy: false });

  useEffect(() => {
    setReplay({ busy: false });
  }, [args.resetKey]);

  const replayFrom = (event: RunEvent) =>
    runAction('replay', async () => {
      if (!run || !event.nodeId) return;
      const confirmed = await confirmAction({
        title: `Re-run from ${event.nodeId}?`,
        body: hermetic
          ? 'Replays this node and everything downstream from recorded effects. No network calls are made.'
          : 'Re-runs this node and everything downstream as a new run. External side effects will repeat.',
        confirmLabel: hermetic ? 'Re-play run' : 'Re-run',
        cancelLabel: 'Cancel',
        tone: 'primary',
      });
      if (!confirmed) {
        return;
      }
      const edited = currentInput();
      if (edited === null) {
        setReplay({ busy: false, error: 'Edited input is not valid JSON.' });
        return;
      }
      const result = await api.replayRun(run.id, 'from', event.nodeId, undefined, edited, hermetic);
      setReplay({ busy: false, result, hermetic });
      onOpenRun(result.runId);
    });

  const replayStep = (event: RunEvent) =>
    runAction('replay', async () => {
      if (!run || !event.nodeId) return;
      const result = await api.replayRun(
        run.id,
        'step',
        event.nodeId,
        undefined,
        undefined,
        hermetic,
      );
      setReplay({ busy: false, result, hermetic });
    });

  const replayStepLive = (event: RunEvent) =>
    runAction('replay', async () => {
      if (!run || !event.nodeId) return;
      const confirmed = await confirmAction({
        title: `Re-run ${event.nodeId} live?`,
        body: 'External side effects will repeat; the original run log is unchanged.',
        confirmLabel: 'Re-run live',
        cancelLabel: 'Cancel',
        tone: 'danger',
      });
      if (!confirmed) {
        return;
      }
      const edited = currentInput();
      if (edited === null) {
        setReplay({ busy: false, error: 'Edited input is not valid JSON.' });
        return;
      }
      const result = await api.replayRun(run.id, 'step', event.nodeId, true, edited);
      setReplay({ busy: false, result });
    });

  return { replay, setReplay, hermetic, setHermetic, replayFrom, replayStep, replayStepLive };
}
