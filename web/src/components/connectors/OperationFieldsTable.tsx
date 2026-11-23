import type { ConnectorField } from '../../types';
import { FIELD_TYPES } from './operations';

/** The labeled field table inside one operation card: a visible
 *  Name/Type/Required header on the same tracks as the rows, then one
 *  row per config field with its accessible names. */
export function OperationFieldsTable({
  fields,
  onChange,
}: {
  fields: Record<string, ConnectorField>;
  onChange: (next: Record<string, ConnectorField>) => void;
}) {
  const entries = Object.entries(fields);
  const setField = (name: string, next: ConnectorField | undefined) => {
    const copy = { ...fields };
    if (next) copy[name] = next;
    else delete copy[name];
    onChange(copy);
  };
  return (
    <div className="connector-fields-editor">
      <div className="section-head-with-hint">
        <h5>Config fields</h5>
        <span className="muted small">The operation node's config fields.</span>
      </div>
      {entries.length > 0 && (
        <div
          className="connector-field-header"
          data-testid="connector-field-header"
          aria-hidden="true"
        >
          <span>Name</span>
          <span>Type</span>
          <span>Required</span>
          <span />
        </div>
      )}
      {entries.map(([name, field]) => (
        <div className="connector-field-row" key={name} data-testid={`connector-field-row-${name}`}>
          <input
            className="text-input"
            aria-label={`Field name (${name})`}
            value={name}
            onChange={(event) => {
              const copy = { ...fields };
              delete copy[name];
              copy[event.target.value] = field;
              onChange(copy);
            }}
          />
          <select
            className="text-input"
            aria-label={`Field type (${name})`}
            value={field.type}
            onChange={(event) =>
              setField(name, { ...field, type: event.target.value as ConnectorField['type'] })
            }
          >
            {FIELD_TYPES.map((type) => (
              <option key={type} value={type}>
                {type}
              </option>
            ))}
          </select>
          <label className="checkbox-field">
            <input
              type="checkbox"
              checked={field.required === true}
              onChange={(event) => setField(name, { ...field, required: event.target.checked })}
            />
            <span>required</span>
          </label>
          <button
            type="button"
            className="text-button"
            aria-label={`Remove field ${name}`}
            onClick={() => setField(name, undefined)}
          >
            ×
          </button>
        </div>
      ))}
      <button
        type="button"
        className="text-button"
        onClick={() => onChange({ ...fields, [`field${entries.length + 1}`]: { type: 'string' } })}
      >
        + Add field
      </button>
    </div>
  );
}
