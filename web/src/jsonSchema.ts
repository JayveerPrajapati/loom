import type { DataSchema, SchemaField } from './types';

// JSON Schema (draft-07) subset conversion (Phase 6 D6-3): Loom data
// contracts are flat field lists with five types; JSON Schema is the
// interchange format. The mapping is deliberately a NAMED subset —
// anything outside it is reported on import, never silently dropped,
// and the export is marked as coming from this subset.

const SUBSET_TYPES = ['string', 'number', 'boolean', 'object', 'array'] as const;

/** draft-07 keywords this subset does not carry; named on import. */
const UNSUPPORTED_KEYWORDS = [
  '$id',
  '$ref',
  'definitions',
  'enum',
  'const',
  'format',
  'pattern',
  'minimum',
  'maximum',
  'exclusiveMinimum',
  'exclusiveMaximum',
  'multipleOf',
  'minLength',
  'maxLength',
  'minItems',
  'maxItems',
  'uniqueItems',
  'minProperties',
  'maxProperties',
  'patternProperties',
  'additionalProperties',
  'propertyNames',
  'dependencies',
  'contains',
  'allOf',
  'anyOf',
  'oneOf',
  'not',
  'if',
  'then',
  'else',
];

type DraftNode = Record<string, unknown>;

function fieldToNode(field: SchemaField): DraftNode {
  const node: DraftNode = { type: field.type };
  if (field.description) node.description = field.description;
  if (field.type === 'object' && field.fields) {
    node.properties = fieldsToProperties(field.fields);
    const required = field.fields.filter((f) => f.required).map((f) => f.name);
    if (required.length > 0) node.required = required;
  }
  if (field.type === 'array' && field.items) {
    node.items = fieldToNode(field.items);
    // JSON Schema array items have no property key, so the Loom element
    // name rides as a marker; import restores it.
    node['x-loom-item-name'] = field.items.name;
  }
  return node;
}

function fieldsToProperties(fields: SchemaField[]): Record<string, DraftNode> {
  const properties: Record<string, DraftNode> = {};
  for (const field of fields) properties[field.name] = fieldToNode(field);
  return properties;
}

/** Export a Loom data contract as a marked draft-07 subset document. */
export function loomToDraft07(schema: DataSchema): Record<string, unknown> {
  const doc: Record<string, unknown> = {
    $schema: 'http://json-schema.org/draft-07/schema#',
    // Clearly marked: consumers can see this is the Loom subset, not a
    // hand-written full schema.
    'x-loom-subset': 'draft-07-subset-v1',
    'x-loom-schema-id': schema.id,
    'x-loom-schema-version': schema.version,
    title: schema.name,
    type: 'object',
    properties: fieldsToProperties(schema.fields ?? []),
  };
  if (schema.description) doc.description = schema.description;
  if (schema.owner) doc['x-loom-owner'] = schema.owner;
  if (schema.tags?.length) doc['x-loom-tags'] = schema.tags;
  const required = (schema.fields ?? []).filter((f) => f.required).map((f) => f.name);
  if (required.length > 0) doc.required = required;
  return doc;
}

export interface Draft07Import {
  name: string;
  description?: string;
  owner?: string;
  tags?: string[];
  fields: SchemaField[];
  /** Every subset-foreign keyword/type that was dropped, with its path. */
  unsupported: string[];
}

