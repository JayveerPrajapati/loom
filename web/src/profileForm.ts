import type { ConfigProfile, FieldDef, NodeDef, RetryPolicy } from './types';

// Pure helpers behind the dedicated profile form pages (Configuration Hub
// v4 plan D4/D5/D6): typed catalog-driven editing with unset semantics —
// a field the user leaves empty is omitted from the saved config, so
// partial overlays stay expressible. Mirrors the server-side rules in
// internal/server/profiles.go validateProfileBody + internal/nodes/profile.go
// ValidateProfileConfig so both ends reject the same things.

/** Profileable config fields split the way the form renders them:
 * essentials first, then the Advanced disclosure. */
export function profileableFieldGroups(
  def: NodeDef,
): { essential: [string, FieldDef][]; advanced: [string, FieldDef][] } {
  const entries = Object.entries(def.config ?? {}).filter(([, spec]) => spec.profileable);
  return {
    essential: entries.filter(([, spec]) => !spec.advanced),
    advanced: entries.filter(([, spec]) => spec.advanced),
  };
}

/**
 * Drops form-level "unset" markers so they never reach the API: undefined
 * values, empty strings, empty objects/arrays and numbers that failed to
 * parse (NaN). A field with an empty container is the same as not setting
 * it in a partial-overlay profile.
 */
export function stripUnset(values: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(values)) {
    if (value === undefined || value === null) continue;
    if (typeof value === 'string' && value.trim() === '') continue;
    if (typeof value === 'number' && !Number.isFinite(value)) continue;
    if (typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length === 0) continue;
    if (Array.isArray(value) && value.length === 0) continue;
    out[key] = value;
  }
  return out;
}

/** D5-B: a profile must set at least one profileable field. */
export function mandatoryError(
  config: Record<string, unknown>,
  retry: RetryPolicy | null,
  bundle?: ConfigProfile['bundle'],
): string | null {
  const hasBundle =
    bundle !== undefined &&
    (Object.keys(bundle.mappings ?? {}).length > 0 ||
      Object.keys(bundle.schemas ?? {}).length > 0 ||
      Boolean(bundle.script));
  if (Object.keys(config).length === 0 && !retry && !hasBundle) {
    return 'Set at least one configuration field, a retry policy, or an included asset — an empty profile does nothing.';
  }
  return null;
}

/** Frontend mirror of the server's catalog validation: enum membership,
 * pattern, numeric range and JSON type per profileable field. */
export function validateProfileConfig(
  def: NodeDef,
  config: Record<string, unknown>,
): string[] {
  const errors: string[] = [];
  for (const [key, value] of Object.entries(config)) {
    const spec = def.config?.[key];
    if (!spec) {
      errors.push(`${key} is not a field of ${def.type}`);
      continue;
    }
    if (!spec.profileable) {
      errors.push(`${key} is not profileable`);
      continue;
    }
    const label = spec.label || key;
    if (spec.enum && !spec.enum.includes(String(value))) {
      errors.push(`${label} must be one of ${spec.enum.join(', ')}`);
    }
    if (spec.pattern && typeof value === 'string') {
      if (!new RegExp(spec.pattern).test(value)) {
        errors.push(`${label} must match ${spec.pattern}`);
      }
    }
    if (typeof value === 'number') {
      if (spec.min !== undefined && value < spec.min) errors.push(`${label} must be at least ${spec.min}`);
      if (spec.max !== undefined && value > spec.max) errors.push(`${label} must be at most ${spec.max}`);
    }
    switch (spec.type) {
      case 'string':
        if (typeof value !== 'string') errors.push(`${label} must be a string`);
        break;
      case 'number':
        if (typeof value !== 'number') errors.push(`${label} must be a number`);
        break;
      case 'boolean':
        if (typeof value !== 'boolean') errors.push(`${label} must be a boolean`);
        break;
      case 'object':
        if (typeof value !== 'object' || Array.isArray(value) || value === null) {
          errors.push(`${label} must be an object`);
        }
        break;
      case 'array':
        if (!Array.isArray(value)) errors.push(`${label} must be an array`);
        break;
      default:
        break;
    }
  }
  return errors;
}

