import { bundleSchemaRefs, injectBundleRefs, mergeProfileConfig } from './paramRows';
import type { ConfigProfile, WorkflowNode } from './types';

/** Builds the patch for attaching a profile to an existing node.
 *
 * A profile-supplied key already present in the node is marked as an
 * override unless explicit ownership proves it was inherited from the
 * previous profile. Nodes without ownership metadata and keys local to the
 * node are conservative: preserving the stored value is safer than silently
 * treating it as inherited. New sparse nodes carry none of these keys, so
 * profiles apply cleanly without suppressing automatic defaults.
 */
export function profileAttachPatch(
  node: WorkflowNode,
  profile: ConfigProfile,
  previousProfile?: ConfigProfile | null,
): Partial<WorkflowNode> {
  const config = node.config ?? {};
  const profileConfig = profile.config ?? {};
  const previousConfig = previousProfile?.config;
  const explicitOwnership = node.profileOverrides !== undefined;
  const overrides: Record<string, boolean> = {};
  for (const key of Object.keys(profileConfig)) {
    if (!Object.prototype.hasOwnProperty.call(config, key)) continue;
    if (node.profileOverrides?.[key] === true) {
      overrides[key] = true;
      continue;
    }
    if (!explicitOwnership) {
      overrides[key] = true;
      continue;
    }
    if (!previousConfig || !Object.prototype.hasOwnProperty.call(previousConfig, key)) {
      overrides[key] = true;
    }
  }
  return { profileRef: profile.id, profileOverrides: overrides };
}

/** Materializes a node's effective values before detaching its profile.
 * Inherited config, retry, and bundle-provided references become node-owned;
 * nothing is silently lost.
 */
export function profileDetachPatch(node: WorkflowNode, profile: ConfigProfile | null): Partial<WorkflowNode> {
  if (!profile) {
    return { profileRef: undefined, profileOverrides: undefined };
  }
  const config = node.config ?? {};
  const effective = mergeProfileConfig(profile.config, config, node.profileOverrides);
  const injected = injectBundleRefs(profile.bundle, node.type, config);
  if (node.type === 'http.request' && profile.bundle?.mappings) {
    const mappingRefs = { ...((injected.mappingRefs ?? {}) as Record<string, unknown>) };
    for (const [group, ref] of Object.entries(profile.bundle.mappings)) {
      if (ref && !mappingRefs[group]) mappingRefs[group] = ref;
    }
    if (Object.keys(mappingRefs).length > 0) injected.mappingRefs = mappingRefs;
  }
  const schemas = bundleSchemaRefs(profile.bundle, node);
  const materializedConfig = { ...effective, ...injected };
  return {
    inputSchemaRef: schemas.input,
    outputSchemaRef: schemas.output,
    config: materializedConfig,
    retry: node.retry ?? profile.retry ?? undefined,
    profileRef: undefined,
    profileOverrides: undefined,
  };
}
