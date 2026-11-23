import { useSyncExternalStore } from 'react';

/**
 * Normalized asset cache for editor pickers. It is intentionally module
 * scoped: the app-level workspace indexes seed it, the Inspector reads it,
 * and management pages invalidate the kind they changed. Assets only — a
 * node draft is never stored here.
 */

export const assetKeys = {
  profiles: 'profiles:all',
  schemas: 'schemas:all',
  mappings: 'mappings:all',
  scripts: 'scripts:all',
  credentials: 'credentials:all',
  workflowPrefix: 'workflow:',
  workflow: (id: string) => `workflow:${id}`,
  schema: (id: string) => `schema:${id}`,
} as const;

interface AssetEntry<T> {
  value?: T;
  promise?: Promise<T>;
  loadedAt: number;
}

const DEFAULT_MAX_AGE_MS = 30_000;
const BLOB_CACHE_LIMIT = 20;

const entries = new Map<string, AssetEntry<unknown>>();
const blobs = new Map<string, { value?: unknown; promise?: Promise<unknown> }>();
const listeners = new Set<() => void>();
let version = 0;

function emit() {
  version += 1;
  for (const listener of listeners) listener();
}

export function subscribeAssetCache(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function assetCacheVersion(): number {
  return version;
}

/** Re-renders the caller when the cache is seeded or invalidated. */
export function useAssetCacheVersion(): number {
  return useSyncExternalStore(subscribeAssetCache, assetCacheVersion, assetCacheVersion);
}

/**
 * Returns the cached value when it is fresh, joins an in-flight request, or
 * starts one. A stale value is returned immediately while the refresh runs
 * in the background (stale-while-revalidate); a failed first load removes
 * the entry so Retry can try again.
 */
export function cachedAsset<T>(
  key: string,
  fetcher: () => Promise<T>,
  maxAgeMs = DEFAULT_MAX_AGE_MS,
): Promise<T> {
  const entry = entries.get(key) as AssetEntry<T> | undefined;
  if (entry?.promise) return entry.promise;
  if (entry?.value !== undefined && Date.now() - entry.loadedAt < maxAgeMs) {
    return Promise.resolve(entry.value);
  }
  const started = (() => {
    try {
      return fetcher();
    } catch (error) {
      return Promise.reject(error);
    }
  })();
  const promise = started
    .then((value) => {
      entries.set(key, { value, loadedAt: Date.now() });
      return value;
    })
    .catch((error) => {
      const current = entries.get(key) as AssetEntry<T> | undefined;
      if (current?.value !== undefined) {
        entries.set(key, { value: current.value, loadedAt: current.loadedAt });
        return current.value;
      }
      entries.delete(key);
      throw error;
    });
  entries.set(key, { value: entry?.value, promise, loadedAt: entry?.loadedAt ?? 0 });
  if (entry?.value !== undefined) {
    promise.catch(() => undefined);
    return Promise.resolve(entry.value);
  }
  return promise;
}

/** Seeds a value without a request (boot indexes, mutation responses). */
export function seedAsset<T>(key: string, value: T): void {
  const current = entries.get(key) as AssetEntry<T> | undefined;
  if (current?.value === value) return;
  entries.set(key, { value, loadedAt: Date.now() });
  emit();
}

/** Drops every entry whose key starts with prefix; bare calls drop all. */
export function invalidateAssets(prefix?: string): void {
  for (const key of [...entries.keys()]) {
    if (!prefix || key.startsWith(prefix)) entries.delete(key);
  }
  emit();
}

/** Clears every asset and blob at a session boundary (boot, logout). */
export function clearAssetCache(): void {
  entries.clear();
  blobs.clear();
  emit();
}

/**
 * Content-addressed blobs are immutable, so they need no invalidation. The
 * cap keeps a long editing session from retaining every output payload.
 */
export function cachedBlob<T>(hash: string, fetcher: () => Promise<T>): Promise<T> {
  const entry = blobs.get(hash);
  if (entry?.promise) return entry.promise as Promise<T>;
  if (entry?.value !== undefined) {
    blobs.delete(hash);
    blobs.set(hash, entry);
    return Promise.resolve(entry.value as T);
  }
  const started = (() => {
    try {
      return fetcher();
    } catch (error) {
      return Promise.reject(error);
    }
  })();
  const promise = started
    .then((value) => {
      blobs.delete(hash);
      blobs.set(hash, { value });
      while (blobs.size > BLOB_CACHE_LIMIT) {
        const oldest = blobs.keys().next().value;
        if (oldest === undefined) break;
        blobs.delete(oldest);
      }
      return value;
    })
    .catch((error) => {
      blobs.delete(hash);
      throw error;
    });
  blobs.set(hash, { promise });
  return promise;
}

/** Test helper: clears module state so each test starts empty. */
export function resetAssetCacheForTests(): void {
  entries.clear();
  blobs.clear();
  version = 0;
}