function nodeToField(
  name: string,
  node: DraftNode,
  path: string,
  unsupported: string[],
): SchemaField | null {
  // Name every subset-foreign keyword this node carries before mapping
  // the parts the subset understands — they are dropped, but never
  // silently.
  for (const keyword of UNSUPPORTED_KEYWORDS) {
    if (node[keyword] !== undefined) {
      unsupported.push(`"${keyword}" at ${path} (constraint dropped by the subset)`);
    }
  }
  const rawType = node.type;
  let type: SchemaField['type'] | null = null;
  if (typeof rawType === 'string' && (SUBSET_TYPES as readonly string[]).includes(rawType)) {
    type = rawType as SchemaField['type'];
  } else if (rawType === 'integer') {
    // Closest subset type; the substitution is named, not silent.
    type = 'number';
    unsupported.push(`type "integer" at ${path} (imported as number)`);
  } else if (Array.isArray(rawType)) {
    const first = rawType.find((t) => (SUBSET_TYPES as readonly string[]).includes(t)) as
      | SchemaField['type']
      | undefined;
    if (first) {
      type = first;
      unsupported.push(`union type ${JSON.stringify(rawType)} at ${path} (first subset type "${first}" kept)`);
    }
  }
  if (!type) {
    unsupported.push(
      `unsupported type ${JSON.stringify(rawType ?? null)} at ${path} (field skipped)`,
    );
    return null;
  }
  const field: SchemaField = { name, type };
  if (typeof node.description === 'string' && node.description) field.description = node.description;
  if (type === 'object') {
    const properties = node.properties;
    if (properties && typeof properties === 'object' && !Array.isArray(properties)) {
      field.fields = markRequired(
        propertiesToFields(properties as Record<string, DraftNode>, `${path}.{`, unsupported),
        node,
      );
    }
  }
  if (type === 'array') {
    const items = node.items;
    if (items && typeof items === 'object' && !Array.isArray(items)) {
      const itemName =
        typeof node['x-loom-item-name'] === 'string' && node['x-loom-item-name']
          ? node['x-loom-item-name']
          : 'item';
      const item = nodeToField(itemName, items as DraftNode, `${path}[]`, unsupported);
      if (item) field.items = item;
    }
  }
  return field;
}

function propertiesToFields(
  properties: Record<string, DraftNode>,
  path: string,
  unsupported: string[],
): SchemaField[] {
  const fields: SchemaField[] = [];
  for (const [name, value] of Object.entries(properties)) {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) {
      unsupported.push(`property "${name}" is not a schema object (field skipped)`);
      continue;
    }
    const field = nodeToField(name, value, `${path}${name}`, unsupported);
    if (field) fields.push(field);
  }
  return fields;
}

function requiredNames(node: DraftNode): Set<string> {
  const names = new Set<string>();
  if (Array.isArray(node.required)) {
    for (const name of node.required) {
      if (typeof name === 'string') names.add(name);
    }
  }
  return names;
}

function markRequired(fields: SchemaField[], node: DraftNode): SchemaField[] {
  const required = requiredNames(node);
  if (required.size === 0) return fields;
  return fields.map((field) =>
    required.has(field.name) ? { ...field, required: true } : field,
  );
}

/** Import a draft-07 document as a Loom data contract draft. */
export function draft07ToLoom(doc: unknown): { schema: Draft07Import; error?: string } {
  if (doc === null || typeof doc !== 'object' || Array.isArray(doc)) {
    return { schema: emptyImport(), error: 'The document must be a JSON object.' };
  }
  const node = doc as DraftNode;
  const unsupported: string[] = [];
  for (const keyword of UNSUPPORTED_KEYWORDS) {
    if (keyword !== '$id' && node[keyword] !== undefined) {
      unsupported.push(`"${keyword}" at $ (constraint dropped by the subset)`);
    }
  }
  if (node.properties === undefined) {
    return {
      schema: emptyImport(),
      error: 'The document has no "properties" — a contract needs at least the field list.',
    };
  }
  if (node.properties === null || typeof node.properties !== 'object' || Array.isArray(node.properties)) {
    return { schema: emptyImport(), error: '"properties" must be an object.' };
  }
  const fields = markRequired(
    propertiesToFields(node.properties as Record<string, DraftNode>, '$.', unsupported),
    node,
  );
  if (node.type !== undefined && node.type !== 'object') {
    unsupported.push(`root type ${JSON.stringify(node.type)} (a contract is always an object)`);
  }
  const schema: Draft07Import = {
    name: typeof node.title === 'string' && node.title ? node.title : '',
    fields,
    unsupported,
  };
  if (typeof node.description === 'string' && node.description) schema.description = node.description;
  if (typeof node['x-loom-owner'] === 'string' && node['x-loom-owner']) {
    schema.owner = node['x-loom-owner'];
  }
  if (Array.isArray(node['x-loom-tags'])) {
    schema.tags = node['x-loom-tags'].filter((tag): tag is string => typeof tag === 'string');
  }
  return { schema };
}

function emptyImport(): Draft07Import {
  return { name: '', fields: [], unsupported: [] };
}
