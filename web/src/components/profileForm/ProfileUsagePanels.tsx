import { AssetLoadingState } from '../assets/AssetState';
import type {
  ConfigProfileUsage,
  DataSchema,
  DataSchemaUsage,
  Mapping,
  MappingUsage,
  Script,
  ScriptUsage,
} from '../../types';
import type { PreviewSite } from './ProfileConfigPanels';

// The Used-by panel (Phase 6 D6-3): workflow versions pinning this
// profile and, through the bundle, each bundled asset.
export function ProfileUsedByPanel(props: {
  profileId: string;
  usageLoading: boolean;
  profileUsage: ConfigProfileUsage[] | null;
  bundleUsage: {
    label: string;
    id: string;
    kind: 'mapping' | 'schema' | 'script';
    usage: MappingUsage[] | DataSchemaUsage[] | ScriptUsage[];
  }[];
  bundleMappingsByKind: Record<string, Mapping[]>;
  bundleScriptsList: Script[];
  bundleSchemasList: DataSchema[];
}) {
  return (
    <div role="tabpanel" aria-labelledby="profile-section-used-by" data-testid="profile-panel-used-by">
      <div className="field-group-head">
        <h3 className="field-heading">Used by</h3>
      </div>
      <p className="field-hint">
        Published workflow versions pinning this profile — and, through it, the included assets.
        A delete is blocked while any pin exists.
      </p>
      {props.usageLoading && <AssetLoadingState label="Loading usage…" className="muted small" />}
      {!props.usageLoading && (
        <section className="inspector-section" data-testid="profile-own-usage">
          <div className="field-group-head">
            <h4 className="field-heading">This profile</h4>
          </div>
          {(props.profileUsage ?? []).length === 0 ? (
            <p className="field-hint">No published workflow versions pin this profile yet.</p>
          ) : (
            (props.profileUsage ?? []).map((usage) => (
              <p
                className="profile-preview-row"
                key={`${usage.workflowId}-v${usage.workflowVersion}`}
              >
                <code>workflow</code>{' '}
                <span
                  title={`${usage.workflowName || usage.workflowId} · v${usage.workflowVersion} (pins profile v${usage.profileVersion})`}
                >
                  {usage.workflowName || usage.workflowId} · v{usage.workflowVersion} (pins profile
                  v{usage.profileVersion})
                </span>
              </p>
            ))
          )}
        </section>
      )}
      {!props.usageLoading && props.bundleUsage.length > 0 && (
        <section className="inspector-section" data-testid="profile-bundle-usage">
          <div className="field-group-head">
            <h4 className="field-heading">Included assets</h4>
          </div>
          {props.bundleUsage.map((section) => (
            <div className="profile-usage" key={section.label}>
              <p className="field-label">
                {section.label}:{' '}
                {section.kind === 'mapping'
                  ? Object.values(props.bundleMappingsByKind)
                      .flat()
                      .find((m) => m.id === section.id)?.name ?? section.id
                  : section.kind === 'script'
                    ? props.bundleScriptsList.find((s) => s.id === section.id)?.name ?? section.id
                    : props.bundleSchemasList.find((s) => s.id === section.id)?.name ?? section.id}
              </p>
              {section.usage.length === 0 ? (
                <span className="muted small">
                  Not pinned by any published workflow version.
                </span>
              ) : (
                section.usage.map((usage) => (
                  <span key={`${section.label}-${usage.workflowId}-v${usage.workflowVersion}`}>
                    {usage.workflowName || usage.workflowId} · v{usage.workflowVersion}
                  </span>
                ))
              )}
            </div>
          ))}
        </section>
      )}
    </div>
  );
}

// The Effective-preview panel (Phase 6 D6-3): the live form composed
// against a synthetic empty node or a chosen usage site, with per-key
// origin badges.
export function ProfilePreviewPanel(props: {
  editing: boolean;
  sites: PreviewSite[] | null;
  siteKey: string;
  setSiteKey: (value: string) => void;
  previewRows: {
    key: string;
    value: unknown;
    origin: string;
  }[];
  mappingsById: Map<string, { name: string; version: number }>;
  schemasById: Map<string, { name: string; version: number }>;
}) {
  const describeRef = (value: unknown, byId: Map<string, { name: string; version: number }>) => {
    if (typeof value !== 'string' || !value) return JSON.stringify(value);
    const found = byId.get(value);
    return found ? `${found.name} (v${found.version})` : value;
  };
  return (
    <div role="tabpanel" aria-labelledby="profile-section-preview" data-testid="profile-panel-preview">
      <div className="field-group-head">
        <h3 className="field-heading">Effective config at publish</h3>
      </div>
      <p className="field-hint">
        What a node of this type gets: its own fields win, the profile fills the rest, and bundle
        refs fill empty slots. This preview tracks the form as you edit it.
      </p>
      {props.editing && props.sites && props.sites.length > 0 && (
        <label className="field" htmlFor="profile-preview-site">
          <span className="field-label">Compose against</span>
          <select
            id="profile-preview-site"
            className="text-input"
            data-testid="profile-preview-site"
            value={props.siteKey}
            onChange={(event) => props.setSiteKey(event.target.value)}
          >
            <option value="">A new node (no overrides)</option>
            {props.sites.map((site) => (
              <option key={`${site.workflowId}/${site.nodeId}`} value={`${site.workflowId}/${site.nodeId}`}>
                {site.workflowName} · {site.nodeName}
              </option>
            ))}
          </select>
          <span className="field-hint">Usage sites override fields with their own values.</span>
        </label>
      )}
      {props.editing && props.sites && props.sites.length === 0 && (
        <p className="field-hint">
          No workflow uses this profile yet — the preview composes against a new node.
        </p>
      )}
      <div className="profile-preview" data-testid="profile-effective-preview">
        {props.previewRows.map((row) => {
          const display =
            row.key === 'mappingRef' || row.key.startsWith('mappingRefs.')
              ? describeRef(row.value, props.mappingsById)
              : row.key === 'inputSchemaRef' || row.key === 'outputSchemaRef'
                ? describeRef(row.value, props.schemasById)
                : JSON.stringify(row.value);
          return (
            <div className="profile-preview-row" key={row.key}>
              <code>{row.key}</code>
              <span title={display}>{display}</span>
              <em className={`from-${row.origin}`}>{row.origin}</em>
            </div>
          );
        })}
        {props.previewRows.length === 0 && (
          <p className="field-hint">Nothing set yet — this profile would change nothing.</p>
        )}
      </div>
    </div>
  );
}
