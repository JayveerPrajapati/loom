import { useCallback, useState } from 'react';
import { api } from '../../api';
import { useDialogFocus } from '../../useDialogFocus';
import type { CompareState } from '../RunCompareDialog';
import type { Run } from '../../types';

// Run-compare cluster (Phase 6 D6-2): the compare dialog state, its
// focus trap, and candidate/diff loading against the current run.
export function useRunCompare(runId: string, run: Run | null) {
  const [compare, setCompare] = useState<CompareState>({
    open: false,
    loading: false,
    runs: [],
    against: '',
    diff: null,
    error: '',
  });
  const compareRef = useDialogFocus<HTMLDivElement>(compare.open, () =>
    setCompare((current) => ({ ...current, open: false })),
  );

  const loadCompare = useCallback(
    async (against: string) => {
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
    },
    [runId],
  );

  const openCompare = useCallback(async () => {
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
  }, [loadCompare, run]);

  return { compare, setCompare, compareRef, loadCompare, openCompare };
}
