import { PARAM_GROUP_TARGETS, injectBundleRefs, mergeProfileConfig, toParamRows } from '../paramRows';
import { OriginBadge } from './assets/OriginBadge';
import { profileAttachPatch, profileDetachPatch } from '../profileOwnership';
import type { ConfigProfile, WorkflowNode } from '../types';

interface Props {
  node: WorkflowNode;
  profiles: ConfigProfile[];
  onNodePatch: (patch: Partial<WorkflowNode>) => void;
}

// The "Configuration profile" inspector section: the profile picker and
// the effective-config-at-publish preview with origin badges. Extracted
// verbatim from Inspector's profileable IIFE (Profile-Centric UX Phase 3
// item 3.2).
export function InspectorProfileSection({ node, profiles, onNodePatch }: Props) {
  const config = node.config ?? {};
  const changeProfile = (nextProfileId: string) => {
    const previousProfile = profiles.find((candidate) => candidate.id === node.profileRef) ?? null;
    const profile = profiles.find((candidate) => candidate.id === nextProfileId);
    if (!profile) {
      onNodePatch(profileDetachPatch(node, previousProfile));
      return;
    }
    onNodePatch(profileAttachPatch(node, profile, previousProfile));
  };
  return (
    <section className="inspector-section" data-testid="profile-section">
      <h3>Configuration profile</h3>
      <label className="field" htmlFor="node-profile-ref">
        <span className="field-label">Profile</span>
        <select
          id="node-profile-ref"
          className="text-input"
          value={node.profileRef ?? ''}
          onChange={(event) => changeProfile(event.target.value)}
        >
          <option value="">None (configure inline)</option>
          {profiles.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name} (v{p.version})
            </option>
          ))}
        </select>
        <span className="field-hint">
          Profile values apply at publish; this node&rsquo;s own fields win.
        </span>
      </label>
      {(() => {
        const profile = profiles.find((p) => p.id === node.profileRef);
        if (!profile) return null;
        // v4.1 E4: parameter-group rows merge per row name; the
        // preview shows one line per merged row with its origin.
        const effective = mergeProfileConfig(profile.config, config, node.profileOverrides);
        const nodeRowsByKey = new Map(
          Object.entries(PARAM_GROUP_TARGETS).map(([fieldsKey]) => [
            fieldsKey,
            new Set(
              toParamRows(config[fieldsKey])
                .map((row) => row.name.trim())
                .filter(Boolean),
            ),
          ]),
        );
        return (
          <div className="profile-preview" data-testid="profile-preview">
            <p className="field-label">Effective config at publish</p>
            {(() => {
              // Phase 1 P0-2: bundle references fill the node's empty
              // slots — mappingRef / mappingRefs groups — with origin
              // "bundle". Anything the node set itself is already in
              // the effective config above.
              const injected = injectBundleRefs(profile.bundle, node.type, config);
              return Object.entries(injected)
                .filter(([key, value]) => !(key in config) && value != null)
                .map(([key, value]) => {
                  if (key === 'mappingRefs' && value && typeof value === 'object') {
                    return Object.entries(value as Record<string, unknown>).map(
                      ([group, ref]) =>
                        typeof ref === 'string' && ref ? (
                          <div className="profile-preview-row" key={`bundle.mappingRefs.${group}`}>
                            <code>{`mappingRefs.${group}`}</code>
                            <span>{JSON.stringify(ref)}</span>
                            <OriginBadge state="bundle" />
                          </div>
                        ) : null,
                    );
                  }
                  // Phase 4 D4-1: a bundle-injected scriptRef reads as
                  // bundle-origin here.
                  if (key === 'scriptRef') {
                    return (
                      <div className="profile-preview-row" key="bundle.scriptRef">
                        <code>scriptRef</code>
                        <span>{JSON.stringify(value)}</span>
                        <OriginBadge state="bundle" />
                      </div>
                    );
                  }
                  if (key !== 'mappingRef') return null;
                  return (
                    <div className="profile-preview-row" key="bundle.mappingRef">
                      <code>mappingRef</code>
                      <span>{JSON.stringify(value)}</span>
                      <OriginBadge state="bundle" />
                    </div>
                  );
                });
            })()}
            {Object.entries(effective).map(([key, value]) => {
              const groupFieldsKey = key in PARAM_GROUP_TARGETS ? key : undefined;
              if (groupFieldsKey) {
                const nodeNames = nodeRowsByKey.get(groupFieldsKey) ?? new Set<string>();
                return toParamRows(value).map((row, index) => (
                  <div className="profile-preview-row" key={`${key}.${row.name}.${index}`}>
                    <code>{`${key}.${row.name}`}</code>
                    <span>{JSON.stringify(row.value)}</span>
                    <OriginBadge state={nodeNames.has(row.name.trim()) ? 'local' : 'inherited'} />
                  </div>
                ));
              }
              // A group's plain target is dead once its row array
              // is present — the array compiles over it at publish.
              if (
                Object.values(PARAM_GROUP_TARGETS).includes(key) &&
                Object.keys(effective).some(
                  (other) => PARAM_GROUP_TARGETS[other] === key && Array.isArray(effective[other]),
                )
              ) {
                return null;
              }
              const profileConfig = profile.config ?? {};
              const explicitOverride = node.profileOverrides?.[key] === true;
              const explicitOwnership = node.profileOverrides !== undefined;
              const fromProfile =
                !(key in config) ||
                (!explicitOverride &&
                  key in profileConfig &&
                  (explicitOwnership ||
                    JSON.stringify(config[key]) === JSON.stringify(profileConfig[key])));
              return (
                <div className="profile-preview-row" key={key}>
                  <code>{key}</code>
                  <span>{JSON.stringify(value)}</span>
                  <OriginBadge state={fromProfile ? 'inherited' : 'local'} />
                </div>
              );
            })}
            {profile.retry && !node.retry && (
              <div className="profile-preview-row" key="retry">
                <code>retry</code>
                <span>{JSON.stringify(profile.retry)}</span>
                <OriginBadge state="inherited" />
              </div>
            )}
          </div>
        );
      })()}
    </section>
  );
}
