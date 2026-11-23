import { FieldGroupEditor } from '../FieldGroupEditor';
import {
  PAGINATION_LIMITS,
  PAGINATION_MODES,
} from '../../profileForm';
import { toParamRows, type ParamRow } from '../../paramRows';
import type { FieldDef } from '../../types';

export interface PaginationState {
  mode: string;
  strings: Record<string, string>;
  numbers: Record<string, string>;
}

// One catalog field rendered typed on the profile form (Phase 6 D6-3,
// extracted verbatim from ProfileFormPage). v4.1 E4: http.request
// parameter groups are typed row editors whose rows merge per name at
// publish — the plain target keys (query, headers, pathParams) are
// owned by their group editors' JSON mode and never render standalone.
// The body group only exists for JSON bodies; other body types edit
// the raw body below. Pagination gets its structured editor.
export function ProfileField(props: {
  fieldKey: string;
  spec: FieldDef;
  nodeType: string;
  values: Record<string, unknown>;
  pagination: PaginationState;
  setPagination: React.Dispatch<React.SetStateAction<PaginationState>>;
  setValue: (key: string, value: unknown) => void;
}) {
  const { fieldKey: key, spec, nodeType, values, pagination, setPagination, setValue } = props;
  const label = spec.label || key;
  const value = values[key];
  const id = `profile-field-${key}`;

  if (nodeType === 'http.request') {
    const groupTarget: Record<string, string> = {
      bodyFields: 'body',
      queryFields: 'query',
      headerFields: 'headers',
      pathFields: 'pathParams',
    };
    if (key in groupTarget) {
      if (key === 'bodyFields' && values.bodyType !== 'json') {
        return (
          <p className="field-hint">
            JSON bodies only: switch body type to json for typed body rows. Other body types set
            the raw body directly.
          </p>
        );
      }
      const target = groupTarget[key];
      const groupLabel: Record<string, string> = {
        bodyFields: 'JSON body fields',
        queryFields: 'Query parameters',
        headerFields: 'Headers',
        pathFields: 'Path parameters',
      };
      return (
        <FieldGroupEditor
          label={groupLabel[key]}
          hint={spec.description}
          rows={toParamRows(value)}
          onRowsChange={(rows: ParamRow[]) => setValue(key, rows.length > 0 ? rows : undefined)}
          jsonValue={
            values[target] !== null && typeof values[target] === 'object' && !Array.isArray(values[target])
              ? values[target]
              : {}
          }
          onJsonChange={(raw: unknown) => setValue(target, raw)}
          testIdPrefix={`profile-${key}`}
        />
      );
    }
    if (key === 'query' || key === 'headers' || key === 'pathParams') return null;
    if (key === 'body' && values.bodyType === 'json') {
      return (
        <p className="field-hint">
          For JSON bodies the typed rows under Body fields compile into the body at publish.
        </p>
      );
    }
  }

  if (nodeType === 'http.request' && key === 'pagination') {
    return (
      <div className="field">
        <span className="field-label">{label}</span>
        <label className="field" htmlFor="profile-pagination-mode">
          <span className="field-label">Mode</span>
          <select
            id="profile-pagination-mode"
            className="text-input"
            data-testid="profile-pagination-mode"
            value={pagination.mode}
            onChange={(event) =>
              setPagination((current) => ({ ...current, mode: event.target.value }))
            }
          >
            <option value="">Not set (single request)</option>
            {PAGINATION_MODES.map((mode) => (
              <option key={mode} value={mode}>
                {mode}
              </option>
            ))}
          </select>
        </label>
        {pagination.mode !== '' && (
          <div className="pagination-editor" data-testid="profile-pagination-fields">
            {(
              [
                ['itemsPath', 'Items path', 'text'],
                ['until', 'Until (expression)', 'text'],
                ['pageParam', 'Page param', 'text'],
                ['offsetParam', 'Offset param', 'text'],
                ['limitParam', 'Limit param', 'text'],
                ['cursorParam', 'Cursor param', 'text'],
                ['cursorPath', 'Cursor path', 'text'],
              ] as const
            )
              .filter(([key]) =>
                pagination.mode === 'page'
                  ? key !== 'offsetParam' && key !== 'cursorParam' && key !== 'cursorPath'
                  : pagination.mode === 'offset'
                    ? key !== 'pageParam' && key !== 'cursorParam' && key !== 'cursorPath' && key !== 'until'
                    : key !== 'pageParam' && key !== 'offsetParam',
              )
              .map(([key, label, kind]) => (
                <label className="field" key={key} htmlFor={`profile-pagination-${key}`}>
                  <span className="field-label">{label}</span>
                  <input
                    id={`profile-pagination-${key}`}
                    className="text-input"
                    type={kind}
                    data-testid={`profile-pagination-${key}`}
                    value={pagination.strings[key] ?? ''}
                    onChange={(event) =>
                      setPagination((current) => ({
                        ...current,
                        strings: { ...current.strings, [key]: event.target.value },
                      }))
                    }
                  />
                </label>
              ))}
            {(
              [
                ['startPage', 'Start page', 1],
                ['limit', 'Limit', 1],
                ['maxPages', 'Max pages', PAGINATION_LIMITS.maxPages],
                ['maxItems', 'Max items', PAGINATION_LIMITS.maxItems],
              ] as const
            )
              .filter(([key]) => (pagination.mode === 'cursor' ? key !== 'startPage' : true))
              .map(([key, label, max]) => (
                <label className="field" key={key} htmlFor={`profile-pagination-${key}`}>
                  <span className="field-label">{label}</span>
                  <input
                    id={`profile-pagination-${key}`}
                    className="text-input"
                    type="number"
                    min={1}
                    max={max}
                    data-testid={`profile-pagination-${key}`}
                    value={pagination.numbers[key] ?? ''}
                    onChange={(event) =>
                      setPagination((current) => ({
                        ...current,
                        numbers: { ...current.numbers, [key]: event.target.value },
                      }))
                    }
                  />
                </label>
              ))}
            <p className="field-hint">
              {pagination.mode === 'offset'
                ? 'Offset mode requires a positive limit (the offset step).'
                : 'Loop over the response; every page is a recorded effect.'}
            </p>
          </div>
        )}
        {spec.description && <p className="field-hint">{spec.description}</p>}
      </div>
    );
  }

  if (spec.enum) {
    return (
      <label className="field" htmlFor={id}>
        <span className="field-label">{label}</span>
        <select
          id={id}
          className="text-input"
          data-testid={id}
          value={typeof value === 'string' ? value : ''}
          onChange={(event) => setValue(key, event.target.value)}
        >
          <option value="">Not set</option>
          {spec.enum.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
        {spec.description && <p className="field-hint">{spec.description}</p>}
      </label>
    );
  }

  if (spec.type === 'boolean') {
    // Profile booleans need a tri-state: unset (profile stays silent),
    // true, false — a checkbox cannot express that.
    return (
      <label className="field" htmlFor={id}>
        <span className="field-label">{label}</span>
        <select
          id={id}
          className="text-input"
          data-testid={id}
          value={value === undefined ? '' : String(value)}
          onChange={(event) =>
            setValue(key, event.target.value === '' ? undefined : event.target.value === 'true')
          }
        >
          <option value="">Not set</option>
          <option value="true">Yes</option>
          <option value="false">No</option>
        </select>
        {spec.description && <p className="field-hint">{spec.description}</p>}
      </label>
    );
  }

  if (spec.type === 'number') {
    return (
      <label className="field" htmlFor={id}>
        <span className="field-label">{label}</span>
        <input
          id={id}
          className="text-input"
          type="number"
          min={spec.min}
          max={spec.max}
          placeholder={spec.placeholder}
          data-testid={id}
          value={typeof value === 'number' ? value : ''}
          onChange={(event) =>
            setValue(key, event.target.value === '' ? undefined : Number(event.target.value))
          }
        />
        {spec.description && <p className="field-hint">{spec.description}</p>}
      </label>
    );
  }

  if (spec.type === 'object' || spec.type === 'array' || spec.type === 'any') {
    return (
      <div className="field">
        <span className="field-label">{label}</span>
        <textarea
          className="text-input code"
          rows={4}
          spellCheck={false}
          aria-label={`${label} as JSON`}
          data-testid={id}
          placeholder="{}"
          value={typeof value === 'string' ? value : JSON.stringify(value ?? (spec.type === 'array' ? [] : {}), null, 2)}
          onChange={(event) => setValue(key, event.target.value)}
          onBlur={(event) => {
            const text = event.target.value.trim();
            if (text === '') {
              setValue(key, undefined);
              return;
            }
            try {
              setValue(key, JSON.parse(text));
            } catch {
              // Leave the text; validation on save reports it.
            }
          }}
        />
        {spec.description && <p className="field-hint">{spec.description}</p>}
      </div>
    );
  }

  return (
    <label className="field" htmlFor={id}>
      <span className="field-label">{label}</span>
      <input
        id={id}
        className="text-input"
        type="text"
        placeholder={spec.placeholder}
        pattern={spec.pattern}
        title={spec.pattern ? `Must match: ${spec.pattern}` : undefined}
        data-testid={id}
        value={typeof value === 'string' ? value : ''}
        onChange={(event) => setValue(key, event.target.value)}
      />
      {spec.description && <p className="field-hint">{spec.description}</p>}
    </label>
  );
}
