import { describe, expect, it } from 'vitest';
import { bodyTypeFor, isConfigFieldVisible, visibleConfigFields } from '../fieldVisibility';
import type { FieldDef, NodeDef } from '../types';

// Phase 0 item 1: freeze the inspector's field-visibility rules. The
// rendered per-node-type snapshot is frozen separately in
// e2e/inspector-fields.spec.ts against the live catalog; these unit tests
// pin the rules themselves.

function defWith(config: Record<string, FieldDef>, type = 'node.custom'): NodeDef {
  return {
    type,
    typeVersion: 1,
    category: 'action',
    description: '',
    inputs: ['main'],
    outputs: ['main'],
    inputType: 'any',
    outputType: 'any',
    config,
  };
}

describe('bodyTypeFor', () => {
  it('returns "none" for an http.request with no body config', () => {
    expect(bodyTypeFor('http.request', {})).toBe('none');
  });

  it('infers json from seeded bodyFields or a form body', () => {
    expect(bodyTypeFor('http.request', { bodyFields: [{ name: 'a' }] })).toBe('json');
    expect(bodyTypeFor('http.request', { body: { a: 1 } })).toBe('json');
  });

  it('infers raw from a seeded rawBody', () => {
    expect(bodyTypeFor('http.request', { rawBody: 'text' })).toBe('raw');
  });

  it('lets an explicit bodyType win over the inferred shape', () => {
    expect(bodyTypeFor('http.request', { bodyType: 'form', bodyFields: [{ name: 'a' }] })).toBe('form');
    expect(bodyTypeFor('http.request', { bodyType: 'none', rawBody: 'text' })).toBe('none');
  });

  it('returns an empty string for non-http node types', () => {
    expect(bodyTypeFor('code.run', { rawBody: 'text' })).toBe('');
    expect(bodyTypeFor('trigger.webhook', {})).toBe('');
  });
});

describe('isConfigFieldVisible', () => {
  it('hides the cron editor-owned fields on trigger.cron', () => {
    expect(isConfigFieldVisible('trigger.cron', 'schedule', '')).toBe(false);
    expect(isConfigFieldVisible('trigger.cron', 'timezone', '')).toBe(false);
    expect(isConfigFieldVisible('trigger.cron', 'anything-else', '')).toBe(true);
  });

  it('hides the parameter-group keys on http.request', () => {
    for (const key of ['query', 'headers', 'pathParams', 'bodyFields', 'queryFields', 'headerFields', 'pathFields', 'mappingRefs']) {
      expect(isConfigFieldVisible('http.request', key, 'json'), key).toBe(false);
    }
  });

  it('shows the http body keys only for their bodyType', () => {
    expect(isConfigFieldVisible('http.request', 'body', 'form')).toBe(true);
    expect(isConfigFieldVisible('http.request', 'body', 'json')).toBe(false);
    expect(isConfigFieldVisible('http.request', 'rawBody', 'raw')).toBe(true);
    expect(isConfigFieldVisible('http.request', 'contentType', 'raw')).toBe(true);
    expect(isConfigFieldVisible('http.request', 'rawBody', 'json')).toBe(false);
    expect(isConfigFieldVisible('http.request', 'contentType', 'none')).toBe(false);
  });

  it('gates the graphql and multipart keys on their bodyTypes (Phase 3 D3-6)', () => {
    expect(isConfigFieldVisible('http.request', 'graphqlQuery', 'graphql')).toBe(true);
    expect(isConfigFieldVisible('http.request', 'graphqlVariables', 'graphql')).toBe(true);
    expect(isConfigFieldVisible('http.request', 'graphqlQuery', 'json')).toBe(false);
    expect(isConfigFieldVisible('http.request', 'graphqlVariables', 'none')).toBe(false);
    expect(isConfigFieldVisible('http.request', 'parts', 'multipart')).toBe(true);
    expect(isConfigFieldVisible('http.request', 'parts', 'form')).toBe(false);
  });

  it('shows every field on other node types', () => {
    expect(isConfigFieldVisible('code.run', 'source', '')).toBe(true);
    expect(isConfigFieldVisible('db.postgres', 'query', '')).toBe(true);
    expect(isConfigFieldVisible('trigger.webhook', 'path', '')).toBe(true);
  });
});

describe('visibleConfigFields', () => {
  it('filters and preserves the catalog declaration order', () => {
    const def = defWith({
      url: { type: 'string', required: true },
      query: { type: 'object' },
      method: { type: 'string', enum: ['GET', 'POST'] },
    }, 'http.request');
    expect(visibleConfigFields('http.request', {}, def).map(([key]) => key)).toEqual(['url', 'method']);
  });

  it('keeps advanced fields in the visible set (the split is a layout concern)', () => {
    const def = defWith({
      timeoutSeconds: { type: 'number', advanced: true },
      source: { type: 'string', required: true },
    }, 'code.python');
    // Declaration order is preserved; advanced is a layout-only split.
    expect(visibleConfigFields('code.python', {}, def).map(([key]) => key)).toEqual(['timeoutSeconds', 'source']);
  });

  it('returns an empty list for a definition with no config', () => {
    expect(visibleConfigFields('trigger.subflow', {}, defWith({}))).toEqual([]);
    expect(visibleConfigFields('anything', {}, undefined)).toEqual([]);
  });
});
