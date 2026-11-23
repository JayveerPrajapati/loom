import { useState } from 'react';
import { OriginBadge } from './assets/OriginBadge';
import {
  compileParamRows,
  objectToParamRows,
  type ParamRow,
  type ParamValueType,
} from '../paramRows';
import type { ExpressionCandidate } from '../autocomplete';

// Shared dual-mode parameter editor (Configuration Hub v3 plan D2/D3/D5):
// typed value rows (Static | Expression chips, layered autocomplete) on
// one side, raw JSON on the other. Used by the Inspector for HTTP
// parameter groups and by the hub pages for parameter-kind mappings —
// the same component, so both surfaces behave identically.

interface FieldGroupEditorProps {
  label: string;
  hint?: string;
  rows: ParamRow[];
  onRowsChange: (rows: ParamRow[]) => void;
  /** Raw-object (JSON) mode. Omit for editors that only have rows (the hub mapping editor). */
  jsonValue?: unknown;
  onJsonChange?: (value: unknown) => void;
  /** Layered expression candidates for the insert pills. */
  candidates?: ExpressionCandidate[];
  testIdPrefix: string;
  /** Per-row origin (UI-audit B2): badges rows the profile provides.
   * The rows shown may be the effective merge; edits still write the
   * node's own row array. */
  rowOrigin?: (name: string) => 'node' | 'profile' | 'bundle' | null;
}

const STATIC_TYPES: ParamValueType[] = ['string', 'number', 'boolean', 'json'];

