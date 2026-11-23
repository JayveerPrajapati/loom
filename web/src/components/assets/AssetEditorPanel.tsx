import { ActionError } from './ActionError';
import type { ReactNode } from 'react';

/**
 * One editor shell for every asset: a stable header, a scrollable body, and a
 * footer that stays visible so Save never disappears on a long form.
 *
 * The footer's Save is a submit button associated with the form through its
 * `formId`, so constraint validation (`required`, patterns) runs before
 * anything is sent and Enter in any field submits the form.
 */
export function AssetEditorPanel({
  title,
  subtitle,
  meta,
  children,
  busy = false,
  dirty = false,
  saveLabel = 'Save',
  savingLabel = 'Saving…',
  cancelLabel = 'Cancel',
  error,
  formId,
  onCancel,
  testId = 'asset-editor',
  ariaLabel,
  saveDisabled = false,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
  busy?: boolean;
  dirty?: boolean;
  saveLabel?: string;
  savingLabel?: string;
  cancelLabel?: string;
  error?: string | null;
  formId: string;
  onCancel: () => void;
  testId?: string;
  ariaLabel?: string;
  saveDisabled?: boolean;
}) {
  return (
    <section className="asset-editor" aria-label={ariaLabel} data-testid={testId}>
      <header className="asset-editor-head">
        <div>
          <h2>{title}</h2>
          {subtitle ? <p className="muted small">{subtitle}</p> : null}
        </div>
        {meta}
      </header>
      <div className="asset-editor-body">{children}</div>
      {error ? (
        <ActionError testId="asset-editor-error">{error}</ActionError>
      ) : null}
      <footer className="asset-editor-foot">
        <span className="asset-editor-status muted small" aria-live="polite" data-testid="asset-editor-status">
          {busy ? savingLabel : dirty ? 'Unsaved changes' : 'All changes saved'}
        </span>
        <div className="asset-editor-actions">
          <button
            type="button"
            className="button subtle"
            data-testid="asset-editor-cancel"
            onClick={onCancel}
            disabled={busy}
          >
            {cancelLabel}
          </button>
          <button
            type="submit"
            form={formId}
            className="button primary"
            data-testid="asset-editor-save"
            disabled={busy || saveDisabled}
          >
            {busy ? savingLabel : saveLabel}
          </button>
        </div>
      </footer>
    </section>
  );
}
