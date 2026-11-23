import type { ReactNode } from 'react';
import type { LoadFailure } from '../../loadState';

export function AssetLoadingState({
  label = 'Loading…',
  className = 'muted small pad',
}: {
  label?: string;
  /** Override when a surface's existing rhythm differs from the default
   *  `muted small pad` (U7: unify the idiom, not the spacing). */
  className?: string;
}) {
  return (
    <p className={className} role="status" data-testid="asset-loading">
      {label}
    </p>
  );
}

export function AssetEmptyState({
  title,
  children,
  action,
  testId = 'asset-empty',
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  testId?: string;
}) {
  return (
    <div className="empty-panel" data-testid={testId}>
      <h2>{title}</h2>
      {children ? <p className="muted">{children}</p> : null}
      {action}
    </div>
  );
}

export function AssetFilteredEmptyState({
  title = 'No matches',
  children,
  onClear,
  testId = 'asset-filtered-empty',
}: {
  title?: string;
  children?: ReactNode;
  onClear: () => void;
  testId?: string;
}) {
  return (
    <div className="empty-panel" data-testid={testId}>
      <h2>{title}</h2>
      {children ? <p className="muted">{children}</p> : null}
      <button type="button" className="button subtle" onClick={onClear}>
        Clear filters
      </button>
    </div>
  );
}

export function AssetErrorState({
  failure,
  onRetry,
  testId = 'asset-error',
}: {
  failure: LoadFailure;
  onRetry?: () => void;
  testId?: string;
}) {
  const alert = failure.kind !== 'offline';
  return (
    <div
      className={`banner ${failure.kind === 'offline' ? 'notice' : 'error'}`}
      role={alert ? 'alert' : 'status'}
      data-testid={testId}
      data-failure-kind={failure.kind}
    >
      <strong>{failure.title}.</strong> <span>{failure.message}</span>
      {failure.retryable && onRetry ? (
        <button
          type="button"
          className="button subtle"
          data-testid={`${testId}-retry`}
          onClick={onRetry}
        >
          Retry
        </button>
      ) : null}
    </div>
  );
}
