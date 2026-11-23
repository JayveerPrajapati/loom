import { AssetRowMenu } from '../assets/AssetRowMenu';
import { AssetRow } from '../assets/AssetRow';
import { VisibilityChip, VisibilityControl } from '../assets/VisibilityControl';
import type { Connector } from '../../types';

/** One connector list row: identity, operation chips, metadata and the
 *  visible View/Edit actions; History, sharing and Delete live in the
 *  kebab. */
export function ConnectorRow({
  connector,
  canWrite,
  onEdit,
  onView,
  onHistory,
  onDuplicate,
  onShare,
  onRemove,
}: {
  connector: Connector;
  canWrite: boolean;
  onEdit: () => void;
  onView: () => void;
  onHistory: () => void;
  onDuplicate: () => void;
  onShare: (visibility: 'private' | 'shared') => void;
  onRemove: () => void;
}) {
  return (
    <AssetRow
      testId="connector-row"
      identity={
        <>
          <strong>{connector.name}</strong>
          <code className="muted small">{connector.id}</code>
          <VisibilityChip visibility={connector.visibility} />
        </>
      }
      description={connector.description}
      meta={
        <span className="meta-list meta-strict">
          <span>v{connector.version}</span>
          <span>
            {connector.operations.length} operation
            {connector.operations.length === 1 ? '' : 's'}
          </span>
          <span>
            {connector.usageCount ?? 0} published pin
            {connector.usageCount === 1 ? '' : 's'}
          </span>
          {connector.owner ? <span title={connector.owner}>{connector.owner}</span> : null}
        </span>
      }
      tags={
        (connector.tags ?? []).length > 0 ? (
          <div className="profile-tags">
            {(connector.tags ?? []).map((tag) => (
              <em key={tag}>{tag}</em>
            ))}
          </div>
        ) : undefined
      }
      actions={
        <>
          <button type="button" className="text-button" onClick={onView}>
            View
          </button>
          {canWrite && (
            <button type="button" className="text-button" onClick={onEdit}>
              Edit
            </button>
          )}
          <AssetRowMenu label={`More actions for ${connector.name}`}>
            <button type="button" data-testid="asset-action-history" onClick={onHistory}>
              History
            </button>
            {canWrite && (
              <button
                type="button"
                data-testid="asset-action-duplicate"
                onClick={onDuplicate}
              >
                Duplicate
              </button>
            )}
            {canWrite && (
              <VisibilityControl
                visibility={connector.visibility}
                name={connector.name}
                onToggle={() =>
                  onShare((connector.visibility ?? 'shared') === 'shared' ? 'private' : 'shared')
                }
              />
            )}
            {canWrite && (
              <button
                type="button"
                className="danger"
                data-testid="asset-action-delete"
                onClick={onRemove}
              >
                Delete
              </button>
            )}
          </AssetRowMenu>
        </>
      }
    >
      <div className="chip-row">
        {connector.operations.map((op) => (
          <span className="chip" key={op.op}>
            <code>{op.op}</code>
          </span>
        ))}
      </div>
    </AssetRow>
  );
}
