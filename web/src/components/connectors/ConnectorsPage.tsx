import { useCallback, useState } from 'react';
import { api, ApiError } from '../../api';
import { confirmAction } from '../../confirm';
import { usePagedList } from '../../usePagedList';
import type { PageParams } from '../../paging';
import { useCanWrite } from '../../principal';
import {
  AssetEmptyState,
  AssetErrorState,
  AssetFilteredEmptyState,
  AssetLoadingState,
} from '../assets/AssetState';
import { ListSearch } from '../assets/ListSearch';
import type { Connector } from '../../types';
import { ConnectorRow } from './ConnectorRow';
import { ConnectorDetailView } from './ConnectorDetailView';
import { ConnectorForm } from './ConnectorForm';
import { ConnectorHistory } from './ConnectorHistory';

// Connectors management: versioned API connectors whose operations
// become connector.<id>.<op> node types on top of http.request's
// trusted pipeline — SSRF, egress policy, credential allowedHosts,
// cassettes and replay all apply. The page owns the list and the rail
// (read-only detail for everyone, editor for writers); rows, the
// editor and the history dialog live in their own modules.

export function ConnectorsPage({
  onNotify,
  onConnectorsChanged,
}: {
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onConnectorsChanged?: () => void;
}) {
  const canWrite = useCanWrite();
  const [editing, setEditing] = useState<Connector | null>(null);
  const [detail, setDetail] = useState<Connector | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [historyFor, setHistoryFor] = useState<Connector | null>(null);
  const railOpen = Boolean(detail) || editorOpen;

  const loadConnectors = useCallback((params: PageParams) => api.listConnectorsPage(params), []);
  const list = usePagedList<Connector>(loadConnectors);
  const filtersActive = list.query.trim() !== '';
  const refreshRows = () => {
    list.reload();
    onConnectorsChanged?.();
  };

  const startCreate = () => {
    setDetail(null);
    setEditing(null);
    setEditorOpen(true);
  };

  const startEdit = (connector: Connector) => {
    setDetail(null);
    setEditing(connector);
    setEditorOpen(true);
  };

  // Saving or cancelling from the editor returns to the detail of the
  // same connector — refreshed, so the version shown is the new one.
  const backToDetail = async (connector: Connector | null) => {
    setEditorOpen(false);
    setEditing(null);
    if (!connector) return;
    try {
      const fresh = (await api.getConnector(connector.id)).connector;
      setDetail(fresh);
    } catch {
      setDetail(connector);
    }
  };

  const onSaved = () => {
    setEditorOpen(false);
    setEditing(null);
    refreshRows();
    if (editing) void backToDetail(editing);
  };

  const share = (connector: Connector, visibility: 'private' | 'shared') => {
    api
      .shareConnector(connector.id, visibility)
      .then(() => {
        onNotify?.(
          visibility === 'shared'
            ? `${connector.name} is now visible to everyone`
            : `${connector.name} is now private`,
        );
        refreshRows();
        if (detail?.id === connector.id) void backToDetail(connector);
      })
      .catch((reason: unknown) =>
        onNotify?.(reason instanceof ApiError ? reason.message : 'Share change failed', 'error'),
      );
  };

  const duplicate = (connector: Connector) => {
    api
      .duplicateConnector(connector.id)
      .then(({ connector: copy }) => {
        onNotify?.(`Duplicated ${connector.name}.`);
        refreshRows();
        setDetail(copy);
        setEditorOpen(false);
      })
      .catch((reason: unknown) =>
        onNotify?.(reason instanceof ApiError ? reason.message : 'Duplicate failed', 'error'),
      );
  };

  const remove = async (connector: Connector) => {
    await confirmAction({
      title: `Delete ${connector.name}?`,
      body: connector.usageCount
        ? `The connector is removed for good. ${connector.usageCount} workflow version(s) pin it and will block deletion until republished.`
        : 'The connector is removed for good. Nothing pins it.',
      confirmLabel: 'Delete connector',
      onConfirm: async () => {
        try {
          await api.deleteConnector(connector.id);
          onNotify?.(`${connector.name} deleted`);
          if (detail?.id === connector.id) setDetail(null);
          refreshRows();
        } catch (reason) {
          if (reason instanceof ApiError) {
            return reason.status === 409
              ? `${reason.message} Open the detail to see the pinned versions.`
              : reason.message;
          }
          return `Delete failed: ${String(reason)}`;
        }
      },
    });
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Connectors</h1>
          <p className="page-sub">
            Reusable API connectors. Each operation becomes a node type that runs on the trusted
            HTTP pipeline: egress rules, credential restrictions and replay apply unchanged.
          </p>
        </div>
        {canWrite && (
          <button type="button" className="button primary" onClick={startCreate}>
            New connector
          </button>
        )}
      </header>

      <div
        className="dashboard-grid connector-workspace"
        data-editor={railOpen ? 'open' : 'closed'}
      >
        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <h2>All connectors</h2>
              <p className="muted small">Connector nodes appear in the editor palette by category.</p>
            </div>
          </header>
          <div className="search-wrap">
            <ListSearch
              value={list.query}
              onChange={list.setQuery}
              placeholder="Search connectors"
              label="Search connectors"
              busy={list.loading || list.loadingMore}
              count={list.loaded ? list.count : undefined}
              testId="connectors-search"
            />
          </div>
          {list.loading && !list.loaded && <AssetLoadingState label="Loading connectors…" />}
          {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
          {!list.failure && list.loaded && list.items.length === 0 ? (
            filtersActive ? (
              <AssetFilteredEmptyState
                title="No connectors match"
                onClear={() => list.setQuery('')}
              >
                No connector matches the current search.
              </AssetFilteredEmptyState>
            ) : (
              <AssetEmptyState
                testId="connectors-empty"
                title="No connectors yet"
                action={
                  canWrite ? (
                    <button type="button" className="button primary" onClick={startCreate}>
                      Create the first connector
                    </button>
                  ) : undefined
                }
              >
                A connector defines API operations as templates
                (<code>{'{{cred.baseUrl}}/things'}</code>) so teams share integrations without
                code.
              </AssetEmptyState>
            )
          ) : !list.failure && list.loaded ? (
            <div className="dashboard-run-list" data-testid="connector-list">
              {list.items.map((connector) => (
                <ConnectorRow
                  key={connector.id}
                  connector={connector}
                  canWrite={canWrite}
                  onEdit={() => startEdit(connector)}
                  onView={() => {
                    setDetail(connector);
                    setEditorOpen(false);
                  }}
                  onHistory={() => setHistoryFor(connector)}
                  onDuplicate={() => duplicate(connector)}
                  onShare={(visibility) => share(connector, visibility)}
                  onRemove={() => void remove(connector)}
                />
              ))}
            </div>
          ) : null}
          {!list.failure && list.hasMore && (
            <div className="list-load-more">
              <button
                type="button"
                className="button"
                onClick={list.loadMore}
                disabled={list.loadingMore}
                data-testid="connectors-load-more"
              >
                {list.loadingMore ? 'Loading more…' : 'Load more'}
              </button>
            </div>
          )}
        </section>

        {detail && !editorOpen && (
          <ConnectorDetailView
            connector={detail}
            canWrite={canWrite}
            onEdit={() => startEdit(detail)}
            onClose={() => setDetail(null)}
            onHistory={() => setHistoryFor(detail)}
          />
        )}
        {editorOpen && canWrite && (
          <ConnectorForm
            initial={editing}
            onCancel={() => void backToDetail(editing)}
            onSaved={onSaved}
            onNotify={onNotify}
          />
        )}
      </div>

      {historyFor && (
        <ConnectorHistory connector={historyFor} onClose={() => setHistoryFor(null)} />
      )}
    </main>
  );
}
