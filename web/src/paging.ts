/**
 * Pure state transitions for the opt-in list pager. The hook in
 * usePagedList.ts owns the request lifecycle; keeping the state shape pure
 * lets the stale-page, append and duplicate-row rules be unit tested
 * without a React renderer.
 */

export interface PageParams {
  limit?: number;
  cursor?: string;
  q?: string;
}

export interface PagedResponse<T> {
  items: T[];
  count: number;
  nextCursor?: string;
  hasMore?: boolean;
}

export interface PagedState<T> {
  items: T[];
  count: number;
  nextCursor: string;
  hasMore: boolean;
  loading: boolean;
  loadingMore: boolean;
  loaded: boolean;
  error: unknown | null;
}

export function initialPagedState<T>(): PagedState<T> {
  return {
    items: [],
    count: 0,
    nextCursor: '',
    hasMore: false,
    loading: false,
    loadingMore: false,
    loaded: false,
    error: null,
  };
}

/**
 * A reset keeps the previous rows visible while the new first page loads
 * (the cached-previous-page rule) but drops its cursor so a failed search
 * can never append more rows from the old result set.
 */
export function pagedLoadStart<T>(state: PagedState<T>, append: boolean): PagedState<T> {
  return {
    ...state,
    loading: !append,
    loadingMore: append,
    error: null,
    nextCursor: append ? state.nextCursor : '',
    hasMore: append ? state.hasMore : false,
  };
}

export function pagedLoadSuccess<T extends { id: string }>(
  state: PagedState<T>,
  page: PagedResponse<T>,
  append: boolean,
): PagedState<T> {
  const items = append ? mergePageItems(state.items, page.items) : mergePageItems([], page.items);
  const nextCursor = page.nextCursor ?? '';
  return {
    ...state,
    items,
    count: page.count,
    nextCursor,
    hasMore: page.hasMore ?? nextCursor !== '',
    loading: false,
    loadingMore: false,
    loaded: true,
    error: null,
  };
}

export function pagedLoadFailure<T>(state: PagedState<T>, error: unknown): PagedState<T> {
  return { ...state, loading: false, loadingMore: false, error };
}

/** Stable append: a row that already exists on the page is not repeated. */
export function mergePageItems<T extends { id: string }>(current: T[], next: T[]): T[] {
  const seen = new Set(current.map((item) => item.id));
  const merged = [...current];
  for (const item of next) {
    if (seen.has(item.id)) continue;
    seen.add(item.id);
    merged.push(item);
  }
  return merged;
}
