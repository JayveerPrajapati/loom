import type { ConnectorField, ConnectorOperation } from '../../types';

// Connector authoring rules shared by the structured cards and the
// raw-JSON escape hatch — both compile into the same operations.

export const METHODS = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD'];
export const FIELD_TYPES: ConnectorField['type'][] = [
  'string',
  'number',
  'boolean',
  'array',
  'object',
  'any',
];
export const RESPONSE_MODES = ['items', 'item', 'json', 'text'];

export function newOperation(): ConnectorOperation {
  return { op: 'list', method: 'GET', url: 'https://', fields: {} };
}

export function validateOperations(operations: ConnectorOperation[]): string {
  if (operations.length === 0) return 'At least one operation is required.';
  const seen = new Set<string>();
  for (const op of operations) {
    if (!op.op.trim()) return 'Every operation needs a short op id (e.g. list).';
    if (seen.has(op.op)) return `Duplicate operation "${op.op}".`;
    seen.add(op.op);
    if (!op.url.trim()) return `Operation "${op.op}" needs a URL template.`;
    for (const [name, field] of Object.entries(op.fields ?? {})) {
      if (!name.trim()) return `Operation "${op.op}" has an unnamed field.`;
      if (!FIELD_TYPES.includes(field.type)) {
        return `Field "${name}" of "${op.op}" has an unsupported type "${field.type}".`;
      }
    }
  }
  return '';
}

/** JSON parse errors either carry a "(line L column C)" suffix or a
 *  bare character position; translate the bare form so the author
 *  always gets a findable spot in the textarea. */
export function describeJsonError(text: string, message: string): string {
  if (/line \d+ column \d+/.test(message)) return message;
  const match = /position (\d+)/.exec(message);
  if (!match) return message;
  const position = Number(match[1]);
  const before = text.slice(0, position);
  const line = before.split('\n').length;
  const column = position - before.lastIndexOf('\n');
  return `${message} (line ${line}, column ${column})`;
}
