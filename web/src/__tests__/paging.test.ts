import { describe, expect, it } from 'vitest';
import {
  initialPagedState,
  mergePageItems,
  pagedLoadFailure,
  pagedLoadStart,
  pagedLoadSuccess,
} from '../paging';

interface Row {
  id: string;
  name: string;
}

const row = (id: string): Row => ({ id, name: id });

describe('paged state', () => {
  it('starts empty and idle', () => {
    const state = initialPagedState<Row>();
    expect(state.items).toEqual([]);
    expect(state.count).toBe(0);
    expect(state.nextCursor).toBe('');
    expect(state.hasMore).toBe(false);
    expect(state.loading).toBe(false);
    expect(state.loaded).toBe(false);
    expect(state.error).toBeNull();
  });

  it('keeps the previous page visible on reset but drops its cursor', () => {
    const ready = pagedLoadSuccess(
      initialPagedState<Row>(),
      { items: [row('a'), row('b')], count: 4, nextCursor: 'cursor-1' },
      false,
    );
    const searching = pagedLoadStart(ready, false);
    expect(searching.items.map((item) => item.id)).toEqual(['a', 'b']);
    expect(searching.loading).toBe(true);
    expect(searching.loadingMore).toBe(false);
    expect(searching.nextCursor).toBe('');
    expect(searching.hasMore).toBe(false);
    expect(searching.error).toBeNull();
  });

  it('keeps the cursor on a load-more start', () => {
    const ready = pagedLoadSuccess(
      initialPagedState<Row>(),
      { items: [row('a')], count: 2, nextCursor: 'cursor-1' },
      false,
    );
    const more = pagedLoadStart(ready, true);
    expect(more.loading).toBe(false);
    expect(more.loadingMore).toBe(true);
    expect(more.nextCursor).toBe('cursor-1');
    expect(more.hasMore).toBe(true);
  });

  it('replaces the page on a reset result', () => {
    const state = pagedLoadSuccess(
      initialPagedState<Row>(),
      { items: [row('b'), row('c')], count: 2, nextCursor: 'next' },
      false,
    );
    expect(state.items.map((item) => item.id)).toEqual(['b', 'c']);
    expect(state.count).toBe(2);
    expect(state.nextCursor).toBe('next');
    expect(state.hasMore).toBe(true);
    expect(state.loaded).toBe(true);
    expect(state.loading).toBe(false);
  });

  it('appends only new rows and dedupes by id', () => {
    const first = pagedLoadSuccess(
      initialPagedState<Row>(),
      { items: [row('a'), row('b')], count: 3, nextCursor: 'cursor-1' },
      false,
    );
    const second = pagedLoadSuccess(
      first,
      { items: [row('b'), row('c')], count: 3, nextCursor: '' },
      true,
    );
    expect(second.items.map((item) => item.id)).toEqual(['a', 'b', 'c']);
    expect(second.hasMore).toBe(false);
    expect(second.nextCursor).toBe('');
  });

  it('uses nextCursor as the hasMore fallback', () => {
    const state = pagedLoadSuccess(
      initialPagedState<Row>(),
      { items: [row('a')], count: 2, nextCursor: 'more' },
      false,
    );
    expect(state.hasMore).toBe(true);
  });

  it('keeps rows when a load fails and records the error', () => {
    const ready = pagedLoadSuccess(
      initialPagedState<Row>(),
      { items: [row('a'), row('b')], count: 2, nextCursor: '' },
      false,
    );
    const error = new Error('boom');
    const failed = pagedLoadFailure(ready, error);
    expect(failed.items.map((item) => item.id)).toEqual(['a', 'b']);
    expect(failed.error).toBe(error);
    expect(failed.loading).toBe(false);
    expect(failed.loadingMore).toBe(false);
    expect(failed.loaded).toBe(true);
  });

  it('merges pages without reordering the existing rows', () => {
    const merged = mergePageItems([row('b'), row('a')], [row('a'), row('c')]);
    expect(merged.map((item) => item.id)).toEqual(['b', 'a', 'c']);
  });
});
