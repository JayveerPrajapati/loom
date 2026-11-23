import { ActionError } from './assets/ActionError';
import { useCallback, useEffect, useRef, useState } from 'react';

import { api } from '../api';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import type {
  ConfigProfile,
  ConfigProfileUsage,
  DataSchemaUsage,
  MappingUsage,
  NodeDef,
} from '../types';
import { ProfileHistory } from './ProfileHistory';
import { AssetErrorState, AssetLoadingState } from './assets/AssetState';
import { VisibilityChip } from './assets/VisibilityControl';

interface Props {
  nodeType: string;
  profileId: string;
  def: NodeDef | undefined;
  principalRole: string | undefined;
  onNavigate: (path: string) => void;
}

const PARAM_GROUP_LABEL: Record<string, string> = {
  bodyFields: 'Body rows',
  queryFields: 'Query rows',
  headerFields: 'Header rows',
  pathFields: 'Path rows',
};

interface ParamRow {
  name?: string;
  mode?: string;
  type?: string;
  value?: unknown;
}

/** A read-only inspect surface for one profile: a sticky summary rail
 *  on the left (identity, metadata, counts) and content sections on
 *  the right — Basics, Configuration, then References with its
 *  credentials, used-by and bundled-asset blocks. Editors continue to
 *  the form; viewers get a page they can open at all. */
