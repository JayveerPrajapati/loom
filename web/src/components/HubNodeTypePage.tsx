import { ActionError } from './assets/ActionError';
import { useCallback, useEffect, useState } from 'react';
import { api } from '../api';
import { confirmAction } from '../confirm';
import { usePagedList } from '../usePagedList';
import type { PageParams } from '../paging';
import type { ConfigProfile, ConfigProfileUsage, NodeDef } from '../types';
import { AssetErrorState, AssetFilteredEmptyState, AssetLoadingState } from './assets/AssetState';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import { useCanWrite } from '../principal';
import { ListSearch } from './assets/ListSearch';
import { ProfileHistory } from './ProfileHistory';

// Configuration Hub v4.1 (E1+E2): ONE merged page per node type at
// /hub/:nodeType. The profiles section is the working surface — the v4
// dedicated list with visible Edit and the kebab action menu (Test,
// History, Usage, Duplicate, Delete) — while mappings and data contracts
// are compact count rows linking to their own pages
// (/mappings?nodeType=…, /schemas). The pre-merge /profiles/:nodeType
// URLs redirect here via the legacy flag in parseRoute.

/** Profile types the server can test end to end (ProfileTester). */
const TESTABLE_TYPES = ['http.request', 'db.postgres'];

interface HubPageData {
  nodeType: string;
  mappingKinds: string[];
  profiles: ConfigProfile[];
  mappings: { id: string; name: string }[];
  schemas: { id: string; name: string; version: number }[];
  schemaSlot: 'input' | 'output';
}