export function FieldGroupEditor({
  label,
  hint,
  rows,
  onRowsChange,
  jsonValue,
  onJsonChange,
  candidates,
  testIdPrefix,
  rowOrigin,
}: FieldGroupEditorProps) {
  const hasJsonMode = jsonValue !== undefined && onJsonChange !== undefined;
  // Start in Fields mode unless the node carries a raw object but no rows
  // (JSON-edited config stays visible in the shape the author chose).
  const [mode, setMode] = useState<'fields' | 'json'>(() => {
    if (!hasJsonMode || rows.length > 0) return 'fields';
    const raw = jsonValue as Record<string, unknown> | null;
    return raw !== null && typeof raw === 'object' && Object.keys(raw).length > 0 ? 'json' : 'fields';
  });
  const [activeRow, setActiveRow] = useState(-1);
  const [jsonText, setJsonText] = useState(() => JSON.stringify(jsonValue ?? {}, null, 2));
  const [jsonError, setJsonError] = useState('');

  const updateRow = (index: number, patch: Partial<ParamRow>) => {
    const next = rows.map((row, i) => (i === index ? { ...row, ...patch } : row));
    onRowsChange(next);
  };

  const switchMode = (next: 'fields' | 'json') => {
    if (next === mode) return;
    if (next === 'json') {
      // Fields → JSON: compile the rows into the plain object the engine
      // executes and clear the row array (single source of truth).
      onJsonChange?.(compileParamRows(rows));
      onRowsChange([]);
      setJsonText(JSON.stringify(compileParamRows(rows), null, 2));
      setJsonError('');
    } else {
      // JSON → Fields: convert the object into typed rows.
      const converted = objectToParamRows(jsonValue);
      onRowsChange(converted);
      setActiveRow(-1);
    }
    setMode(next);
  };

  const insertExpression = (index: number, expression: string) => {
    const current = rows[index];
    const text = typeof current.value === 'string' && current.value.trim() ? `${current.value} ${expression}` : expression;
    updateRow(index, { value: text });
  };

  return (
    <div className="field-group-editor" data-testid={testIdPrefix}>
      <div className="field-group-head">
        <span className="field-label">{label}</span>
        {hasJsonMode && (
          <div className="mode-toggle" role="group" aria-label={`${label} editor mode`}>
            <button
              type="button"
              className={`mode-toggle-button ${mode === 'fields' ? 'active' : ''}`}
              aria-pressed={mode === 'fields'}
              onClick={() => switchMode('fields')}
            >
              Fields
            </button>
            <button
              type="button"
              className={`mode-toggle-button ${mode === 'json' ? 'active' : ''}`}
              aria-pressed={mode === 'json'}
              onClick={() => switchMode('json')}
            >
              JSON
            </button>
          </div>
        )}
      </div>

      {mode === 'fields' ? (
        <div className="value-rows">
          {rows.map((row, index) => {
            const origin = rowOrigin?.(row.name);
            return (
            <div className="value-row" key={index} data-testid={`${testIdPrefix}-row-${index}`}>
              <input
                className="text-input value-row-name"
                placeholder="name"
                aria-label={`${label} row ${index + 1} name`}
                data-testid={`${testIdPrefix}-row-${index}-name`}
                value={row.name}
                onChange={(event) => updateRow(index, { name: event.target.value })}
                onFocus={() => setActiveRow(-1)}
              />
              {origin && origin !== 'node' && (
                <OriginBadge
                  state={origin === 'bundle' ? 'bundle' : 'inherited'}
                  testId={`${testIdPrefix}-row-${index}-origin`}
                />
              )}
              <div className="mode-chip" role="group" aria-label={`${label} row ${index + 1} value mode`}>
                <button
                  type="button"
                  className={row.mode === 'static' ? 'active' : ''}
                  aria-pressed={row.mode === 'static'}
                  title="A typed literal sent as-is"
                  onClick={() => updateRow(index, {
                    mode: 'static',
                    type: row.type ?? (typeof row.value === 'number' ? 'number' : 'string'),
                    value: typeof row.value === 'string' ? row.value : row.value,
                  })}
                >
                  Static
                </button>
                <button
                  type="button"
                  className={row.mode === 'expression' ? 'active' : ''}
                  aria-pressed={row.mode === 'expression'}
                  title="A {{ }} expression resolved at run time"
                  onClick={() => updateRow(index, {
                    mode: 'expression',
                    value: typeof row.value === 'string' ? row.value : '',
                  })}
                >
                  Expression
                </button>
              </div>
              {row.mode === 'static' ? (
                <>
                  <select
                    className="text-input value-row-type"
                    aria-label={`${label} row ${index + 1} type`}
                    data-testid={`${testIdPrefix}-row-${index}-type`}
                    value={row.type ?? 'string'}
                    onChange={(event) => {
                      const type = event.target.value as ParamValueType;
                      updateRow(index, { type, value: defaultValueForType(type, row.value) });
                    }}
                  >
                    {STATIC_TYPES.map((type) => (
                      <option key={type} value={type}>{type}</option>
                    ))}
                  </select>
                  <StaticValueInput
                    testIdPrefix={testIdPrefix}
                    index={index}
                    label={label}
                    type={row.type ?? 'string'}
                    value={row.value}
                    onChange={(value) => updateRow(index, { value })}
                  />
                </>
              ) : (
                <input
                  className="text-input value-row-expression"
                  placeholder="{{ $json.field }}"
                  aria-label={`${label} row ${index + 1} expression`}
                  data-testid={`${testIdPrefix}-row-${index}-expression`}
                  value={typeof row.value === 'string' ? row.value : ''}
                  onFocus={() => setActiveRow(index)}
                  onChange={(event) => updateRow(index, { value: event.target.value })}
                />
              )}
              <button
                type="button"
                className="text-button value-row-remove"
                aria-label={`Remove ${label} row ${row.name || index + 1}`}
                data-testid={`${testIdPrefix}-row-${index}-remove`}
                onClick={() => onRowsChange(rows.filter((_, i) => i !== index))}
              >
                ×
              </button>
            </div>
            );
          })}
          {rows.length === 0 && (
            <p className="muted small" data-testid={`${testIdPrefix}-empty`}>
              No parameters: the group sends nothing.
            </p>
          )}
          <button
            type="button"
            className="text-button"
            data-testid={`${testIdPrefix}-add`}
            onClick={() => {
              onRowsChange([...rows, { name: '', mode: 'static', type: 'string', value: '' }]);
              setActiveRow(-1);
            }}
          >
            + Add parameter
          </button>
          {activeRow >= 0 && rows[activeRow]?.mode === 'expression' && candidates && candidates.length > 0 && (
            <div className="expression-quick-toolbar" aria-label="Dynamic expression helpers" data-testid={`${testIdPrefix}-pills`}>
              <span className="quick-label">Insert data:</span>
              {candidates.map((candidate) => (
                <button
                  type="button"
                  className="expression-pill"
                  key={candidate.path}
                  data-source={candidate.source}
                  title={`Insert {{ ${candidate.path} }} (${candidate.source})`}
                  onClick={() => insertExpression(activeRow, `{{ ${candidate.path} }}`)}
                >
                  {candidate.path}
                </button>
              ))}
            </div>
          )}
        </div>
      ) : (
        <div className="json-field">
          <textarea
            className={`text-input code ${jsonError ? 'invalid' : ''}`}
            spellCheck={false}
            rows={5}
            aria-label={`${label} as JSON`}
            data-testid={`${testIdPrefix}-json`}
            value={jsonText}
            onChange={(event) => setJsonText(event.target.value)}
            onBlur={() => {
              try {
                const parsed: unknown = JSON.parse(jsonText);
                if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
                  throw new Error('object');
                }
                setJsonError('');
                onJsonChange?.(parsed);
              } catch {
                setJsonError('Expected a valid JSON object.');
              }
            }}
          />
          {jsonError && <p className="field-error">{jsonError}</p>}
          <p className="field-hint">Edited as JSON. Changes apply when the field loses focus.</p>
        </div>
      )}
      {hint && <p className="field-hint">{hint}</p>}
    </div>
  );
}

