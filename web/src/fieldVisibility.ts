import type { FieldDef, NodeDef } from './types';

// Field-visibility rules for the node inspector, extracted verbatim from
// Inspector.tsx (Profile-Centric UX Phase 0 item 1: freeze current behavior
// in tests before Phase 3 restructures it). The rules are pure so the unit
// suite can pin them per node type, and the e2e freeze spec can snapshot
// the rendered result against the live catalog.

// Config keys owned by the HTTP parameter groups (Configuration Hub v3
// plan D2): the raw object inputs and the row editors render once, in
// Parameters, so the generic field renderer must skip them.
const HTTP_PARAM_GROUP_KEYS = [
  'query',
  'headers',
  'pathParams',
  'bodyFields',
  'queryFields',
  'headerFields',
  'pathFields',
  'mappingRefs',
] as const;

// bodyTypeFor returns the effective http.request body mode: an explicit
// bodyType wins; otherwise the shape of the seeded config decides (rows or
// a form body mean json, a rawBody means raw, nothing means none).
// Non-http nodes return ''.
export function bodyTypeFor(nodeType: string, config: Record<string, unknown> | undefined): string {
  if (nodeType !== 'http.request') return '';
  return String(
    config?.bodyType ??
      (config?.bodyFields !== undefined || config?.body !== undefined
        ? 'json'
        : config?.rawBody !== undefined
          ? 'raw'
          : 'none'),
  );
}

// isConfigFieldVisible answers whether the generic field renderer shows a
// catalog config key for the given node type. trigger.cron's schedule and
// timezone live in the dedicated cron editor; http.request's parameter
// keys live in HTTPParamGroups; body/rawBody/contentType follow bodyType;
// graphqlQuery/graphqlVariables and parts only render for their body
// types (Phase 3 D3-6 — no irrelevant advanced fields in normal mode).
export function isConfigFieldVisible(nodeType: string, key: string, bodyType: string): boolean {
  if (nodeType === 'trigger.cron') return key !== 'schedule' && key !== 'timezone';
  if (nodeType !== 'http.request') return true;
  if ((HTTP_PARAM_GROUP_KEYS as readonly string[]).includes(key)) return false;
  if (key === 'body') return bodyType === 'form';
  if (key === 'rawBody' || key === 'contentType') return bodyType === 'raw';
  if (key === 'graphqlQuery' || key === 'graphqlVariables') return bodyType === 'graphql';
  if (key === 'parts') return bodyType === 'multipart';
  return true;
}

// visibleConfigFields filters a definition's config spec down to the keys
// the inspector renders, preserving the catalog's declaration order.
export function visibleConfigFields(
  nodeType: string,
  config: Record<string, unknown> | undefined,
  def: NodeDef | undefined,
): Array<[string, FieldDef]> {
  const bodyType = bodyTypeFor(nodeType, config);
  return Object.entries(def?.config ?? {}).filter(([key]) => isConfigFieldVisible(nodeType, key, bodyType));
}
