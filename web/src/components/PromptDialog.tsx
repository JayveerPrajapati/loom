import { ActionError } from './assets/ActionError';
import { useEffect, useId, useState } from 'react';
import { useDialogFocus } from '../useDialogFocus';

/**
 * PromptDialog: the shared single-line text prompt (U3). Replaces
 * `window.prompt` wherever the app needs a name or short value from the
 * user. Mounts on the same `.dialog-backdrop` / `.dialog` shell as the
 * other shared dialogs and follows the same rules as ConfirmActionDialog:
 * focus is trapped, Escape and the backdrop close only when no request
 * is in flight, and the server error stays inside the dialog.
 *
 * The caller owns the request: it passes `busy` and `error` back in and
 * closes the dialog itself on success. The dialog owns only the input
 * state and the empty-value validation.
 */
export function PromptDialog({
  open,
  title,
  label,
  initialValue,
  confirmLabel,
  cancelLabel = 'Cancel',
  busy = false,
  error,
  onCancel,
  onConfirm,
}: {
  open: boolean;
  title: string;
  label: string;
  initialValue: string;
  confirmLabel: string;
  cancelLabel?: string;
  busy?: boolean;
  error?: string | null;
  onCancel: () => void;
  onConfirm: (value: string) => void;
}) {
  const titleId = useId();
  const inputId = useId();
  const errorId = useId();
  const [value, setValue] = useState(initialValue);
  const [emptyError, setEmptyError] = useState('');
  const ref = useDialogFocus<HTMLDivElement>(open, onCancel, !busy);

  // A reopen starts from the caller's value again; the previous session's
  // input and validation error never leak into the next one.
  useEffect(() => {
    if (open) {
      setValue(initialValue);
      setEmptyError('');
    }
  }, [open, initialValue]);

  if (!open) return null;

  const trimmed = value.trim();
  const unchanged = trimmed === initialValue.trim();
  const message = emptyError || error || '';
  const canConfirm = !busy && trimmed !== '' && !unchanged;

  // The confirm button is disabled for an empty or unchanged value, so the
  // mouse path cannot submit those. Enter still routes through here, which
  // is what makes the empty-name error reachable from the keyboard.
  const submit = () => {
    if (busy) return;
    if (trimmed === '') {
      setEmptyError(`${label} is required.`);
      return;
    }
    if (unchanged) return;
    onConfirm(trimmed);
  };

  return (
    <div
      className="dialog-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) onCancel();
      }}
    >
      <div
        className="dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        tabIndex={-1}
        ref={ref}
        data-testid="prompt-dialog"
      >
        <h2 id={titleId}>{title}</h2>
        <div className="dialog-body">
          <label className="field" htmlFor={inputId}>
            <span className="field-label">{label}</span>
            <input
              id={inputId}
              className="text-input"
              type="text"
              value={value}
              data-autofocus
              data-testid="prompt-dialog-input"
              aria-invalid={emptyError ? 'true' : undefined}
              aria-describedby={message ? errorId : undefined}
              disabled={busy}
              onChange={(event) => {
                setValue(event.target.value);
                if (emptyError) setEmptyError('');
              }}
              onKeyDown={(event) => {
                if (event.key === 'Enter') {
                  event.preventDefault();
                  submit();
                }
              }}
            />
          </label>
        </div>
        {message ? (
          <ActionError id={errorId} testId="prompt-dialog-error">
            {message}
          </ActionError>
        ) : null}
        <div className="dialog-actions">
          <button
            type="button"
            className="button subtle"
            data-testid="prompt-dialog-cancel"
            onClick={onCancel}
            disabled={busy}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className="button primary"
            data-testid="prompt-dialog-confirm"
            onClick={submit}
            disabled={!canConfirm}
          >
            {busy ? `${confirmLabel}…` : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
