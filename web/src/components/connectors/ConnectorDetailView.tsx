import { AssetLoadingState } from '../assets/AssetState';
import { useEffect, useState } from 'react';
import { api } from '../../api';
import { classifyCurrentLoadFailure, type LoadFailure } from '../../loadState';
import type { Connector, ConnectorUsage } from '../../types';
import { AssetErrorState } from '../assets/AssetState';

/** The read-only connector detail rail, available to every signed-in
 *  user: metadata, every operation with its method, URL template and
 *  config fields, usage, and the version-history entry point. Writers
 *  continue into the editor from here. */
export function ConnectorDetailView({
  connector,
  canWrite,
  onEdit,
  onClose,
  onHistory,
}: {
  connector: Connector;
  canWrite: boolean;
  onEdit: () => void;
  onClose: () => void;
  onHistory: () => void;
}) {
  const [usage, setUsage] = useState<ConnectorUsage[] | null>(null);
  // U6: a failed usage read is a named error with Retry, never "no usage".
  const [usageFailure, setUsageFailure] = useState<LoadFailure | null>(null);
  const [usageReload, setUsageReload] = useState(0);

  useEffect(() => {
    setUsage(null);
    setUsageFailure(null);
    let alive = true;
    api
      .connectorUsage(connector.id)
      .then((result) => {
        if (alive) setUsage(result.usage ?? []);
      })
      .catch((reason: unknown) => {
        if (alive) setUsageFailure(classifyCurrentLoadFailure(reason));
      });
    return () => {
      alive = false;
    };
  }, [connector.id, usageReload]);

  return (
    <section className="asset-editor" aria-label="Connector detail" data-testid="connector-detail">
      <header className="asset-editor-head">
        <div>
          <h2>{connector.name}</h2>
          <p className="muted small">
            Connector · v{connector.version} · {connector.operations.length} operation
            {connector.operations.length === 1 ? '' : 's'}
          </p>
        </div>
        <div className="asset-editor-actions">
          {canWrite ? (
            <button type="button" className="button primary" onClick={onEdit}>
              Edit
            </button>
          ) : null}
          <button
            type="button"
            className="icon-button"
            onClick={onClose}
            aria-label="Close detail"
            title="Close detail"
          >
            ×
          </button>
        </div>
      </header>
      <div className="asset-editor-body">
        {connector.description ? <p className="muted small">{connector.description}</p> : null}
        <p className="meta-list meta-strict">
          <span>v{connector.version}</span>
          {connector.owner ? <span title={connector.owner}>{connector.owner}</span> : null}
        </p>
        {(connector.tags ?? []).length > 0 ? (
          <span className="profile-tags">
            {(connector.tags ?? []).map((tag) => (
              <em key={tag}>{tag}</em>
            ))}
          </span>
        ) : null}

        <p className="field-label">Operations</p>
        <div className="connector-view-ops" data-testid="connector-view-ops">
          {connector.operations.map((op) => (
            <div className="connector-view-op" key={op.op}>
              <div className="connector-view-op-line">
                <code>{op.op}</code>
                <span className="count-pill">{op.method}</span>
                <span className="muted small connector-view-url" title={op.url}>
                  {op.url}
                </span>
              </div>
              {op.label ? <p className="muted small">{op.label}</p> : null}
              {Object.keys(op.fields ?? {}).length > 0 ? (
                <div className="connector-view-fields">
                  <div className="connector-view-header" aria-hidden="true">
                    <span>Name</span>
                    <span>Type</span>
                    <span>Required</span>
                  </div>
                  {Object.entries(op.fields ?? {}).map(([name, field]) => (
                    <div className="connector-view-line" key={name}>
                      <code title={name}>{name}</code>
                      <span className="muted small">{field.type}</span>
                      {field.required ? (
                        <em className="schema-view-required">required</em>
                      ) : null}
                    </div>
                  ))}
                </div>
              ) : (
                <p className="muted small">No config fields.</p>
              )}
            </div>
          ))}
        </div>

        <p className="field-label">Usage</p>
        {usageFailure ? (
          <AssetErrorState
            failure={usageFailure}
            onRetry={() => setUsageReload((count) => count + 1)}
            testId="connector-usage-error"
          />
        ) : usage === null ? (
          <AssetLoadingState label="Loading usage…" className="muted small" />
        ) : usage.length === 0 ? (
          <p className="muted small">No published workflows pin this connector.</p>
        ) : (
          <div className="profile-usage" data-testid="connector-detail-usage">
            {usage.map((entry) => (
              <span key={`${entry.workflowId}-${entry.workflowVersion}`}>
                {entry.workflowName || entry.workflowId} (workflow v{entry.workflowVersion},
                connector v{entry.connectorVersion})
              </span>
            ))}
          </div>
        )}

        <div className="asset-editor-actions">
          <button type="button" className="button subtle" onClick={onHistory}>
            View history
          </button>
        </div>
      </div>
    </section>
  );
}
