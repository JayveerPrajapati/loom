import { describe, expect, it } from 'vitest';
import { formFieldGroups, hasProfileableFields, inspectorTabs } from '../formLayout';
import type { FieldDef, NodeDef } from '../types';

function def(type: string, config: Record<string, FieldDef>): NodeDef {
  return { type, category: 'action', config } as NodeDef;
}

const PROFILEABLE: FieldDef = { type: 'string', profileable: true };
const STRING: FieldDef = { type: 'string' };
const NUMBER: FieldDef = { type: 'number' };
const OBJECT: FieldDef = { type: 'object' };
const ARRAY: FieldDef = { type: 'array' };
const BOOLEAN: FieldDef = { type: 'boolean' };

const HTTP_DEF = def('http.request', {
  method: { ...STRING, profileable: true },
  url: { ...STRING, profileable: true },
  query: { ...OBJECT, profileable: true },
  bodyType: { ...STRING, profileable: true },
  body: { type: 'any' },
  rawBody: { ...STRING, advanced: true },
  contentType: { ...STRING, advanced: true, profileable: true },
  parts: { ...ARRAY, advanced: true },
  graphqlQuery: { ...STRING, advanced: true },
  graphqlVariables: { ...OBJECT, advanced: true },
  pagination: { ...OBJECT, advanced: true, profileable: true },
  bodyFields: { ...ARRAY, advanced: true, profileable: true },
  mappingRefs: { ...OBJECT, advanced: true },
});

const MAP_DEF = def('data.map', {
  mappingRef: { ...STRING, required: true },
  mappingRows: { ...ARRAY, advanced: true },
  overrides: OBJECT,
  outputType: STRING,
});

describe('inspectorTabs', () => {
  it('gives every non-trigger node a Data tab', () => {
    expect(inspectorTabs('flow.if')).toEqual(['configuration', 'data', 'runtime', 'advanced']);
  });

  it('omits Data for the cron and manual triggers', () => {
    expect(inspectorTabs('trigger.cron')).toEqual(['configuration', 'runtime', 'advanced']);
    expect(inspectorTabs('trigger.manual')).toEqual(['configuration', 'runtime', 'advanced']);
  });

  it('keeps Profile out of the tabs for every type — it lives in the summary', () => {
    expect(inspectorTabs('http.request')).toEqual(['configuration', 'data', 'runtime', 'advanced']);
    expect(inspectorTabs('data.map')).toEqual(['configuration', 'data', 'runtime', 'advanced']);
    expect(hasProfileableFields(HTTP_DEF)).toBe(true);
    expect(hasProfileableFields(MAP_DEF)).toBe(false);
  });

  it('keeps the tab order configuration, data, runtime, advanced', () => {
    expect(inspectorTabs('db.postgres')).toEqual(['configuration', 'data', 'runtime', 'advanced']);
  });
});

describe('formFieldGroups', () => {
  it('splits common and advanced and preserves declaration order', () => {
    const groups = formFieldGroups('db.postgres', {}, def('db.postgres', { query: STRING, operation: PROFILEABLE }));
    expect(groups.common.map(([key]) => key)).toEqual(['query', 'operation']);
    expect(groups.advanced).toEqual([]);
  });

  it('hides data.map mappingRows entirely (publish overwrites it)', () => {
    const groups = formFieldGroups('data.map', {}, MAP_DEF);
    expect(groups.common.map(([key]) => key).sort()).toEqual(['mappingRef', 'outputType', 'overrides']);
    expect(groups.advanced).toEqual([]);
  });

  it('gates http.request body fields on the effective body type', () => {
    const none = formFieldGroups('http.request', {}, HTTP_DEF);
    expect(none.common.map(([key]) => key)).toEqual(['method', 'url', 'bodyType']);
    expect(none.advanced.map(([key]) => key)).not.toContain('contentType');

    const raw = formFieldGroups('http.request', { bodyType: 'raw' }, HTTP_DEF);
    expect(raw.advanced.map(([key]) => key)).toContain('rawBody');
    expect(raw.advanced.map(([key]) => key)).toContain('contentType');
    expect(raw.common.map(([key]) => key)).not.toContain('body');
  });

  it('keeps the http param-group keys out of the generic loop', () => {
    const groups = formFieldGroups('http.request', { bodyType: 'json' }, HTTP_DEF);
    const keys = [...groups.common, ...groups.advanced].map(([key]) => key);
    expect(keys).not.toContain('query');
    expect(keys).not.toContain('bodyFields');
    expect(keys).not.toContain('mappingRefs');
  });

  it('excludes the cron schedule keys owned by the cron editor', () => {
    const cronDef = def('trigger.cron', { schedule: { ...STRING, required: true }, timezone: STRING });
    const groups = formFieldGroups('trigger.cron', {}, cronDef);
    expect([...groups.common, ...groups.advanced]).toEqual([]);
  });

  it('groups boolean and number fields with common fields by default', () => {
    const groups = formFieldGroups('code.python', {}, def('code.python', {
      source: { ...STRING, required: true },
      requirements: ARRAY,
      timeoutSeconds: { ...NUMBER, advanced: true },
      maxMemoryMB: { ...NUMBER, advanced: true },
    }));
    expect(groups.common.map(([key]) => key)).toEqual(['source', 'requirements']);
    expect(groups.advanced.map(([key]) => key)).toEqual(['timeoutSeconds', 'maxMemoryMB']);
  });

  it('handles a definition with no config', () => {
    const groups = formFieldGroups('trigger.subflow', {}, def('trigger.subflow', {}));
    expect(groups.common).toEqual([]);
    expect(groups.advanced).toEqual([]);
  });

  it('does not leak boolean profileable checks into tab math', () => {
    // sanity for hasProfileableFields with mixed spec types
    expect(hasProfileableFields(def('x', { a: BOOLEAN, b: PROFILEABLE }))).toBe(true);
    expect(hasProfileableFields(def('x', { a: BOOLEAN }))).toBe(false);
    expect(hasProfileableFields(undefined)).toBe(false);
  });
});
