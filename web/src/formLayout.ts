import type { FieldDef, NodeDef } from './types';
import { visibleConfigFields } from './fieldVisibility';

// Pure layout rules for the node inspector (Profile-Centric UX Phase 3,
// D3-2/D3-4/D3-6): which tabs a node type gets, which catalog fields land
// on Basics vs Advanced, and which raw/internal keys are hidden from
// normal mode entirely (they remain reachable only through the Advanced
// tab's raw-JSON editor). Everything here is pure so the unit suite pins
// it per node type and the e2e freeze spec walks the same rules in the
// DOM.

export type InspectorTab = 'configuration' | 'data' | 'runtime' | 'advanced';

export function hasProfileableFields(def: NodeDef | undefined): boolean {
  return Object.values(def?.config ?? {}).some((spec) => spec.profileable);
}

// inspectorTabs returns the node's tab inventory in render order
// (Apple-Grade Phase 3 D3-1): Configuration always; Data for every type
// with a data surface (the contract-section rule — everything except the
// cron and manual triggers); Runtime always (retry, failure handling,
// batch, webhook tester, run data); Advanced always (it hosts the
// raw-JSON escape hatch). Profile is not a tab: its identity and picker
// live in the stable summary above the tabs.
export function inspectorTabs(nodeType: string): InspectorTab[] {
  const tabs: InspectorTab[] = ['configuration'];
  const hasData = nodeType !== 'trigger.cron' && nodeType !== 'trigger.manual';
  if (hasData) tabs.push('data');
  tabs.push('runtime', 'advanced');
  return tabs;
}

// Config keys that never render as fields in normal mode (D3-6): they
// are publish-computed artifacts (data.map's mappingRows is overwritten
// at publish) or raw refs owned by a friendly editor (http.request's
// mappingRefs and the param-row arrays live in HTTPParamGroups).
const HIDDEN_FIELD_KEYS: Record<string, readonly string[]> = {
  'data.map': ['mappingRows'],
};

export interface FormFieldGroups {
  common: Array<[string, FieldDef]>;
  advanced: Array<[string, FieldDef]>;
}

// formFieldGroups splits a definition's visible fields into the Basics
// (common) and Advanced lists, applying the normal-mode hiding rules on
// top of fieldVisibility's base gating. Declaration order is preserved.
export function formFieldGroups(
  nodeType: string,
  config: Record<string, unknown> | undefined,
  def: NodeDef | undefined,
): FormFieldGroups {
  const hidden = HIDDEN_FIELD_KEYS[nodeType] ?? [];
  const visible = visibleConfigFields(nodeType, config, def).filter(([key]) => !hidden.includes(key));
  return {
    common: visible.filter(([, spec]) => !spec.advanced),
    advanced: visible.filter(([, spec]) => spec.advanced),
  };
}
