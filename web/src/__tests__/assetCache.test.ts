import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  cachedAsset,
  cachedBlob,
  invalidateAssets,
  resetAssetCacheForTests,
  seedAsset,
} from '../assetCache';

beforeEach(() => {
  resetAssetCacheForTests();
});

async function flush() {
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe('asset cache', () => {
  it('joins an in-flight request instead of starting a second one', async () => {
    let calls = 0;
    let release: (value: string) => void = () => undefined;
    const fetcher = () => {
      calls += 1;
      return new Promise<string>((resolve) => {
        release = resolve;
      });
    };
    const first = cachedAsset('asset', fetcher);
    const second = cachedAsset('asset', fetcher);
    expect(calls).toBe(1);
    release('value');
    await expect(first).resolves.toBe('value');
    await expect(second).resolves.toBe('value');
  });

  it('reuses a fresh value', async () => {
    let calls = 0;
    const fetcher = async () => {
      calls += 1;
      return calls;
    };
    expect(await cachedAsset('asset', fetcher, 60_000)).toBe(1);
    expect(await cachedAsset('asset', fetcher, 60_000)).toBe(1);
    expect(calls).toBe(1);
  });

  it('returns a stale value immediately and refreshes in the background', async () => {
    const values = ['old', 'new'];
    let index = 0;
    const fetcher = async () => values[index++];
    expect(await cachedAsset('asset', fetcher, 60_000)).toBe('old');
    await expect(cachedAsset('asset', fetcher, 0)).resolves.toBe('old');
    await flush();
    expect(await cachedAsset('asset', fetcher, 60_000)).toBe('new');
    expect(index).toBe(2);
  });

  it('seeds a value without a request', async () => {
    seedAsset('asset', 'seeded');
    let calls = 0;
    const value = await cachedAsset('asset', async () => {
      calls += 1;
      return 'fetched';
    });
    expect(value).toBe('seeded');
    expect(calls).toBe(0);
  });

  it('invalidates one kind without touching another', async () => {
    seedAsset('mappings:all', 'mappings-old');
    seedAsset('schemas:all', 'schemas-old');
    invalidateAssets('mappings:');
    let mappingCalls = 0;
    let schemaCalls = 0;
    expect(
      await cachedAsset('mappings:all', async () => {
        mappingCalls += 1;
        return 'mappings-new';
      }),
    ).toBe('mappings-new');
    expect(
      await cachedAsset('schemas:all', async () => {
        schemaCalls += 1;
        return 'schemas-new';
      }),
    ).toBe('schemas-old');
    expect(mappingCalls).toBe(1);
    expect(schemaCalls).toBe(0);
  });

  it('retries after a failed first load', async () => {
    let calls = 0;
    const failing = async () => {
      calls += 1;
      throw new Error('boom');
    };
    await expect(cachedAsset('asset', failing)).rejects.toThrow('boom');
    await expect(cachedAsset('asset', failing)).rejects.toThrow('boom');
    expect(calls).toBe(2);
  });

  it('deduplicates blobs and evicts the oldest past the cap', async () => {
    const fetcher = vi.fn(async (hash: string) => `value-${hash}`);
    for (let index = 0; index < 20; index += 1) {
      await cachedBlob(`h${index}`, () => fetcher(`h${index}`));
    }
    expect(fetcher).toHaveBeenCalledTimes(20);
    // The 21st insert evicts h0, the oldest entry.
    await cachedBlob('h20', () => fetcher('h20'));
    expect(fetcher).toHaveBeenCalledTimes(21);
    await cachedBlob('h0', () => fetcher('h0'));
    expect(fetcher).toHaveBeenCalledTimes(22);
    await cachedBlob('h2', () => fetcher('h2'));
    expect(fetcher).toHaveBeenCalledTimes(22);
  });

  it('joins concurrent blob requests', async () => {
    let calls = 0;
    let release: (value: string) => void = () => undefined;
    const fetcher = () => {
      calls += 1;
      return new Promise<string>((resolve) => {
        release = resolve;
      });
    };
    const first = cachedBlob('hash', fetcher);
    const second = cachedBlob('hash', fetcher);
    expect(calls).toBe(1);
    release('blob');
    await expect(first).resolves.toBe('blob');
    await expect(second).resolves.toBe('blob');
  });
});
