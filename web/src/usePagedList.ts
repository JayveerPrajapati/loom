import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { classifyCurrentLoadFailure, type LoadFailure } from './loadState';
import {
  initialPagedState,
  pagedLoadFailure,
  pagedLoadStart,
  pagedLoadSuccess,
  type PageParams,
  type PagedResponse,
  type PagedState,
} from './paging';

export interface UsePagedListOptions {
  limit?: number;
  debounceMs?: number;
}

export interface UsePagedListResult<T> {
  items: T[];
  count: number;
  query: string;
  setQuery: (query: string) => void;
  loading: boolean;
  loadingMore: boolean;
  loaded: boolean;
  hasMore: boolean;
  failure: LoadFailure | null;
  reload: () => void;
  loadMore: () => void;
}

/**
 * One keyset pager for every browse list. `load` must be stable (wrap it
 * in useCallback with the active filters); a new identity means the filter
 * changed and starts a fresh first page. The previous rows stay visible
 * while that page loads, then are replaced; load-more appends and dedupes.
 */
export function usePagedList<T extends { id: string }>(
  load: (params: PageParams) => Promise<PagedResponse<T>>,
  options: UsePagedListOptions = {},
): UsePagedListResult<T> {
  const { limit = 50, debounceMs = 250 } = options;
  const [state, setState] = useState<PagedState<T>>(() => initialPagedState<T>());
  const [query, setQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');
  const requestId = useRef(0);
  const stateRef = useRef(state);
  stateRef.current = state;

  useEffect(() => {
    const timer = window.setTimeout(() => setDebouncedQuery(query.trim()), debounceMs);
    return () => window.clearTimeout(timer);
  }, [query, debounceMs]);

  const fetchPage = useCallback(
    async (append: boolean, q: string) => {
      const id = ++requestId.current;
      setState((current) => pagedLoadStart(current, append));
      try {
        const page = await load({
          limit,
          cursor: append ? stateRef.current.nextCursor : undefined,
          q: q || undefined,
        });
        if (id !== requestId.current) return;
        setState((current) => pagedLoadSuccess(current, page, append));
      } catch (error) {
        if (id !== requestId.current) return;
        setState((current) => pagedLoadFailure(current, error));
      }
    },
    [limit, load],
  );

  useEffect(() => {
    void fetchPage(false, debouncedQuery);
  }, [fetchPage, debouncedQuery]);

  const loadMore = useCallback(() => {
    const current = stateRef.current;
    if (current.loading || current.loadingMore || !current.hasMore) return;
    void fetchPage(true, debouncedQuery);
  }, [fetchPage, debouncedQuery]);

  const reload = useCallback(() => {
    void fetchPage(false, debouncedQuery);
  }, [fetchPage, debouncedQuery]);

  const failure = useMemo(
    () => (state.error === null ? null : classifyCurrentLoadFailure(state.error)),
    [state.error],
  );

  return {
    items: state.items,
    count: state.count,
    query,
    setQuery,
    loading: state.loading,
    loadingMore: state.loadingMore,
    loaded: state.loaded,
    hasMore: state.hasMore,
    failure,
    reload,
    loadMore,
  };
}
