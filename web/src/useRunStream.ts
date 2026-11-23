import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from './api';
import type { Run, RunEvent } from './types';

// Run streaming state: the SSE effect (subscribe, dedupe by seq+type,
// refresh the run row on terminal/waiting events, bounded reconnect with
// exponential backoff) plus the run/events state it feeds. Extracted
// verbatim from App; callers keep setRun/setEvents for their own mutation
// paths (history view, resume, retry) via the returned setters.
export function useRunStream(
  runId: string | null,
  notify: (message: string, tone?: 'info' | 'error') => void,
) {
  const [run, setRun] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);

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

  const clearRun = useCallback(() => {
    setRun(null);
    setEvents([]);
  }, []);

  return { run, setRun, events, setEvents, clearRun };
}