/** Retry fields the form exposes, with the same bounds the server enforces. */
export const RETRY_FIELDS: { key: keyof RetryPolicy; label: string; min: number; max: number }[] = [
  { key: 'maxAttempts', label: 'Max attempts', min: 1, max: 10 },
  { key: 'backoffMs', label: 'Backoff (ms)', min: 0, max: 600000 },
  { key: 'maxDelayMs', label: 'Max delay (ms)', min: 0, max: 3600000 },
];

/** Parses the retry inputs; empty means the policy omits that key. */
export function retryFromInputs(inputs: Record<string, string>): RetryPolicy | null {
  const retry: RetryPolicy = {};
  for (const field of RETRY_FIELDS) {
    const text = (inputs[field.key] ?? '').trim();
    if (text === '') continue;
    const num = Number(text);
    if (!Number.isFinite(num)) return null;
    retry[field.key] = num;
  }
  return Object.keys(retry).length > 0 ? retry : null;
}

/** Pagination model (Configuration Hub v4 D4): the profile form edits the
 * documented pagination keys typed; see internal/nodes/http.go
 * parsePagination for the engine-side reader of the same keys. */
export const PAGINATION_MODES = ['page', 'offset', 'cursor'] as const;

const PAGINATION_STRING_KEYS = [
  'itemsPath',
  'pageParam',
  'offsetParam',
  'limitParam',
  'cursorParam',
  'cursorPath',
  'until',
] as const;

const PAGINATION_NUMBER_KEYS = ['startPage', 'limit', 'maxPages', 'maxItems'] as const;

export function paginationFromObject(raw: unknown): {
  mode: string;
  strings: Record<string, string>;
  numbers: Record<string, string>;
} {
  const record = (raw ?? {}) as Record<string, unknown>;
  const strings: Record<string, string> = {};
  for (const key of PAGINATION_STRING_KEYS) {
    if (typeof record[key] === 'string') strings[key] = record[key] as string;
  }
  const numbers: Record<string, string> = {};
  for (const key of PAGINATION_NUMBER_KEYS) {
    if (typeof record[key] === 'number') numbers[key] = String(record[key]);
  }
  return {
    mode: typeof record.mode === 'string' ? record.mode : '',
    strings,
    numbers,
  };
}

export function paginationToObject(
  mode: string,
  strings: Record<string, string>,
  numbers: Record<string, string>,
): Record<string, unknown> | null {
  const out: Record<string, unknown> = {};
  if (mode) out.mode = mode;
  for (const key of PAGINATION_STRING_KEYS) {
    const text = (strings[key] ?? '').trim();
    if (text !== '') out[key] = text;
  }
  for (const key of PAGINATION_NUMBER_KEYS) {
    const text = (numbers[key] ?? '').trim();
    if (text !== '') {
      const num = Number(text);
      if (Number.isFinite(num)) out[key] = num;
    }
  }
  return Object.keys(out).length > 0 ? out : null;
}

/** The engine's pagination caps (http.go hardMaxPages/hardMaxItems). */
export const PAGINATION_LIMITS = { maxPages: 1000, maxItems: 100000 };

/** Credential slots a profile may reference (mirrors
 * nodes.ProfileableCredentialSlots). */
export function profileableCredentialSlots(def: NodeDef): string[] {
  return [...new Set([...(def.credentials ?? []), ...(def.optionalCredentials ?? [])])].sort();
}

/** Seeds form state from an existing profile. */
export function profileToValues(profile: ConfigProfile): Record<string, unknown> {
  return { ...(profile.config ?? {}) };
}

/** Assembles the create/update payload from form state. */
export function profilePayload(input: {
  name: string;
  nodeType: string;
  description: string;
  owner: string;
  tags: string;
  config: Record<string, unknown>;
  credentialRefs: Record<string, string>;
  retry: RetryPolicy | null;
  version?: number;
  bundle?: ConfigProfile['bundle'];
  testInput?: unknown;
}): {
  name: string;
  nodeType: string;
  description?: string;
  config: Record<string, unknown>;
  credentialRefs?: Record<string, string>;
  retry?: RetryPolicy;
  tags: string[];
  owner?: string;
  bundle?: ConfigProfile['bundle'];
  testInput?: unknown;
  version?: number;
} {
  const tags = input.tags
    .split(',')
    .map((tag) => tag.trim())
    .filter(Boolean);
  const credentialRefs = Object.fromEntries(
    Object.entries(input.credentialRefs).filter(([, id]) => id !== ''),
  );
  return {
    name: input.name.trim(),
    nodeType: input.nodeType,
    description: input.description.trim() || undefined,
    config: input.config,
    credentialRefs: Object.keys(credentialRefs).length > 0 ? credentialRefs : undefined,
    retry: input.retry ?? undefined,
    tags,
    owner: input.owner.trim() || undefined,
    bundle: input.bundle,
    testInput: input.testInput,
    version: input.version,
  };
}


