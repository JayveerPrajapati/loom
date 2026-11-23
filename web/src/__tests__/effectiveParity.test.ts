import { describe, expect, it } from 'vitest';
import { nodeEffective } from '../effectiveNode';
import { effectiveRows, type EffectiveInput, type EffectiveOrigin } from '../profileEffective';
import type { ConfigProfile, WorkflowNode } from '../types';

// P7-2: the browser preview and the publish resolver must agree. The
// scalar cases mirror internal/store/profiles_test.go
// (TestResolveProfileRefs*), the row cases mirror
// TestPublishMergesProfileParamRowsPerName, and the ref cases mirror
// TestResolveProfileBundlePrecedence.

function node(config: Record<string, unknown>, extra: Partial<WorkflowNode> = {}): WorkflowNode {
  return {
    id: 'n',
    type: 'http.request',
    typeVersion: 1,
    name: 'n',
    position: { x: 0, y: 0 },
    config,
    ...extra,
  };
}

function profile(config: Record<string, unknown>, extra: Record<string, unknown> = {}): ConfigProfile {
  return {
    id: 'p1',
    name: 'P',
    nodeType: 'http.request',
    version: 1,
    config,
    ...extra,
  } as ConfigProfile;
}

function preview(nodeValue: WorkflowNode, profileValue: ConfigProfile): EffectiveInput {
  return {
    nodeType: nodeValue.type,
    profile: {
      config: profileValue.config,
      bundle: profileValue.bundle ?? null,
      retry: profileValue.retry ?? null,
    },
    node: {
      config: nodeValue.config,
      profileOverrides: nodeValue.profileOverrides,
      retry: nodeValue.retry,
      inputSchemaRef: nodeValue.inputSchemaRef,
      outputSchemaRef: nodeValue.outputSchemaRef,
    },
  };
}

interface ScalarCase {
  name: string;
  node: WorkflowNode;
  profile: ConfigProfile;
  expected: Record<string, { value: unknown; origin: EffectiveOrigin; overridden: boolean }>;
}

const scalarCases: ScalarCase[] = [
  {
    name: 'a bare node inherits every profile value',
    node: node({}),
    profile: profile({ method: 'GET', timeout: 45 }),
    expected: {
      method: { value: 'GET', origin: 'profile', overridden: false },
      timeout: { value: 45, origin: 'profile', overridden: false },
    },
  },
  {
    name: 'an explicit override is node-owned',
    node: node({ url: 'https://node.example' }, { profileOverrides: { url: true } }),
    profile: profile({ url: 'https://profile.example' }),
    expected: {
      url: { value: 'https://node.example', origin: 'node', overridden: true },
    },
  },
  {
    name: 'explicit ownership without an override follows the profile',
    node: node({ url: 'https://node.example' }, { profileOverrides: {} }),
    profile: profile({ url: 'https://profile.example' }),
    expected: {
      url: { value: 'https://profile.example', origin: 'profile', overridden: false },
    },
  },
  {
    name: 'explicit ownership keeps a stored equal value profile-owned',
    node: node({ url: 'https://same.example' }, { profileOverrides: {} }),
    profile: profile({ url: 'https://same.example' }),
    expected: {
      url: { value: 'https://same.example', origin: 'profile', overridden: false },
    },
  },
  {
    name: 'legacy equal values stay node-owned',
    node: node({ url: 'https://same.example' }),
    profile: profile({ url: 'https://same.example' }),
    expected: {
      url: { value: 'https://same.example', origin: 'node', overridden: false },
    },
  },
  {
    name: 'legacy differing values stay node-owned and overridden',
    node: node({ url: 'https://node.example' }),
    profile: profile({ url: 'https://profile.example' }),
    expected: {
      url: { value: 'https://node.example', origin: 'node', overridden: true },
    },
  },
];

