import { useCallback, useState } from 'react';
import { api, ApiError } from '../api';
import { usePagedList } from '../usePagedList';
import type { PageParams } from '../paging';
import type { Script, SubprocessNodes } from '../types';
import { PythonStatusCard } from './PythonStatusCard';
import { ScriptRowView } from './scripts/ScriptRowView';
import { useCanWrite } from '../principal';
import { AssetEditorPanel } from './assets/AssetEditorPanel';
import {
  AssetEmptyState,
  AssetErrorState,
  AssetFilteredEmptyState,
  AssetLoadingState,
} from './assets/AssetState';
import { ListSearch } from './assets/ListSearch';

// Scripts management: reusable JavaScript and Python sources
// referenced by code.run / code.python nodes and profile bundles. Same
// governance surfaces as mappings — versions, usage, delete guards —
// plus a live test panel (capability gated server-side: python tests
// answer 503 on sim hosts). The Python runtime status rides above the
// list as a compact strip.

/** One string for the whole form state; the editor's dirty flag is a
 *  comparison against the string captured when it opened. */
function formSnapshot(form: {
  name: string;
  language: string;
  description: string;
  owner: string;
  tags: string;
  source: string;
  requirements: string;
  timeoutSeconds: string;
  maxMemoryMB: string;
}): string {
  return JSON.stringify(form);
}

