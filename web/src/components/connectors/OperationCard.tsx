import type { ConnectorOperation } from '../../types';
import { METHODS, RESPONSE_MODES } from './operations';
import { OperationFieldsTable } from './OperationFieldsTable';

/** One operation's structured card: the request template plus the field
 *  spec rows that become the node type's config fields. Advanced keys
 *  (query, headers, body templates) stay in the JSON escape hatch. */
export function OperationCard({
  op,
  index,
  onChange,
  onRemove,
}: {
  op: ConnectorOperation;
  index: number;
  onChange: (next: ConnectorOperation) => void;
  onRemove: () => void;
}) {
  const patch = (patched: Partial<ConnectorOperation>) => onChange({ ...op, ...patched });
  return (
    <div className="connector-op-card" data-testid={`connector-op-${index}`}>
      <div className="connector-op-head">
        <h4>Operation {index + 1}</h4>
        <button type="button" className="text-button" onClick={onRemove}>
          Remove
        </button>
      </div>
      <div className="grid-2">
        <label className="field" htmlFor={`op-${index}-op`}>
          <span className="field-label">Op id</span>
          <input
            id={`op-${index}-op`}
            className="text-input"
            value={op.op}
            placeholder="list"
            required
            onChange={(event) => patch({ op: event.target.value })}
          />
          <span className="field-hint">Becomes connector.&lt;id&gt;.{op.op || 'list'}.</span>
        </label>
        <label className="field" htmlFor={`op-${index}-label`}>
          <span className="field-label">Label</span>
          <input
            id={`op-${index}-label`}
            className="text-input"
            value={op.label ?? ''}
            placeholder="List things"
            onChange={(event) => patch({ label: event.target.value })}
          />
        </label>
      </div>
      <div className="grid-2">
        <label className="field" htmlFor={`op-${index}-method`}>
          <span className="field-label">Method</span>
          <select
            id={`op-${index}-method`}
            className="text-input"
            value={op.method}
            onChange={(event) => patch({ method: event.target.value })}
          >
            {METHODS.map((method) => (
              <option key={method} value={method}>
                {method}
              </option>
            ))}
          </select>
        </label>
        <label className="field" htmlFor={`op-${index}-successCodes`}>
          <span className="field-label">Success codes</span>
          <input
            id={`op-${index}-successCodes`}
            className="text-input"
            value={op.successCodes ?? ''}
            placeholder="200,2xx"
            onChange={(event) => patch({ successCodes: event.target.value })}
          />
        </label>
      </div>
      <label className="field" htmlFor={`op-${index}-url`}>
        <span className="field-label">URL template</span>
        <input
          id={`op-${index}-url`}
          className="text-input code-input"
          value={op.url}
          placeholder="{{cred.baseUrl}}/things"
          required
          onChange={(event) => patch({ url: event.target.value })}
        />
        <span className="field-hint">
          Placeholders resolve from config fields, credential fields, then the item scope.
        </span>
      </label>
      <label className="field" htmlFor={`op-${index}-responseMode`}>
        <span className="field-label">Response mode</span>
        <select
          id={`op-${index}-responseMode`}
          className="text-input"
          value={op.responseMode ?? 'items'}
          onChange={(event) => patch({ responseMode: event.target.value })}
        >
          {RESPONSE_MODES.map((mode) => (
            <option key={mode} value={mode}>
              {mode}
            </option>
          ))}
        </select>
      </label>

      <OperationFieldsTable
        fields={op.fields ?? {}}
        onChange={(fields) => patch({ fields })}
      />
    </div>
  );
}
