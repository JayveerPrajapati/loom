import { ActionError } from './assets/ActionError';
import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { api } from '../api';
import { assetKeys, invalidateAssets } from '../assetCache';
import { confirmAction } from '../confirm';
import { usePagedList } from '../usePagedList';
import { usePrincipalRole } from '../principal';
import type { PageParams } from '../paging';
import type { CredentialInfo, CredentialUsage } from '../types';
import { AssetEditorPanel } from './assets/AssetEditorPanel';
import { AssetRow } from './assets/AssetRow';
import {
  AssetEmptyState,
  AssetErrorState,
  AssetFilteredEmptyState,
  AssetLoadingState,
} from './assets/AssetState';
import { ConfirmActionDialog } from './assets/ConfirmActionDialog';
import { ListSearch } from './assets/ListSearch';

/** One typed credential form: fields drive the JSON the server receives. */
type CredentialField = {
  key: string;
  label: string;
  required?: boolean;
  secret?: boolean;
  placeholder?: string;
  hint?: string;
  /** Comma-separated list field: stored as a JSON array. */
  list?: boolean;
};

type CredentialShape = {
  /** Credential type the server stores (http, postgres, …). */
  type: string;
  label: string;
  fields: CredentialField[];
};

/** Optional egress restriction shared by every http credential shape:
 *  comma-separated hosts the credential may call (empty = unrestricted). */
const RESTRICTION_FIELD: CredentialField[] = [
  {
    key: 'allowedHosts',
    label: 'Allowed hosts',
    list: true,
    placeholder: 'api.github.com, *.api.slack.com',
    hint: 'Optional egress restriction: comma-separated hosts this credential may call. Empty = unrestricted.',
  },
];

const HTTP_SHAPES: CredentialShape[] = [
  {
    type: 'http',
    label: 'Bearer token',
    fields: [
      { key: 'token', label: 'Token', required: true, secret: true, placeholder: 'paste-your-token' },
      ...RESTRICTION_FIELD,
    ],
  },
  {
    type: 'http',
    label: 'API key',
    fields: [
      { key: 'apiKey', label: 'API key', required: true, secret: true, placeholder: 'paste-your-api-key' },
      { key: 'apiKeyHeader', label: 'Header name', placeholder: 'x-api-key' },
      ...RESTRICTION_FIELD,
    ],
  },
  {
    type: 'http',
    label: 'Basic auth',
    fields: [
      { key: 'username', label: 'Username', required: true },
      { key: 'password', label: 'Password', required: true, secret: true },
      ...RESTRICTION_FIELD,
    ],
  },
  {
    type: 'http',
    label: 'OAuth2',
    fields: [
      { key: 'clientId', label: 'Client id', required: true },
      { key: 'clientSecret', label: 'Client secret', required: true, secret: true },
      { key: 'tokenUrl', label: 'Token URL', required: true, placeholder: 'https://provider.example.com/oauth/token' },
      { key: 'scope', label: 'Scope', placeholder: 'read write' },
      ...RESTRICTION_FIELD,
    ],
  },
];

const POSTGRES_SHAPES: CredentialShape[] = [
  {
    type: 'postgres',
    label: 'Connection string',
    fields: [
      {
        key: 'url',
        label: 'Connection string',
        required: true,
        secret: true,
        placeholder: 'postgres://user:password@host:5432/database?sslmode=require',
      },
    ],
  },
  {
    type: 'postgres',
    label: 'Structured',
    fields: [
      { key: 'host', label: 'Host', required: true, placeholder: 'localhost' },
      { key: 'port', label: 'Port', placeholder: '5432' },
      { key: 'user', label: 'User', required: true },
      { key: 'password', label: 'Password', required: true, secret: true },
      { key: 'database', label: 'Database', required: true },
      { key: 'sslmode', label: 'SSL mode', placeholder: 'require' },
    ],
  },
];

/** Shapes with typed forms; every other type falls back to raw JSON. */
const SHAPES: CredentialShape[] = [...HTTP_SHAPES, ...POSTGRES_SHAPES];

const OTHER_TYPES = [
  'slack',
  'sheets',
  'discord',
  'teams',
  'resend',
  'hubspot',
  'jira',
  'openai',
  'hmac',
];

/** Draft the advanced JSON form starts from (and resets to). */
const DEFAULT_JSON = '{\n  "headers": {\n    "Authorization": "Bearer …"\n  }\n}';

