import { useEffect, useReducer, useState, type ReactNode } from 'react';
import { ConfirmActionDialog } from './components/assets/ConfirmActionDialog';

export interface ConfirmOptions {
  title: string;
  body: ReactNode;
  confirmLabel: string;
  cancelLabel?: string;
  tone?: 'danger' | 'primary';
  /** Extra escape hatch rendered before Cancel, e.g. "View usage". */
  secondaryAction?: { label: string; run: () => void };
  /**
   * Runs after the user confirms. Return a message to keep the dialog open
   * with that error (for example a server usage guard); return nothing to
   * close and resolve `true`.
   */
  onConfirm?: () => Promise<string | void> | string | void;
}

interface Pending {
  options: ConfirmOptions;
  resolve: (confirmed: boolean) => void;
}

let queue: Pending[] = [];
let wake: (() => void) | null = null;

/**
 * Imperative confirmation shared by components and hooks. The dialog is
 * mounted once by ConfirmHost, so route guards and run actions can await a
 * real accessible dialog without prop drilling.
 */
export function confirmAction(options: ConfirmOptions): Promise<boolean> {
  return new Promise((resolve) => {
    queue = [...queue, { options, resolve }];
    wake?.();
  });
}

function settle(confirmed: boolean) {
  const [current, ...rest] = queue;
  queue = rest;
  current?.resolve(confirmed);
  wake?.();
}

export function ConfirmHost() {
  const [, rerender] = useReducer((value: number) => value + 1, 0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    wake = () => rerender();
    return () => {
      wake = null;
    };
  }, [rerender]);

  const current = queue[0] ?? null;
  useEffect(() => {
    setBusy(false);
    setError(null);
  }, [current?.options]);

  if (!current) return null;

  const confirm = async () => {
    const handler = current.options.onConfirm;
    if (!handler) {
      settle(true);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const outcome = await handler();
      if (typeof outcome === 'string' && outcome) {
        setError(outcome);
        setBusy(false);
        return;
      }
      settle(true);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
      setBusy(false);
    }
  };

  return (
    <ConfirmActionDialog
      open
      title={current.options.title}
      body={current.options.body}
      confirmLabel={current.options.confirmLabel}
      cancelLabel={current.options.cancelLabel}
      tone={current.options.tone}
      busy={busy}
      error={error}
      onCancel={() => settle(false)}
      onConfirm={() => void confirm()}
    >
      {current.options.secondaryAction ? (
        <button
          type="button"
          className="text-button"
          data-testid="confirm-dialog-secondary"
          onClick={() => {
            const run = current.options.secondaryAction?.run;
            settle(false);
            run?.();
          }}
        >
          {current.options.secondaryAction.label}
        </button>
      ) : null}
    </ConfirmActionDialog>
  );
}
