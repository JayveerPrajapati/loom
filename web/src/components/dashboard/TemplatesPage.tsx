import { useMemo, useState } from 'react';
import type { WorkflowSummary } from '../../types';
import { formatTime } from './shared';

interface TemplatesProps {
  summaries: WorkflowSummary[];
  onNew: () => void;
  onOpenTemplate: (id: string) => void;
  onRenameTemplate: (id: string, name: string) => Promise<void>;
  onDeleteTemplate: (id: string) => Promise<void>;
}

export function TemplatesPage({
  summaries,
  onNew,
  onOpenTemplate,
  onRenameTemplate,
  onDeleteTemplate,
}: TemplatesProps) {
  const [query, setQuery] = useState('');
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return summaries;
    return summaries.filter((summary) => summary.name.toLowerCase().includes(needle));
  }, [query, summaries]);

  const rename = async (summary: WorkflowSummary) => {
    const name = window.prompt('Template name', summary.name)?.trim();
    if (!name || name === summary.name) return;
    setBusy(summary.id);
    setError('');
    try {
      await onRenameTemplate(summary.id, name);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  const remove = async (summary: WorkflowSummary) => {
    if (!window.confirm(`Delete “${summary.name}”? This cannot be undone.`)) return;
    setBusy(summary.id);
    setError('');
    try {
      await onDeleteTemplate(summary.id);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy('');
    }
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <p className="eyebrow">Library</p>
          <h1>Templates</h1>
          <p className="lede">A template is the reusable flow design. Publish it, then run jobs from it.</p>
        </div>
        <button className="button primary" onClick={onNew}>
          Blank workflow
        </button>
      </header>

      {error && <p className="banner error">{error}</p>}

      <div className="page-toolbar">
        <label className="field inline-field" htmlFor="template-search">
          <span className="field-label">Search</span>
          <input
            id="template-search"
            className="text-input"
            type="search"
            placeholder="Filter templates"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <span className="muted small">{filtered.length} templates</span>
      </div>

      {filtered.length === 0 && (
        <div className="empty-panel">
          <h2>{summaries.length === 0 ? 'No templates yet' : 'No matches'}</h2>
          <p className="muted">
            {summaries.length === 0
              ? 'Create a blank workflow or start from an example.'
              : 'Try a different name.'}
          </p>
          {summaries.length === 0 && (
            <button className="button primary" onClick={onNew}>
              Blank workflow
            </button>
          )}
        </div>
      )}

      {filtered.length > 0 && (
        <div className="template-grid">
          {filtered.map((summary) => (
            <article className="template-card" key={summary.id}>
              <button
                className="template-card-open"
                onClick={() => onOpenTemplate(summary.id)}
                disabled={busy === summary.id}
              >
                <span className="eyebrow">Template</span>
                <strong>{summary.name}</strong>
                <span className="template-meta">
                  {summary.publishedVersion ? `v${summary.publishedVersion}` : 'Draft'}
                  {summary.draft && summary.publishedVersion ? ' · draft' : ''} · updated{' '}
                  {formatTime(summary.updatedAt)}
                </span>
                <span className="template-jobs">
                  {summary.jobCount ?? 0} job{summary.jobCount === 1 ? '' : 's'}
                  {summary.activeJobCount ? ` · ${summary.activeJobCount} active` : ''}
                </span>
                <code>{summary.id}</code>
              </button>
              <div className="template-card-actions">
                <button
                  className="text-button"
                  onClick={() => void rename(summary)}
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
    </main>
  );
}
