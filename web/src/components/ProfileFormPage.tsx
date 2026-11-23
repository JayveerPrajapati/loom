import { AssetLoadingState } from './assets/AssetState';
import { ActionError } from './assets/ActionError';
import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import {
  CONFIG_SECTION_LABEL,
  TRUST_SECTION_LABEL,
  paginationFromObject,
  profileableCredentialSlots,
  profileableFieldGroups,
  profileChangeKeys,
  resolveProfileLocation,
  type ProfileArea,
  type ProfileConfigSection,
  type ProfileTrustSection,
} from '../profileForm';
import { effectiveRows } from '../profileEffective';
import { labelFor } from './inspectorHelpers';
import type {
  ConfigProfile,
  FieldDef,
  NodeDef,
} from '../types';
import type { PaginationState } from './profileForm/ProfileField';
import {
  ProfileBasicsPanel,
  ProfileDataPanel,
  ProfileDefaultsPanel,
  ProfileIncludedPanel,
} from './profileForm/ProfileConfigPanels';
import { ProfileTestPanel } from './profileForm/ProfileTestPanel';
import { ProfilePreviewPanel, ProfileUsedByPanel } from './profileForm/ProfileUsagePanels';
import { useProfileAssets } from './profileForm/useProfileAssets';
import { useProfileUsage } from './profileForm/useProfileUsage';
import { useProfileSubmit } from './profileForm/useProfileSubmit';
import {
  ProfileAreaStrip,
  ProfileFormHeader,
  ProfileHistoryTabPanel,
  ProfileSectionStrip,
  ProfileShareButton,
  ProfileStickyBar,
} from './profileForm/ProfileFormChrome';

// Configuration Hub v4 plan D4-A/D5-B: the dedicated profile create/edit
// page. The form is plated to the node type — every profileable catalog
// field renders typed (enum selects, ranged numbers, structured editors
// for http.request headers and pagination), fields left empty are omitted
// so partial overlays stay expressible, and a raw-JSON escape hatch covers
// everything exotic. Edit reuses the exact create surface (D1-A) with a
// version header and stale-save 409 recovery. History opens the shared
// ProfileHistory dialog from the header (v4.1 E3: Diff | Full-config
// tabs); the create page has no history.
//
// UX Phase 2 (D2-2): the form is a URL-synced tab strip — Basics
// (identity/governance), Node defaults (catalog fields, credentials,
// retry; raw-JSON mode toggle lives here), Data (http.request parameter
// groups + bundle mappings) and Contracts (bundle data contracts). The
// error/stale banners and the save actions stay outside the tabs so they
// are reachable from every panel.
//
// Phase 6 D6-3: tab panels and data loading live in
// web/src/components/profileForm/; this page owns state, save, and
// composition.

