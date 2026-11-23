import { FIELD_TYPES } from '../../schemaFields';
import type { SchemaField } from '../../types';

export function cloneFields(fields: SchemaField[]): SchemaField[] {
  return fields.map((field) => ({
    ...field,
    fields: field.fields ? cloneFields(field.fields) : undefined,
    items: field.items ? cloneFields([field.items])[0] : undefined,
  }));
}

/**
 * FieldRows is the recursive contract editor: one row per field (name,
 * type, required), nested rows under object fields, and an element
 * contract under array fields.
 */
export function FieldRows({
  fields,
  onChange,
  depth,
}: {
  fields: SchemaField[];
  onChange: (next: SchemaField[]) => void;
  depth: number;
}) {
  const update = (index: number, next: SchemaField) => {
    const copy = [...fields];
    copy[index] = next;
    onChange(copy);
  };
  const remove = (index: number) => {
    onChange(fields.filter((_, i) => i !== index));
  };
  const append = () => {
    onChange([...fields, { name: '', type: 'string' }]);
  };
  return (
    <div className="schema-fields" data-testid="schema-fields">
      {fields.map((field, index) => (
        <div className="schema-field" key={index}>
          <div className="schema-field-row">
            <input
              className="text-input"
              placeholder="field name"
              aria-label={`Field ${index + 1} name`}
              value={field.name}
              onChange={(event) => update(index, { ...field, name: event.target.value })}
            />
            <select
              className="text-input"
              aria-label={`Field ${index + 1} type`}
              value={field.type}
              onChange={(event) =>
                update(index, { ...field, type: event.target.value as SchemaField['type'] })
              }
            >
              {FIELD_TYPES.map((type) => (
                <option key={type} value={type}>
                  {type}
                </option>
              ))}
            </select>
            <label className="schema-field-required">
              <input
                type="checkbox"
                aria-label={`Field ${index + 1} required`}
                checked={!!field.required}
                onChange={(event) => update(index, { ...field, required: event.target.checked })}
              />
              required
            </label>
            <input
              className="text-input"
              placeholder="description (optional)"
              aria-label={`Field ${index + 1} description`}
              value={field.description ?? ''}
              onChange={(event) => update(index, { ...field, description: event.target.value })}
            />
            <button
              type="button"
              className="button subtle"
              aria-label={`Remove field ${field.name || index + 1}`}
              onClick={() => remove(index)}
            >
              Remove
            </button>
          </div>
          {field.type === 'object' && (
            <div className="schema-field-children">
              <FieldRows
                fields={field.fields ?? []}
                onChange={(next) => update(index, { ...field, fields: next })}
                depth={depth + 1}
              />
            </div>
          )}
          {field.type === 'array' && (
            <div className="schema-field-children">
              <p className="field-hint">Each element:</p>
              <FieldRows
                fields={
                  field.items
                    ? [field.items]
                    : [{ name: 'item', type: 'string' as const }]
                }
                onChange={(next) => update(index, { ...field, items: next[0] })}
                depth={depth + 1}
              />
            </div>
          )}
        </div>
      ))}
      <button type="button" className="button subtle" onClick={append}>
        Add field
      </button>
    </div>
  );
}