const JSON_HINTS: Record<string, { keys: string; example: Record<string, unknown> }> = {
  slack: {
    keys: 'webhookUrl (an incoming webhook) or token (a bot token)',
    example: { webhookUrl: 'https://hooks.slack.com/services/…' },
  },
};

export function CredentialsPage({
  onNotify,
}: {
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}) {
  const [shape, setShape] = useState<CredentialShape>(HTTP_SHAPES[0]);
  const [values, setValues] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [egressMode, setEgressMode] = useState<'allow' | 'deny' | null>(null);
  const [egressEntries, setEgressEntries] = useState(0);
  const [advanced, setAdvanced] = useState(false);
  const [data, setData] = useState(DEFAULT_JSON);
  const [freeType, setFreeType] = useState('');
  const jsonHint = JSON_HINTS[freeType.trim().toLowerCase()];
  // Honest status line: the typed form is dirty once any field has text,
  // the JSON form once its draft differs from the starting template.
  const dirty = advanced
    ? freeType.trim() !== '' || data !== DEFAULT_JSON
    : Object.values(values).some((value) => value.trim() !== '');
  const role = usePrincipalRole();
  const isAdmin = role === 'admin';
  const [rotating, setRotating] = useState<CredentialInfo | null>(null);
  const [rotateData, setRotateData] = useState('{}');
  const [rotateError, setRotateError] = useState('');
  const [rotateBusy, setRotateBusy] = useState(false);
  const [usageById, setUsageById] = useState<
    Record<string, { status: 'loading' | 'ready' | 'error'; usage?: CredentialUsage; error?: string }>
  >({});

  const loadUsage = useCallback(async (id: string) => {
    setUsageById((current) => ({ ...current, [id]: { status: 'loading' } }));
    try {
      const usage = await api.credentialUsage(id);
      setUsageById((current) => ({ ...current, [id]: { status: 'ready', usage } }));
    } catch (reason) {
      setUsageById((current) => ({
        ...current,
        [id]: { status: 'error', error: reason instanceof Error ? reason.message : String(reason) },
      }));
    }
  }, []);

  const loadCredentials = useCallback(
    (params: PageParams) => api.listCredentialsPage(params),
    [],
  );
  const list = usePagedList<CredentialInfo>(loadCredentials);

  useEffect(() => {
    // Surface the operator's global egress posture beside the credential
    // restrictions it complements (operational-trust campaign).
    api
      .healthz()
      .then((result) => {
        if (result.egress?.mode) {
          setEgressMode(result.egress.mode);
          setEgressEntries(result.egress.allowlist ?? 0);
        }
      })
      .catch(() => undefined);
  }, []);

  const pickShape = (next: CredentialShape) => {
    setShape(next);
    setValues({});
    setError('');
  };

  const createTyped = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    const missing = shape.fields.filter((field) => field.required && !values[field.key]?.trim());
    if (missing.length > 0) {
      setError(`Missing required field${missing.length > 1 ? 's' : ''}: ${missing.map((field) => field.label).join(', ')}.`);
      return;
    }
    const payload: Record<string, unknown> = {};
    for (const field of shape.fields) {
      const value = values[field.key]?.trim();
      if (!value) continue;
      if (field.list) {
        payload[field.key] = value
          .split(',')
          .map((entry) => entry.trim())
          .filter(Boolean);
      } else if (field.key === 'port' && /^\d+$/.test(value)) {
        payload[field.key] = Number(value);
      } else {
        payload[field.key] = value;
      }
    }
    setBusy(true);
    try {
      await api.createCredential({ type: shape.type, data: payload });
      onNotify?.('Credential saved.');
      invalidateAssets(assetKeys.credentials);
      setValues({});
      list.reload();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  const createJson = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    const type = freeType.trim();
    if (!type) {
      setError('Choose a credential type.');
      return;
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(data);
    } catch {
      setError('Credential data must be a valid JSON object.');
      return;
    }
    if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
      setError('Credential data must be a JSON object.');
      return;
    }
    setBusy(true);
    try {
      await api.createCredential({ type, data: parsed as Record<string, unknown> });
      onNotify?.('Credential saved.');
      invalidateAssets(assetKeys.credentials);
      setData('{}');
      list.reload();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (credential: CredentialInfo) => {
    if (credential.usageCount > 0) {
      onNotify?.(
        `Remove the ${credential.usageCount} reference${credential.usageCount === 1 ? '' : 's'} before deleting this credential.`,
        'error',
      );
      return;
    }
    await confirmAction({
      title: `Delete the ${credential.type} credential “${credential.id}”?`,
      body: 'No profile or workflow version references this credential. Deleting it cannot be undone.',
      confirmLabel: 'Delete credential',
      onConfirm: async () => {
        setError('');
        try {
          await api.deleteCredential(credential.id);
          onNotify?.('Credential deleted.');
          invalidateAssets(assetKeys.credentials);
          list.reload();
        } catch (reason) {
          return reason instanceof Error ? reason.message : String(reason);
        }
      },
    });
  };

  const openRotate = (credential: CredentialInfo) => {
    setRotating(credential);
    setRotateData('{}');
    setRotateError('');
  };

  const rotate = async () => {
    if (!rotating) return;
    let parsed: unknown;
    try {
      parsed = JSON.parse(rotateData);
    } catch {
      setRotateError('Enter a valid JSON object.');
      return;
    }
    if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
      setRotateError('Credential data must be a JSON object.');
      return;
    }
    const id = rotating.id;
    setRotateBusy(true);
    setRotateError('');
    try {
      await api.rotateCredential(id, parsed as Record<string, unknown>);
      onNotify?.('Credential rotated. Every reference now uses the new value.');
      invalidateAssets(assetKeys.credentials);
      setRotating(null);
      setUsageById((current) => {
        const next = { ...current };
        delete next[id];
        return next;
      });
      list.reload();
    } catch (reason) {
      setRotateError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setRotateBusy(false);
    }
  };

  if (!isAdmin) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1} data-testid="credentials-page">
        <header className="page-head">
          <div>
            <h1>Credentials</h1>
          </div>
        </header>
        <p className="banner notice">Credentials are admin-only.</p>
      </main>
    );
  }

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <h1>Credentials</h1>
          <p className="lede">
            Values are encrypted at rest and redacted server-side: no API response returns the
            secret. Nodes and profiles reference a credential by id, so rotating replaces the value
            everywhere it is used. Only admins can create or rotate credentials.
          </p>
        </div>
      </header>

      {egressMode === 'deny' && (
        <p className="banner notice" data-testid="egress-posture-banner">
          Server egress policy: deny — outbound destinations are restricted to{' '}
          {egressEntries} allowlisted destination{egressEntries === 1 ? '' : 's'}. Credential
          restrictions below apply on top of it.
        </p>
      )}

      <div className="dashboard-grid">
        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <h2>Credential ids</h2>
            </div>
            <button className="button subtle" onClick={list.reload} disabled={list.loading}>
              Refresh
            </button>
          </header>
          <div className="search-wrap">
            <ListSearch
              value={list.query}
              onChange={list.setQuery}
              placeholder="Search by id or type"
              label="Search credentials"
              busy={list.loading || list.loadingMore}
              count={list.loaded ? list.count : undefined}
              testId="credentials-search"
            />
          </div>
          {list.loading && !list.loaded && <AssetLoadingState label="Loading credentials…" />}
          {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
          {!list.failure &&
            list.loaded &&
            list.count === 0 &&
            (list.query.trim() ? (
              <AssetFilteredEmptyState
                title="No credentials match"
                onClear={() => list.setQuery('')}
              >
                No credential matches the current search.
              </AssetFilteredEmptyState>
            ) : (
              <AssetEmptyState
                title="No credentials yet"
                testId="credentials-empty"
                action={
                  <button
                    type="button"
                    className="button primary"
                    data-testid="credentials-empty-create"
                    onClick={() => {
                      // The typed form is always mounted in the right
                      // panel (below the list on narrow widths): bring
                      // it into view and put focus on its first field
                      // rather than navigating anywhere.
                      const shapeSelect = document.getElementById('credential-shape');
                      if (!shapeSelect) return;
                      shapeSelect.scrollIntoView({ block: 'center', behavior: 'smooth' });
                      shapeSelect.focus({ preventScroll: true });
                    }}
                  >
                    Create a credential
                  </button>
                }
              >
                Credentials are encrypted at rest and never returned by the API — nodes
                reference them by id.
              </AssetEmptyState>
            ))}
          {!list.failure && list.items.length > 0 && (
            <div className="dashboard-run-list">
              {list.items.map((credential) => {
                const usage = usageById[credential.id];
                return (
                  <AssetRow
                    key={credential.id}
                    testId={`credential-row-${credential.id}`}
                    identity={
                      <>
                        <strong>{credential.type}</strong> <code>{credential.id}</code>
                      </>
                    }
                    meta={
                      <>
                        <time dateTime={new Date(credential.createdAt).toISOString()}>
                          {new Date(credential.createdAt).toLocaleString()}
                        </time>{' '}
                        <span data-testid={`credential-usage-count-${credential.id}`}>
                          {credential.usageCount === 0
                            ? 'Not referenced'
                            : `${credential.usageCount} reference${credential.usageCount === 1 ? '' : 's'}`}
                        </span>
                      </>
                    }
                    actions={
                      <>
                        <button
                          type="button"
                          className="button subtle"
                          onClick={() => openRotate(credential)}
                          aria-label={`Rotate credential ${credential.id}`}
                        >
                          Rotate
                        </button>
                        <button
                          type="button"
                          className="button subtle"
                          onClick={() => void remove(credential)}
                          disabled={credential.usageCount > 0}
                          title={
                            credential.usageCount > 0
                              ? 'Remove every profile and workflow reference first'
                              : undefined
                          }
                          aria-label={`Delete credential ${credential.id}`}
                        >
                          Delete
                        </button>
                      </>
                    }
                  >
                    <details
                      className="credential-usage"
                      onToggle={(event) => {
                        if (event.currentTarget.open && !usageById[credential.id]) {
                          void loadUsage(credential.id);
                        }
                      }}
                    >
                      <summary>Where used</summary>
                      {usage?.status === 'loading' && (
                        <AssetLoadingState label="Loading references…" className="muted small" />
                      )}
                      {usage?.status === 'error' && (
                        <ActionError>{usage.error}</ActionError>
                      )}
                      {usage?.status === 'ready' &&
                        usage.usage &&
                        (usage.usage.profiles.length === 0 && usage.usage.workflows.length === 0 ? (
                          <p className="muted small">
                            No profiles or workflow versions reference this credential.
                          </p>
                        ) : (
                          <ul className="credential-usage-list">
                            {usage.usage.profiles.map((profile) => (
                              <li key={`p-${profile.profileId}`}>
                                <span>
                                  Profile <strong>{profile.name}</strong>{' '}
                                  <span className="muted small">
                                    ({profile.nodeType} v{profile.version})
                                  </span>
                                </span>
                                <span className="muted small">{profile.slots.join(', ')}</span>
                              </li>
                            ))}
                            {usage.usage.workflows.map((workflow) => (
                              <li
                                key={`w-${workflow.workflowId}-${workflow.version}-${workflow.current ? 'current' : 'published'}`}
                              >
                                <span>
                                  Workflow <strong>{workflow.name}</strong>{' '}
                                  <span className="muted small">
                                    {workflow.current ? 'current' : `v${workflow.version}`}
                                  </span>
                                </span>
                                <span className="muted small">{workflow.slots.join(', ')}</span>
                              </li>
                            ))}
                          </ul>
                        ))}
                    </details>
                  </AssetRow>
                );
              })}
            </div>
          )}
          {!list.failure && list.hasMore && (
            <div className="list-load-more">
              <button
                type="button"
                className="button"
                onClick={list.loadMore}
                disabled={list.loadingMore}
                data-testid="credentials-load-more"
              >
                {list.loadingMore ? 'Loading more…' : 'Load more'}
              </button>
            </div>
          )}
        </section>

        <AssetEditorPanel
          title="Add a credential"
          subtitle="Values are encrypted at rest and redacted server-side."
          testId="credential-editor"
          ariaLabel="Add a credential"
          formId={advanced ? 'credential-json-form' : 'credential-typed-form'}
          busy={busy}
          dirty={dirty}
          saveLabel="Save credential"
          savingLabel="Saving…"
          cancelLabel="Reset form"
          error={error}
          onCancel={() => {
            setError('');
            if (advanced) {
              setFreeType('');
              setData(DEFAULT_JSON);
            } else {
              setValues({});
            }
          }}
        >
          <div className="pad form-mode-toggle" role="group" aria-label="Credential input mode">
            <button
              type="button"
              className={`button subtle ${!advanced ? 'is-active' : ''}`}
              aria-pressed={!advanced}
              onClick={() => setAdvanced(false)}
            >
              Typed form
            </button>
            <button
              type="button"
              className={`button subtle ${advanced ? 'is-active' : ''}`}
              aria-pressed={advanced}
              onClick={() => setAdvanced(true)}
            >
              Advanced JSON
            </button>
          </div>

          {!advanced && (
            <form
              id="credential-typed-form"
              className="pad"
              onSubmit={(event) => void createTyped(event)}
            >
              <label className="field" htmlFor="credential-shape">
                <span className="field-label">Credential</span>
                <select
                  id="credential-shape"
                  className="text-input"
                  value={`${shape.type}:${shape.label}`}
                  onChange={(event) => {
                    const found = SHAPES.find(
                      (candidate) => `${candidate.type}:${candidate.label}` === event.target.value,
                    );
                    if (found) pickShape(found);
                  }}
                >
                  <optgroup label="HTTP">
                    {HTTP_SHAPES.map((option) => (
                      <option key={option.label} value={`http:${option.label}`}>
                        {option.label}
                      </option>
                    ))}
                  </optgroup>
                  <optgroup label="Postgres">
                    {POSTGRES_SHAPES.map((option) => (
                      <option key={option.label} value={`postgres:${option.label}`}>
                        {option.label}
                      </option>
                    ))}
                  </optgroup>
                </select>
              </label>
              {shape.fields.map((field) => (
                <label className="field" htmlFor={`cred-field-${field.key}`} key={field.key}>
                  <span className="field-label">
                    {field.label}
                    {field.required && <span className="required">*</span>}
                  </span>
                  <input
                    id={`cred-field-${field.key}`}
                    className="text-input"
                    type={field.secret ? 'password' : 'text'}
                    autoComplete="off"
                    placeholder={field.placeholder}
                    value={values[field.key] ?? ''}
                    onChange={(event) =>
                      setValues((current) => ({ ...current, [field.key]: event.target.value }))
                    }
                  />
                  {field.hint && <p className="field-hint">{field.hint}</p>}
                </label>
              ))}
            </form>
          )}

          {advanced && (
            <form
              id="credential-json-form"
              className="pad"
              onSubmit={(event) => void createJson(event)}
            >
              <label className="field" htmlFor="credential-type">
                <span className="field-label">Type</span>
                <input
                  id="credential-type"
                  className="text-input"
                  list="credential-types"
                  value={freeType}
                  onChange={(event) => setFreeType(event.target.value)}
                  required
                />
                <datalist id="credential-types">
                  <option value="http" />
                  <option value="postgres" />
                  {OTHER_TYPES.map((option) => (
                    <option key={option} value={option} />
                  ))}
                </datalist>
              </label>
              {jsonHint && (
                <div className="field-hint">
                  <span>{jsonHint.keys}</span>
                  <button
                    type="button"
                    className="button subtle"
                    onClick={() => setData(JSON.stringify(jsonHint.example, null, 2))}
                  >
                    Insert example
                  </button>
                </div>
              )}
              <label className="field" htmlFor="credential-data">
                <span className="field-label">Data (JSON)</span>
                <textarea
                  id="credential-data"
                  className="text-input code"
                  rows={9}
                  spellCheck={false}
                  value={data}
                  onChange={(event) => setData(event.target.value)}
                />
              </label>
            </form>
          )}
        </AssetEditorPanel>
      </div>

      {rotating && (
        <ConfirmActionDialog
          open
          title={`Rotate the ${rotating.type} credential “${rotating.id}”?`}
          body={
            <div>
              <p className="muted small">
                Replaces the stored value. The id, type and every profile or workflow reference stay
                the same. Enter the full credential object.
              </p>
              <label className="field" htmlFor="credential-rotate-data">
                <span className="field-label">New credential data (JSON)</span>
                <textarea
                  id="credential-rotate-data"
                  data-testid="credential-rotate-data"
                  className="text-input code"
                  rows={8}
                  spellCheck={false}
                  value={rotateData}
                  onChange={(event) => setRotateData(event.target.value)}
                />
              </label>
              <p className="field-hint">
                For example {'{"token":"…"}'} or {'{"url":"postgres://…"}'}.
              </p>
            </div>
          }
          confirmLabel="Rotate credential"
          tone="primary"
          busy={rotateBusy}
          error={rotateError}
          onCancel={() => {
            if (!rotateBusy) setRotating(null);
          }}
          onConfirm={() => void rotate()}
        />
      )}
    </main>
  );
}