export function HubNodeTypePage({
  nodeType,
  def,
  onNavigate,
  onNotify,
  onProfilesChanged,
}: {
  nodeType: string;
  def: NodeDef | undefined;
  onNavigate: (path: string) => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onProfilesChanged?: () => void;
}) {
  const [data, setData] = useState<HubPageData | null>(null);
  const [loadFailure, setLoadFailure] = useState<LoadFailure | null>(null);
  const canWrite = useCanWrite();
  const [error, setError] = useState('');
  const [testResult, setTestResult] = useState('');
  const [testOk, setTestOk] = useState(false);
  const [testingId, setTestingId] = useState('');
  const [usageFor, setUsageFor] = useState<Record<string, ConfigProfileUsage[]>>({});
  const [historyProfile, setHistoryProfile] = useState<ConfigProfile | null>(null);

  const typePath = `/hub/${encodeURIComponent(nodeType)}`;

  const load = () => {
    setLoadFailure(null);
    api
      .hubNodeType(nodeType, false)
      .then((result) => setData(result))
      .catch((reason: unknown) => setLoadFailure(classifyCurrentLoadFailure(reason)));
  };
  const loadProfiles = useCallback(
    (params: PageParams) => api.listConfigProfilesPage(nodeType, params),
    [nodeType],
  );
  const list = usePagedList<ConfigProfile>(loadProfiles);
  const filtersActive = list.query.trim() !== '';
  const refreshAll = () => {
    load();
    list.reload();
    onProfilesChanged?.();
  };
  useEffect(() => {
    setData(null);
    setLoadFailure(null);
    setError('');
    list.setQuery('');
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nodeType]);

  if (!def) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <div className="empty-panel">
          <h2>Unknown node type</h2>
          <p className="muted">
            Nothing called <code>{nodeType}</code> is in the catalog.
          </p>
          <button className="button primary" onClick={() => onNavigate('/profiles')}>
            Back to Profiles
          </button>
        </div>
      </main>
    );
  }

  const profileable = Object.values(def.config ?? {}).some((field) => field.profileable);
  const testable = TESTABLE_TYPES.includes(nodeType);
  const mappingKinds: string[] = data?.mappingKinds ?? [];
  const mappingCount = data?.mappings.length ?? 0;
  const schemaCount = data?.schemas.length ?? 0;

  const toggleUsage = async (profile: ConfigProfile) => {
    if (usageFor[profile.id]) {
      const next = { ...usageFor };
      delete next[profile.id];
      setUsageFor(next);
      return;
    }
    try {
      const result = await api.configProfileUsage(profile.id);
      setUsageFor((current) => ({ ...current, [profile.id]: result.usage ?? [] }));
    } catch (reason) {
      onNotify?.(reason instanceof Error ? reason.message : 'Could not load usage', 'error');
    }
  };

  const test = async (profile: ConfigProfile) => {
    setTestingId(profile.id);
    setTestResult('');
    setTestOk(false);
    try {
      const result = await api.testConfigProfile(profile.id);
      // D2-7: the summary folds connectivity (reachable / failed /
      // skipped) together with the bundle resolution counts.
      const parts: string[] = [];
      if (result.ok) {
        const latency = typeof result.latencyMs === 'number' ? ` in ${result.latencyMs} ms` : '';
        const status = typeof result.status === 'number' ? ` — HTTP ${result.status}` : '';
        parts.push(`reachable${status}${latency}`);
      } else if (result.error) {
        parts.push(result.error);
      } else if (result.skipped) {
        parts.push(`connectivity skipped: ${result.skipped}`);
      }
      const bundle = result.bundle;
      if (bundle) {
        const counts: string[] = [];
        for (const mapping of bundle.mappings ?? []) {
          counts.push(`${mapping.kind} mapping ${mapping.name} (v${mapping.version}, ${mapping.rows} rows)`);
        }
        for (const schema of bundle.schemas ?? []) {
          counts.push(`${schema.slot} contract ${schema.name} (v${schema.version}, ${schema.fields} fields)`);
        }
        for (const rows of bundle.paramRows ?? []) {
          counts.push(`${rows.group}: ${rows.rows} rows compile`);
        }
        if (counts.length > 0) parts.push(`bundle — ${counts.join(', ')}`);
        for (const issue of bundle.issues ?? []) {
          parts.push(`${issue.name}: ${issue.message}`);
        }
      }
      const ok = result.ok || (Boolean(result.skipped) && result.bundleOk !== false);
      setTestOk(ok);
      setTestResult(
        `${profile.name}: ${parts.join('; ') || (ok ? 'nothing to test' : 'test failed')}`,
      );
    } catch (reason) {
      setTestOk(false);
      setTestResult(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setTestingId('');
    }
  };

  const duplicate = async (profile: ConfigProfile) => {
    setError('');
    try {
      const copy = await api.duplicateConfigProfile(profile.id);
      onNotify?.(`Duplicated ${profile.name} as ${copy.name}.`);
      refreshAll();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  };

  const remove = async (profile: ConfigProfile) => {
    await confirmAction({
      title: `Delete ${profile.name}?`,
      body: `The profile is removed for good. Published workflow versions that already resolved it keep working, but ${profile.usageCount ? `${profile.usageCount} workflow(s) pin it and will block deletion until republished` : 'nothing pins it'}.`,
      confirmLabel: 'Delete profile',
      secondaryAction: profile.usageCount
        ? { label: 'View usage', run: () => void toggleUsage(profile) }
        : undefined,
      onConfirm: async () => {
        setError('');
        try {
          await api.deleteConfigProfile(profile.id);
          onNotify?.('Profile deleted.');
          refreshAll();
        } catch (reason) {
          return reason instanceof Error ? reason.message : String(reason);
        }
      },
    });
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <p className="breadcrumb">
            <button className="text-button" onClick={() => onNavigate('/profiles')}>
              Profiles
            </button>{' '}
            / {def.category}
          </p>
          <h1>{def.displayName || nodeType}</h1>
          <p className="page-sub">{def.description}</p>
        </div>
        <span className="hub-head-actions">
          <code className="hub-type-id">{def.type}</code>
          {profileable && canWrite && (
            <button
              className="button primary"
              data-testid="new-profile"
              onClick={() => onNavigate(`${typePath}/new`)}
            >
              New profile
            </button>
          )}
        </span>
      </header>

      {loadFailure && (
        <div data-testid="hub-load-error">
          <AssetErrorState failure={loadFailure} onRetry={load} />
        </div>
      )}
      {error && (
        <ActionError>{error}</ActionError>
      )}
      {testResult && (
        <p
          className={`banner ${testOk ? 'notice' : 'error'}`}
          role="status"
          data-testid="profile-test-result"
        >
          {testResult}
        </p>
      )}

      <div className="dashboard-grid">
        <section
          className="dashboard-panel hub-profiles-panel"
          aria-label={`Profiles for ${def.displayName || nodeType}`}
        >
          <header className="dashboard-panel-head">
            <div>
              <h2>Profiles</h2>
            </div>
            <span className="hub-head-actions">
              {list.loaded && <span className="count-pill">{list.count}</span>}
              <button
                className="button subtle"
                onClick={refreshAll}
                disabled={list.loading || (!data && !loadFailure)}
              >
                Refresh
              </button>
            </span>
          </header>
          <div className="search-wrap">
            <ListSearch
              value={list.query}
              onChange={list.setQuery}
              placeholder="Search profiles"
              label="Search profiles"
              busy={list.loading || list.loadingMore}
              count={list.loaded ? list.count : undefined}
              testId="hub-profiles-search"
            />
          </div>
          {list.loading && !list.loaded && <AssetLoadingState label="Loading profiles…" />}
          {list.failure && <AssetErrorState failure={list.failure} onRetry={list.reload} />}
          {list.loaded &&
            list.items.length === 0 &&
            (filtersActive ? (
              <AssetFilteredEmptyState
                title="No profiles match"
                onClear={() => list.setQuery('')}
              >
                No profile matches the current search.
              </AssetFilteredEmptyState>
            ) : (
              <div className="empty-panel pad">
                <h3>No {def.displayName || nodeType} profiles yet</h3>
                <p className="muted">
                  {profileable ? (
                    <>
                      A profile holds the non-secret configuration every{' '}
                      {def.displayName || nodeType} node can inherit: nodes keep their own values,
                      the profile fills the rest.
                    </>
                  ) : (
                    <>This type has no profileable fields, so it cannot carry profiles.</>
                  )}
                </p>
                {profileable && (
                  <button
                    className="button primary"
                    onClick={() => onNavigate(`${typePath}/new`)}
                  >
                    Create the first profile
                  </button>
                )}
              </div>
            ))}
          {list.loaded && list.items.length > 0 && (
            <div className="dashboard-run-list hub-profile-list" data-testid="profile-list">
              {list.items.map((profile) => (
                <div className="dashboard-run-row" key={profile.id}>
                  <span className="dashboard-run-main">
                    {/* The name stretches over the row: clicking anywhere
                       on the row opens the read-only view. */}
                    <button
                      className="text-button hub-row-link"
                      data-testid={`view-profile-${profile.name}`}
                      onClick={() =>
                        onNavigate(`${typePath}/${encodeURIComponent(profile.id)}`)
                      }
                    >
                      <strong>{profile.name}</strong>
                    </button>
                    <span className="meta-list meta-strict">
                      <span>v{profile.version}</span>
                      {profile.usageCount ? <span>used by {profile.usageCount}</span> : null}
                      {profile.owner ? <span title={profile.owner}>{profile.owner}</span> : null}
                    </span>
                    {profile.description ? (
                      <span className="muted small">{profile.description}</span>
                    ) : null}
                    {(profile.tags ?? []).length > 0 && (
                      <span className="profile-tags" data-testid="profile-tags">
                        {(profile.tags ?? []).map((tag) => (
                          <em key={tag}>{tag}</em>
                        ))}
                      </span>
                    )}
                  </span>
                  <span className="dashboard-run-meta">
                    <em>{new Date(profile.updatedAt).toLocaleString()}</em>
                    {canWrite && (
                      <button
                        className="button subtle"
                        data-testid={`edit-profile-${profile.name}`}
                        onClick={() =>
                          onNavigate(`${typePath}/${encodeURIComponent(profile.id)}/edit`)
                        }
                      >
                        Edit
                      </button>
                    )}
                    <details className="action-menu profile-row-actions">
                      <summary className="button subtle" aria-label={`More actions for ${profile.name}`}>
                        ⋯
                      </summary>
                      <div className="action-menu-list">
                        {testable && canWrite && (
                          <button
                            onClick={() => void test(profile)}
                            disabled={testingId === profile.id}
                          >
                            {testingId === profile.id ? 'Testing…' : 'Test'}
                          </button>
                        )}
                        <button onClick={() => setHistoryProfile(profile)}>History</button>
                        {profile.usageCount ? (
                          <button onClick={() => void toggleUsage(profile)}>
                            {usageFor[profile.id] ? 'Hide usage' : 'Usage'}
                          </button>
                        ) : null}
                        {canWrite && (
                          <button onClick={() => void duplicate(profile)}>Duplicate</button>
                        )}
                        {canWrite && (
                          <button className="danger" onClick={() => void remove(profile)}>
                            Delete
                          </button>
                        )}
                      </div>
                    </details>
                  </span>
                </div>
              ))}
            </div>
          )}
          {list.items
            .filter((profile) => usageFor[profile.id])
            .map((profile) => (
                <div
                  className="profile-usage"
                  key={`usage-${profile.id}`}
                  data-testid="profile-usage"
                >
                  <p className="field-label">Used by</p>
                  {(usageFor[profile.id] ?? []).map((usage) => (
                    <span key={`${usage.workflowId}-v${usage.workflowVersion}`}>
                      {usage.workflowName || usage.workflowId} · v{usage.workflowVersion} (pinned
                      profile v{usage.profileVersion})
                    </span>
                  ))}
                </div>
              ))}
          {!list.failure && list.hasMore && (
            <div className="list-load-more">
              <button
                type="button"
                className="button"
                onClick={list.loadMore}
                disabled={list.loadingMore}
                data-testid="hub-profiles-load-more"
              >
                {list.loadingMore ? 'Loading more…' : 'Load more'}
              </button>
            </div>
          )}
        </section>

        <section className="dashboard-panel" aria-label="Related assets for this node type">
          <header className="dashboard-panel-head">
            <div>
              <h2>Related assets</h2>
            </div>
          </header>
          <div className="pad">
            {(data && mappingKinds.length > 0) && (
              <div className="entity-line">
                <strong>
                  {mappingCount} mapping{mappingCount === 1 ? '' : 's'}
                </strong>
                <span className="muted small">
                  of the kinds this type consumes ({mappingKinds.join(', ')})
                </span>
                <button
                  type="button"
                  className="text-button"
                  data-testid="hub-manage-mappings"
                  onClick={() => onNavigate(`/mappings?nodeType=${encodeURIComponent(def.type)}`)}
                >
                  Manage mappings
                </button>
              </div>
            )}
            <div className="entity-line">
              <strong>
                {schemaCount} data contract{schemaCount === 1 ? '' : 's'}
              </strong>
              <span className="muted small">
                {data?.schemaSlot === 'input'
                  ? 'Inbound payloads on this trigger are validated against the attached input contract.'
                  : 'The output contract is checked against this node’s main output and recorded on its finished event.'}
              </span>
              <button
                type="button"
                className="text-button"
                data-testid="hub-manage-contracts"
                onClick={() => onNavigate('/schemas')}
              >
                Manage contracts
              </button>
            </div>
          </div>
        </section>
      </div>

      <ProfileHistory profile={historyProfile} onClose={() => setHistoryProfile(null)} />
    </main>
  );
}
