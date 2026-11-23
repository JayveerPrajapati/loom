import type { ReactNode } from 'react';

/**
 * One row grammar for every asset list: identity and description on the left,
 * owner/version/usage metadata under them, actions aligned right.
 */
export function AssetRow({
  identity,
  description,
  meta,
  tags,
  actions,
  children,
  testId = 'asset-row',
}: {
  identity: ReactNode;
  description?: ReactNode;
  meta?: ReactNode;
  tags?: ReactNode;
  actions: ReactNode;
  children?: ReactNode;
  testId?: string;
}) {
  return (
    <article className="asset-row dashboard-run-row" data-testid={testId}>
      <div className="asset-row-main">
        <div className="asset-row-identity">{identity}</div>
        {description ? <p className="asset-row-description muted small">{description}</p> : null}
        {meta ? <p className="asset-row-meta muted small">{meta}</p> : null}
        {tags}
      </div>
      <div className="asset-row-actions">{actions}</div>
      {children ? <div className="asset-row-detail">{children}</div> : null}
    </article>
  );
}
