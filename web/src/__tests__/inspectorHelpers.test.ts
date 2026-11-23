import { describe, expect, it } from 'vitest';
import {
  bundleAssetLabels,
  collectFieldPaths,
  expressionFor,
  formatConfigValue,
  issueTouchesNode,
  labelFor,
} from '../components/inspectorHelpers';
import type { ConfigProfile, DataSchema, Mapping, Script, ValidationIssue } from '../types';

describe('labelFor', () => {
  it('splits camelCase and capitalizes the first word', () => {
    expect(labelFor('itemsPath')).toBe('Items Path');
    expect(labelFor('maxParallelRuns')).toBe('Max Parallel Runs');
  });

  it('handles keys with digits and single words', () => {
    expect(labelFor('url2')).toBe('Url2');
    expect(labelFor('method')).toBe('Method');
  });
});

describe('collectFieldPaths', () => {
  it('walks nested objects up to depth 3', () => {
    const out: string[] = [];
    collectFieldPaths({ user: { name: 'a', address: { city: 'b' } } }, '', out);
    expect(out).toEqual(['user.name', 'user.address.city']);
  });

  it('uses the first item for arrays with [0] indexing', () => {
    const out: string[] = [];
    collectFieldPaths({ items: [{ id: 1 }] }, '', out);
    expect(out).toEqual(['items', 'items[0].id']);
  });

  it('pushes the prefix for null, undefined, and empty containers', () => {
    const out: string[] = [];
    collectFieldPaths({ a: null, b: undefined, c: {}, d: [], e: 'x' }, '', out);
    expect(out).toEqual(['a', 'b', 'c', 'd', 'e']);
  });

  it('drops paths whose leaves live deeper than depth 3', () => {
    const deep = { a: { b: { c: { d: { e: 'x' } } } } };
    const out: string[] = [];
    collectFieldPaths(deep, '', out);
    expect(out).toEqual([]);
  });

  it('appends to a prefixed path', () => {
    const out: string[] = [];
    collectFieldPaths({ id: 1 }, 'items[0]', out);
    expect(out).toEqual(['items[0].id']);
  });
});

describe('expressionFor', () => {
  it('uses the short form for the main port', () => {
    expect(expressionFor('http', 'main', 'body.title')).toBe(
      '{{ $node["http"].json.body.title }}',
    );
  });

  it('qualifies non-main ports', () => {
    expect(expressionFor('each', 'items', '[0].id')).toBe(
      '{{ $node["each"].ports["items"][0].id }}',
    );
  });

  it('omits the suffix for an empty path', () => {
    expect(expressionFor('set', 'main', '')).toBe('{{ $node["set"].json }}');
  });
});

describe('issueTouchesNode', () => {
  it('matches node issues directly', () => {
    const issue = { severity: 'error', message: 'bad', node: 'a' } as ValidationIssue;
    expect(issueTouchesNode(issue, 'a')).toBe(true);
    expect(issueTouchesNode(issue, 'b')).toBe(false);
  });

  it('matches edge issues on either endpoint', () => {
    const issue = {
      severity: 'error',
      message: 'bad',
      edge: { from: { node: 'a', port: 'main' }, to: { node: 'b', port: 'main' } },
    } as unknown as ValidationIssue;
    expect(issueTouchesNode(issue, 'a')).toBe(true);
    expect(issueTouchesNode(issue, 'b')).toBe(true);
    expect(issueTouchesNode(issue, 'c')).toBe(false);
  });

  it('returns false for workflow-level issues', () => {
    const issue = { severity: 'error', message: 'bad' } as ValidationIssue;
    expect(issueTouchesNode(issue, 'a')).toBe(false);
  });
});

describe('formatConfigValue', () => {
  it('keeps strings readable and serializes structured values', () => {
    expect(formatConfigValue('https://example.com')).toBe('https://example.com');
    expect(formatConfigValue(20)).toBe('20');
    expect(formatConfigValue({ a: 1 })).toBe('{"a":1}');
    expect(formatConfigValue(undefined)).toBe('');
  });

  it('truncates long serialized values', () => {
    expect(formatConfigValue({ text: 'x'.repeat(200) }).endsWith('…')).toBe(true);
  });
});

describe('bundleAssetLabels', () => {
  it('returns no labels without a bundle', () => {
    expect(bundleAssetLabels(null, [], [], [])).toEqual([]);
  });

  it('names mapping, contract and script pins with their slots', () => {
    const profile = {
      id: 'p',
      name: 'P',
      nodeType: 'http.request',
      version: 1,
      config: {},
      createdAt: '',
      updatedAt: '',
      bundle: {
        mappings: { body: 'm1', query: 'm2' },
        schemas: { output: 's1' },
        script: 'sc1',
      },
    } as ConfigProfile;
    const mappings = [{ id: 'm1', name: 'Order body' }, { id: 'm2', name: 'Query' }] as Mapping[];
    const schemas = [{ id: 's1', name: 'Order' }] as DataSchema[];
    const scripts = [{ id: 'sc1', name: 'Transform' }] as Script[];
    expect(bundleAssetLabels(profile, mappings, schemas, scripts)).toEqual([
      'Order body (body)',
      'Query (query)',
      'Order (output)',
      'Transform (script)',
    ]);
  });

  it('falls back to the id when the asset is not loaded', () => {
    const profile = {
      id: 'p',
      name: 'P',
      nodeType: 'http.request',
      version: 1,
      config: {},
      createdAt: '',
      updatedAt: '',
      bundle: { mappings: { body: 'missing' } },
    } as ConfigProfile;
    expect(bundleAssetLabels(profile, [], [], [])).toEqual(['missing (body)']);
  });
});
