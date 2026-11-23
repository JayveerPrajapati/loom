import type { ReactNode } from 'react';

/**
 * ActionError: the one banner for action, validation and sub-panel
 * errors (U5). Renders the shared `banner error` styling as a live
 * region so asynchronous errors announce once. Classified load failures
 * stay in AssetErrorState, which owns the offline/permission states and
 * the Retry action.
 */
export function ActionError({
  children,
  id,
  testId,
}: {
  children: ReactNode;
  /** Needed when another element (e.g. an input) points at the message. */
  id?: string;
  testId?: string;
}) {
  return (
    <p className="banner error" role="alert" id={id} data-testid={testId}>
      {children}
    </p>
  );
}
