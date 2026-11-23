import { describe, expect, it } from 'vitest';
import {
  compileMappingRows,
  compileParamRows,
  mergeParamRows,
  mergeProfileConfig,
  objectToParamRows,
  paramRowsToMappingRows,
  toParamRows,
} from '../paramRows';
import type { MappingRow } from '../types';

// Configuration Hub v4.1 E4: profile parameter rows merge per row name —
// these tests mirror the Go cases in TestPublishMergesProfileParamRowsPerName
// (internal/server/profiles_test.go) so browser preview/validation and
// server publish agree.

describe('mergeParamRows', () => {
  it('node rows win per name and keep their order', () => {
    const nodeRows = toParamRows([
      { name: 'limit', mode: 'static', type: 'number', value: 25 },
    ]);
    const profileRows = toParamRows([
      { name: 'limit', mode: 'static', type: 'number', value: 50 },
      { name: 'active', mode: 'static', type: 'boolean', value: true },
    ]);
    const merged = mergeParamRows(nodeRows, profileRows);
    expect(merged).toHaveLength(2);
    expect(merged[0]).toMatchObject({ name: 'limit', value: 25 });
    expect(merged[1]).toMatchObject({ name: 'active', value: true });
  });
});

describe('mergeProfileConfig', () => {
  it('merges parameter rows per name', () => {
    const merged = mergeProfileConfig(
      {
        method: 'POST',
        queryFields: [
          { name: 'limit', mode: 'static', type: 'number', value: 50 },
          { name: 'active', mode: 'static', type: 'boolean', value: true },
        ],
      },
      {
        method: 'POST',
        queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 25 }],
      },
    );
    const rows = toParamRows(merged.queryFields);
    expect(rows.map((row) => [row.name, row.value])).toEqual([
      ['limit', 25],
      ['active', true],
    ]);
    expect(merged.method).toBe('POST');
  });

  it('passes profile rows through when the node sets none', () => {
    const merged = mergeProfileConfig(
      { headerFields: [{ name: 'accept', mode: 'static', type: 'string', value: 'application/json' }] },
      { method: 'GET' },
    );
    expect(toParamRows(merged.headerFields)).toHaveLength(1);
    expect(merged.method).toBe('GET');
  });

  it('drops profile rows when the node sets the plain target object', () => {
    const merged = mergeProfileConfig(
      { queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 50 }] },
      { query: { q: 'x' } },
    );
    expect(merged.queryFields).toBeUndefined();
    expect(merged.query).toEqual({ q: 'x' });
  });

  it('keeps flat precedence for ordinary keys', () => {
    const merged = mergeProfileConfig({ timeout: 30, url: 'https://a.example' }, { timeout: 60 });
    expect(merged.timeout).toBe(60);
    expect(merged.url).toBe('https://a.example');
  });
});

// Profile-Centric UX Phase 1 (P0-2): bundle injection — these mirror the
// Go cases in TestResolveProfileBundlePrecedence
// (internal/store/profiles_bundle_resolution_test.go) so the browser's
// effective refs and the server's publish agree at every layer.
import { bundleSchemaRefs, injectBundleRefs } from '../paramRows';

describe('injectBundleRefs', () => {
  it('fills empty mappingRef on data.map', () => {
    const out = injectBundleRefs({ mappings: { item: 'm-1' } }, 'data.map', {});
    expect(out.mappingRef).toBe('m-1');
  });

  it('keeps the node mappingRef', () => {
    const out = injectBundleRefs({ mappings: { item: 'm-bundle' } }, 'data.map', {
      mappingRef: 'm-node',
    });
    expect(out.mappingRef).toBe('m-node');
  });

  it('item is ignored on http.request', () => {
    const out = injectBundleRefs({ mappings: { item: 'm-1' } }, 'http.request', {});
    expect(out.mappingRef).toBeUndefined();
  });

  it('fills empty mappingRefs groups on http.request', () => {
    const out = injectBundleRefs({ mappings: { query: 'm-q' } }, 'http.request', {});
    expect(out.mappingRefs).toEqual({ query: 'm-q' });
  });

  it('a node mappingRef for the same group wins', () => {
    const out = injectBundleRefs({ mappings: { query: 'm-bundle' } }, 'http.request', {
      mappingRefs: { query: 'm-node' },
    });
    expect(out.mappingRefs).toEqual({ query: 'm-node' });
  });

  it('a node plain target object drops the bundle group', () => {
    const out = injectBundleRefs({ mappings: { query: 'm-q' } }, 'http.request', {
      query: { mine: true },
    });
    expect(out.mappingRefs).toBeUndefined();
    expect(out.query).toEqual({ mine: true });
  });

  it('mixes into other existing groups without touching them', () => {
    const out = injectBundleRefs({ mappings: { query: 'm-q' } }, 'http.request', {
      mappingRefs: { headers: 'm-h' },
    });
    expect(out.mappingRefs).toEqual({ headers: 'm-h', query: 'm-q' });
  });

  it('does not mutate the input config', () => {
    const input: Record<string, unknown> = { mappingRefs: { headers: 'm-h' } };
    injectBundleRefs({ mappings: { query: 'm-q' } }, 'http.request', input);
    expect(input).toEqual({ mappingRefs: { headers: 'm-h' } });
  });

  it('returns the config untouched without a bundle', () => {
    expect(injectBundleRefs(undefined, 'http.request', { method: 'GET' })).toEqual({
      method: 'GET',
    });
  });
});