export function ProfileFormPage({
  nodeType,
  profileId,
  def,
  onNavigate,
  onNotify,
  onProfilesChanged,
}: {
  nodeType: string;
  profileId?: string;
  def: NodeDef | undefined;
  onNavigate: (path: string) => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onProfilesChanged?: () => void;
}) {
  const editing = Boolean(profileId);
  const typePath = `/hub/${encodeURIComponent(nodeType)}`;

  const [profile, setProfile] = useState<ConfigProfile | null>(null);
  const [loading, setLoading] = useState(editing);
  const [loadError, setLoadError] = useState('');

  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [owner, setOwner] = useState('');
  const [tagsText, setTagsText] = useState('');
  const [values, setValues] = useState<Record<string, unknown>>({});
  const [pagination, setPagination] = useState<PaginationState>(() => paginationFromObject(null));
  const [credentialRefs, setCredentialRefs] = useState<Record<string, string>>({});
  const [retryInputs, setRetryInputs] = useState<Record<string, string>>({});
  const [configMode, setConfigMode] = useState<'fields' | 'json'>('fields');
  const [jsonText, setJsonText] = useState('{}');

  // Bundle (Phase 1 P0-1): mapping refs per group, schema refs per slot.
  const [bundleMappings, setBundleMappings] = useState<Record<string, string>>({});
  const [bundleSchemas, setBundleSchemas] = useState<Record<'input' | 'output', string>>({
    input: '',
    output: '',
  });
  // Phase 4 D4-1: the bundle's script (code profiles only) fills a
  // node's empty scriptRef.
  const [bundleScript, setBundleScript] = useState('');
  // Phase 5: the saved test fixture (JSON) prefills test surfaces.
  const [testInput, setTestInput] = useState('');

  // Phase 4 D4-3: dirty state is a snapshot comparison against the last
  // seeded form, so no individual field needs change plumbing.
  const [seedRevision, setSeedRevision] = useState(0);
  const [baseline, setBaseline] = useState(() =>
    JSON.stringify({
      name: '',
      description: '',
      owner: '',
      tagsText: '',
      values: {},
      pagination: paginationFromObject(null),
      credentialRefs: {},
      retryInputs: { maxAttempts: '', backoffMs: '', maxDelayMs: '' },
      configMode: 'fields',
      jsonText: '{}',
      bundleMappings: {},
      bundleSchemas: { input: '', output: '' },
      bundleScript: '',
      testInput: '',
    }),
  );

  const { essential, advanced } = useMemo(
    () => (def ? profileableFieldGroups(def) : { essential: [], advanced: [] }),
    [def],
  );
  const credSlots = useMemo(() => (def ? profileableCredentialSlots(def) : []), [def]);

  const {
    credentials,
    bundleMappingsByKind,
    bundleSchemasList,
    bundleScriptsList,
    mappingsById,
    schemasById,
  } = useProfileAssets();

  // Phase 4 D4-1: three URL-synced primary areas with secondary sections.
  // Old ?tab= values resolve through resolveProfileLocation, so existing
  // deep links and bookmarks keep working.
  const groupFieldKeys = useMemo(
    () => new Set(['bodyFields', 'queryFields', 'headerFields', 'pathFields']),
    [],
  );
  const hasDataTab =
    (def?.mappingKinds?.length ?? 0) > 0 ||
    [...essential, ...advanced].some(([key]) => groupFieldKeys.has(key));
  const initialLocation = resolveProfileLocation(
    new URLSearchParams(window.location.search).get('tab'),
    { hasData: hasDataTab, editing },
  );
  const [area, setArea] = useState<ProfileArea>(initialLocation.area);
  const [configSection, setConfigSection] = useState<ProfileConfigSection>(
    initialLocation.configSection,
  );
  const [trustSection, setTrustSection] = useState<ProfileTrustSection>(
    initialLocation.trustSection,
  );
  const writeTab = (canonical: string) => {
    // replaceState, not push: section switches must not spam history.
    window.history.replaceState({}, '', `${window.location.pathname}?tab=${canonical}`);
  };
  const selectArea = (next: ProfileArea) => {
    setArea(next);
    writeTab(
      next === 'identity' ? 'identity' : next === 'configuration' ? configSection : trustSection,
    );
  };
  const selectConfigSection = (next: ProfileConfigSection) => {
    setArea('configuration');
    setConfigSection(next);
    writeTab(next);
  };
  const selectTrustSection = (next: ProfileTrustSection) => {
    setArea('trust');
    setTrustSection(next);
    writeTab(next);
  };
  const configSections: ProfileConfigSection[] = [
    'defaults',
    ...(hasDataTab ? (['data'] as const) : []),
    'included',
  ];
  const trustSections: ProfileTrustSection[] = editing
    ? ['test', 'preview', 'history', 'used-by']
    : ['test', 'preview'];
  const activeConfigSection = configSections.includes(configSection) ? configSection : 'defaults';
  const activeTrustSection = trustSections.includes(trustSection) ? trustSection : 'test';

  const { profileUsage, bundleUsage, usageLoading, sites, siteKey, setSiteKey } = useProfileUsage({
    editing,
    profileId,
    version: profile?.version,
    activeTab: area === 'trust' ? activeTrustSection : '',
    bundleMappings,
    bundleSchemas,
    bundleScript,
  });

  const seedForm = (source: ConfigProfile | null) => {
    const config = source?.config ?? {};
    setName(source?.name ?? '');
    setDescription(source?.description ?? '');
    setOwner(source?.owner ?? '');
    setTagsText((source?.tags ?? []).join(', '));
    setValues(config);
    setPagination(paginationFromObject(config.pagination));
    setCredentialRefs(source?.credentialRefs ? { ...source.credentialRefs } : {});
    const retry = source?.retry ?? {};
    setRetryInputs({
      maxAttempts: retry.maxAttempts !== undefined ? String(retry.maxAttempts) : '',
      backoffMs: retry.backoffMs !== undefined ? String(retry.backoffMs) : '',
      maxDelayMs: retry.maxDelayMs !== undefined ? String(retry.maxDelayMs) : '',
    });
    setBundleMappings({ ...(source?.bundle?.mappings ?? {}) });
    setBundleSchemas({
      input: source?.bundle?.schemas?.input ?? '',
      output: source?.bundle?.schemas?.output ?? '',
    });
    setBundleScript(source?.bundle?.script ?? '');
    setTestInput(
      source?.testInput !== undefined && source?.testInput !== null
        ? JSON.stringify(source.testInput, null, 2)
        : '',
    );
    setConfigMode('fields');
    setSeedRevision((current) => current + 1);
    resetErrors();
  };

  const reload = () => {
    if (!profileId) return;
    setLoading(true);
    setLoadError('');
    api
      .getConfigProfile(profileId)
      .then((loaded) => {
        setProfile(loaded);
        seedForm(loaded);
      })
      .catch((reason: Error) => setLoadError(reason.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    if (!editing) {
      seedForm(null);
      setProfile(null);
    } else {
      reload();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [profileId, nodeType]);

  const {
    save,
    busy,
    setBusy,
    error,
    clearError,
    resetErrors,
    stale,
    currentConfig,
    retryFromInputsSafe,
    bundleForPayload,
  } = useProfileSubmit({
    nodeType,
    def,
    editing,
    profile,
    typePath,
    name,
    description,
    owner,
    tagsText,
    configMode,
    jsonText,
    values,
    pagination,
    retryInputs,
    credentialRefs,
    bundleMappings,
    bundleSchemas,
    bundleScript,
    testInput,
    onNavigate,
    onNotify,
    onProfilesChanged,
  });

  const formSnapshot = JSON.stringify({
    name,
    description,
    owner,
    tagsText,
    values,
    pagination,
    credentialRefs,
    retryInputs,
    configMode,
    jsonText,
    bundleMappings,
    bundleSchemas,
    bundleScript,
    testInput,
  });
  useEffect(() => {
    setBaseline(formSnapshot);
    // The baseline resets only when the form is seeded, not on every edit.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seedRevision]);
  const dirty = formSnapshot !== baseline;

  const changeKeys = profileChangeKeys({
    config: currentConfig(),
    retry: retryFromInputsSafe(),
    bundle: bundleForPayload(),
  });
  const changeLabel = (key: string) => {
    if (key === 'retry') return 'Retry policy';
    if (key === 'script') return 'Script';
    if (key.startsWith('mapping:')) {
      return `${labelFor(key.slice('mapping:'.length))} mapping`;
    }
    if (key.startsWith('schema:')) {
      return `${labelFor(key.slice('schema:'.length))} contract`;
    }
    return labelFor(key);
  };

  if (!def) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <div className="empty-panel">
          <h2>Unknown node type</h2>
          <p className="muted">
            <code>{nodeType}</code> is not in the catalog, so it has no profile pages.
          </p>
          <button className="button primary" onClick={() => onNavigate('/profiles')}>
            Back to Profiles
          </button>
        </div>
      </main>
    );
  }

  const setValue = (key: string, value: unknown) => {
    setValues((current) => ({ ...current, [key]: value }));
    clearError();
  };

  const switchConfigMode = (next: 'fields' | 'json') => {
    if (next === configMode) return;
    if (next === 'json') {
      setJsonText(JSON.stringify(currentConfig(), null, 2));
    } else {
      try {
        const parsed = JSON.parse(jsonText) as Record<string, unknown>;
        setValues(parsed);
        setPagination(paginationFromObject(parsed.pagination));
      } catch {
        // Keep the fields as they were; the user can retry the switch.
      }
    }
    setConfigMode(next);
  };

  if (loading) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <AssetLoadingState label="Loading profile…" className="muted pad" />
      </main>
    );
  }

  if (loadError) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1}>
        <ActionError>{loadError}</ActionError>
        <button className="button subtle" onClick={() => onNavigate(typePath)}>
          Back to {def.displayName || nodeType} profiles
        </button>
      </main>
    );
  }

  const activeSite = sites?.find((site) => `${site.workflowId}/${site.nodeId}` === siteKey);
  const previewRows = effectiveRows({
    nodeType,
    profile: {
      config: currentConfig(),
      bundle: bundleForPayload(),
      retry: retryFromInputsSafe(),
    },
    node: activeSite
      ? {
          config: activeSite.config,
          profileOverrides: activeSite.profileOverrides,
          retry: activeSite.retry,
          inputSchemaRef: activeSite.inputSchemaRef,
          outputSchemaRef: activeSite.outputSchemaRef,
        }
      : { config: {} },
  });
  const dataEntries = ([...essential, ...advanced] as [string, FieldDef][]).filter(([key]) =>
    groupFieldKeys.has(key),
  );

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <ProfileFormHeader
        nodeType={nodeType}
        typePath={typePath}
        def={def}
        editing={editing}
        profile={profile}
        onNavigate={onNavigate}
      />

      <div className="profile-form-grid">
        <form
          id="profile-form"
          className="dashboard-panel pad profile-form"
          data-testid="profile-form"
          onSubmit={(event) => void save(event)}
        >
          <ProfileStickyBar
            title={
              editing
                ? profile?.name || name || 'Edit profile'
                : name.trim() || 'New profile'
            }
            meta={
              editing
                ? `v${profile?.version ?? '—'} · ${profile?.visibility ?? 'shared'} · used by ${
                    profileUsage?.length ?? 0
                  } workflow${(profileUsage?.length ?? 0) === 1 ? '' : 's'}`
                : 'new · private until shared'
            }
            dirty={dirty}
            busy={busy}
            saveLabel={
              busy
                ? 'Saving…'
                : editing
                  ? `Save as v${(profile?.version ?? 0) + 1}`
                  : 'Save profile'
            }
            onCancel={() => onNavigate(typePath)}
          >
            {editing && profile && (
              <ProfileShareButton
                profile={profile}
                busy={busy}
                setBusy={setBusy}
                onNotify={onNotify}
                onReload={reload}
              />
            )}
          </ProfileStickyBar>

          <section
            className="profile-changes"
            data-testid="profile-changes-summary"
            aria-label="This profile changes"
          >
            <span className="profile-changes-label">This profile changes</span>
            {changeKeys.length === 0 ? (
              <span className="field-hint" data-testid="profile-changes-empty">
                Nothing yet — empty fields stay inherited from the node.
              </span>
            ) : (
              <ul className="profile-change-list">
                {changeKeys.map((key) => (
                  <li key={key} data-testid={`profile-change-${key}`}>
                    {changeLabel(key)}
                  </li>
                ))}
              </ul>
            )}
          </section>

          {error && (
            <ActionError testId="profile-form-error">{error}</ActionError>
          )}
          {stale && (
            <p className="banner notice" role="status" data-testid="profile-form-stale">
              <button type="button" className="button subtle" onClick={reload}>
                Reload latest version
              </button>
            </p>
          )}

          {/* Phase 4 D4-1: three primary areas; banners and actions stay
              outside so they are reachable from every panel. */}
          <ProfileAreaStrip area={area} onSelect={selectArea} />

          {area === 'identity' && (
            <ProfileBasicsPanel
              name={name}
              setName={setName}
              description={description}
              setDescription={setDescription}
              owner={owner}
              setOwner={setOwner}
              tagsText={tagsText}
              setTagsText={setTagsText}
              visibility={profile?.visibility}
              editing={editing}
            />
          )}

          {area === 'configuration' && (
            <>
              <ProfileSectionStrip
                sections={configSections}
                active={activeConfigSection}
                labels={CONFIG_SECTION_LABEL}
                label="Configuration sections"
                onSelect={selectConfigSection}
              />

              {activeConfigSection === 'defaults' && (
                <ProfileDefaultsPanel
                  nodeType={nodeType}
                  essential={essential}
                  advanced={advanced}
                  groupFieldKeys={groupFieldKeys}
                  configMode={configMode}
                  switchConfigMode={switchConfigMode}
                  values={values}
                  pagination={pagination}
                  setPagination={setPagination}
                  setValue={setValue}
                  jsonText={jsonText}
                  setJsonText={setJsonText}
                  credSlots={credSlots}
                  credentials={credentials}
                  credentialRefs={credentialRefs}
                  setCredentialRefs={setCredentialRefs}
                  retryInputs={retryInputs}
                  setRetryInputs={setRetryInputs}
                />
              )}

              {activeConfigSection === 'data' && (
                <ProfileDataPanel
                  nodeType={nodeType}
                  entries={dataEntries}
                  values={values}
                  pagination={pagination}
                  setPagination={setPagination}
                  setValue={setValue}
                />
              )}

              {activeConfigSection === 'included' && (
                <ProfileIncludedPanel
                  nodeType={nodeType}
                  mappingKinds={def?.mappingKinds ?? []}
                  bundleMappings={bundleMappings}
                  setBundleMappings={setBundleMappings}
                  bundleMappingsByKind={bundleMappingsByKind}
                  bundleSchemas={bundleSchemas}
                  setBundleSchemas={setBundleSchemas}
                  bundleSchemasList={bundleSchemasList}
                  bundleScript={bundleScript}
                  setBundleScript={setBundleScript}
                  bundleScriptsList={bundleScriptsList}
                />
              )}
            </>
          )}

          {area === 'trust' && (
            <>
              <ProfileSectionStrip
                sections={trustSections}
                active={activeTrustSection}
                labels={TRUST_SECTION_LABEL}
                label="Trust sections"
                onSelect={selectTrustSection}
              />

              {activeTrustSection === 'test' && (
                <ProfileTestPanel
                  nodeType={nodeType}
                  editing={editing}
                  profileId={profileId}
                  busy={busy}
                  testInput={testInput}
                  setTestInput={setTestInput}
                />
              )}

              {activeTrustSection === 'preview' && (
                <ProfilePreviewPanel
                  editing={editing}
                  sites={sites}
                  siteKey={siteKey}
                  setSiteKey={setSiteKey}
                  previewRows={previewRows}
                  mappingsById={mappingsById}
                  schemasById={schemasById}
                />
              )}

              {activeTrustSection === 'history' && profileId && (
                <ProfileHistoryTabPanel profileId={profileId} />
              )}

              {activeTrustSection === 'used-by' && profileId && (
                <ProfileUsedByPanel
                  profileId={profileId}
                  usageLoading={usageLoading}
                  profileUsage={profileUsage}
                  bundleUsage={bundleUsage}
                  bundleMappingsByKind={bundleMappingsByKind}
                  bundleScriptsList={bundleScriptsList}
                  bundleSchemasList={bundleSchemasList}
                />
              )}
            </>
          )}

        </form>
      </div>
    </main>
  );
}
