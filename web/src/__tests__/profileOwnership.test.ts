import { describe, expect, it } from 'vitest';
import { profileAttachPatch, profileDetachPatch } from '../profileOwnership';
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

function profile(config: Record<string, unknown>, extra: Partial<ConfigProfile> = {}): ConfigProfile {
  return {
    id: 'p',
    name: 'Profile',
    nodeType: 'http.request',
    version: 2,
    config,
    ...extra,
  } as ConfigProfile;
}

describe('profile ownership transitions', () => {
  it('marks every pre-existing profile key as an explicit override on attach', () => {
    const patch = profileAttachPatch(node({ timeout: 45, url: 'https://node' }), profile({ timeout: 45 }));
    expect(patch).toEqual({
      profileRef: 'p',
      profileOverrides: { timeout: true },
    });
  });

  it('keeps sparse new-node config fully inherited on attach', () => {
    const patch = profileAttachPatch(node({}, { profileOverrides: {} }), profile({ timeout: 45, method: 'POST' }));
    expect(patch).toEqual({ profileRef: 'p', profileOverrides: {} });
  });

  it('keeps inherited keys inherited when switching profiles', () => {
    const previous = profile({ timeout: 45 });
    const next = profile({ timeout: 60, method: 'POST' }, { id: 'p2' });
    const attached = node({ timeout: 45 }, { profileRef: 'p', profileOverrides: {} });
    expect(profileAttachPatch(attached, next, previous)).toEqual({
      profileRef: 'p2',
      profileOverrides: {},
    });
  });

  it('marks a local key as an override when the next profile supplies it', () => {
    const previous = profile({ timeout: 45 });
    const next = profile({ timeout: 45, url: 'https://profile' }, { id: 'p2' });
    const attached = node(
      { timeout: 45, url: 'https://node' },
      { profileRef: 'p', profileOverrides: {} },
    );
    expect(profileAttachPatch(attached, next, previous)).toEqual({
      profileRef: 'p2',
      profileOverrides: { url: true },
    });
  });

  it('keeps an explicit override when switching profiles', () => {
    const previous = profile({ timeout: 45 });
    const next = profile({ timeout: 60 }, { id: 'p2' });
    const attached = node(
      { timeout: 45 },
      { profileRef: 'p', profileOverrides: { timeout: true } },
    );
    expect(profileAttachPatch(attached, next, previous)).toEqual({
      profileRef: 'p2',
      profileOverrides: { timeout: true },
    });
  });

  it('materializes inherited config, retry, and bundle refs on detach', () => {
    const current = profile(
      { timeout: 45 },
      {
        retry: { maxAttempts: 3 },
        bundle: {
          schemas: { input: 'schema-in', output: 'schema-out' },
          mappings: { query: 'mapping-q' },
          script: 'script-1',
        },
      },
    );
    const attached = node(
      { url: 'https://override', mappingRef: 'node-map' },
      { profileRef: 'p', profileOverrides: { url: true } },
    );
    const patch = profileDetachPatch(attached, current) as Partial<WorkflowNode> & {
      config: Record<string, unknown>;
    };
    expect(patch.profileRef).toBeUndefined();
    expect(patch.profileOverrides).toBeUndefined();
    expect(patch.config).toEqual({
      url: 'https://override',
      timeout: 45,
      mappingRef: 'node-map',
      mappingRefs: { query: 'mapping-q' },
    });
    expect(patch.retry).toEqual({ maxAttempts: 3 });
    expect(patch.inputSchemaRef).toBe('schema-in');
    expect(patch.outputSchemaRef).toBe('schema-out');
    expect(patch.config.mappingRefs).toEqual({ query: 'mapping-q' });
  });

  it('returns a minimal detach patch without a profile', () => {
    expect(profileDetachPatch(node({}), null)).toEqual({
      profileRef: undefined,
      profileOverrides: undefined,
    });
  });
});
