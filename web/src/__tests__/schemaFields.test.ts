import { describe, expect, it } from 'vitest';
import { countFields, fieldDepth, validateSchemaFields } from '../schemaFields';
import type { SchemaField } from '../types';

const lead: SchemaField[] = [
  { name: 'email', type: 'string', required: true },
  { name: 'name', type: 'string' },
  {
    name: 'address',
    type: 'object',
    fields: [{ name: 'city', type: 'string', required: true }],
  },
  { name: 'tags', type: 'array', items: { name: 'item', type: 'string' } },
];

describe('validateSchemaFields', () => {
  it('accepts a sound contract', () => {
    expect(validateSchemaFields(lead)).toEqual([]);
  });

  it('reports empty and malformed names', () => {
    expect(validateSchemaFields([{ name: ' ', type: 'string' }])[0]).toContain('no name');
    expect(validateSchemaFields([{ name: 'bad-name', type: 'string' }])[0]).toContain(
      'letters, digits and underscores',
    );
  });

  it('reports duplicates per level but allows them across levels', () => {
    const dup = [
      { name: 'x', type: 'string' as const },
      { name: 'x', type: 'number' as const },
    ];
    expect(validateSchemaFields(dup)).toHaveLength(1);
    const nested = [
      {
        name: 'outer',
        type: 'object' as const,
        fields: [{ name: 'outer', type: 'string' as const }],
      },
    ];
    expect(validateSchemaFields(nested)).toEqual([]);
  });

  it('enforces per-type child rules', () => {
    expect(validateSchemaFields([{ name: 'obj', type: 'object' }])[0]).toContain(
      'at least one nested field',
    );
    expect(
      validateSchemaFields([
        { name: 'arr', type: 'array', fields: [{ name: 'x', type: 'string' }] },
      ])[0],
    ).toContain('use items');
    expect(
      validateSchemaFields([{ name: 's', type: 'string', fields: [{ name: 'x', type: 'string' }] }])[0],
    ).toContain('cannot declare nested fields');
  });

  it('reports problems inside array item contracts', () => {
    const broken = [
      {
        name: 'items',
        type: 'array' as const,
        items: { name: 'item', type: 'object' as const, fields: [] },
      },
    ];
    expect(validateSchemaFields(broken)[0]).toContain('at least one nested field');
  });
});

describe('countFields', () => {
  it('counts across levels and array item contracts', () => {
    expect(countFields(lead)).toBe(6);
    expect(countFields([])).toBe(0);
  });
});

describe('fieldDepth', () => {
  it('measures nesting depth', () => {
    expect(fieldDepth(lead)).toBe(2);
    const deep: SchemaField[] = [
      {
        name: 'a',
        type: 'object',
        fields: [
          {
            name: 'b',
            type: 'object',
            fields: [
              {
                name: 'c',
                type: 'object',
                fields: [{ name: 'd', type: 'string' }],
              },
            ],
          },
        ],
      },
    ];
    expect(fieldDepth(deep)).toBe(4);
  });
});
