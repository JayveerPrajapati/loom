import type { ReactNode } from 'react';

/**
 * One usage section for every asset: a heading, an optional history entry
 * point, the rows supplied by the page, and honest empty copy.
 */
export function UsagePanel({
  rows,
  empty = false,
  emptyLabel = 'No published workflow uses this yet.',
  title = 'Used by',
  onHistory,
  onClose,
}: {
  rows: ReactNode;
  empty?: boolean;
  emptyLabel?: string;
  title?: string;
  onHistory?: () => void;
  onClose?: () => void;
}) {
  return (
    <section className="asset-usage" data-testid="asset-usage">
      <div className="asset-usage-head">
        <h3>{title}</h3>
        {onHistory ? (
          <button type="button" className="text-button" onClick={onHistory}>
            History
          </button>
        ) : null}
        {onClose ? (
          <button type="button" className="text-button" onClick={onClose}>
            Close
          </button>
        ) : null}
      </div>
      {empty ? (
        <p className="muted small" data-testid="asset-usage-empty">
          {emptyLabel}
        </p>
      ) : (
        rows
      )}
    </section>
  );
}
