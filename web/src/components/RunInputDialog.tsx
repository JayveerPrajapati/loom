import type { RefObject } from 'react';

interface Props {
  draft: string;
  error: string;
  focusRef: RefObject<HTMLDivElement>;
  onDraftChange: (value: string) => void;
  onClose: () => void;
  onSubmit: () => void;
}

// Leaf dialog: all state lives in App; this component only renders the
// run-input overlay and reports interactions back.
export function RunInputDialog({ draft, error, focusRef, onDraftChange, onClose, onSubmit }: Props) {
  return (
    <div className="overlay" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div ref={focusRef} className="dialog" role="dialog" aria-modal="true" aria-label="Run input" tabIndex={-1}>
        <h2>Run input</h2>
        <p className="muted small">
          This object becomes the trigger item. Use fields here from <code>{'{{ $json.field }}'}</code> in node configs.
        </p>
        <form
          onSubmit={(event) => {
            event.preventDefault();
            onSubmit();
          }}
        >
          <label className="field" htmlFor="run-input">
            <span className="field-label">JSON object</span>
            <textarea
              id="run-input"
              className={`text-input code ${error ? 'invalid' : ''}`}
              rows={10}
              autoFocus
              data-autofocus
              spellCheck={false}
              value={draft}
              onChange={(event) => onDraftChange(event.target.value)}
            />
          </label>
          {error && <p className="field-error">{error}</p>}
          <div className="dialog-actions">
            <button className="button subtle" type="button" onClick={onClose}>
              Cancel
            </button>
            <button className="button primary" type="submit">
              Run with input
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