describe('bundleSchemaRefs', () => {
  it('the node ref wins, the bundle fills the empty slot', () => {
    expect(
      bundleSchemaRefs(
        { schemas: { input: 'sch-bundle', output: 'sch-out' } },
        { inputSchemaRef: 'sch-node' },
      ),
    ).toEqual({ input: 'sch-node', output: 'sch-out' });
  });

  it('empty without a bundle', () => {
    expect(bundleSchemaRefs(undefined, {})).toEqual({ input: '', output: '' });
  });
});

describe('injectBundleRefs scripts (Phase 4 D4-1)', () => {
  it('fills a code node empty scriptRef from the bundle script', () => {
    const out = injectBundleRefs({ script: 'scr-1' }, 'code.run', {});
    expect(out.scriptRef).toBe('scr-1');
  });

  it('keeps the node own scriptRef over the bundle script', () => {
    const out = injectBundleRefs({ script: 'scr-bundle' }, 'code.python', {
      scriptRef: 'scr-node',
    });
    expect(out.scriptRef).toBe('scr-node');
  });

  it('never injects a script into non-code nodes', () => {
    const out = injectBundleRefs({ script: 'scr-1' }, 'data.set', {});
    expect('scriptRef' in out).toBe(false);
  });
});

// P7-2: these mirror internal/graph/paramfields_test.go
// (TestCompileOverlayRows) so the mapping preview and save-as-mapping
// round-trip compile exactly like publish.
describe('compileMappingRows', () => {
  const rows: MappingRow[] = [
    { target: 'limit', source: '20' },
    { target: 'zip', source: '02134' },
    { target: 'active', source: 'true' },
    { target: 'meta', source: '{"a":1}' },
    { target: 'tpl', source: '{{ $json.page }}' },
    { target: 'text', source: 'hello' },
    { target: 'empty', source: '' },
  ];

  it('body keeps canonical JSON typed and everything else verbatim', () => {
    expect(compileMappingRows('body', rows)).toEqual({
      limit: 20,
      zip: '02134',
      active: true,
      meta: { a: 1 },
      tpl: '{{ $json.page }}',
      text: 'hello',
      empty: '',
    });
  });

  it('query, headers and pathParams stay verbatim strings', () => {
    expect(compileMappingRows('query', rows)).toEqual({
      limit: '20',
      zip: '02134',
      active: 'true',
      meta: '{"a":1}',
      tpl: '{{ $json.page }}',
      text: 'hello',
      empty: '',
    });
  });
});

describe('Fields/JSON compile round-trip', () => {
  it('compiles typed rows back to the source object', () => {
    const source = {
      limit: 20,
      active: true,
      meta: { a: 1 },
      text: 'hello',
      tpl: '{{ $json.page }}',
    };
    expect(compileParamRows(objectToParamRows(source))).toEqual(source);
  });

  it('keeps JSON-looking strings strings through save-as-mapping and compile', () => {
    const rows = objectToParamRows({ text: '20', zip: '02134' });
    expect(compileParamRows(rows)).toEqual({ text: '20', zip: '02134' });
    const mapping = paramRowsToMappingRows(rows);
    expect(mapping).toEqual([
      { target: 'text', source: '"20"' },
      { target: 'zip', source: '02134' },
    ]);
    expect(compileMappingRows('body', mapping)).toEqual({ text: '20', zip: '02134' });
  });
});