export function ProfileViewPage({
  nodeType,
  profileId,
  def,
  principalRole,
  onNavigate,
}: Props) {
  const [profile, setProfile] = useState<ConfigProfile | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [usage, setUsage] = useState<ConfigProfileUsage[]>([]);
  const [usageLoading, setUsageLoading] = useState(true);
  const [usageFailure, setUsageFailure] = useState<LoadFailure | null>(null);
  const [bundleFailure, setBundleFailure] = useState<LoadFailure | null>(null);
  const [bundleUsage, setBundleUsage] = useState<
    { label: string; name: string; usage: MappingUsage[] | DataSchemaUsage[] }[]
  >([]);
  const [historyOpen, setHistoryOpen] = useState(false);
  const usageRequest = useRef(0);

  // U4: usage and bundle lookups have their own failure state. A failed read
  // must never render as "0 workflow versions" / "no bundled assets".
  const loadUsage = useCallback(async (target: ConfigProfile) => {
    const request = ++usageRequest.current;
    setUsageLoading(true);
    setUsageFailure(null);
    setBundleFailure(null);

    let usageReason: unknown = null;
    const own = await api.configProfileUsage(target.id).catch((reason: unknown) => {
      usageReason = reason;
      return null;
    });
    if (request !== usageRequest.current) return;
    setUsage(own?.usage ?? []);
    setUsageFailure(usageReason ? classifyCurrentLoadFailure(usageReason) : null);

    const sections: { label: string; name: string; usage: MappingUsage[] | DataSchemaUsage[] }[] = [];
    let bundleReason: unknown = null;
    for (const [kind, id] of Object.entries(target.bundle?.mappings ?? {})) {
      if (!id) continue;
      const [mapping, pins] = await Promise.all([
        api.getMapping(id).catch((reason: unknown) => {
          bundleReason ??= reason;
          return null;
        }),
        api.mappingUsage(id).catch((reason: unknown) => {
          bundleReason ??= reason;
          return null;
        }),
      ]);
      if (request !== usageRequest.current) return;
      sections.push({
        label: `${kind} mapping`,
        name: mapping?.name ?? id,
        usage: pins?.usage ?? [],
      });
    }
    for (const [slot, id] of Object.entries(target.bundle?.schemas ?? {})) {
      if (!id) continue;
      const [schema, pins] = await Promise.all([
        api.getDataSchema(id).catch((reason: unknown) => {
          bundleReason ??= reason;
          return null;
        }),
        api.dataSchemaUsage(id).catch((reason: unknown) => {
          bundleReason ??= reason;
          return null;
        }),
      ]);
      if (request !== usageRequest.current) return;
      sections.push({
        label: `${slot} data contract`,
        name: schema?.name ?? id,
        usage: pins?.usage ?? [],
      });
    }
    setBundleUsage(sections);
    setBundleFailure(bundleReason ? classifyCurrentLoadFailure(bundleReason) : null);
    setUsageLoading(false);
  }, []);

  useEffect(() => {
    let cancelled = false;
    // Invalidate any usage request from the previous profile.
    usageRequest.current += 1;
    setLoading(true);
    setLoadError('');
    setUsageLoading(true);
    setUsage([]);
    setBundleUsage([]);
    setUsageFailure(null);
    setBundleFailure(null);
    api
      .getConfigProfile(profileId)
      .then((result) => {
        if (cancelled) return;
        setProfile(result);
        void loadUsage(result);
      })
      .catch((reason: unknown) => {
        if (!cancelled) setLoadError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
      usageRequest.current += 1;
    };
  }, [profileId, loadUsage]);

  if (loading) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <AssetLoadingState label="Loading profile…" className="muted" />
      </main>
    );
  }
  if (loadError || !profile) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <div className="page-head">
          <div>
            <h1>Profile not available</h1>
            <ActionError testId="profile-view-error">{loadError || 'This profile does not exist or is private to its creator.'}</ActionError>
            <button className="button" onClick={() => onNavigate(`/hub/${encodeURIComponent(nodeType)}`)}>
              Back to {def?.displayName || nodeType} profiles
            </button>
          </div>
        </div>
      </main>
    );
  }

  const typePath = `/hub/${encodeURIComponent(nodeType)}`;
  const editPath = `${typePath}/${encodeURIComponent(profileId)}/edit`;
  const paramGroups = Object.keys(PARAM_GROUP_LABEL).filter(
    (key) => Array.isArray(profile.config[key]) && (profile.config[key] as ParamRow[]).length > 0,
  );
  const plainKeys = Object.keys(profile.config).filter(
    (key) => !(key in PARAM_GROUP_LABEL) && !paramGroups.includes(key),
  );
  const credentialCount = Object.keys(profile.credentialRefs ?? {}).length;

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <div className="page-body">
        <header className="page-head">
          <div>
            <p className="breadcrumb">
              <button className="text-button" onClick={() => onNavigate('/profiles')}>
                Profiles
              </button>{' '}
              /{' '}
              <button className="text-button" onClick={() => onNavigate(typePath)}>
                {def?.displayName || nodeType}
              </button>{' '}
              / <code>{profile.name}</code>
            </p>
            <h1 data-testid="profile-view-name">{profile.name}</h1>
            <p className="page-sub">
              {def?.displayName || nodeType} profile · v{profile.version}
              {profile.visibility === 'private' ? ' · private' : ''}
              {profile.owner ? ` · owner ${profile.owner}` : ''}
            </p>
          </div>
          <span className="hub-head-actions">
            <VisibilityChip visibility={profile.visibility} testId="profile-view-visibility" />
            {principalRole !== 'viewer' && (
              <button
                className="button primary"
                data-testid="profile-view-edit"
                onClick={() => onNavigate(editPath)}
              >
                Edit
              </button>
            )}
            <button
              className="button subtle"
              data-testid="profile-view-history"
              onClick={() => setHistoryOpen(true)}
            >
              History
            </button>
          </span>
        </header>

        <div className="profile-view-grid" data-testid="profile-view-sections">
          <aside className="profile-view-rail" aria-label="Profile summary" data-testid="profile-view-rail">
            <div className="profile-rail-identity">
              <p className="field-label">Profile</p>
              <h2 className="profile-rail-name">{profile.name}</h2>
              <code className="muted small">{profile.nodeType}</code>
            </div>
            <dl className="profile-rail-meta">
              <div className="profile-rail-row">
                <dt className="field-label">Version</dt>
                <dd>v{profile.version}</dd>
              </div>
              <div className="profile-rail-row">
                <dt className="field-label">Visibility</dt>
                <dd>{profile.visibility === 'private' ? 'private' : 'shared'}</dd>
              </div>
              <div className="profile-rail-row">
                <dt className="field-label">Owner</dt>
                <dd>{profile.owner || <span className="muted">unassigned</span>}</dd>
              </div>
              <div className="profile-rail-row">
                <dt className="field-label">Tags</dt>
                <dd>
                  {profile.tags?.length ? (
                    <span title={profile.tags.join(', ')}>{profile.tags.join(', ')}</span>
                  ) : (
                    <span className="muted">none</span>
                  )}
                </dd>
              </div>
            </dl>
            <dl className="profile-rail-meta">
              <div className="profile-rail-row">
                <dt className="field-label">Used by</dt>
                <dd>
                  {usageFailure
                    ? 'unavailable'
                    : usageLoading
                      ? 'loading…'
                      : `${usage.length} workflow version${usage.length === 1 ? '' : 's'}`}
                </dd>
              </div>
              <div className="profile-rail-row">
                <dt className="field-label">Credentials</dt>
                <dd>{credentialCount} reference{credentialCount === 1 ? '' : 's'}</dd>
              </div>
              <div className="profile-rail-row">
                <dt className="field-label">Bundled assets</dt>
                <dd>
                  {bundleFailure
                    ? 'unavailable'
                    : usageLoading
                      ? 'loading…'
                      : bundleUsage.length > 0
                        ? bundleUsage.length
                        : 'none'}
                </dd>
              </div>
            </dl>
          </aside>

          <div className="profile-view-content">
            <section className="dashboard-panel" aria-label="Profile basics">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Basics</h2>
                </div>
              </header>
              <div className="pad">
                <dl className="definition-grid" data-testid="profile-view-basics">
                  <div className="definition-cell">
                    <dt className="field-label">Node type</dt>
                    <dd>
                      <code>{profile.nodeType}</code>
                    </dd>
                  </div>
                  <div className="definition-cell">
                    <dt className="field-label">Retry policy</dt>
                    <dd>
                      {profile.retry
                        ? `max attempts ${profile.retry.maxAttempts ?? 'none'}, backoff ${
                            profile.retry.backoffMs ?? 'none'
                          }ms`
                        : 'none'}
                    </dd>
                  </div>
                  <div className="definition-cell">
                    <dt className="field-label">Description</dt>
                    <dd>{profile.description || <span className="muted">none</span>}</dd>
                  </div>
                  <div className="definition-cell">
                    <dt className="field-label">Tags</dt>
                    <dd>
                      {profile.tags?.length ? (
                        <span title={profile.tags.join(', ')}>{profile.tags.join(', ')}</span>
                      ) : (
                        <span className="muted">none</span>
                      )}
                    </dd>
                  </div>
                </dl>
              </div>
            </section>

            <section className="dashboard-panel" aria-label="Profile configuration values">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Configuration</h2>
                </div>
              </header>
              <div className="pad" data-testid="profile-view-config">
                {plainKeys.length === 0 && paramGroups.length === 0 && (
                  <p className="muted">No configuration values.</p>
                )}
                {plainKeys.length > 0 && (
                  <dl className="definition-grid">
                    {plainKeys.map((key) => (
                      <div className="definition-cell" key={key}>
                        <dt className="field-label">
                          <code>{key}</code>
                        </dt>
                        <dd>
                          <code title={JSON.stringify(profile.config[key])}>
                            {JSON.stringify(profile.config[key])}
                          </code>
                        </dd>
                      </div>
                    ))}
                  </dl>
                )}
                {paramGroups.map((group) => {
                  const rows = profile.config[group] as ParamRow[];
                  return (
                    <div className="profile-usage" key={group}>
                      <p className="field-label">{PARAM_GROUP_LABEL[group]}</p>
                      {rows.map((row, index) => (
                        <span key={`${row.name ?? index}`}>
                          <code>{row.name}</code>{' '}
                          <span className="meta-list meta-strict">
                            <span>{row.mode ?? 'static'}</span>
                            <span title={JSON.stringify(row.value)}>{JSON.stringify(row.value)}</span>
                          </span>
                        </span>
                      ))}
                    </div>
                  );
                })}
              </div>
            </section>

            <section className="dashboard-panel" aria-label="Profile references">
              <header className="dashboard-panel-head">
                <div>
                  <h2>References</h2>
                </div>
              </header>
              <div className="pad">
                <div className="reference-block" data-testid="profile-view-credentials">
                  <h3>Credentials</h3>
                  {credentialCount === 0 ? (
                    <p className="muted">No credential references.</p>
                  ) : (
                    <dl className="definition-grid">
                      {Object.entries(profile.credentialRefs ?? {}).map(([slot, id]) => (
                        <div className="definition-cell" key={slot}>
                          <dt className="field-label">
                            <code>{slot}</code>
                          </dt>
                          <dd>{id}</dd>
                        </div>
                      ))}
                    </dl>
                  )}
                </div>

                <div className="reference-block" data-testid="profile-view-usage">
                  <h3>Used by</h3>
                  {usageFailure ? (
                    <AssetErrorState
                      failure={usageFailure}
                      onRetry={() => void loadUsage(profile)}
                      testId="profile-usage-error"
                    />
                  ) : usageLoading ? (
                    <AssetLoadingState label="Loading usage…" />
                  ) : usage.length === 0 ? (
                    <p className="muted">No published workflow versions pin this profile.</p>
                  ) : (
                    usage.map((pin) => (
                      <p
                        className="profile-preview-row"
                        key={`${pin.workflowId}-v${pin.workflowVersion}`}
                      >
                        <code>workflow</code>{' '}
                        <span className="meta-list meta-strict">
                          <span title={pin.workflowName || pin.workflowId}>
                            {pin.workflowName || pin.workflowId}
                          </span>
                          <span>v{pin.workflowVersion}</span>
                          <span>pins profile v{pin.profileVersion}</span>
                        </span>
                      </p>
                    ))
                  )}
                </div>

                <div className="reference-block">
                  <h3>Bundled assets</h3>
                  {bundleFailure ? (
                    <AssetErrorState
                      failure={bundleFailure}
                      onRetry={() => void loadUsage(profile)}
                      testId="profile-bundle-error"
                    />
                  ) : null}
                  {usageLoading && bundleUsage.length === 0 ? null : bundleUsage.length === 0 &&
                    !bundleFailure ? (
                    <p className="muted">No bundled mappings or contracts.</p>
                  ) : (
                    bundleUsage.map((section) => (
                      <div className="profile-usage" key={section.label}>
                        <p className="field-label">
                          {section.label}: {section.name}
                        </p>
                        {section.usage.length === 0 ? (
                          <span className="muted small">Not pinned by published workflows.</span>
                        ) : (
                          section.usage.map((pin) => (
                            <span key={`${section.label}-${pin.workflowId}-v${pin.workflowVersion}`}>
                              {pin.workflowName || pin.workflowId} · v{pin.workflowVersion}
                            </span>
                          ))
                        )}
                      </div>
                    ))
                  )}
                </div>
              </div>
            </section>
          </div>
        </div>
      </div>

      <ProfileHistory
        profile={historyOpen ? profile : null}
        onClose={() => setHistoryOpen(false)}
      />
    </main>
  );
}
