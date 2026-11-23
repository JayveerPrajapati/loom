import { useMemo, useState } from 'react';
import { api, ApiError } from '../../api';
import { AssetEditorPanel } from '../assets/AssetEditorPanel';
import type { Connector, ConnectorOperation } from '../../types';
import { describeJsonError, newOperation, validateOperations } from './operations';
import { OperationCard } from './OperationCard';

/** The connector editor rail: Basics, Operations and Metadata
 *  sections. Operations are authored through structured cards with a
 *  raw-JSON escape hatch that validates live — the textarea content
 *  survives an error, and an invalid document blocks the save with the
 *  message in the panel's error slot. */
export function ConnectorForm({
  initial,
  onCancel,
  onSaved,
  onNotify,
}: {
  initial: Connector | null;
  onCancel: () => void;
  onSaved: (saved: { name: string; description: string; operations: ConnectorOperation[] }) => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}) {
  const [name, setName] = useState(initial?.name ?? '');
  const [description, setDescription] = useState(initial?.description ?? '');
  const [owner, setOwner] = useState(initial?.owner ?? '');
  const [tags, setTags] = useState((initial?.tags ?? []).join(', '));
  const [operations, setOperations] = useState<ConnectorOperation[]>(
    initial?.operations?.length ? initial.operations : [newOperation()],
  );
  const [jsonMode, setJsonMode] = useState(false);
  const [jsonText, setJsonText] = useState(JSON.stringify(initial?.operations ?? [], null, 2));
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);

  // Snapshot of the draft as the editor opened (the default operation
  // card included), so an untouched edit reads clean instead of always
  // "Unsaved changes" — same grammar as the scripts and contracts editors.
  const [baseline] = useState(() =>
    JSON.stringify({ name, description, owner, tags, operations }),
  );
  const dirty =
    JSON.stringify({ name, description, owner, tags, operations }) !== baseline ||
    (jsonMode && jsonText !== JSON.stringify(operations, null, 2));

  // Live JSON validation: parse plus the same rules the cards enforce,
  // with the parse position translated into a line and column.
  const jsonProblem = useMemo(() => {
    if (!jsonMode) return '';
    try {
      const parsed = JSON.parse(jsonText) as ConnectorOperation[];
      if (!Array.isArray(parsed)) return 'Operations JSON: operations must be a JSON array';
      const problem = validateOperations(parsed);
      return problem ? `Operations JSON: ${problem}` : '';
    } catch (parseError) {
      return `Operations JSON: ${describeJsonError(jsonText, (parseError as Error).message)}`;
    }
  }, [jsonMode, jsonText]);

  const parseJsonText = (): ConnectorOperation[] | null => {
    if (!jsonProblem) {
      try {
        return JSON.parse(jsonText) as ConnectorOperation[];
      } catch {
        return null;
      }
    }
    return null;
  };

  const leaveJsonMode = () => {
    const parsed = parseJsonText();
    if (!parsed) return;
    setOperations(parsed);
    setJsonMode(false);
  };

  const submit = () => {
    let ops = operations;
    if (jsonMode) {
      // An invalid document never reaches the server; the live message
      // is already in the error slot.
      if (jsonProblem) return;
      const parsed = parseJsonText();
      if (!parsed) return;
      ops = parsed;
      setOperations(parsed);
    }
    if (!name.trim()) {
      setError('Name is required.');
      return;
    }
    const problem = validateOperations(ops);
    if (problem) {
      setError(problem);
      return;
    }
    setBusy(true);
    const tagList = tags
      .split(',')
      .map((tag) => tag.trim())
      .filter(Boolean);
    const body = {
      name: name.trim(),
      description,
      operations: ops,
      tags: tagList,
      owner,
    };
    const fail = (reason: unknown) => {
      setBusy(false);
      setError(reason instanceof ApiError ? reason.message : String(reason));
    };
    if (initial) {
      api
        .updateConnector(initial.id, { ...body, version: initial.version })
        .then(() => {
          onNotify?.(`${body.name} saved`);
          onSaved(body);
        })
        .catch(fail);
    } else {
      api
        .createConnector(body)
        .then(() => {
          onNotify?.(`${body.name} saved`);
          onSaved(body);
        })
        .catch(fail);
    }
  };

  return (
    <AssetEditorPanel
      testId="connector-editor"
      ariaLabel="Connector editor"
      title={initial ? `Edit ${initial.name}` : 'New connector'}
      subtitle="Operations are request templates; placeholders resolve from config fields, credential fields, then the item scope."
      busy={busy}
      dirty={dirty}
      saveLabel="Save connector"
      error={error || jsonProblem || null}
      formId="connector-form"
      onCancel={onCancel}
    >
      <form
        id="connector-form"
        className="hub-form"
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          submit();
        }}
      >
        <fieldset className="hub-form-section">
          <legend className="field-label">Basics</legend>
          <label className="field" htmlFor="connector-name">
            <span className="field-label">Name</span>
            <input
              id="connector-name"
              className="text-input"
              value={name}
              onChange={(event) => setName(event.target.value)}
              required
            />
          </label>
          <label className="field" htmlFor="connector-description">
            <span className="field-label">Description</span>
            <input
              id="connector-description"
              className="text-input"
              value={description}
              onChange={(event) => setDescription(event.target.value)}
            />
          </label>
        </fieldset>

        <fieldset className="hub-form-section">
          <legend className="field-label">Operations</legend>
          <div className="connector-ops-toolbar">
            <span className="muted small">
              {operations.length} operation{operations.length === 1 ? '' : 's'}
            </span>
            <div className="mode-chip" role="group" aria-label="Operations editing mode">
              <button
                type="button"
                className={jsonMode ? '' : 'active'}
                aria-pressed={!jsonMode}
                onClick={() => {
                  if (jsonMode) leaveJsonMode();
                }}
              >
                Cards
              </button>
              <button
                type="button"
                className={jsonMode ? 'active' : ''}
                aria-pressed={jsonMode}
                onClick={() => {
                  if (!jsonMode) {
                    setJsonText(JSON.stringify(operations, null, 2));
                    setJsonMode(true);
                  }
                }}
              >
                JSON
              </button>
            </div>
          </div>

          {jsonMode ? (
            <label className="field" htmlFor="connector-operations-json">
              <span className="field-label">Operations (JSON array)</span>
              <textarea
                id="connector-operations-json"
                className="code-input"
                rows={14}
                value={jsonText}
                onChange={(event) => setJsonText(event.target.value)}
                spellCheck={false}
                data-testid="connector-operations-json"
              />
              <span className="field-hint">
                JSON mode also authors the advanced templates: query, headers, body.
              </span>
            </label>
          ) : (
            <>
              {operations.map((op, index) => (
                <OperationCard
                  key={index}
                  op={op}
                  index={index}
                  onChange={(next) =>
                    setOperations(operations.map((candidate, i) => (i === index ? next : candidate)))
                  }
                  onRemove={() => setOperations(operations.filter((_, i) => i !== index))}
                />
              ))}
              <button
                type="button"
                className="text-button"
                onClick={() => setOperations([...operations, newOperation()])}
              >
                + Add operation
              </button>
            </>
          )}
        </fieldset>

        <fieldset className="hub-form-section">
          <legend className="field-label">Metadata</legend>
          <div className="grid-2">
            <label className="field" htmlFor="connector-owner">
              <span className="field-label">Owner</span>
              <input
                id="connector-owner"
                className="text-input"
                value={owner}
                onChange={(event) => setOwner(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="connector-tags">
              <span className="field-label">Tags (comma-separated)</span>
              <input
                id="connector-tags"
                className="text-input"
                value={tags}
                onChange={(event) => setTags(event.target.value)}
              />
            </label>
          </div>
        </fieldset>
      </form>
    </AssetEditorPanel>
  );
}
