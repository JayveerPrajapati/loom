import { useCallback, useEffect, useState } from 'react';
import { api } from '../../api';
import { isTerminal } from '../runDetailHelpers';
import type { Job, Run, RunEvent, Workflow } from '../../types';

// Run-detail data cluster (Phase 6 D6-2): loading the run, its events,
// child runs, workflow snapshot and job row, plus the SSE stream that
// keeps an in-flight run live. Terminal and waiting runs do not stream.
export function useRunDetailData(runId: string, onReset: () => void) {
  const [run, setRun] = useState<Run | null>(null);
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [children, setChildren] = useState<Run[]>([]);
  const [job, setJob] = useState<Job | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      // The run is the primary load. A missing or denied run must not be
      // masked by a secondary 404 from its events or workflow snapshot.
      // allSettled keeps the four requests parallel while giving the run
      // error precedence when more than one fails.
      const [runResult, eventResult, childResult, workflowResult] = await Promise.allSettled([
        api.getRun(runId),
        api.listEvents(runId),
        api.listChildRuns(runId, 20),
        api.getRunWorkflow(runId).catch(() => null),
      ]);
      if (runResult.status === 'rejected') throw runResult.reason;
      if (eventResult.status === 'rejected') throw eventResult.reason;
      if (childResult.status === 'rejected') throw childResult.reason;
      const nextRun = runResult.value;
      const eventValue = eventResult.value;
      const childValue = childResult.value;
      const nextWorkflow = workflowResult.status === 'fulfilled' ? workflowResult.value : null;
      setRun(nextRun);
      setEvents(eventValue.events ?? []);
      setChildren(childValue.runs ?? []);
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
    onReset();
    void load();
  }, [load, onReset]);

  const streaming = run ? !isTerminal(run.status) && run.status !== 'waiting' : false;

  useEffect(() => {
    if (!streaming) return;
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
            if (event.type === 'run.waiting') {
              setRun((previous) =>
                previous && previous.status !== 'waiting'
                  ? { ...previous, status: 'waiting' }
                  : previous,
              );
            } else if (['run.finished', 'run.failed', 'run.cancelled'].includes(event.type)) {
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
  }, [load, runId, streaming]);

  return {
    run,
    setRun,
    events,
    workflow,
    children,
    job,
    loading,
    error,
    setError,
    load,
    streaming,
  };
}
