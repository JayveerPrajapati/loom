import { draft07ToLoom } from '../../jsonSchema';
import { countFields } from '../../schemaFields';

/**
 * SchemaImportDialog (6.6): paste a draft-07 document, see the named
 * subset conversion — including every unsupported keyword it drops —
 * before it fills the create form.
 */
export function SchemaImportDialog({
  open,
  text,
  preview,
  focusRef,
  onTextChange,
  onClose,
  onConfirm,
}: {
  open: boolean;
  text: string;
  preview: { error: string } | { schema: ReturnType<typeof draft07ToLoom>['schema']; error?: string } | null;
  focusRef: React.Ref<HTMLDivElement>;
  onTextChange: (next: string) => void;
  onClose: () => void;
  onConfirm: () => void;
}) {
  if (!open) return null;
  const parsed = preview && !('error' in preview) ? preview : null;
  const canConfirm =
    !!parsed && !parsed.error && parsed.schema.name.trim().length > 0 && parsed.schema.fields.length > 0;
  return (
    <div className="dialog-backdrop" data-testid="schema-import-backdrop">
      <div
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Import JSON Schema"
        ref={focusRef}
        tabIndex={-1}
      >
        <header className="dialog-head">
          <h2>Import JSON Schema (draft-07 subset)</h2>
          <button className="button subtle" onClick={onClose} aria-label="Close import">
            Close
          </button>
        </header>
        <div className="dialog-body">
          <p className="field-hint">
            Loom imports a named subset: field name, type (string/number/boolean/object/array),
            required, description, nesting and array items. Everything else the document carries is
            listed below — dropped, never silently.
          </p>
          <label className="field" htmlFor="schema-import-text">
            <span className="field-label">JSON Schema document</span>
            <textarea
              id="schema-import-text"
              className="text-input code"
              rows={10}
              spellCheck={false}
              data-testid="schema-import-text"
              value={text}
              onChange={(event) => onTextChange(event.target.value)}
              placeholder={'{\n  "$schema": "http://json-schema.org/draft-07/schema#",\n  "title": …\n}'}
            />
          </label>
          {preview && 'error' in preview && <p className="field-hint" role="alert">{preview.error}</p>}
          {parsed && parsed.error && <p className="field-hint" role="alert">{parsed.error}</p>}
          {parsed && !parsed.error && (
            <div className="banner notice" role="status" data-testid="schema-import-review">
              <p>
                <strong>{parsed.schema.name || '(untitled)'}</strong> ·{' '}
                {countFields(parsed.schema.fields)} fields
                {parsed.schema.unsupported.length > 0
                  ? ` · ${parsed.schema.unsupported.length} subset-foreign item(s) dropped`
                  : ' · clean subset'}
              </p>
              {parsed.schema.unsupported.length > 0 && (
                <ul>
                  {parsed.schema.unsupported.map((item, index) => (
                    <li key={index}>{item}</li>
                  ))}
                </ul>
              )}
            </div>
          )}
          <button
            className="button primary"
            type="button"
            onClick={onConfirm}
            disabled={!canConfirm}
            data-testid="schema-import-confirm"
          >
            Fill the create form
          </button>
        </div>
      </div>
    </div>
  );
}
