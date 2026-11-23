import { useEffect, useState } from 'react';
import { formatConfigValue, labelFor } from './inspectorHelpers';
import { OriginBadge, originState } from './assets/OriginBadge';
import type { FieldDef, ValidationIssue } from '../types';

export function IssueList({
  issues,
  onSelectNode,
}: {
  issues: ValidationIssue[];
  onSelectNode: (id: string | null) => void;
}) {
  if (issues.length === 0) {
    return <p className="inspector-ok">No issues here.</p>;
  }
  return (
    <div className="issue-list">
      {issues.map((issue, index) => (
        <div className={`issue issue-${issue.severity}`} key={`${issue.code}-${issue.node ?? ''}-${index}`}>
          <span className="issue-badge">{issue.severity === 'error' ? 'Error' : 'Warning'}</span>
          <p>{issue.message}</p>
          {issue.node && (
            <button className="issue-link" onClick={() => onSelectNode(issue.node ?? null)}>
              Go to node
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

function JsonField({
  fieldKey,
  value,
  type,
  onChange,
  onFocus,
}: {
  fieldKey: string;
  value: unknown;
  type: 'object' | 'array' | 'any';
  onChange: (value: unknown) => void;
  onFocus?: () => void;
}) {
  const [text, setText] = useState(() => JSON.stringify(value ?? (type === 'array' ? [] : {}), null, 2));
  const [error, setError] = useState('');

  useEffect(() => {
    setText(JSON.stringify(value ?? (type === 'array' ? [] : {}), null, 2));
    setError('');
  }, [value, type]);

  const commit = () => {
    try {
      const parsed: unknown = JSON.parse(text);
      if (type === 'array' && !Array.isArray(parsed)) throw new Error('array');
      if (type === 'object' && (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object')) {
        throw new Error('object');
      }
      setError('');
      onChange(parsed);
    } catch {
      setError(`Expected a valid JSON ${type}.`);
    }
  };

  return (
    <div className="json-field">
      <textarea
        id={`field-${fieldKey}`}
        className={`text-input code ${error ? 'invalid' : ''}`}
        value={text}
        spellCheck={false}
        rows={type === 'object' ? 5 : 4}
        onFocus={onFocus}
        onChange={(event) => setText(event.target.value)}
        onBlur={commit}
      />
      {error && <p className="field-error">{error}</p>}
      <p className="field-hint">Edited as JSON. Changes apply when the field loses focus.</p>
    </div>
  );
}

export function ConfigField({
  fieldKey,
  spec,
  value,
  onChange,
  onFocus,
  origin,
  onReset,
  overridden,
  profileValue,
  issue,
}: {
  fieldKey: string;
  spec: FieldDef;
  value: unknown;
  onChange: (value: unknown) => void;
  onFocus?: () => void;
  /** Effective-value origin (UI-audit B1): badges inherited fields. */
  origin?: 'node' | 'profile' | 'bundle';
  /** True when this node's own value overrides the profile's. */
  overridden?: boolean;
  /** Present when a node-level override hides an inherited value. */
  onReset?: () => void;
  /** The pinned profile's value, shown as a hint on overrides. */
  profileValue?: unknown;
  /** Inline validation message for this field. */
  issue?: string;
}) {
  const label = spec.label || labelFor(fieldKey);
  const id = `field-${fieldKey}`;

  const renderLabel = (text: string) => (
    <span className="field-label">
      {text}
      {spec.required && <span className="required">*</span>}
      <OriginBadge state={originState(origin, overridden === true)} fieldKey={fieldKey} />
      {onReset && (
        <button
          type="button"
          className="field-reset"
          data-testid={`reset-override-${fieldKey}`}
          onClick={onReset}
          title="Reset to the profile value"
          aria-label={`Reset ${label} to the profile value`}
        >
          Reset to profile
        </button>
      )}
    </span>
  );

  const renderFeedback = () => (
    <>
      {issue && (
        <p className="field-error" role="alert" data-testid={`field-error-${fieldKey}`}>
          {issue}
        </p>
      )}
      {overridden === true && profileValue !== undefined && (
        <p className="field-hint field-inherited-hint" data-testid={`profile-value-${fieldKey}`}>
          Profile: <code>{formatConfigValue(profileValue)}</code>
        </p>
      )}
    </>
  );

  const renderDescription = spec.description ? <p className="field-hint">{spec.description}</p> : null;

  if (spec.enum) {
    return (
      <label className="field" htmlFor={id}>
        {renderLabel(label)}
        <select
          id={id}
          className="text-input"
          value={typeof value === 'string' ? value : ''}
          aria-invalid={issue ? true : undefined}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value)}
        >
          {/* A required enum commits to a real choice instead of a
              placeholder option; optional ones keep "Choose…". */}
          {!value && !spec.required && <option value="">Choose…</option>}
          {spec.enum.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
        {renderFeedback()}
        {renderDescription}
      </label>
    );
  }

  if (spec.type === 'boolean') {
    return (
      <label className="field field-check" htmlFor={id}>
        <input
          id={id}
          type="checkbox"
          checked={Boolean(value)}
          aria-invalid={issue ? true : undefined}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.checked)}
        />
        <span>{label}</span>
        <OriginBadge state={originState(origin, overridden === true)} fieldKey={fieldKey} />
        {renderFeedback()}
        {renderDescription}
      </label>
    );
  }

  if (spec.type === 'number') {
    return (
      <label className="field" htmlFor={id}>
        {renderLabel(label)}
        <input
          id={id}
          className="text-input"
          type="number"
          min={spec.min}
          max={spec.max}
          placeholder={spec.placeholder}
          value={typeof value === 'number' ? value : ''}
          aria-invalid={issue ? true : undefined}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value === '' ? 0 : Number(event.target.value))}
        />
        {renderFeedback()}
        {renderDescription}
      </label>
    );
  }

  if (spec.type === 'object' || spec.type === 'array' || spec.type === 'any') {
    return (
      <label className="field" htmlFor={id}>
        {renderLabel(label)}
        <JsonField fieldKey={fieldKey} value={value} type={spec.type} onChange={onChange} onFocus={onFocus} />
        {renderFeedback()}
        {renderDescription}
      </label>
    );
  }

  const isCode = fieldKey === 'source';
  return (
    <label className="field" htmlFor={id}>
      {renderLabel(label)}
      {isCode ? (
        <textarea
          id={id}
          className="text-input code"
          value={typeof value === 'string' ? value : ''}
          spellCheck={false}
          rows={7}
          aria-invalid={issue ? true : undefined}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : (
        <input
          id={id}
          className="text-input"
          type={spec.secret ? 'password' : 'text'}
          pattern={spec.pattern}
          title={spec.pattern ? `Must match: ${spec.pattern}` : undefined}
          placeholder={spec.placeholder}
          value={typeof value === 'string' ? value : ''}
          aria-invalid={issue ? true : undefined}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {renderFeedback()}
      {renderDescription}
    </label>
  );
}
