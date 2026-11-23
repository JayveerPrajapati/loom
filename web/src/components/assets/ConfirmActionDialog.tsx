import { ActionError } from './ActionError';
import { useId, type ReactNode } from 'react';
import { useDialogFocus } from '../../useDialogFocus';

export function ConfirmActionDialog({
  open,
  title,
  body,
  confirmLabel,
  cancelLabel = 'Cancel',
  tone = 'danger',
  busy = false,
  error,
  onCancel,
  onConfirm,
  children,
}: {
  open: boolean;
  title: string;
  body: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  tone?: 'danger' | 'primary';
  busy?: boolean;
  error?: string | null;
  onCancel: () => void;
  onConfirm: () => void;
  children?: ReactNode;
}) {
  const titleId = useId();
  const ref = useDialogFocus<HTMLDivElement>(open, onCancel, !busy);
  if (!open) return null;
  return (
    <div
      className="dialog-backdrop"
      data-testid="confirm-dialog-backdrop"
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
        data-testid="confirm-dialog"
      >
        <h2 id={titleId}>{title}</h2>
        <div className="dialog-body">{body}</div>
        {error ? (
          <ActionError testId="confirm-dialog-error">{error}</ActionError>
        ) : null}
        <div className="dialog-actions">
          {children}
          <button
            type="button"
            className="button subtle"
            data-testid="confirm-dialog-cancel"
            data-autofocus
            onClick={onCancel}
            disabled={busy}
          >
            {cancelLabel}
          </button>
          <button
            type="button"
            className={`button primary${tone === 'danger' ? ' danger' : ''}`}
            data-testid="confirm-dialog-confirm"
            onClick={onConfirm}
            disabled={busy}
          >
            {busy ? `${confirmLabel}…` : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  );
}
