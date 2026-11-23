import { describe, expect, it } from 'vitest';
import { draft07ToLoom, loomToDraft07 } from '../jsonSchema';
import type { DataSchema, SchemaField } from '../types';

const base: DataSchema = {
  id: 'sch_test',
  name: 'Lead payload',
  description: 'A webhook lead',
  version: 3,
  fields: [
    { name: 'id', type: 'string', required: true, description: 'Lead id' },
    { name: 'score', type: 'number' },
    { name: 'vip', type: 'boolean' },
    {
      name: 'contact',
      type: 'object',
      required: true,
      fields: [
        { name: 'email', type: 'string', required: true },
        { name: 'phone', type: 'string' },
      ],
    },
    {
      name: 'events',
      type: 'array',
      items: {
        name: 'event',
        type: 'object',
        fields: [{ name: 'kind', type: 'string', required: true }],
      },
    },
  ],
  tags: ['webhook', 'leads'],
  owner: 'growth-team',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-01T00:00:00Z',
};

describe('jsonSchema round-trip', () => {
  it('exports a clearly-marked draft-07 subset document', () => {
    const doc = loomToDraft07(base);
    expect(doc['$schema']).toBe('http://json-schema.org/draft-07/schema#');
    expect(doc['x-loom-subset']).toBe('draft-07-subset-v1');
    expect(doc['title']).toBe('Lead payload');
    expect(doc['type']).toBe('object');
    expect(doc['required']).toEqual(['id', 'contact']);
    expect(doc['x-loom-owner']).toBe('growth-team');
    expect(doc['x-loom-tags']).toEqual(['webhook', 'leads']);
    const properties = doc['properties'] as Record<string, Record<string, unknown>>;
    expect(properties.id).toEqual({ type: 'string', description: 'Lead id' });
    expect(properties.contact['required']).toEqual(['email']);
    expect(properties.events['items']).toEqual({
      type: 'object',
      properties: { kind: { type: 'string' } },
      required: ['kind'],
    });
    expect(properties.events['x-loom-item-name']).toBe('event');
  });

  it('round-trips back to the same fields, tags and owner', () => {
    const doc = loomToDraft07(base);
    const { schema, error } = draft07ToLoom(doc);
    expect(error).toBeUndefined();
    expect(schema.name).toBe('Lead payload');
    expect(schema.description).toBe('A webhook lead');
    expect(schema.owner).toBe('growth-team');
    expect(schema.tags).toEqual(['webhook', 'leads']);
    expect(schema.unsupported).toEqual([]);
    expect(schema.fields).toEqual(base.fields);
  });

  it('names unsupported keywords instead of silently dropping them', () => {
    const { schema, error } = draft07ToLoom({
      $schema: 'http://json-schema.org/draft-07/schema#',
      title: 'Foreign',
      type: 'object',
      properties: {
        email: { type: 'string', format: 'email', minLength: 3 },
        count: { type: 'integer' },
        nested: {
          type: 'object',
          properties: { keep: { type: 'string' } },
          additionalProperties: false,
        },
      },
      required: ['email'],
    });
    expect(error).toBeUndefined();
    const joined = schema.unsupported.join('\n');
    expect(joined).toContain('"format" at $.email');
    expect(joined).toContain('"minLength" at $.email');
    expect(joined).toContain('"integer" at $.count (imported as number)');
    expect(joined).toContain('"additionalProperties" at $.nested');
    expect(schema.fields.find((f) => f.name === 'email')?.required).toBe(true);
    expect(schema.fields.find((f) => f.name === 'count')?.type).toBe('number');
    expect(
      schema.fields.find((f) => f.name === 'nested')?.fields?.map((f) => f.name),
    ).toEqual(['keep']);
  });

  it('rejects documents without properties, naming the problem', () => {
    expect(draft07ToLoom({ title: 'Empty' }).error).toContain('properties');
    expect(draft07ToLoom(null).error).toContain('JSON object');
    expect(draft07ToLoom({ properties: 3 }).error).toContain('"properties" must be an object');
  });

  it('reports union types and unsupported types', () => {
    const { schema } = draft07ToLoom({
      title: 'Unions',
      type: 'object',
      properties: {
        maybe: { type: ['string', 'null'] },
        weird: { type: 'symbol' },
      },
    });
    const joined = schema.unsupported.join('\n');
    expect(joined).toContain('union type ["string","null"] at $.maybe');
    expect(joined).toContain('first subset type "string" kept');
    expect(joined).toContain('unsupported type "symbol" at $.weird (field skipped)');
    expect(schema.fields.map((f) => f.name)).toEqual(['maybe']);
    expect(schema.fields[0].type).toBe('string');
  });

  it('imports a plain hand-written draft-07 schema with default item names', () => {
    const { schema } = draft07ToLoom({
      $schema: 'http://json-schema.org/draft-07/schema#',
      title: 'Hand written',
      type: 'object',
      properties: {
        rows: { type: 'array', items: { type: 'number' } },
      },
    });
    expect(schema.unsupported).toEqual([]);
    const rows = schema.fields[0] as SchemaField;
    expect(rows.type).toBe('array');
    expect(rows.items?.name).toBe('item');
    expect(rows.items?.type).toBe('number');
  });
});
