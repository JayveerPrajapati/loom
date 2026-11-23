import { describe, expect, it } from 'vitest';
import { effectiveRows } from '../profileEffective';
import type { EffectiveInput } from '../profileEffective';

// UX Phase 2 D2-3 (plan P0-5/P1-7): the preview must mirror the publish
// merge exactly — the same cases the Go ResolveProfileRefs precedence
// test freezes, expressed as preview rows with origins.

function httpInput(node: EffectiveInput['node'], profile: Partial<EffectiveInput['profile']>): EffectiveInput {
  return {
    nodeType: 'http.request',
    profile: {
      config: profile.config,
      bundle: profile.bundle ?? null,
      retry: profile.retry ?? null,
    },
    node,
  };
}

describe('effectiveRows', () => {
  it('composes a bare node: every profile field is profile-origin, bundle refs are bundle-origin', () => {
    const rows = effectiveRows(
      httpInput(
        { config: {} },
        {
          config: { method: 'GET', url: 'https://example.test' },
          bundle: { mappings: { query: 'map-1' }, schemas: { input: 'schema-1' } },
          retry: { maxAttempts: 3 },
        },
      ),
    );
    expect(rows).toEqual([
      { key: 'mappingRefs.query', value: 'map-1', origin: 'bundle' },
      { key: 'inputSchemaRef', value: 'schema-1', origin: 'bundle' },
      { key: 'method', value: 'GET', origin: 'profile' },
      { key: 'url', value: 'https://example.test', origin: 'profile' },
      { key: 'retry', value: { maxAttempts: 3 }, origin: 'profile' },
    ]);
  });

  it('a node-level value wins per key and is node-origin', () => {
    const rows = effectiveRows(
      httpInput(
        { config: { method: 'POST' } },
        { config: { method: 'GET', url: 'https://example.test' } },
      ),
    );
    expect(rows.find((row) => row.key === 'method')).toEqual({
      key: 'method',
      value: 'POST',
      origin: 'node',
    });
    expect(rows.find((row) => row.key === 'url')?.origin).toBe('profile');
  });

  it('parameter-group rows merge per name: node rows win, profile rows fill', () => {
    const rows = effectiveRows(
      httpInput(
        {
          config: {
            queryFields: [
              { name: 'limit', type: 'number', value: 10 },
              { name: 'extra', type: 'string', value: 'x' },
            ],
          },
        },
        {
          config: {
            queryFields: [
              { name: 'limit', type: 'number', value: 20 },
              { name: 'offset', type: 'number', value: 0 },
            ],
          },
        },
      ),
    );
    const limit = rows.find((row) => row.key === 'queryFields.limit');
    expect(limit).toEqual({ key: 'queryFields.limit', value: 10, origin: 'node' });
    expect(rows.find((row) => row.key === 'queryFields.offset')).toEqual({
      key: 'queryFields.offset',
      value: 0,
      origin: 'profile',
    });
    // The node row the profile did not override keeps its origin.
    expect(rows.find((row) => row.key === 'queryFields.extra')?.origin).toBe('node');
  });

  it('a node-level plain target object drops the profile group rows entirely', () => {
    const rows = effectiveRows(
      httpInput(
        { config: { query: { limit: 5 } } },
        {
          config: {
            queryFields: [{ name: 'offset', type: 'number', value: 0 }],
          },
        },
      ),
    );
    expect(rows.find((row) => row.key === 'queryFields.offset')).toBeUndefined();
    expect(rows.find((row) => row.key === 'query')).toEqual({
      key: 'query',
      value: { limit: 5 },
      origin: 'node',
    });
  });

  it("the node's own mapping ref group is node-origin; the bundle fills only empty groups", () => {
    const rows = effectiveRows(
      httpInput(
        { config: { mappingRefs: { query: 'node-map' } } },
        { bundle: { mappings: { query: 'p-map', headers: 'h-map' } } },
      ),
    );
    expect(rows.find((row) => row.key === 'mappingRefs.query')).toEqual({
      key: 'mappingRefs.query',
      value: 'node-map',
      origin: 'node',
    });
    expect(rows.find((row) => row.key === 'mappingRefs.headers')).toEqual({
      key: 'mappingRefs.headers',
      value: 'h-map',
      origin: 'bundle',
    });
  });

  it("the node's own schema refs and retry win over the bundle/profile", () => {
    const rows = effectiveRows(
      httpInput(
        { config: {}, retry: { maxAttempts: 1 }, inputSchemaRef: 'node-schema' },
        { bundle: { schemas: { input: 'bundle-schema' } }, retry: { maxAttempts: 5 } },
      ),
    );
    expect(rows.find((row) => row.key === 'inputSchemaRef')).toEqual({
      key: 'inputSchemaRef',
      value: 'node-schema',
      origin: 'node',
    });
    expect(rows.find((row) => row.key === 'retry')).toEqual({
      key: 'retry',
      value: { maxAttempts: 1 },
      origin: 'node',
    });
  });

  it('a data.map bundle injects the item mapping into the empty mappingRef', () => {
    const rows = effectiveRows(
      {
        nodeType: 'data.map',
        profile: { config: {}, bundle: { mappings: { item: 'item-map' } }, retry: null },
        node: { config: {} },
      },
    );
    expect(rows.find((row) => row.key === 'mappingRef')).toEqual({
      key: 'mappingRef',
      value: 'item-map',
      origin: 'bundle',
    });
  });
});

describe('effectiveRows scripts (Phase 4 D4-1)', () => {
  it('marks a bundle-filled scriptRef as bundle-origin, a node-set one as node-origin', () => {
    const codeInput = (
      config: EffectiveInput['node']['config'],
      bundle?: { script: string },
    ): EffectiveInput => ({
      nodeType: 'code.run',
      profile: { config: { language: 'javascript' }, bundle: bundle ?? null, retry: null },
      node: { config },
    });
    expect(
      effectiveRows(codeInput({}, { script: 'scr-1' })).find((row) => row.key === 'scriptRef'),
    ).toEqual({ key: 'scriptRef', value: 'scr-1', origin: 'bundle' });
    expect(
      effectiveRows(codeInput({ scriptRef: 'scr-node' }, { script: 'scr-1' })).find(
        (row) => row.key === 'scriptRef',
      ),
    ).toEqual({ key: 'scriptRef', value: 'scr-node', origin: 'node' });
  });
});