describe('effective-value parity', () => {
  for (const testCase of scalarCases) {
    it(testCase.name, () => {
      const rows = effectiveRows(preview(testCase.node, testCase.profile));
      const effective = nodeEffective(testCase.node, testCase.profile);
      for (const [key, expected] of Object.entries(testCase.expected)) {
        expect(rows.find((row) => row.key === key)).toEqual({
          key,
          value: expected.value,
          origin: expected.origin,
        });
        expect(effective?.field(key)).toEqual({
          value: expected.value,
          origin: expected.origin,
          overridden: expected.overridden,
        });
      }
    });
  }

  it('merges parameter rows per name and agrees on each row origin', () => {
    const nodeValue = node({
      queryFields: [
        { name: 'limit', mode: 'static', type: 'number', value: 10 },
        { name: 'extra', mode: 'static', type: 'string', value: 'x' },
      ],
    });
    const profileValue = profile({
      queryFields: [
        { name: 'limit', mode: 'static', type: 'number', value: 20 },
        { name: 'offset', mode: 'static', type: 'number', value: 0 },
      ],
    });
    const rows = effectiveRows(preview(nodeValue, profileValue));
    expect(rows.find((row) => row.key === 'queryFields.limit')).toEqual({
      key: 'queryFields.limit',
      value: 10,
      origin: 'node',
    });
    expect(rows.find((row) => row.key === 'queryFields.offset')).toEqual({
      key: 'queryFields.offset',
      value: 0,
      origin: 'profile',
    });
    const effective = nodeEffective(nodeValue, profileValue);
    expect(effective?.rowOrigin('queryFields', 'limit')).toBe('node');
    expect(effective?.rowOrigin('queryFields', 'offset')).toBe('profile');
    expect(effective?.rowOrigin('queryFields', 'extra')).toBe('node');
  });

  it('drops profile rows when the node sets the plain target object', () => {
    const nodeValue = node({ query: { limit: 5 } });
    const profileValue = profile({
      queryFields: [{ name: 'offset', mode: 'static', type: 'number', value: 0 }],
    });
    const rows = effectiveRows(preview(nodeValue, profileValue));
    expect(rows.find((row) => row.key === 'queryFields.offset')).toBeUndefined();
    expect(rows.find((row) => row.key === 'query')).toEqual({
      key: 'query',
      value: { limit: 5 },
      origin: 'node',
    });
    const effective = nodeEffective(nodeValue, profileValue);
    expect(effective?.field('queryFields')).toBeNull();
    expect(effective?.field('query')).toEqual({
      value: { limit: 5 },
      origin: 'node',
      overridden: false,
    });
  });

  it('fills bundle refs and schema slots once; the node wins', () => {
    const nodeValue = node(
      { mappingRefs: { query: 'node-map' } },
      { inputSchemaRef: 'node-schema' },
    );
    const profileValue = profile(
      {},
      {
        bundle: {
          mappings: { query: 'bundle-map', headers: 'bundle-headers' },
          schemas: { input: 'bundle-schema', output: 'bundle-out' },
        },
      },
    );
    const rows = effectiveRows(preview(nodeValue, profileValue));
    expect(rows.find((row) => row.key === 'mappingRefs.query')).toEqual({
      key: 'mappingRefs.query',
      value: 'node-map',
      origin: 'node',
    });
    expect(rows.find((row) => row.key === 'mappingRefs.headers')).toEqual({
      key: 'mappingRefs.headers',
      value: 'bundle-headers',
      origin: 'bundle',
    });
    expect(rows.find((row) => row.key === 'inputSchemaRef')).toEqual({
      key: 'inputSchemaRef',
      value: 'node-schema',
      origin: 'node',
    });
    expect(rows.find((row) => row.key === 'outputSchemaRef')).toEqual({
      key: 'outputSchemaRef',
      value: 'bundle-out',
      origin: 'bundle',
    });
    const effective = nodeEffective(nodeValue, profileValue);
    expect(effective?.field('mappingRefs')).toEqual({
      value: { query: 'node-map', headers: 'bundle-headers' },
      origin: 'node',
      overridden: false,
    });
    expect(effective?.field('inputSchemaRef')).toBeNull();
  });

  it('lets the node retry win and falls back to the profile retry', () => {
    const nodeRetry = { maxAttempts: 2 };
    const profileRetry = { maxAttempts: 5 };
    const nodeValue = node({}, { retry: nodeRetry });
    const profileValue = profile({}, { retry: profileRetry });
    expect(
      effectiveRows(preview(nodeValue, profileValue)).find((row) => row.key === 'retry'),
    ).toEqual({ key: 'retry', value: nodeRetry, origin: 'node' });
    expect(
      effectiveRows(preview(node({}), profileValue)).find((row) => row.key === 'retry'),
    ).toEqual({ key: 'retry', value: profileRetry, origin: 'profile' });
  });
});
