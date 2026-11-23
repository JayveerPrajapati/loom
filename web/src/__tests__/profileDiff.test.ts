import { describe, expect, it } from 'vitest';

import type { ConfigProfile, DataSchema, Mapping } from '../types';
import {
  diffMappingRows,
  diffProfileDocs,
  diffSchemaFields,
  type DiffRow,
} from '../components/ProfileHistory';

function profile(overrides: Partial<ConfigProfile>): ConfigProfile {
  return {
    id: 'p1',
    name: 'P',
    nodeType: 'http.request',
    version: 1,
    config: {},
    createdAt: '',
    updatedAt: '',
    ...overrides,
  } as ConfigProfile;
}

function find(rows: DiffRow[], key: string) {
  return rows.find((row) => row.key === key);
}

describe('diffProfileDocs (UX Phase 2 D2-4 deepening)', () => {
  it('expands parameter-group arrays to one diff row per row name', () => {
    const before = profile({
      config: {
        queryFields: [
          { name: 'limit', mode: 'static', type: 'number', value: 20 },
          { name: 'verbose', mode: 'static', type: 'boolean', value: false },
        ],
      },
    });
    const after = profile({
      config: {
        queryFields: [
          { name: 'limit', mode: 'static', type: 'number', value: 50 },
          { name: 'cursor', mode: 'expression', type: 'string', value: '{{ $.next }}' },
        ],
      },
    });
    const rows = diffProfileDocs(before, after);
    // changed row
    const limit = find(rows, 'config.queryFields.limit');
    expect(limit?.change).toBe('changed');
    expect(limit?.before).toContain('"value":20');
    expect(limit?.after).toContain('"value":50');
    // removed row
    const verbose = find(rows, 'config.queryFields.verbose');
    expect(verbose?.change).toBe('removed');
    // added row
    const cursor = find(rows, 'config.queryFields.cursor');
    expect(cursor?.change).toBe('added');
    expect(cursor?.after).toContain('{{ $.next }}');
    // no whole-group blob row
    expect(find(rows, 'config.queryFields')).toBeUndefined();
  });

  it('reports a whole added group as per-row added rows', () => {
    const rows = diffProfileDocs(
      profile({ config: {} }),
      profile({
        config: { headerFields: [{ name: 'accept', mode: 'static', type: 'string', value: 'json' }] },
      }),
    );
    // Parseable rows diff per row name — an added group reads as its
    // added rows, not an unreadable array blob.
    const accept = find(rows, 'config.headerFields.accept');
    expect(accept?.change).toBe('added');
    expect(accept?.after).toContain('json');
    // The blob fallback covers only unparseable content.
    expect(find(rows, 'config.headerFields')).toBeUndefined();
  });

  it('keeps credential, retry and bundle-ref rows', () => {
    const rows = diffProfileDocs(
      profile({
        config: { url: 'https://a.example.com' },
        credentialRefs: { http: 'old-cred' },
        retry: { maxAttempts: 3 },
        bundle: { mappings: { query: 'map-a' }, schemas: { input: 'schema-a' } },
      }),
      profile({
        config: { url: 'https://b.example.com' },
        credentialRefs: { http: 'new-cred' },
        retry: { maxAttempts: 5 },
        bundle: { mappings: { query: 'map-b' }, schemas: { input: 'schema-a' } },
      }),
    );
    expect(find(rows, 'credentialRefs.http')?.change).toBe('changed');
    expect(find(rows, 'retry')?.after).toContain('"maxAttempts":5');
    expect(find(rows, 'bundle.mappings.query')?.before).toContain('map-a');
    expect(find(rows, 'bundle.mappings.query')?.after).toContain('map-b');
    // unchanged refs produce no rows.
    expect(find(rows, 'bundle.schemas.input')).toBeUndefined();
  });
});

describe('diffMappingRows / diffSchemaFields (bundle content sub-diffs)', () => {
  it('diffs mapping rows by target', () => {
    const before = { rows: [{ target: 'limit', source: '20' }] } as Mapping;
    const after = {
      rows: [
        { target: 'limit', source: '50' },
        { target: 'cursor', source: '{{ $.next }}' },
      ],
    } as Mapping;
    const rows = diffMappingRows(before, after);
    expect(find(rows, 'limit')?.change).toBe('changed');
    expect(find(rows, 'cursor')?.change).toBe('added');
  });

  it('diffs schema fields by name', () => {
    const before = { fields: [{ name: 'total', type: 'number' }] } as DataSchema;
    const after = { fields: [{ name: 'total', type: 'string' }] } as DataSchema;
    const rows = diffSchemaFields(before, after);
    expect(find(rows, 'total')?.change).toBe('changed');
    expect(find(rows, 'total')?.before).toContain('"type":"number"');
  });

  it('treats missing sides as empty', () => {
    const rows = diffMappingRows(null, { rows: [{ target: 'a', source: '1' }] } as Mapping);
    expect(find(rows, 'a')?.change).toBe('added');
    expect(
      diffSchemaFields(
        { id: 's', name: 'S', version: 1, fields: [], createdAt: '', updatedAt: '' },
        null,
      ),
    ).toEqual([]);
  });
});
