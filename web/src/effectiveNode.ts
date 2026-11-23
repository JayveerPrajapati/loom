// Effective-value resolution for the node inspector (UI-audit campaign
// B1): what each form field would resolve to at publish, with its
// origin — node, profile, or bundle — plus the override state a
// "reset to inherited" affordance needs. Mirrors the publish-parity
// merge helpers in paramRows.ts and the ownership inference of
// UpgradeWorkflowProfiles: a stored value equal to the pinned profile
// version's value follows the profile forward, so published sites
// (whose node configs already carry resolved values) still badge as
// profile-origin.

import {
  PARAM_GROUP_TARGETS,
  bundleSchemaRefs,
  injectBundleRefs,
  mergeProfileConfig,
  toParamRows,
} from './paramRows';
import type { ConfigProfile, WorkflowNode } from './types';

export type FieldOrigin = 'node' | 'profile' | 'bundle';

export interface EffectiveFieldInfo {
  value: unknown;
  origin: FieldOrigin;
  /** True when the node's own config carries a value the profile also
   * provides — a reset would restore the inherited value. */
  overridden: boolean;
}

function sameValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

export interface NodeEffective {
  /** Scalar config keys and reference slots. */
  field(key: string): EffectiveFieldInfo | null;
  /** The merged display value for a parameter-group key (row arrays
   * merged per row name; node row wins). */
  groupValue(key: string): unknown;
  /** Per-row origin inside a parameter group: node when the node's own
   * row array carries the name, otherwise profile. */
  rowOrigin(groupKey: string, rowName: string): 'node' | 'profile' | null;
  /** True when the key carries a node-level override a reset can clear. */
  canReset(key: string): boolean;
  /** The pinned profile's own value for a key, when it supplies one.
   * Overridden fields show it as the "Profile: …" hint. */
  inheritedValue(key: string): unknown;
}

/** Computes the effective view of a node's config under its pinned
 * profile. `profile` is the profile list entry the inspector already
 * holds (its `config` is the current pinned version's config). */
export function nodeEffective(
  node: WorkflowNode,
  profile: ConfigProfile | null,
): NodeEffective | null {
  if (!profile) return null;
  const nodeConfig = node.config ?? {};
  const profileConfig = profile.config ?? {};
  const merged = mergeProfileConfig(profileConfig, nodeConfig, node.profileOverrides);
  const injected = injectBundleRefs(profile.bundle, node.type, nodeConfig);
  const nodeRowNames = new Map(
    Object.entries(PARAM_GROUP_TARGETS).map(([fieldsKey]) => [
      fieldsKey,
      new Set(
        toParamRows(nodeConfig[fieldsKey])
          .map((row) => row.name.trim())
          .filter(Boolean),
      ),
    ]),
  );

  const field = (key: string): EffectiveFieldInfo | null => {
    // Parameter-group keys: the display value is the merged row array;
    // the group as a whole is not a scalar field with one origin.
    if (key in PARAM_GROUP_TARGETS) {
      if (!(key in merged)) return null;
      return { value: merged[key], origin: 'node', overridden: false };
    }
    // A group's plain target is dead once its row array survives.
    if (
      Object.values(PARAM_GROUP_TARGETS).includes(key) &&
      Object.keys(merged).some(
        (other) => PARAM_GROUP_TARGETS[other] === key && Array.isArray(merged[other]),
      )
    ) {
      return null;
    }
    // mappingRefs is a ref-group object the bundle can extend with
    // groups the node left empty: the injected view is always the
    // union, with origin decided by whether the node set any refs.
    if (key === 'mappingRefs') {
      const value = injected.mappingRefs;
      if (value == null) return null;
      return {
        value,
        origin: 'mappingRefs' in nodeConfig ? 'node' : 'bundle',
        overridden: false,
      };
    }
    // Bundle-filled reference slots the node left empty.
    const nodeHas = key in nodeConfig;
    if (!nodeHas) {
      const bundleValue = injected[key];
      if (bundleValue != null) {
        return { value: bundleValue, origin: 'bundle', overridden: false };
      }
      // A scalar key only the profile carries: inherited from the
      // profile config.
      if (key in merged) {
        return { value: merged[key], origin: 'profile', overridden: false };
      }
      return null;
    }
    // The node carries the key: it is an authored value, an override,
    // or a value publish resolved out of the profile. Explicit ownership
    // is authoritative; a legacy node (no profileOverrides) keeps its
    // stored value, and an equal value is not an override.
    const profileSuppliesKey = Object.prototype.hasOwnProperty.call(profileConfig, key);
    const explicitOwnership = node.profileOverrides !== undefined;
    const explicitOverride = node.profileOverrides?.[key] === true;
    const legacyEqualValue =
      !explicitOwnership && profileSuppliesKey && sameValue(nodeConfig[key], profileConfig[key]);
    if (explicitOverride) {
      return { value: nodeConfig[key], origin: 'node', overridden: true };
    }
    if (legacyEqualValue) {
      // Publish's legacy value-wins rule: the stored node value is the
      // effective value, so its origin is node, not profile.
      return { value: nodeConfig[key], origin: 'node', overridden: false };
    }
    if (profileSuppliesKey && explicitOwnership) {
      return { value: merged[key], origin: 'profile', overridden: false };
    }
    return {
      value: nodeConfig[key],
      origin: 'node',
      overridden: profileSuppliesKey,
    };
  };

  const rowOrigin = (groupKey: string, rowName: string): 'node' | 'profile' | null => {
    const names = nodeRowNames.get(groupKey);
    if (!names) return null;
    return names.has(rowName.trim()) ? 'node' : 'profile';
  };

  const groupValue = (key: string): unknown => merged[key];

  const canReset = (key: string): boolean => {
    if (!(key in nodeConfig)) return false;
    if (key in PARAM_GROUP_TARGETS) {
      // A node row array resets per row, not as a whole group; the
      // group editor owns that, so scalar reset does not apply.
      return false;
    }
    if (Object.values(PARAM_GROUP_TARGETS).includes(key)) return false;
    const info = field(key);
    return info?.overridden ?? false;
  };

  const inheritedValue = (key: string): unknown =>
    Object.prototype.hasOwnProperty.call(profileConfig, key) ? profileConfig[key] : undefined;

  return { field, groupValue, rowOrigin, canReset, inheritedValue };
}

/** The effective input/output schema refs for a node under a profile
 * bundle: node refs win, bundle refs fill empty slots. */
export function effectiveSchemaRefs(
  node: Pick<WorkflowNode, 'inputSchemaRef' | 'outputSchemaRef'>,
  profile: ConfigProfile | null,
): { input: { value: string; origin: FieldOrigin } | null; output: { value: string; origin: FieldOrigin } | null } {
  if (!profile) {
    return {
      input: node.inputSchemaRef ? { value: node.inputSchemaRef, origin: 'node' } : null,
      output: node.outputSchemaRef ? { value: node.outputSchemaRef, origin: 'node' } : null,
    };
  }
  const refs = bundleSchemaRefs(profile.bundle, node);
  return {
    input: refs.input
      ? { value: refs.input, origin: node.inputSchemaRef ? 'node' : 'bundle' }
      : null,
    output: refs.output
      ? { value: refs.output, origin: node.outputSchemaRef ? 'node' : 'bundle' }
      : null,
  };
}