// Phase 4 D4-4: the compact "This profile changes" inventory. Config
// keys use the form's unset semantics; retry, included mappings,
// contracts and script are appended in that order.
export function profileChangeKeys(input: {
  config: Record<string, unknown>;
  retry: RetryPolicy | null;
  bundle?: ConfigProfile['bundle'];
}): string[] {
  const keys = Object.keys(stripUnset(input.config));
  if (input.retry && Object.keys(input.retry).length > 0) keys.push('retry');
  for (const [kind, id] of Object.entries(input.bundle?.mappings ?? {})) {
    if (id) keys.push(`mapping:${kind}`);
  }
  for (const [slot, id] of Object.entries(input.bundle?.schemas ?? {})) {
    if (id) keys.push(`schema:${slot}`);
  }
  if (input.bundle?.script) keys.push('script');
  return keys;
}

// UX Phase 4 D4-1: the profile editor's three primary areas. Old
// `?tab=` values stay valid aliases so existing links and tests keep
// resolving; the canonical URL keeps writing the section value.
export type ProfileArea = 'identity' | 'configuration' | 'trust';
export type ProfileConfigSection = 'defaults' | 'data' | 'included';
export type ProfileTrustSection = 'test' | 'preview' | 'history' | 'used-by';

export const AREA_LABEL: Record<ProfileArea, string> = {
  identity: 'Identity',
  configuration: 'Configuration',
  trust: 'Trust',
};

export const CONFIG_SECTION_LABEL: Record<ProfileConfigSection, string> = {
  defaults: 'Defaults',
  data: 'Data',
  included: 'Included behavior',
};

export const TRUST_SECTION_LABEL: Record<ProfileTrustSection, string> = {
  test: 'Test',
  preview: 'Effective preview',
  history: 'History',
  'used-by': 'Used by',
};

export interface ProfileLocation {
  area: ProfileArea;
  configSection: ProfileConfigSection;
  trustSection: ProfileTrustSection;
  /** The value written back to `?tab=`; always one of the section ids. */
  canonical: string;
}

export function resolveProfileLocation(
  raw: string | null,
  options: { hasData: boolean; editing: boolean },
): ProfileLocation {
  const identity: ProfileLocation = {
    area: 'identity',
    configSection: 'defaults',
    trustSection: 'test',
    canonical: 'identity',
  };
  switch (raw) {
    case 'identity':
    case 'basics':
      return identity;
    case 'configuration':
    case 'defaults':
      return { area: 'configuration', configSection: 'defaults', trustSection: 'test', canonical: 'defaults' };
    case 'data':
      return {
        area: 'configuration',
        configSection: options.hasData ? 'data' : 'defaults',
        trustSection: 'test',
        canonical: options.hasData ? 'data' : 'defaults',
      };
    case 'contracts':
    case 'bundle':
    case 'included':
      return { area: 'configuration', configSection: 'included', trustSection: 'test', canonical: 'included' };
    case 'test':
      return { area: 'trust', configSection: 'defaults', trustSection: 'test', canonical: 'test' };
    case 'preview':
      return { area: 'trust', configSection: 'defaults', trustSection: 'preview', canonical: 'preview' };
    case 'history':
      return {
        area: 'trust',
        configSection: 'defaults',
        trustSection: options.editing ? 'history' : 'test',
        canonical: options.editing ? 'history' : 'test',
      };
    case 'used-by':
      return {
        area: 'trust',
        configSection: 'defaults',
        trustSection: options.editing ? 'used-by' : 'test',
        canonical: options.editing ? 'used-by' : 'test',
      };
    case 'trust':
      return { area: 'trust', configSection: 'defaults', trustSection: 'test', canonical: 'test' };
    default:
      return identity;
  }
}
