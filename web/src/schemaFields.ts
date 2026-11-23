import type { SchemaField } from './types';

/** Field types a data contract can express (mirrors internal/schemas). */
export const FIELD_TYPES: SchemaField['type'][] = [
  'string',
  'number',
  'boolean',
  'object',
  'array',
];

const NAME_RE = /^[a-zA-Z_][a-zA-Z0-9_]*$/;

/**
 * Client-side contract validation mirroring the server's rules so authors
 * get immediate feedback: names must be non-empty identifiers and unique
 * per level, objects need nested fields, arrays carry items not fields,
 * scalars carry neither, and nesting is depth-capped.
 */
export function validateSchemaFields(
  fields: SchemaField[],
  prefix = '',
  depth = 0,
): string[] {
  const errors: string[] = [];
  if (depth > 6) {
    return [`${labelFor(prefix)}: nesting exceeds the maximum depth of 6`];
  }
  const seen = new Set<string>();
  fields.forEach((field, index) => {
    const path = prefix ? `${prefix}.${field.name || `#${index + 1}`}` : field.name || `#${index + 1}`;
    if (!field.name.trim()) {
      errors.push(`${labelFor(prefix)}: field ${index + 1} has no name`);
    } else if (!NAME_RE.test(field.name.trim())) {
      errors.push(`${path}: field names may only use letters, digits and underscores`);
    } else if (seen.has(field.name.trim())) {
      errors.push(`${path}: duplicate field name`);
    }
    seen.add(field.name.trim());
    if (!FIELD_TYPES.includes(field.type)) {
      errors.push(`${path}: unknown field type`);
    }
    if (field.type === 'object') {
      if ((field.fields ?? []).length === 0) {
        errors.push(`${path}: object fields need at least one nested field`);
      }
      if (field.items) {
        errors.push(`${path}: objects cannot declare array items`);
      }
      errors.push(...validateSchemaFields(field.fields ?? [], path, depth + 1));
    }
    if (field.type === 'array') {
      if ((field.fields ?? []).length > 0) {
        errors.push(`${path}: arrays cannot declare nested fields (use items)`);
      }
      if (field.items) {
        // The element contract is a field level of its own — its type,
        // name and nested rules are validated like any other row.
        errors.push(...validateSchemaFields([field.items], `${path}[]`, depth + 1));
      }
    }
    if (field.type !== 'object' && field.type !== 'array' && (field.fields ?? []).length > 0) {
      errors.push(`${path}: scalar fields cannot declare nested fields`);
    }
  });
  return errors;
}

function labelFor(prefix: string): string {
  return prefix ? prefix : 'the contract';
}

/** Total number of fields across all levels, for list rows and summaries. */
export function countFields(fields: SchemaField[]): number {
  let count = fields.length;
  for (const field of fields) {
    if (field.fields) count += countFields(field.fields);
    if (field.items) count += 1 + (field.items.fields ? countFields(field.items.fields) : 0);
  }
  return count;
}

/** Deepest nesting level (root = 1), for the depth-cap hint. */
export function fieldDepth(fields: SchemaField[]): number {
  let depth = 1;
  for (const field of fields) {
    let sub = 0;
    if (field.fields) sub = fieldDepth(field.fields);
    if (field.items) sub = Math.max(sub, fieldDepth([field.items]));
    depth = Math.max(depth, sub + 1);
  }
  return depth;
}
