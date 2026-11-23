import { useCallback, useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import { confirmAction } from '../confirm';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import { usePagedList } from '../usePagedList';
import type { PageParams } from '../paging';
import type { BundleProfileUse, DataSchema, DataSchemaUsage, SchemaField } from '../types';
import { useCanWrite } from '../principal';
import { countFields, fieldDepth, validateSchemaFields } from '../schemaFields';
import { draft07ToLoom, loomToDraft07 } from '../jsonSchema';
import { useDialogFocus } from '../useDialogFocus';
import { FieldRows, cloneFields } from './schemas/FieldRows';
import { SchemaHistory } from './schemas/SchemaHistory';
import { SchemaImportDialog } from './schemas/SchemaImportDialog';
import { SchemaSampleTester } from './schemas/SchemaSampleTester';
import { AssetEditorPanel } from './assets/AssetEditorPanel';
import {
  AssetEmptyState,
  AssetErrorState,
  AssetFilteredEmptyState,
  AssetLoadingState,
} from './assets/AssetState';
import { AssetRow } from './assets/AssetRow';
import { ListSearch } from './assets/ListSearch';
import { UsagePanel } from './assets/UsagePanel';
import { VisibilityChip, VisibilityControl } from './assets/VisibilityControl';
import { AssetRowMenu } from './assets/AssetRowMenu';

/**
 * Data contracts management (Configuration Hub v2): reusable, versioned
 * field lists describing the shape of data. The form edits the current
 * version and saves a new one (stale concurrent edits are a server 409);
 * a sample tester checks payloads against the contract.
 */
export function SchemasPage({
  onNotify,
  onSchemasChanged,
}: {
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onSchemasChanged?: () => void;
}) {
  const canWrite = useCanWrite();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  const [editing, setEditing] = useState<DataSchema | null>(null);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [owner, setOwner] = useState('');
  const [tagsText, setTagsText] = useState('');
  const [fields, setFields] = useState<SchemaField[]>([]);
  const [usageFor, setUsageFor] = useState<Record<string, DataSchemaUsage[]>>({});
  const [usageProfilesFor, setUsageProfilesFor] = useState<Record<string, BundleProfileUse[]>>({});
  const [historySchema, setHistorySchema] = useState<DataSchema | null>(null);
  // The rail holds the read-only detail (everyone) or the editor
  // (writers); the list is full width while both are closed.
  const [detail, setDetail] = useState<DataSchema | null>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const railOpen = Boolean(detail) || editorOpen;

  // 6.6: JSON Schema (draft-07) subset import/export.
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const importRef = useDialogFocus<HTMLDivElement>(importOpen, () => setImportOpen(false));
  const importPreview = useMemo(() => {
    const trimmed = importText.trim();
    if (!trimmed) return null;
    let parsed: unknown;
    try {
      parsed = JSON.parse(trimmed);
    } catch (reason) {
      return { error: `Not valid JSON: ${reason instanceof Error ? reason.message : String(reason)}` };
    }
    return draft07ToLoom(parsed);
  }, [importText]);

  const exportSchema = (schema: DataSchema) => {
    const doc = loomToDraft07(schema);
    const blob = new Blob([JSON.stringify(doc, null, 2) + '\n'], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement('a');
    anchor.href = url;
    anchor.download = `loom-${schema.id}-v${schema.version}.schema.json`;
    // Headless browsers only fire the download for anchors attached to
    // the document.
    document.body.appendChild(anchor);
    anchor.click();
    anchor.remove();
    // Revoking immediately cancels the download the click just started
    // in some engines; give the fetch of the blob time to begin.
    window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
    onNotify?.(`Exported ${schema.name} as a draft-07 subset document.`);
  };

  const confirmImport = () => {
    if (!importPreview || 'error' in importPreview || !importPreview.schema.name.trim()) return;
    const { schema } = importPreview;
    setEditing(null);
    setName(schema.name.trim());
    setDescription(schema.description ?? '');
    setOwner(schema.owner ?? '');
    setTagsText((schema.tags ?? []).join(', '));
    setFields(schema.fields);
    setError('');
    setDetail(null);
    setEditorOpen(true);
    setImportOpen(false);
    setImportText('');
    const dropped = schema.unsupported.length;
    onNotify?.(
      dropped > 0
        ? `Imported ${countFields(schema.fields)} fields; ${dropped} subset-foreign item${dropped === 1 ? '' : 's'} named in the review.`
        : `Imported ${countFields(schema.fields)} fields.`,
    );
  };

  const loadSchemas = useCallback(
    (params: PageParams) => api.listDataSchemasPage(params),
    [],
  );
  const list = usePagedList<DataSchema>(loadSchemas);
  const filtersActive = list.query.trim() !== '';
  const refreshSchemas = () => {
    list.reload();
    onSchemasChanged?.();
  };

  const resetForm = () => {
    setEditing(null);
    setName('');
    setDescription('');
    setOwner('');
    setTagsText('');
    setFields([]);
    setError('');
    setEditorOpen(false);
  };

  const startCreate = () => {
    resetForm();
    setDetail(null);
    setEditorOpen(true);
  };

  const startEdit = (schema: DataSchema) => {
    setEditing(schema);
    setName(schema.name);
    setDescription(schema.description ?? '');
    setOwner(schema.owner ?? '');
    setTagsText((schema.tags ?? []).join(', '));
    setFields(schema.fields ? cloneFields(schema.fields) : []);
    setError('');
    setDetail(null);
    setEditorOpen(true);
  };

  const openDetail = (schema: DataSchema) => {
    setDetail(schema);
    setEditorOpen(false);
  };

  const save = async () => {
    setError('');
    if (!name.trim()) {
      setError('Name is required.');
      return;
    }
    const problems = validateSchemaFields(fields);
    if (problems.length > 0) {
      setError(problems[0]);
      return;
    }
    const tags = tagsText
      .split(',')
      .map((tag) => tag.trim())
      .filter(Boolean);
    setBusy(true);
    try {
      if (editing) {
        await api.updateDataSchema(editing.id, {
          name: name.trim(),
          description,
          fields,
          tags,
          owner: owner.trim() || undefined,
          version: editing.version,
        });
        onNotify?.(`Saved ${name.trim()} as v${editing.version + 1}.`);
      } else {
        await api.createDataSchema({
          name: name.trim(),
          description,
          fields,
          tags,
          owner: owner.trim() || undefined,
        });
        onNotify?.(`Created ${name.trim()}.`);
      }
      resetForm();
      refreshSchemas();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  const duplicate = async (schema: DataSchema) => {
    setError('');
    try {
      const copy = await api.duplicateDataSchema(schema.id);
      onNotify?.(`Duplicated ${schema.name} as ${copy.name}.`);
      refreshSchemas();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  };

  const share = async (schema: DataSchema) => {
    setError('');
    const shared = (schema.visibility ?? 'shared') === 'shared';
    try {
      await (shared ? api.unshareDataSchema(schema.id) : api.shareDataSchema(schema.id));
      onNotify?.(
        shared ? `${schema.name} is now private` : `${schema.name} is now visible to everyone`,
      );
      refreshSchemas();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  };

  const remove = async (schema: DataSchema) => {
    let result: Awaited<ReturnType<typeof api.dataSchemaUsage>>;
    try {
      result = await api.dataSchemaUsage(schema.id);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
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
      title: `Delete the data contract “${schema.name}”?`,
      body: inUse
        ? `${pins.length} published workflow version${pins.length === 1 ? '' : 's'} pin this contract and ${profiles.length} profile bundle${profiles.length === 1 ? '' : 's'} reference it. Deletion is blocked until they are removed.${names ? ` Bundle profiles: ${names}.` : ''}`
        : 'No published workflow or profile bundle references this contract. This cannot be undone.',
      confirmLabel: 'Delete contract',
      secondaryAction: inUse
        ? {
            label: 'View usage',
            run: () => {
              setUsageFor((current) => ({ ...current, [schema.id]: pins }));
              setUsageProfilesFor((current) => ({ ...current, [schema.id]: profiles }));
            },
          }
        : undefined,
      onConfirm: async () => {
        setError('');
        try {
          await api.deleteDataSchema(schema.id);
          onNotify?.('Data contract deleted.');
          if (editing?.id === schema.id) resetForm();
          refreshSchemas();
        } catch (reason) {
          return reason instanceof Error ? reason.message : String(reason);
        }
      },
    });
  };

  const toggleUsage = async (schema: DataSchema) => {
    if (usageFor[schema.id]) {
      const next = { ...usageFor };
      delete next[schema.id];
      setUsageFor(next);
      const nextProfiles = { ...usageProfilesFor };
      delete nextProfiles[schema.id];
      setUsageProfilesFor(nextProfiles);
      return;
    }
    try {
      const result = await api.dataSchemaUsage(schema.id);
      setUsageFor((current) => ({ ...current, [schema.id]: result.usage ?? [] }));
      setUsageProfilesFor((current) => ({ ...current, [schema.id]: result.profiles ?? [] }));
    } catch (reason) {
      onNotify?.(reason instanceof Error ? reason.message : 'Could not load usage', 'error');
    }
  };

  const clientProblems = fields.length > 0 ? validateSchemaFields(fields) : [];

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Data contracts</h1>
          <p className="lede">
            Reusable, versioned field lists describing the shape of data — webhook inputs,
            workflow inputs and outputs, node outputs. Contracts validate known fields; they
            never forbid extra ones.
          </p>
        </div>
        {canWrite && (
          <div className="page-head-actions">
            <button
              type="button"
              className="button subtle"
              onClick={() => {
                setImportText('');
                setImportOpen(true);
              }}
            >
              Import JSON Schema
            </button>
            <button type="button" className="button primary" onClick={startCreate}>
              New contract
            </button>
          </div>
        )}
      </header>

      <div className="page-toolbar">
        <ListSearch
          value={list.query}
          onChange={list.setQuery}
          placeholder="Search data contracts"
          label="Search data contracts"
          busy={list.loading || list.loadingMore}
          count={list.loaded ? list.count : undefined}
          testId="schemas-search"
        />
        <button className="button subtle" onClick={refreshSchemas} disabled={list.loading}>
          Refresh
        </button>
      </div>

      <div className="dashboard-grid schema-workspace" data-editor={railOpen ? 'open' : 'closed'}>
        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <h2>Contracts</h2>
            </div>
          </header>
          {list.loading && !list.loaded && (
            <AssetLoadingState label="Loading data contracts…" />
          )}
          {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
          {!list.failure &&
            list.loaded &&
            list.items.length === 0 &&
            (filtersActive ? (
              <AssetFilteredEmptyState
                title="No data contracts match"
                onClear={() => list.setQuery('')}
              >
                No data contract matches the current search.
              </AssetFilteredEmptyState>
            ) : (
              <AssetEmptyState
                testId="schemas-empty"
                title="No data contracts yet"
                action={
                  canWrite ? (
                    <button type="button" className="button primary" onClick={startCreate}>
                      Create the first contract
                    </button>
                  ) : undefined
                }
              >
                A contract describes the shape a payload must have — validators, subflows and the
                schema-aware webhook check it on every run.
              </AssetEmptyState>
            ))}
          {!list.failure && list.loaded && list.items.length > 0 && (
            <div className="dashboard-run-list" data-testid="schema-list">
              {list.items.map((schema) => (
                <AssetRow
                  key={schema.id}
                  identity={
                    <>
                      <strong>{schema.name}</strong>
                      <VisibilityChip
                        visibility={schema.visibility}
                        testId="schema-visibility-chip"
                      />
                    </>
                  }
                  description={schema.description}
                  meta={
                    <span className="meta-list meta-strict">
                      <span>{countFields(schema.fields ?? [])} fields</span>
                      <span>v{schema.version}</span>
                      <span>
                        {schema.usageCount ?? 0} published pin
                        {schema.usageCount === 1 ? '' : 's'}
                      </span>
                      {schema.owner ? <span title={schema.owner}>{schema.owner}</span> : null}
                      <span>updated {new Date(schema.updatedAt).toLocaleString()}</span>
                    </span>
                  }
                  tags={
                    (schema.tags ?? []).length > 0 ? (
                      <span className="profile-tags">
                        {(schema.tags ?? []).map((tag) => (
                          <em key={tag}>{tag}</em>
                        ))}
                      </span>
                    ) : undefined
                  }
                  actions={
                    <>
                      <button
                        type="button"
                        className="text-button"
                        onClick={() => openDetail(schema)}
                      >
                        View
                      </button>
                      {canWrite && (
                        <button
                          type="button"
                          className="text-button"
                          onClick={() => startEdit(schema)}
                        >
                          Edit
                        </button>
                      )}
                      <AssetRowMenu label={`More actions for ${schema.name}`}>
                        <button
                          type="button"
                          aria-label={`Version history of ${schema.name}`}
                          data-testid="asset-action-history"
                          onClick={() => setHistorySchema(schema)}
                        >
                          History
                        </button>
                        <button
                          type="button"
                          aria-label={`Export ${schema.name} as JSON Schema`}
                          data-testid="asset-action-export"
                          onClick={() => exportSchema(schema)}
                        >
                          Export
                        </button>
                        {canWrite && (
                          <VisibilityControl
                            visibility={schema.visibility}
                            name={schema.name}
                            labelPrefix="data contract "
                            onToggle={() => void share(schema)}
                          />
                        )}
                        {canWrite && (
                          <button
                            type="button"
                            aria-label={`Duplicate data contract ${schema.name}`}
                            data-testid="asset-action-duplicate"
                            onClick={() => void duplicate(schema)}
                          >
                            Duplicate
                          </button>
                        )}
                        {canWrite && (
                          <button
                            type="button"
                            className="danger"
                            aria-label={`Delete data contract ${schema.name}`}
                            data-testid="asset-action-delete"
                            onClick={() => void remove(schema)}
                          >
                            Delete
                          </button>
                        )}
                      </AssetRowMenu>
                    </>
                  }
                >
                  {usageFor[schema.id] && (
                    <div className="profile-usage" data-testid="schema-usage">
                      <UsagePanel
                        title="Used by workflows or profile bundles"
                        empty={
                          (usageFor[schema.id]?.length ?? 0) +
                            (usageProfilesFor[schema.id]?.length ?? 0) ===
                          0
                        }
                        emptyLabel="No published workflow or profile bundle references this contract yet."
                        onClose={() => void toggleUsage(schema)}
                        rows={
                          <>
                            {(usageFor[schema.id] ?? []).map((usage) => (
                              <span key={`${usage.workflowId}-v${usage.workflowVersion}`}>
                                {usage.workflowName || usage.workflowId} · v{usage.workflowVersion}{' '}
                                (pinned contract v{usage.schemaVersion})
                              </span>
                            ))}
                            {(usageProfilesFor[schema.id] ?? []).map((profile) => (
                              <span key={`profile-${profile.id}`}>
                                {profile.name} · {profile.nodeType} · profile v{profile.version}
                              </span>
                            ))}
                          </>
                        }
                      />
                    </div>
                  )}
                </AssetRow>
              ))}
            </div>
          )}
          {!list.failure && list.hasMore && (
            <div className="list-load-more">
              <button
                type="button"
                className="button"
                onClick={list.loadMore}
                disabled={list.loadingMore}
                data-testid="schemas-load-more"
              >
                {list.loadingMore ? 'Loading more…' : 'Load more'}
              </button>
            </div>
          )}
        </section>

        {detail && !editorOpen && (
          <SchemaDetailView
            schema={detail}
            canWrite={canWrite}
            onEdit={() => startEdit(detail)}
            onClose={() => setDetail(null)}
            onHistory={() => setHistorySchema(detail)}
          />
        )}
        {editorOpen && canWrite && (
          <AssetEditorPanel
            ariaLabel="Data contract editor"
          title={editing ? `Edit ${editing.name}` : 'Create a data contract'}
          subtitle={
            editing
              ? `Saving creates v${editing.version + 1}`
              : 'Reusable field lists for payload boundaries.'
          }
          busy={busy}
          dirty={Boolean(name.trim() || description || owner || tagsText || fields.length)}
          saveLabel={editing ? `Save as v${editing.version + 1}` : 'Save contract'}
          onCancel={resetForm}
          error={error || null}
          formId="schema-form"
        >
          <form
            id="schema-form"
            className="pad"
            onSubmit={(event) => {
              event.preventDefault();
              void save();
            }}
          >
            <label className="field" htmlFor="schema-name">
              <span className="field-label">Name</span>
              <input
                id="schema-name"
                className="text-input"
                value={name}
                onChange={(event) => setName(event.target.value)}
                required
              />
            </label>
            <label className="field" htmlFor="schema-description">
              <span className="field-label">Description (optional)</span>
              <input
                id="schema-description"
                className="text-input"
                value={description}
                onChange={(event) => setDescription(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="schema-owner">
              <span className="field-label">Owner (optional — team or person responsible)</span>
              <input
                id="schema-owner"
                className="text-input"
                placeholder="growth-team"
                value={owner}
                onChange={(event) => setOwner(event.target.value)}
              />
            </label>
            <label className="field" htmlFor="schema-tags">
              <span className="field-label">Tags (comma-separated, optional)</span>
              <input
                id="schema-tags"
                className="text-input"
                placeholder="webhook, leads"
                value={tagsText}
                onChange={(event) => setTagsText(event.target.value)}
              />
            </label>

            <div className="field">
              <span className="field-label">Fields</span>
              <div className="schema-field-header" aria-hidden="true">
                <span>Name</span>
                <span>Type</span>
                <span>Required</span>
                <span>Description (optional)</span>
              </div>
              <FieldRows fields={fields} onChange={setFields} depth={0} />
              {fields.length > 0 && fieldDepth(fields) > 6 && (
                <p className="field-hint">Nesting exceeds the maximum depth of 6.</p>
              )}
              {clientProblems.length > 0 && (
                <p className="field-hint" role="alert">
                  {clientProblems[0]}
                </p>
              )}
            </div>
          </form>
          {editing && <SchemaSampleTester schema={editing} />}
          </AssetEditorPanel>
        )}
      </div>

      <SchemaHistory schema={historySchema} onClose={() => setHistorySchema(null)} />
      <SchemaImportDialog
        open={importOpen}
        text={importText}
        preview={importPreview}
        focusRef={importRef}
        onTextChange={setImportText}
        onClose={() => setImportOpen(false)}
        onConfirm={confirmImport}
      />
    </main>
  );
}

/** Read-only field rows for the detail rail: one line per field with
 *  name, type and required, description underneath, nested groups
 *  indented one level. Depth is capped so malformed deep data cannot
 *  render an unbounded tree. */
const MAX_VIEW_DEPTH = 6;

function SchemaFieldTree({ fields, depth = 0 }: { fields: SchemaField[]; depth?: number }) {
  if (fields.length === 0) {
    return <p className="muted small">No fields.</p>;
  }
  if (depth >= MAX_VIEW_DEPTH) {
    return <p className="field-hint">Deeper levels are hidden.</p>;
  }
  return (
    <div className="schema-view" data-testid="schema-view-fields">
      {fields.map((field, index) => (
        <div className="schema-view-field" key={index}>
          <div className="schema-view-line">
            <code title={field.name}>{field.name}</code>
            <span className="muted small">{field.type}</span>
            {field.required ? <em className="schema-view-required">required</em> : null}
          </div>
          {field.description ? <p className="muted small">{field.description}</p> : null}
          {field.type === 'object' && (
            <div className="schema-view-children">
              <SchemaFieldTree fields={field.fields ?? []} depth={depth + 1} />
            </div>
          )}
          {field.type === 'array' && field.items && (
            <div className="schema-view-children">
              <p className="field-hint">Each element:</p>
              <SchemaFieldTree fields={[field.items]} depth={depth + 1} />
            </div>
          )}
        </div>
      ))}
    </div>
  );
}

/** The read-only contract detail rail, available to every signed-in
 *  user: identity and metadata, the complete field tree, the sample
 *  tester, usage and the version-history entry point. Writers continue
 *  into the editor from here. */
function SchemaDetailView({
  schema,
  canWrite,
  onEdit,
  onClose,
  onHistory,
}: {
  schema: DataSchema;
  canWrite: boolean;
  onEdit: () => void;
  onClose: () => void;
  onHistory: () => void;
}) {
  const [usage, setUsage] = useState<
    { pins: DataSchemaUsage[]; profiles: BundleProfileUse[] } | null
  >(null);
  // U6: a failed usage read is a named error with Retry, never "no usage".
  const [usageFailure, setUsageFailure] = useState<LoadFailure | null>(null);
  const [usageReload, setUsageReload] = useState(0);

  useEffect(() => {
    setUsage(null);
    setUsageFailure(null);
    let alive = true;
    api
      .dataSchemaUsage(schema.id)
      .then((result) => {
        if (alive) setUsage({ pins: result.usage ?? [], profiles: result.profiles ?? [] });
      })
      .catch((reason: unknown) => {
        if (alive) setUsageFailure(classifyCurrentLoadFailure(reason));
      });
    return () => {
      alive = false;
    };
  }, [schema.id, usageReload]);

  return (
    <section className="asset-editor" aria-label="Data contract detail" data-testid="schema-detail">
      <header className="asset-editor-head">
        <div>
          <h2>{schema.name}</h2>
          <p className="muted small">Data contract · v{schema.version}</p>
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
        {schema.description ? <p className="muted small">{schema.description}</p> : null}
        <p className="meta-list meta-strict">
          <span>v{schema.version}</span>
          <span>{countFields(schema.fields ?? [])} fields</span>
          {schema.owner ? <span title={schema.owner}>{schema.owner}</span> : null}
          <span>updated {new Date(schema.updatedAt).toLocaleString()}</span>
        </p>
        {(schema.tags ?? []).length > 0 ? (
          <span className="profile-tags">
            {(schema.tags ?? []).map((tag) => (
              <em key={tag}>{tag}</em>
            ))}
          </span>
        ) : null}

        <p className="field-label">Fields</p>
        <div className="schema-view-header" aria-hidden="true">
          <span>Name</span>
          <span>Type</span>
          <span>Required</span>
        </div>
        <SchemaFieldTree fields={schema.fields ?? []} />

        <p className="field-label">Usage</p>
        {usageFailure ? (
          <AssetErrorState
            failure={usageFailure}
            onRetry={() => setUsageReload((count) => count + 1)}
            testId="schema-usage-error"
          />
        ) : usage === null ? (
          <AssetLoadingState label="Loading usage…" className="muted small" />
        ) : usage.pins.length + usage.profiles.length === 0 ? (
          <p className="muted small">No published workflow or profile bundle references this contract yet.</p>
        ) : (
          <div className="profile-usage" data-testid="schema-detail-usage">
            {usage.pins.map((pin) => (
              <span key={`${pin.workflowId}-v${pin.workflowVersion}`}>
                {pin.workflowName || pin.workflowId} v{pin.workflowVersion} (pinned v
                {pin.schemaVersion})
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
          <button type="button" className="button subtle" onClick={onHistory}>
            View history
          </button>
        </div>

        <SchemaSampleTester schema={schema} />
      </div>
    </section>
  );
}
