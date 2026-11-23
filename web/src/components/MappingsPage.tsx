import { ActionError } from './assets/ActionError';
import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from '../api';
import { confirmAction } from '../confirm';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import { useDialogFocus } from '../useDialogFocus';
import { usePagedList } from '../usePagedList';
import type { PageParams } from '../paging';
import type { BundleProfileUse, Mapping, MappingRow } from '../types';
import { useCanWrite } from '../principal';
import {
  AssetEmptyState,
  AssetErrorState,
  AssetFilteredEmptyState,
  AssetLoadingState,
} from './assets/AssetState';
import { AssetEditorPanel } from './assets/AssetEditorPanel';
import { AssetRow } from './assets/AssetRow';
import { ListSearch } from './assets/ListSearch';
import { UsagePanel } from './assets/UsagePanel';
import { VisibilityChip, VisibilityControl } from './assets/VisibilityControl';
import { AssetRowMenu } from './assets/AssetRowMenu';

// Mappings management (Configuration Hub v2, D13): reusable target←source
// row sets consumed by data.map nodes. Same governance surfaces as profiles
// and contracts — versions, usage, delete guards.

function emptyRows(): MappingRow[] {
  return [{ target: '', source: '', required: false }];
}

export function MappingsPage({
  onNotify,
  onMappingsChanged,
}: {
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onMappingsChanged?: () => void;
}) {
  const canWrite = useCanWrite();
  const [editing, setEditing] = useState<Mapping | null>(null);
  const [detail, setDetail] = useState<Mapping | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [error, setError] = useState('');
  const railOpen = Boolean(detail) || editorOpen;
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [owner, setOwner] = useState('');
  const [tags, setTags] = useState('');
  const [rows, setRows] = useState<MappingRow[]>(emptyRows());
  const [busy, setBusy] = useState(false);
  // Hub filters (v3 plan D1, v4.1 E2): ?kind= narrows the all-items list
  // to one mapping kind (the create form follows the filter); ?nodeType=
  // arrives from the merged hub page's stat-row link and narrows to the
  // kinds that node type consumes.
  const [kindFilter, setKindFilter] = useState(
    () => new URLSearchParams(window.location.search).get('kind') ?? '',
  );
  const [nodeTypeFilter, setNodeTypeFilter] = useState(
    () => new URLSearchParams(window.location.search).get('nodeType') ?? '',
  );

  const loadMappings = useCallback(
    (params: PageParams) =>
      api.listMappingsPage(kindFilter || undefined, nodeTypeFilter || undefined, params),
    [kindFilter, nodeTypeFilter],
  );
  const list = usePagedList<Mapping>(loadMappings);
  const filtersActive = Boolean(kindFilter || nodeTypeFilter || list.query.trim());
  const refreshRows = () => {
    list.reload();
    onMappingsChanged?.();
  };

  const startCreate = () => {
    setEditing(null);
    setName('');
    setDescription('');
    setOwner('');
    setTags('');
    setRows(emptyRows());
    setError('');
    setDetail(null);
    setEditorOpen(true);
  };
  const closeEditor = () => {
    setEditing(null);
    setName('');
    setDescription('');
    setOwner('');
    setTags('');
    setRows(emptyRows());
    setError('');
    setEditorOpen(false);
  };
  const startEdit = (mapping: Mapping) => {
    setEditing(mapping);
    setName(mapping.name);
    setDescription(mapping.description ?? '');
    setOwner(mapping.owner ?? '');
    setTags((mapping.tags ?? []).join(', '));
    setRows(mapping.rows.map((row) => ({ ...row })));
    setError('');
    setDetail(null);
    setEditorOpen(true);
  };
  const openDetail = (mapping: Mapping) => {
    setDetail(mapping);
    setEditorOpen(false);
  };

  const submit = () => {
    setError('');
    if (!name.trim()) {
      setError('Name is required.');
      return;
    }
    // A row with only one side filled is a mistake, not an empty row to
    // drop silently — name it before anything is sent.
    const halfFilled = rows.findIndex(
      (row) => row.target.trim() !== row.source.trim() && (row.target.trim() || row.source.trim()),
    );
    if (halfFilled >= 0) {
      setError(`Row ${halfFilled + 1} needs both target and source.`);
      return;
    }
    setBusy(true);
    // Editing follows the mapping's own kind so a param-kind mapping is
    // never silently downgraded to item.
    const payload = {
      name,
      description: description || undefined,
      kind: editing ? editing.kind ?? 'item' : kindFilter || undefined,
      owner: owner || undefined,
      tags: tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      rows: rows.filter((row) => row.target.trim() && row.source.trim()),
    };
    const done = () => {
      setBusy(false);
      refreshRows();
      closeEditor();
    };
    if (editing) {
      api
        .updateMapping(editing.id, { ...payload, version: editing.version })
        .then(() => {
          onNotify?.(`Saved ${name} as v${editing.version + 1}.`);
          done();
        })
        .catch((error: ApiError) => {
          setBusy(false);
          setError(error.message);
        });
    } else {
      api
        .createMapping(payload)
        .then(() => {
          onNotify?.(`Created ${name}.`);
          done();
        })
        .catch((error: ApiError) => {
          setBusy(false);
          setError(error.message);
        });
    }
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Mappings</h1>
          <p className="page-sub">Reusable target ← source transforms for data.map nodes.</p>
        </div>
        {canWrite && (
          <button type="button" className="button primary" onClick={startCreate}>
            New mapping
          </button>
        )}
      </header>
      {nodeTypeFilter && (
        <p className="banner notice" role="status" data-testid="mapping-node-type-filter">
          Filtered to mappings for <code>{nodeTypeFilter}</code>.{' '}
          <button
            type="button"
            className="text-button"
            onClick={() => {
              setNodeTypeFilter('');
              const url = new URL(window.location.href);
              url.searchParams.delete('nodeType');
              window.history.replaceState({}, '', url);
            }}
          >
            Show all node types
          </button>
        </p>
      )}
      <div className="page-toolbar">
        <ListSearch
          value={list.query}
          onChange={list.setQuery}
          placeholder="Search mappings"
          label="Search mappings"
          busy={list.loading || list.loadingMore}
          count={list.query ? list.count : undefined}
          testId="mappings-search"
        />
        <label className="field inline-field" htmlFor="mapping-kind-filter">
          <span className="visually-hidden">Filter by kind</span>
          <select
            id="mapping-kind-filter"
            className="text-input"
            data-testid="mapping-kind-filter"
            value={kindFilter}
            onChange={(event) => {
              setKindFilter(event.target.value);
              const url = new URL(window.location.href);
              if (event.target.value) url.searchParams.set('kind', event.target.value);
              else url.searchParams.delete('kind');
              window.history.replaceState({}, '', url);
            }}
          >
            <option value="">All kinds</option>
            {['item', 'body', 'query', 'headers', 'pathParams'].map((kind) => (
              <option key={kind} value={kind}>{kind}</option>
            ))}
          </select>
        </label>
      </div>
      <div className="dashboard-grid mapping-workspace" data-editor={railOpen ? 'open' : 'closed'}>
        <section className="dashboard-panel" aria-label="Mappings list">
          <div className="panel-head">
            <h2>All mappings</h2>
            {!list.loading && !list.failure && <span className="count-pill">{list.count}</span>}
          </div>
          {list.loading && !list.loaded && <AssetLoadingState label="Loading mappings…" />}
          {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
          {!list.failure &&
            list.loaded &&
            list.items.length === 0 &&
            (filtersActive ? (
              <AssetFilteredEmptyState
                title="No mappings match"
                onClear={() => {
                  setKindFilter('');
                  setNodeTypeFilter('');
                  list.setQuery('');
                  const url = new URL(window.location.href);
                  url.searchParams.delete('kind');
                  url.searchParams.delete('nodeType');
                  window.history.replaceState({}, '', url);
                }}
              >
                No mapping matches the current filters or search.
              </AssetFilteredEmptyState>
            ) : (
              <AssetEmptyState
                testId="mappings-empty"
                title="No mappings yet"
                action={
                  canWrite ? (
                    <button type="button" className="button primary" onClick={startCreate}>
                      Create the first mapping
                    </button>
                  ) : undefined
                }
              >
                A mapping renames and reshapes items between nodes — target ← source rows that
                data.map and body params resolve at run time.
              </AssetEmptyState>
            ))}
          {!list.failure &&
            list.loaded &&
            list.items.map((mapping) => (
              <MappingRowView
                key={mapping.id}
                mapping={mapping}
                onEdit={() => startEdit(mapping)}
                onView={() => openDetail(mapping)}
                onRefresh={refreshRows}
                onNotify={onNotify}
              />
            ))}
          {!list.failure && list.hasMore && (
            <div className="list-load-more">
              <button
                type="button"
                className="button"
                onClick={list.loadMore}
                disabled={list.loadingMore}
                data-testid="mappings-load-more"
              >
                {list.loadingMore ? 'Loading more…' : 'Load more'}
              </button>
            </div>
          )}
        </section>
        {detail && !editorOpen && (
          <MappingDetailView
            mapping={detail}
            canWrite={canWrite}
            onEdit={() => startEdit(detail)}
            onClose={() => setDetail(null)}
          />
        )}
        {editorOpen && canWrite && (
        <AssetEditorPanel
          ariaLabel="Mapping editor"
          title={editing ? `Edit ${editing.name}` : 'New mapping'}
          subtitle={
            editing
              ? `Saving creates v${editing.version + 1}`
              : 'Reusable target ← source rows for data.map and parameter groups.'
          }
          busy={busy}
          dirty={Boolean(
            name.trim() ||
              description.trim() ||
              owner.trim() ||
              tags.trim() ||
              rows.some((row) => row.target.trim() || row.source.trim()),
          )}
          saveLabel={editing ? `Save as v${editing.version + 1}` : 'Create mapping'}
          formId="mapping-form"
          onCancel={closeEditor}
          error={error}
        >
          <form
            id="mapping-form"
            onSubmit={(event) => {
              event.preventDefault();
              submit();
            }}
            className="hub-form"
          >
            <label className="field" htmlFor="mapping-name">
              <span className="field-label">Name</span>
              <input
                id="mapping-name"
                className="text-input"
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
              />
            </label>
            <label className="field" htmlFor="mapping-description">
              <span className="field-label">Description</span>
              <input
                id="mapping-description"
                className="text-input"
                value={description}
                onChange={(event) => setDescription(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="mapping-owner">
              <span className="field-label">Owner</span>
              <input
                id="mapping-owner"
                className="text-input"
                value={owner}
                onChange={(event) => setOwner(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="mapping-tags">
              <span className="field-label">Tags (comma-separated)</span>
              <input
                id="mapping-tags"
                className="text-input"
                value={tags}
                onChange={(event) => setTags(event.target.value)}
              />
            </label>
            <fieldset className="field">
              <legend className="field-label">Rows (target ← source)</legend>
              <div className="mapping-row-header" aria-hidden="true">
                <span>Target</span>
                <span>Source</span>
                <span>Required</span>
              </div>
              {rows.map((row, index) => (
                <div className="mapping-row-edit" key={index}>
                  <input
                    className="text-input"
                    aria-label={`Row ${index + 1} target`}
                    placeholder="target"
                    value={row.target}
                    onChange={(event) =>
                      setRows(rows.map((r, i) => (i === index ? { ...r, target: event.target.value } : r)))
                    }
                  />
                  <input
                    className="text-input"
                    aria-label={`Row ${index + 1} source`}
                    placeholder="{{ $json.field }}"
                    value={row.source}
                    onChange={(event) =>
                      setRows(rows.map((r, i) => (i === index ? { ...r, source: event.target.value } : r)))
                    }
                  />
                  <label className="schema-field-required">
                    <input
                      type="checkbox"
                      aria-label={`Row ${index + 1} required`}
                      checked={!!row.required}
                      onChange={(event) =>
                        setRows(rows.map((r, i) => (i === index ? { ...r, required: event.target.checked } : r)))
                      }
                    />
                    required
                  </label>
                  <button
                    type="button"
                    className="text-button"
                    aria-label={`Remove row ${index + 1}`}
                    onClick={() => setRows(rows.filter((_, i) => i !== index))}
                    disabled={rows.length <= 1}
                  >
                    Remove
                  </button>
                </div>
              ))}
              <button type="button" className="text-button" onClick={() => setRows([...rows, { target: '', source: '' }])}>
                Add row
              </button>
            </fieldset>
          </form>
        </AssetEditorPanel>
        )}
      </div>
    </main>
  );
}

function MappingRowView({
  mapping,
  onEdit,
  onView,
  onRefresh,
  onNotify,
}: {
  mapping: Mapping;
  onEdit: () => void;
  onView: () => void;
  onRefresh: () => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}) {
  const canWrite = useCanWrite();
  const [usage, setUsage] = useState(false);
  const [usageRows, setUsageRows] = useState<
    { workflowName?: string; workflowId: string; workflowVersion: number }[]
  >([]);
  const [usageProfiles, setUsageProfiles] = useState<BundleProfileUse[]>([]);
  const [history, setHistory] = useState(false);

  const shared = (mapping.visibility ?? 'shared') === 'shared';
  const share = () => {
    (shared ? api.unshareMapping(mapping.id) : api.shareMapping(mapping.id))
      .then(() => {
        onNotify?.(
          shared ? `${mapping.name} is now private` : `${mapping.name} is now visible to everyone`,
        );
        onRefresh();
      })
      .catch((error: ApiError) => onNotify?.(error.message, 'error'));
  };

  const loadUsage = () => {
    api
      .mappingUsage(mapping.id)
      .then((result) => {
        setUsageRows(result.usage ?? []);
        setUsageProfiles(result.profiles ?? []);
        setUsage(true);
      })
      .catch((error: ApiError) => onNotify?.(error.message, 'error'));
  };

  const remove = async () => {
    let result: Awaited<ReturnType<typeof api.mappingUsage>>;
    try {
      result = await api.mappingUsage(mapping.id);
    } catch (reason) {
      onNotify?.(reason instanceof ApiError ? reason.message : 'Could not load usage', 'error');
      return;
    }
    const pins = result.usage ?? [];
    const profiles = result.profiles ?? [];
    const inUse = pins.length + profiles.length > 0;
    const names = profiles
      .slice(0, 3)
      .map((profile) => profile.name)
      .join(', ');
    await confirmAction({
      title: `Delete the mapping “${mapping.name}”?`,
      body: inUse
        ? `${pins.length} published workflow version${pins.length === 1 ? '' : 's'} pin this mapping and ${profiles.length} profile bundle${profiles.length === 1 ? '' : 's'} reference it. Deletion is blocked until they are removed.${names ? ` Bundle profiles: ${names}.` : ''}`
        : 'No published workflow or profile bundle references this mapping. This cannot be undone.',
      confirmLabel: 'Delete mapping',
      secondaryAction: inUse
        ? {
            label: 'View usage',
            run: () => {
              setUsageRows(pins);
              setUsageProfiles(profiles);
              setUsage(true);
            },
          }
        : undefined,
      onConfirm: async () => {
        try {
          await api.deleteMapping(mapping.id);
          onNotify?.(`Deleted ${mapping.name}.`);
          onRefresh();
        } catch (reason) {
          if (reason instanceof ApiError) {
            return reason.status === 409
              ? `${reason.message} Use View usage to find the pinned versions.`
              : reason.message;
          }
          return 'Delete failed.';
        }
      },
    });
  };

  return (
    <>
    <AssetRow
      identity={
        <>
          <strong>{mapping.name}</strong>
          {(mapping.kind ?? 'item') !== 'item' && (
            <span className="count-pill" data-testid="mapping-kind-badge">{mapping.kind}</span>
          )}
          <VisibilityChip visibility={mapping.visibility} testId="mapping-visibility-chip" />
        </>
      }
      description={mapping.description}
      meta={
        <span className="meta-list meta-strict">
          <span>{mapping.rows.length} rows</span>
          <span>v{mapping.version}</span>
          <span>
            {mapping.usageCount ?? 0} published pin{mapping.usageCount === 1 ? '' : 's'}
          </span>
          {mapping.owner ? <span title={mapping.owner}>{mapping.owner}</span> : null}
        </span>
      }
      tags={
        (mapping.tags ?? []).length > 0 ? (
          <div className="profile-tags">
            {mapping.tags!.map((tag) => (
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
          <AssetRowMenu label={`More actions for ${mapping.name}`}>
            <button
              type="button"
              data-testid="asset-action-history"
              onClick={() => setHistory(true)}
            >
              History
            </button>
            <button
              type="button"
              aria-label={`Usage of ${mapping.name}`}
              onClick={loadUsage}
            >
              Usage
            </button>
            {canWrite && (
              <VisibilityControl visibility={mapping.visibility} name={mapping.name} onToggle={share} />
            )}
            {canWrite && (
              <button
                type="button"
                data-testid="asset-action-duplicate"
                onClick={() => {
                  api.duplicateMapping(mapping.id).then(() => {
                    onNotify?.(`Duplicated ${mapping.name}.`);
                    onRefresh();
                  });
                }}
              >
                Duplicate
              </button>
            )}
            {canWrite && (
              <button
                type="button"
                className="danger"
                data-testid="asset-action-delete"
                onClick={() => void remove()}
              >
                Delete
              </button>
            )}
          </AssetRowMenu>
        </>
      }
    >
      <div className="mapping-rows-preview">
        {mapping.rows.slice(0, 4).map((row) => (
          <code key={row.target}>
            {row.target} ← {row.source}
            {row.required ? ' *' : ''}
          </code>
        ))}
        {mapping.rows.length > 4 && <span className="muted">+{mapping.rows.length - 4} more</span>}
      </div>
      {usage && (
        <MappingUsageList
          rows={usageRows}
          profiles={usageProfiles}
          onClose={() => setUsage(false)}
          onHistory={() => {
            setUsage(false);
            setHistory(true);
          }}
        />
      )}
    </AssetRow>
    {/* The history dialog renders outside the row: rows carry a hover
        transform, which would become the containing block for the
        dialog's fixed backdrop and clip the wide shell to the row. */}
    {history && <MappingHistory mapping={mapping} onClose={() => setHistory(false)} />}
    </>
  );
}

function MappingUsageList({
  rows,
  profiles,
  onClose,
  onHistory,
}: {
  rows: { workflowName?: string; workflowId: string; workflowVersion: number }[];
  profiles: BundleProfileUse[];
  onClose: () => void;
  onHistory: () => void;
}) {
  return (
    <div className="mapping-detail">
      <UsagePanel
        title="Pinned by workflows or profile bundles"
        empty={rows.length === 0 && profiles.length === 0}
        emptyLabel="No published workflow or profile bundle references this mapping yet."
        onHistory={onHistory}
        onClose={onClose}
        rows={
          <>
            {rows.map((row, index) => (
              <div className="profile-preview-row" key={index}>
                <code>{row.workflowName ?? row.workflowId}</code>
                <span>v{row.workflowVersion}</span>
              </div>
            ))}
            {profiles.map((profile) => (
              <div className="profile-preview-row" key={`profile-${profile.id}`}>
                <code>{profile.name}</code>
                <span>
                  {profile.nodeType} · profile v{profile.version}
                </span>
              </div>
            ))}
          </>
        }
      />
    </div>
  );
}

function MappingHistory({ mapping, onClose }: { mapping: Mapping; onClose: () => void }) {
  const ref = useDialogFocus<HTMLDivElement>(true, onClose);
  const [versions, setVersions] = useState<{ version: number; actor: string; createdAt: string }[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // Bumped by the error state's retry so the load effect re-runs for
  // the same mapping id.
  const [reload, setReload] = useState(0);
  useEffect(() => {
    setLoading(true);
    setError('');
    api
      .mappingVersions(mapping.id)
      .then((result) => setVersions(result.versions ?? []))
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : String(reason)),
      )
      .finally(() => setLoading(false));
  }, [mapping.id, reload]);
  return (
    <div className="dialog-backdrop" role="presentation">
      <div
        className="dialog asset-history-dialog"
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label="Mapping history"
        data-testid="mapping-history"
      >
        <header className="dialog-head">
          <h3>History: {mapping.name}</h3>
          <button className="icon-button" onClick={onClose} aria-label="Close history" type="button">
            ×
          </button>
        </header>
        <div className="dialog-body">
          {loading && <AssetLoadingState label="Loading versions…" className="muted small" />}
          {error ? (
            <>
              <ActionError>{error}</ActionError>
              <button
                type="button"
                className="button subtle"
                onClick={() => setReload((count) => count + 1)}
              >
                Try again
              </button>
            </>
          ) : (
            <>
              {!loading && versions.length === 0 && (
                <p className="muted small">No mapping versions recorded yet.</p>
              )}
              {versions.map((version) => (
                <div className="profile-preview-row" key={version.version}>
                  <span>v{version.version}</span>
                  <span>{new Date(version.createdAt).toLocaleString()}</span>
                  <span>{version.actor || 'unknown'}</span>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

/** The read-only mapping detail rail, available to every signed-in
 *  user: every row (target ← source, required), metadata, usage and
 *  the version-history entry point. Writers continue into the editor
 *  from here. */
function MappingDetailView({
  mapping,
  canWrite,
  onEdit,
  onClose,
}: {
  mapping: Mapping;
  canWrite: boolean;
  onEdit: () => void;
  onClose: () => void;
}) {
  const [usage, setUsage] = useState<
    { pins: { workflowName?: string; workflowId: string; workflowVersion: number }[]; profiles: BundleProfileUse[] } | null
  >(null);
  // U6: a failed usage read is a named error with Retry, never "no usage".
  const [usageFailure, setUsageFailure] = useState<LoadFailure | null>(null);
  const [usageReload, setUsageReload] = useState(0);
  const [history, setHistory] = useState(false);

  useEffect(() => {
    setUsage(null);
    setUsageFailure(null);
    let alive = true;
    api
      .mappingUsage(mapping.id)
      .then((result) => {
        if (alive) setUsage({ pins: result.usage ?? [], profiles: result.profiles ?? [] });
      })
      .catch((reason: unknown) => {
        if (alive) setUsageFailure(classifyCurrentLoadFailure(reason));
      });
    return () => {
      alive = false;
    };
  }, [mapping.id, usageReload]);

  return (
    <>
      <section className="asset-editor" aria-label="Mapping detail" data-testid="mapping-detail">
        <header className="asset-editor-head">
          <div>
            <h2>{mapping.name}</h2>
            <p className="muted small">
              Mapping · v{mapping.version}
              {mapping.kind && mapping.kind !== 'item' ? ` · ${mapping.kind}` : ''}
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
          {mapping.description ? <p className="muted small">{mapping.description}</p> : null}
          <p className="meta-list meta-strict">
            <span>v{mapping.version}</span>
            <span>{mapping.rows.length} rows</span>
            {mapping.kind && mapping.kind !== 'item' ? <span>{mapping.kind}</span> : null}
            {mapping.owner ? <span title={mapping.owner}>{mapping.owner}</span> : null}
          </p>
          {(mapping.tags ?? []).length > 0 ? (
            <span className="profile-tags">
              {(mapping.tags ?? []).map((tag) => (
                <em key={tag}>{tag}</em>
              ))}
            </span>
          ) : null}

          <p className="field-label">Rows (target ← source)</p>
          <div className="mapping-view-rows" data-testid="mapping-view-rows">
            {mapping.rows.map((row, index) => (
              <code key={index}>
                {row.target} ← {row.source}
                {row.required ? ' *' : ''}
              </code>
            ))}
          </div>

          <p className="field-label">Usage</p>
          {usageFailure ? (
            <AssetErrorState
              failure={usageFailure}
              onRetry={() => setUsageReload((count) => count + 1)}
              testId="mapping-usage-error"
            />
          ) : usage === null ? (
            <AssetLoadingState label="Loading usage…" className="muted small" />
          ) : usage.pins.length + usage.profiles.length === 0 ? (
            <p className="muted small">No published workflow or profile bundle references this mapping yet.</p>
          ) : (
            <div className="profile-usage" data-testid="mapping-detail-usage">
              {usage.pins.map((pin) => (
                <span key={`${pin.workflowId}-v${pin.workflowVersion}`}>
                  {pin.workflowName || pin.workflowId} v{pin.workflowVersion}
                </span>
              ))}
              {usage.profiles.map((profile) => (
                <span key={`profile-${profile.id}`}>
                  {profile.name} · {profile.nodeType} · profile v{profile.version}
                </span>
              ))}
            </div>
          )}

          <div className="asset-editor-actions">
            <button type="button" className="button subtle" onClick={() => setHistory(true)}>
              View history
            </button>
          </div>
        </div>
      </section>
      {history && <MappingHistory mapping={mapping} onClose={() => setHistory(false)} />}
    </>
  );
}
