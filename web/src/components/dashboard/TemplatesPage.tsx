import { useCallback, useState } from 'react';
import { api } from '../../api';
import { confirmAction } from '../../confirm';
import { usePagedList } from '../../usePagedList';
import type { PageParams } from '../../paging';
import type { Example, WorkflowSummary } from '../../types';
import { AssetErrorState, AssetFilteredEmptyState, AssetLoadingState } from '../assets/AssetState';
import { PromptDialog } from '../PromptDialog';
import { formatTime, triggerName } from './shared';

interface TemplatesProps {
  examples: Example[];
  onNew: () => void;
  onUseExample: (example: Example) => void;
  onOpenTemplate: (id: string) => void;
  onRenameTemplate: (id: string, name: string) => Promise<void>;
  onDeleteTemplate: (id: string) => Promise<void>;
}

export function TemplatesPage({
  examples,
  onNew,
  onUseExample,
  onOpenTemplate,
  onRenameTemplate,
  onDeleteTemplate,
}: TemplatesProps) {
  const [busy, setBusy] = useState('');
  // U3: rename lives in the shared PromptDialog instead of window.prompt.
  // The page owns the target, the in-flight state and the server error so
  // a failed rename stays inside the dialog for a retry or cancel.
  const [renameTarget, setRenameTarget] = useState<WorkflowSummary | null>(null);
  const [renameBusy, setRenameBusy] = useState(false);
  const [renameError, setRenameError] = useState('');
  const loadWorkflows = useCallback((params: PageParams) => api.listWorkflowsPage(params), []);
  const list = usePagedList<WorkflowSummary>(loadWorkflows);

  const closeRename = () => {
    if (renameBusy) return;
    setRenameTarget(null);
    setRenameError('');
  };

  const confirmRename = async (name: string) => {
    if (!renameTarget) return;
    setRenameBusy(true);
    setRenameError('');
    try {
      await onRenameTemplate(renameTarget.id, name);
      setRenameTarget(null);
      list.reload();
    } catch (reason) {
      setRenameError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setRenameBusy(false);
    }
  };

  const remove = async (summary: WorkflowSummary) => {
    await confirmAction({
      title: `Delete “${summary.name}”?`,
      body: 'The workflow is removed and run history is kept. Jobs that pin it block deletion until they are deleted.',
      confirmLabel: 'Delete workflow',
      onConfirm: async () => {
        setBusy(summary.id);
        try {
          await onDeleteTemplate(summary.id);
          list.reload();
        } catch (reason) {
          return reason instanceof Error ? reason.message : String(reason);
        } finally {
          setBusy('');
        }
      },
    });
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Workflows</h1>
          <p className="lede">A workflow is the reusable flow design. Publish it, then run jobs from it.</p>
        </div>
        <button className="button primary" onClick={onNew}>
          Blank workflow
        </button>
      </header>

      {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}

      <div className="page-toolbar">
        <label className="field inline-field" htmlFor="workflow-search">
          <span className="field-label">Search</span>
          <input
            id="workflow-search"
            className="text-input"
            type="search"
            placeholder="Filter workflows"
            value={list.query}
            onChange={(event) => list.setQuery(event.target.value)}
          />
        </label>
        <span className="muted small">
          {list.loaded
            ? `${list.count} workflow${list.count === 1 ? '' : 's'}`
            : 'Loading workflows…'}
        </span>
      </div>

      {list.loading && !list.loaded && <AssetLoadingState label="Loading workflows…" />}

      {list.loaded && list.count === 0 && list.query.trim() !== '' && !list.failure && (
        <AssetFilteredEmptyState title="No workflows match" onClear={() => list.setQuery('')}>
          Try a different name.
        </AssetFilteredEmptyState>
      )}

      {/* 6.5: first-run guidance — the empty workflow list offers both
          start paths (blank canvas and examples) instead of only the
          blank one. */}
      {list.loaded && list.count === 0 && list.query.trim() === '' && !list.failure && (
        <div className="empty-panel" data-testid="workflows-first-run">
          <h2>No workflows yet</h2>
          <p className="muted">
            A workflow is the reusable design; runs are its recordings. Start from a blank canvas
            or open a working example.
          </p>
          <button className="button primary" onClick={onNew}>
            Blank workflow
          </button>
          {examples.length > 0 && (
            <>
              <p className="field-label">Or start from an example</p>
              <div className="first-run-examples">
                {examples.map((example) => (
                  <button
                    key={example.id}
                    type="button"
                    className="button subtle"
                    onClick={() => onUseExample(example)}
                  >
                    {example.name}
                    <span className="muted small"> · {triggerName(example)}</span>
                  </button>
                ))}
              </div>
            </>
          )}
        </div>
      )}

      {list.items.length > 0 && (
        <div className="template-grid">
          {list.items.map((summary) => (
            <article className="template-card" key={summary.id}>
              <button
                className="template-card-open"
                onClick={() => onOpenTemplate(summary.id)}
                disabled={busy === summary.id}
              >
                <strong>{summary.name}</strong>
                <span className="template-meta meta-list">
                  <span>
                    {summary.publishedVersion ? `v${summary.publishedVersion}` : 'Draft'}
                    {summary.draft && summary.publishedVersion ? ' draft' : ''}
                  </span>
                  <span>Updated {formatTime(summary.updatedAt)}</span>
                </span>
                <span className="template-jobs meta-list">
                  <span>
                    {summary.jobCount ?? 0} job{summary.jobCount === 1 ? '' : 's'}
                  </span>
                  {summary.activeJobCount ? <span>{summary.activeJobCount} active</span> : null}
                </span>
                <code>{summary.id}</code>
              </button>
              <div className="template-card-actions">
                <button
                  type="button"
                  className="text-button"
                  onClick={() => setRenameTarget(summary)}
                  disabled={busy === summary.id}
                >
                  Rename
                </button>
                <button
                  className="text-button danger"
                  onClick={() => void remove(summary)}
                  disabled={busy === summary.id || (summary.jobCount ?? 0) > 0}
                  title={
                    (summary.jobCount ?? 0) > 0
                      ? 'Delete the jobs using this template first'
                      : 'Delete template'
                  }
                >
                  Delete
                </button>
              </div>
            </article>
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
            data-testid="workflows-load-more"
          >
            {list.loadingMore ? 'Loading more…' : 'Load more'}
          </button>
        </div>
      )}

      <PromptDialog
        open={renameTarget !== null}
        title="Rename workflow"
        label="Workflow name"
        initialValue={renameTarget?.name ?? ''}
        confirmLabel="Rename"
        busy={renameBusy}
        error={renameError}
        onCancel={closeRename}
        onConfirm={(name) => void confirmRename(name)}
      />
    </main>
  );
}
