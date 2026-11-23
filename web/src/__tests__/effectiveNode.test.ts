import { describe, expect, it } from 'vitest';
import { effectiveSchemaRefs, nodeEffective } from '../effectiveNode';
import type { ConfigProfile, WorkflowNode } from '../types';

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

function profile(
  config: Record<string, unknown>,
  extra: Record<string, unknown> = {},
): ConfigProfile {
  return {
    id: 'p1',
    name: 'P',
    nodeType: 'http.request',
    version: 3,
    config,
    ...extra,
  } as ConfigProfile;
}

describe('nodeEffective', () => {
  it('returns null without a pinned profile', () => {
    expect(nodeEffective(node({ url: 'https://x' }), null)).toBeNull();
  });

  it('reports no inherited value for keys the profile does not supply', () => {
    const eff = nodeEffective(node({ url: 'https://x' }), profile({ timeout: 45 }));
    expect(eff?.inheritedValue('url')).toBeUndefined();
  });

  it('shows the profile value for a key the node leaves empty', () => {
    const eff = nodeEffective(node({ url: 'https://x' }), profile({ timeout: 45 }));
    expect(eff?.field('timeout')).toEqual({ value: 45, origin: 'profile', overridden: false });
    expect(eff?.field('url')).toEqual({ value: 'https://x', origin: 'node', overridden: false });
    expect(eff?.field('unknown')).toBeNull();
  });

  it('badges a differing node value as an override a reset can clear', () => {
    const eff = nodeEffective(
      node({ timeout: 10 }),
      profile({ timeout: 45 }),
    );
    expect(eff?.field('timeout')).toEqual({ value: 10, origin: 'node', overridden: true });
    expect(eff?.canReset('timeout')).toBe(true);
    expect(eff?.inheritedValue('timeout')).toBe(45);
  });

  it('treats a legacy node value equal to the profile value as node-owned', () => {
    // Publish's legacy value-wins rule keeps the stored node value; an
    // equal profile value is not evidence that the profile supplied it.
    const eff = nodeEffective(
      node({ timeout: 45 }),
      profile({ timeout: 45 }),
    );
    expect(eff?.field('timeout')).toEqual({ value: 45, origin: 'node', overridden: false });
    expect(eff?.canReset('timeout')).toBe(false);
    expect(eff?.inheritedValue('timeout')).toBe(45);
  });

  it('uses explicit ownership instead of value equality', () => {
    const eff = nodeEffective(
      node(
        { timeout: 45, url: 'https://override.example.com' },
        { profileOverrides: { url: true } },
      ),
      profile({ timeout: 60, url: 'https://profile.example.com' }),
    );
    expect(eff?.field('timeout')).toEqual({
      value: 60,
      origin: 'profile',
      overridden: false,
    });
    expect(eff?.field('url')).toEqual({
      value: 'https://override.example.com',
      origin: 'node',
      overridden: true,
    });
    expect(eff?.canReset('url')).toBe(true);
  });

  it('fills empty reference slots from the bundle', () => {
    const codeEff = nodeEffective(
      node({ source: 'return 1' }, { type: 'code.run' }),
      profile({}, { bundle: { script: 's-3' } }),
    );
    expect(codeEff?.field('scriptRef')).toEqual({ value: 's-3', origin: 'bundle', overridden: false });
    expect(codeEff?.field('source')).toEqual({ value: 'return 1', origin: 'node', overridden: false });

    const mapEff = nodeEffective(
      node({}, { type: 'data.map' }),
      profile({}, { bundle: { mappings: { item: 'm-9' } } }),
    );
    expect(mapEff?.field('mappingRef')).toEqual({ value: 'm-9', origin: 'bundle', overridden: false });
  });

  it('merges parameter-group rows and reports per-row origins', () => {
    const eff = nodeEffective(
      node({
        bodyFields: [
          { name: 'userId', value: 'node-value', mode: 'static' },
        ],
      }),
      profile({
        bodyFields: [
          { name: 'userId', value: 'profile-value', mode: 'static' },
          { name: 'plan', value: 'pro', mode: 'static' },
        ],
      }),
    );
    const group = eff?.field('bodyFields');
    expect(group?.origin).toBe('node');
    const rows = group?.value as { name: string; value: string }[];
    expect(rows.find((row) => row.name === 'userId')?.value).toBe('node-value');
    expect(rows.find((row) => row.name === 'plan')?.value).toBe('pro');
    expect(eff?.rowOrigin('bodyFields', 'userId')).toBe('node');
    expect(eff?.rowOrigin('bodyFields', 'plan')).toBe('profile');
    expect(eff?.rowOrigin('bodyFields', 'missing')).toBe('profile');
    // Group keys never reset as a whole.
    expect(eff?.canReset('bodyFields')).toBe(false);
  });

  it('covers connector node types through the generic path', () => {
    // Connector nodes (connector.<id>.<op>) have Profileable catalog
    // fields and render through GenericFormFields — the same
    // FormFieldList wiring. The resolver is type-agnostic.
    const connectorNode = node(
      { limit: 10 },
      { type: 'connector.example-api.list' },
    );
    const connectorProfile: ConfigProfile = {
      ...profile({ limit: 25, cursor: 'next-page' }),
      nodeType: 'connector.example-api.list',
    };
    const eff = nodeEffective(connectorNode, connectorProfile);
    expect(eff?.field('limit')).toEqual({ value: 10, origin: 'node', overridden: true });
    expect(eff?.field('cursor')).toEqual({ value: 'next-page', origin: 'profile', overridden: false });
  });

  it('hides a group plain target once its row array survives', () => {
    const eff = nodeEffective(
      node({ bodyFields: [{ name: 'a', value: 'x', mode: 'static' }] }),
      profile({ body: { a: 'x' }, bodyFields: [{ name: 'a', value: 'y', mode: 'static' }] }),
    );
    expect(eff?.field('body')).toBeNull();
  });
});

describe('effectiveSchemaRefs', () => {
  it('node refs win; bundle refs fill empty slots', () => {
    const withNodeRefs = effectiveSchemaRefs(
      { inputSchemaRef: 'node-in', outputSchemaRef: 'node-out' },
      profile({}, { bundle: { schemas: { input: 'b-in', output: 'b-out' } } }),
    );
    expect(withNodeRefs.input).toEqual({ value: 'node-in', origin: 'node' });
    expect(withNodeRefs.output).toEqual({ value: 'node-out', origin: 'node' });

    const empty = effectiveSchemaRefs(
      {},
      profile({}, { bundle: { schemas: { input: 'b-in', output: 'b-out' } } }),
    );
    expect(empty.input).toEqual({ value: 'b-in', origin: 'bundle' });
    expect(empty.output).toEqual({ value: 'b-out', origin: 'bundle' });
  });

  it('without a profile only node refs exist', () => {
    const refs = effectiveSchemaRefs({ inputSchemaRef: 'node-in' }, null);
    expect(refs.input).toEqual({ value: 'node-in', origin: 'node' });
    expect(refs.output).toBeNull();
  });
});
