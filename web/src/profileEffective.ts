// Effective-configuration preview for the profile form (UX Phase 2 D2-3,
// plan P0-5/P1-7): composes the profile under review against either a
// synthetic empty node of its type (what a bare node inherits) or a real
// usage site's node (node override + profile + bundle), producing one row
// per effective value with its origin. Shares the publish-parity helpers
// in paramRows.ts with the Inspector and validate.ts so the browser
// shows exactly what the server will publish.

import {
  PARAM_GROUP_TARGETS,
  bundleSchemaRefs,
  injectBundleRefs,
  mergeProfileConfig,
  toParamRows,
} from './paramRows';
import type { ProfileBundle, RetryPolicy } from './types';

export type EffectiveOrigin = 'node' | 'profile' | 'bundle';

export interface EffectiveRow {
  key: string;
  value: unknown;
  origin: EffectiveOrigin;
}

export interface EffectiveInput {
  nodeType: string;
  profile: {
    config?: Record<string, unknown>;
    bundle?: ProfileBundle | null;
    retry?: RetryPolicy | null;
  };
  node: {
    config: Record<string, unknown>;
    profileOverrides?: Record<string, boolean>;
    retry?: RetryPolicy | null;
    inputSchemaRef?: string;
    outputSchemaRef?: string;
  };
}

/** One row per effective value at publish, in Inspector-preview order:
 * bundle reference slots first, then the merged config (parameter-group
 * rows one line per row name), then the effective retry policy. */
export function effectiveRows(input: EffectiveInput): EffectiveRow[] {
  const nodeConfig = input.node.config ?? {};
  const profileConfig = input.profile.config ?? {};
  const rows: EffectiveRow[] = [];
  const push = (key: string, value: unknown, origin: EffectiveOrigin) => {
    rows.push({ key, value, origin });
  };

  // 1. Reference slots the bundle fills (the node's own ref wins, so
  // anything the node set reads as a node row).
  const injected = injectBundleRefs(input.profile.bundle ?? undefined, input.nodeType, nodeConfig);
  const nodeRefs = (nodeConfig.mappingRefs ?? {}) as Record<string, unknown>;
  if (injected.mappingRefs && typeof injected.mappingRefs === 'object') {
    for (const [group, ref] of Object.entries(injected.mappingRefs as Record<string, unknown>)) {
      if (typeof ref !== 'string' || !ref) continue;
      push(`mappingRefs.${group}`, ref, group in nodeRefs ? 'node' : 'bundle');
    }
  }
  if (typeof injected.mappingRef === 'string' && injected.mappingRef) {
    push('mappingRef', injected.mappingRef, 'mappingRef' in nodeConfig ? 'node' : 'bundle');
  }
  // Phase 4 D4-1: the bundle's script fills the empty scriptRef slot.
  if (typeof injected.scriptRef === 'string' && injected.scriptRef) {
    push('scriptRef', injected.scriptRef, 'scriptRef' in nodeConfig ? 'node' : 'bundle');
  }
  const schemas = bundleSchemaRefs(input.profile.bundle ?? undefined, input.node);
  if (schemas.input) {
    push('inputSchemaRef', schemas.input, input.node.inputSchemaRef ? 'node' : 'bundle');
  }
  if (schemas.output) {
    push('outputSchemaRef', schemas.output, input.node.outputSchemaRef ? 'node' : 'bundle');
  }

  // 2. The merged config, one row per key — parameter-group row arrays
  // expand to one row per row name with the winning side's origin.
  const effective = mergeProfileConfig(profileConfig, nodeConfig, input.node.profileOverrides);
  for (const [key, value] of Object.entries(effective)) {
    if (key in PARAM_GROUP_TARGETS) {
      const nodeNames = new Set(
        toParamRows(nodeConfig[key])
          .map((row) => row.name.trim())
          .filter(Boolean),
      );
      for (const row of toParamRows(value)) {
        push(
          `${key}.${row.name}`,
          row.value,
          nodeNames.has(row.name.trim()) ? 'node' : 'profile',
        );
      }
      continue;
    }
    // A group's plain target is dead once its row array survives the
    // merge — the array compiles over it at publish.
    if (
      Object.values(PARAM_GROUP_TARGETS).includes(key) &&
      Object.keys(effective).some(
        (other) => PARAM_GROUP_TARGETS[other] === key && Array.isArray(effective[other]),
      )
    ) {
      continue;
    }
    const explicitOwnership = input.node.profileOverrides !== undefined;
    const explicitOverride = input.node.profileOverrides?.[key] === true;
    const nodeSuppliesKey = Object.prototype.hasOwnProperty.call(nodeConfig, key);
    const fromProfile =
      key in profileConfig && (!nodeSuppliesKey || (explicitOwnership && !explicitOverride));
    push(key, value, fromProfile ? 'profile' : 'node');
  }

  // 3. Retry: the node's own policy wins; otherwise the profile's
  // applies — both are effective config at publish.
  if (input.node.retry) {
    push('retry', input.node.retry, 'node');
  } else if (input.profile.retry) {
    push('retry', input.profile.retry, 'profile');
  }
  return rows;
}