export function ScriptsPage({
  onNotify,
  onScriptsChanged,
  subprocess,
}: {
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onScriptsChanged?: () => void;
  subprocess?: SubprocessNodes | null;
}) {
  const canWrite = useCanWrite();
  const [languageFilter, setLanguageFilter] = useState('');
  const [editing, setEditing] = useState<Script | null>(null);
  const [name, setName] = useState('');
  const [language, setLanguage] = useState('javascript');
  const [description, setDescription] = useState('');
  const [owner, setOwner] = useState('');
  const [tags, setTags] = useState('');
  const [source, setSource] = useState('');
  const [requirements, setRequirements] = useState('');
  const [timeoutSeconds, setTimeoutSeconds] = useState('');
  const [maxMemoryMB, setMaxMemoryMB] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  // Snapshot of the form as it was when the editor opened; the dirty
  // flag is a comparison against it, so an untouched edit is clean.
  const [snapshot, setSnapshot] = useState('');
  // The editor mounts only on create/edit; Cancel closes it so the
  // list gets the full workspace width between edits.
  const [editorOpen, setEditorOpen] = useState(false);

  const loadScripts = useCallback(
    (params: PageParams) => api.listScriptsPage(languageFilter || undefined, params),
    [languageFilter],
  );
  const list = usePagedList<Script>(loadScripts);
  const filtersActive = Boolean(languageFilter || list.query.trim());
  const refreshRows = () => {
    list.reload();
    onScriptsChanged?.();
  };

  const startCreate = () => {
    setEditing(null);
    setName('');
    setLanguage(languageFilter || 'javascript');
    setDescription('');
    setOwner('');
    setTags('');
    setSource('');
    setRequirements('');
    setTimeoutSeconds('');
    setMaxMemoryMB('');
    setError('');
    setSnapshot(formSnapshot({
      name: '',
      language: languageFilter || 'javascript',
      description: '',
      owner: '',
      tags: '',
      source: '',
      requirements: '',
      timeoutSeconds: '',
      maxMemoryMB: '',
    }));
    setEditorOpen(true);
  };
  const startEdit = (script: Script) => {
    setEditing(script);
    setName(script.name);
    setLanguage(script.language);
    setDescription(script.description ?? '');
    setOwner(script.owner ?? '');
    setTags((script.tags ?? []).join(', '));
    setSource(script.source);
    setRequirements((script.requirements ?? []).join(', '));
    setTimeoutSeconds(script.timeoutSeconds ? String(script.timeoutSeconds) : '');
    setMaxMemoryMB(script.maxMemoryMB ? String(script.maxMemoryMB) : '');
    setError('');
    setSnapshot(
      formSnapshot({
        name: script.name,
        language: script.language,
        description: script.description ?? '',
        owner: script.owner ?? '',
        tags: (script.tags ?? []).join(', '),
        source: script.source,
        requirements: (script.requirements ?? []).join(', '),
        timeoutSeconds: script.timeoutSeconds ? String(script.timeoutSeconds) : '',
        maxMemoryMB: script.maxMemoryMB ? String(script.maxMemoryMB) : '',
      }),
    );
    setEditorOpen(true);
  };

  // The dirty flag compares the live form against the snapshot taken
  // when the editor opened — an untouched edit is not dirty.
  const dirty = editing
    ? formSnapshot({
        name,
        language,
        description,
        owner,
        tags,
        source,
        requirements,
        timeoutSeconds,
        maxMemoryMB,
      }) !== snapshot
    : true;

  const submit = () => {
    setError('');
    // The form opts out of native validation so the editor's error slot
    // carries the message instead of a browser bubble; an empty save
    // must never reach the server.
    if (!name.trim()) {
      setError('Name is required.');
      return;
    }
    if (!source.trim()) {
      setError('Source is required.');
      return;
    }
    setBusy(true);
    const reqs = requirements
      .split(',')
      .map((entry) => entry.trim())
      .filter(Boolean);
    const payload = {
      name,
      description: description || undefined,
      source,
      requirements: language === 'python' && reqs.length > 0 ? reqs : undefined,
      timeoutSeconds: timeoutSeconds ? Number(timeoutSeconds) : undefined,
      maxMemoryMB: language === 'python' && maxMemoryMB ? Number(maxMemoryMB) : undefined,
      owner: owner || undefined,
      tags: tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
    };
    const done = () => {
      setBusy(false);
      refreshRows();
      setEditorOpen(false);
    };
    if (editing) {
      api
        .updateScript(editing.id, { ...payload, version: editing.version })
        .then(() => {
          onNotify?.(`Saved ${name} as v${editing.version + 1}.`);
          done();
        })
        .catch((error: ApiError) => {
          setBusy(false);
          onNotify?.(error.message, 'error');
        });
    } else {
      api
        .createScript({ ...payload, language })
        .then(() => {
          onNotify?.(`Created ${name}.`);
          done();
        })
        .catch((error: ApiError) => {
          setBusy(false);
          onNotify?.(error.message, 'error');
        });
    }
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Scripts</h1>
          <p className="page-sub">
            Reusable JavaScript and Python sources for code nodes. Publish inlines the pinned
            version; a node&rsquo;s own diverging source still wins.
          </p>
        </div>
        {canWrite && (
          <button type="button" className="button primary" onClick={startCreate}>
            New script
          </button>
        )}
      </header>
      <div className="page-toolbar">
        <ListSearch
          value={list.query}
          onChange={list.setQuery}
          placeholder="Search scripts"
          label="Search scripts"
          busy={list.loading || list.loadingMore}
          count={list.loaded ? list.count : undefined}
          testId="scripts-search"
        />
        <label className="field inline-field" htmlFor="script-language-filter">
          <span className="visually-hidden">Filter by language</span>
          <select
            id="script-language-filter"
            className="text-input"
            data-testid="script-language-filter"
            value={languageFilter}
            onChange={(event) => setLanguageFilter(event.target.value)}
          >
            <option value="">All languages</option>
            <option value="javascript">javascript</option>
            <option value="python">python</option>
          </select>
        </label>
      </div>
      <PythonStatusCard subprocess={subprocess ?? null} />
      <div className="dashboard-grid script-workspace" data-editor={editorOpen ? 'open' : 'closed'}>
        <section className="dashboard-panel" aria-label="Scripts list">
          <div className="panel-head">
            <h2>All scripts</h2>
            {!list.loading && !list.failure && <span className="count-pill">{list.count}</span>}
          </div>
          {list.loading && !list.loaded && <AssetLoadingState label="Loading scripts…" />}
          {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
          {!list.failure &&
            list.loaded &&
            list.items.length === 0 &&
            (filtersActive ? (
              <AssetFilteredEmptyState
                title="No scripts match"
                onClear={() => {
                  setLanguageFilter('');
                  list.setQuery('');
                }}
              >
                No script matches the current language filter or search.
              </AssetFilteredEmptyState>
            ) : (
              <AssetEmptyState
                testId="scripts-empty"
                title="No scripts yet"
                action={
                  canWrite ? (
                    <button
                      type="button"
                      className="button primary"
                      onClick={startCreate}
                    >
                      Create the first script
                    </button>
                  ) : undefined
                }
              >
                Scripts hold the code code.run and code.python nodes execute — versioned, testable
                and reusable across workflows.
              </AssetEmptyState>
            ))}
          {!list.failure &&
            list.loaded &&
            list.items.map((script) => (
              <ScriptRowView
                key={script.id}
                script={script}
                onEdit={() => startEdit(script)}
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
                data-testid="scripts-load-more"
              >
                {list.loadingMore ? 'Loading more…' : 'Load more'}
              </button>
            </div>
          )}
        </section>
        {canWrite && editorOpen && (
          <AssetEditorPanel
            ariaLabel="Script editor"
            title={editing ? `Edit ${editing.name}` : 'New script'}
            subtitle={
              editing
                ? `Saving creates v${editing.version + 1}`
                : 'Reusable source for code.run and code.python nodes.'
            }
            busy={busy}
            dirty={dirty}
            saveLabel={editing ? `Save as v${editing.version + 1}` : 'Create script'}
            saveDisabled={Boolean(editing && !dirty)}
            error={error || null}
            formId="script-form"
            onCancel={() => setEditorOpen(false)}
          >
          <form
            id="script-form"
            noValidate
            onSubmit={(event) => {
              event.preventDefault();
              submit();
            }}
            className="hub-form"
          >
            <fieldset className="hub-form-section">
              <legend className="field-label">Basics</legend>
              <label className="field" htmlFor="script-name">
                <span className="field-label">Name</span>
                <input
                  id="script-name"
                  className="text-input"
                  value={name}
                  onChange={(event) => setName(event.target.value)}
                  required
                />
              </label>
              <label className="field" htmlFor="script-description">
                <span className="field-label">Description</span>
                <input
                  id="script-description"
                  className="text-input"
                  value={description}
                  onChange={(event) => setDescription(event.target.value)}
                />
              </label>
              <label className="field" htmlFor="script-language">
                <span className="field-label">Language</span>
                <select
                  id="script-language"
                  className="text-input"
                  value={language}
                  disabled={!!editing}
                  onChange={(event) => setLanguage(event.target.value)}
                >
                  <option value="javascript">javascript</option>
                  <option value="python">python</option>
                </select>
                {editing && (
                  <span className="field-hint">Language is fixed after create.</span>
                )}
              </label>
              <label className="field" htmlFor="script-owner">
                <span className="field-label">Owner</span>
                <input
                  id="script-owner"
                  className="text-input"
                  value={owner}
                  onChange={(event) => setOwner(event.target.value)}
                />
              </label>
              <label className="field" htmlFor="script-tags">
                <span className="field-label">Tags (comma-separated)</span>
                <input
                  id="script-tags"
                  className="text-input"
                  value={tags}
                  onChange={(event) => setTags(event.target.value)}
                />
              </label>
            </fieldset>
            <fieldset className="hub-form-section">
              <legend className="field-label">Source</legend>
              <label className="field" htmlFor="script-source">
                <span className="field-label">Source</span>
                <textarea
                  id="script-source"
                  className="text-input code"
                  rows={10}
                  spellCheck={false}
                  value={source}
                  onChange={(event) => setSource(event.target.value)}
                  required
                />
              </label>
              {language === 'python' && (
                <label className="field" htmlFor="script-requirements">
                  <span className="field-label">Requirements (comma-separated, pinned)</span>
                  <input
                    id="script-requirements"
                    className="text-input"
                    placeholder="idna==3.10, certifi==2025.1.1"
                    value={requirements}
                    onChange={(event) => setRequirements(event.target.value)}
                  />
                </label>
              )}
            </fieldset>
            <fieldset className="hub-form-section">
              <legend className="field-label">Runtime</legend>
              <label className="field" htmlFor="script-timeout">
                <span className="field-label">
                  Timeout (seconds, max {language === 'python' ? 900 : 300})
                </span>
                <input
                  id="script-timeout"
                  className="text-input"
                  type="number"
                  min={1}
                  max={language === 'python' ? 900 : 300}
                  value={timeoutSeconds}
                  onChange={(event) => setTimeoutSeconds(event.target.value)}
                />
              </label>
              {language === 'python' && (
                <label className="field" htmlFor="script-max-memory">
                  <span className="field-label">Memory limit (MB)</span>
                  <input
                    id="script-max-memory"
                    className="text-input"
                    type="number"
                    min={16}
                    max={8192}
                    value={maxMemoryMB}
                    onChange={(event) => setMaxMemoryMB(event.target.value)}
                  />
                </label>
              )}
            </fieldset>
          </form>
          </AssetEditorPanel>
        )}
      </div>
    </main>
  );
}