function defaultValueForType(type: ParamValueType, current: unknown): unknown {
  switch (type) {
    case 'number':
      return typeof current === 'number' ? current : Number(current) || 0;
    case 'boolean':
      return typeof current === 'boolean' ? current : false;
    case 'json':
      if (current !== null && typeof current === 'object') return current;
      if (typeof current === 'string' && current.trim() !== '') {
        try {
          return JSON.parse(current);
        } catch {
          return {};
        }
      }
      return {};
    default:
      return typeof current === 'string' ? current : String(current ?? '');
  }
}

function StaticValueInput({
  testIdPrefix,
  index,
  label,
  type,
  value,
  onChange,
}: {
  testIdPrefix: string;
  index: number;
  label: string;
  type: ParamValueType;
  value: unknown;
  onChange: (value: unknown) => void;
}) {
  const ariaLabel = `${label} row ${index + 1} value`;
  const testId = `${testIdPrefix}-row-${index}-value`;
  if (type === 'boolean') {
    return (
      <label className="value-row-check">
        <input
          type="checkbox"
          aria-label={ariaLabel}
          data-testid={testId}
          checked={Boolean(value)}
          onChange={(event) => onChange(event.target.checked)}
        />
      </label>
    );
  }
  if (type === 'number') {
    return (
      <input
        className="text-input value-row-value"
        type="number"
        aria-label={ariaLabel}
        data-testid={testId}
        value={typeof value === 'number' ? value : ''}
        onChange={(event) => onChange(event.target.value === '' ? 0 : Number(event.target.value))}
      />
    );
  }
  if (type === 'json') {
    return (
      <input
        className="text-input value-row-value code"
        placeholder='{"key": "value"}'
        aria-label={ariaLabel}
        data-testid={testId}
        value={typeof value === 'string' ? value : JSON.stringify(value ?? null)}
        onFocus={(event) => event.target.select()}
        onChange={(event) => onChange(event.target.value)}
        onBlur={(event) => {
          // Free the raw text through a JSON round-trip when it parses;
          // otherwise keep it as text so the user never loses a keystroke.
          const text = event.target.value;
          if (text.trim() === '') return;
          try {
            onChange(JSON.parse(text));
          } catch {
            onChange(text);
          }
        }}
      />
    );
  }
  return (
    <input
      className="text-input value-row-value"
      aria-label={ariaLabel}
      data-testid={testId}
      value={typeof value === 'string' ? value : String(value ?? '')}
      onChange={(event) => onChange(event.target.value)}
    />
  );
}
