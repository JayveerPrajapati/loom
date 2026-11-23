import type { MappingRow, ProfileBundle } from './types';

// Parameter-group value rows (Configuration Hub v3 plan D2/D3): the typed
// row model behind the shared FieldGroupEditor. Static rows carry typed
// literals; expression rows carry {{ }} template text placed verbatim.
// Mirrors internal/graph/paramfields.go — publish compiles rows into the
// plain parameter objects the engine already executes.

export type ParamMode = 'static' | 'expression';
export type ParamValueType = 'string' | 'number' | 'boolean' | 'json';

export interface ParamRow {
  name: string;
  mode: ParamMode;
  /** Static rows only; empty means string. */
  type?: ParamValueType;
  /** Typed literal (static) or template text (expression). */
  value: unknown;
}

const STATIC_TYPES: ParamValueType[] = ['string', 'number', 'boolean', 'json'];

function isParamMode(mode: unknown): mode is ParamMode {
  return mode === 'static' || mode === 'expression';
}

function isParamValueType(type: unknown): type is ParamValueType {
  return STATIC_TYPES.includes(type as ParamValueType);
}

/**
 * Coerces a raw config value (the *Fields array) into rows the editor can
 * bind to. Tolerant by design: rows the editor itself writes are already
 * well-formed, and hand-edited JSON the server rejected still renders
 * (with static/expression guessed from the value) instead of vanishing.
 */
export function toParamRows(raw: unknown): ParamRow[] {
  if (!Array.isArray(raw)) return [];
  return raw.map((item) => {
    const record = (item ?? {}) as Record<string, unknown>;
    const mode = isParamMode(record.mode) ? record.mode : 'static';
    let type = isParamValueType(record.type) ? record.type : undefined;
    if (mode === 'static' && !type) {
      type = typeof record.value === 'number'
        ? 'number'
        : typeof record.value === 'boolean'
          ? 'boolean'
          : record.value !== null && typeof record.value === 'object'
            ? 'json'
            : 'string';
    }
    return {
      name: typeof record.name === 'string' ? record.name : '',
      mode,
      type: mode === 'static' ? type : undefined,
      value: record.value,
    };
  });
}

/**
 * Compiles rows into the plain parameter object publish produces: static
 * rows keep their typed literal, expression rows go in verbatim. This is
 * the editor's preview of the published config and the payload written
 * when switching to JSON mode.
 */
export function compileParamRows(rows: ParamRow[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const row of rows) {
    if (!row.name.trim()) continue;
    out[row.name.trim()] = row.mode === 'expression' ? String(row.value ?? '') : row.value;
  }
  return out;
}

function isTemplateText(value: string): boolean {
  return value.includes('{{');
}

/**
 * Converts a raw parameter object (JSON mode) into rows (Fields mode):
 * template-bearing strings become expressions, every other value keeps
 * its shape — plain strings become static string rows (wrapping a
 * literal in {{ }} would turn it into an expression, changing meaning).
 */
export function objectToParamRows(value: unknown): ParamRow[] {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return [];
  return Object.entries(value as Record<string, unknown>).map(([name, val]) => {
    if (typeof val === 'string') {
      return isTemplateText(val)
        ? { name, mode: 'expression' as ParamMode, value: val }
        : { name, mode: 'static' as ParamMode, type: 'string' as ParamValueType, value: val };
    }
    if (typeof val === 'number' || typeof val === 'boolean') {
      return { name, mode: 'static' as ParamMode, type: typeof val as ParamValueType, value: val };
    }
    return { name, mode: 'static' as ParamMode, type: 'json' as ParamValueType, value: val };
  });
}

/**
 * Serializes rows into mapping rows for save-as-mapping (v3 plan D4).
 * Static rows keep their type across the round-trip: numbers, booleans
 * and json become their canonical JSON text (which the body-kind compile
 * parses back into typed values), strings stay raw text unless they would
 * round-trip as another JSON type, in which case they are quoted.
 */
export function paramRowsToMappingRows(rows: ParamRow[]): MappingRow[] {
  return rows
    .filter((row) => row.name.trim())
    .map((row) => {
      let source: string;
      if (row.mode === 'expression') {
        source = String(row.value ?? '');
      } else if (row.type === 'string' || (row.type === undefined && typeof row.value === 'string')) {
        const text = String(row.value ?? '');
        source = wouldRoundTripAsNonString(text) ? JSON.stringify(text) : text;
      } else {
        source = JSON.stringify(row.value ?? null);
      }
      return { target: row.name.trim(), source };
    });
}

function wouldRoundTripAsNonString(text: string): boolean {
  const trimmed = text.trim();
  if (trimmed === '') return false;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    return JSON.stringify(parsed) === trimmed && typeof parsed !== 'string';
  } catch {
    return false;
  }
}

/**
 * Compiles mapping rows of a parameter kind back into their plain object —
 * the preview shown next to an attached mapping. Mirrors the Go compile:
 * body-kind sources that are canonical JSON become typed values (so
 * "20" is the number 20 but "02134" stays a string), every other group
 * and non-canonical text is verbatim.
 */
export function compileMappingRows(group: string, rows: MappingRow[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const row of rows) {
    if (group === 'body') {
      const trimmed = row.source.trim();
      if (trimmed !== '') {
        try {
          const parsed: unknown = JSON.parse(trimmed);
          if (JSON.stringify(parsed) === trimmed) {
            out[row.target] = parsed;
            continue;
          }
        } catch {
          // Not JSON — falls through to the verbatim string.
        }
      }
    }
    out[row.target] = row.source;
  }
  return out;
}

/**
 * Compiles mapping rows into editable param rows (the hub mapping editor
 * for parameter kinds): canonical-JSON sources on body become static
 * typed rows, template-bearing sources become expressions, everything
 * else is a static string.
 */
export function mappingRowsToParamRows(group: string, rows: MappingRow[]): ParamRow[] {
  return rows.map((row) => {
    if (row.source.includes('{{')) {
      return { name: row.target, mode: 'expression' as ParamMode, value: row.source };
    }
    if (group === 'body') {
      const trimmed = row.source.trim();
      if (trimmed !== '') {
        try {
          const parsed: unknown = JSON.parse(trimmed);
          if (JSON.stringify(parsed) === trimmed) {
            return {
              name: row.target,
              mode: 'static' as ParamMode,
              type: (parsed !== null && typeof parsed === 'object' ? 'json' : typeof parsed) as ParamValueType,
              value: parsed,
            };
          }
        } catch {
          // Verbatim string below.
        }
      }
    }
    return { name: row.target, mode: 'static' as ParamMode, type: 'string' as ParamValueType, value: row.source };
  });
}

// ----- Profile parameter-group merging (Configuration Hub v4.1 E4) -----
// Mirrors internal/store/profiles.go ResolveProfileRefs: profile rows fill
// around the node's own. Both the editor's effective-config preview and
// the client-side draft validator use these so the browser shows exactly
// what the server will publish.

/** Parameter-group row arrays and the plain config keys they compile into. */
export const PARAM_GROUP_TARGETS: Record<string, string> = {
  bodyFields: 'body',
  queryFields: 'query',
  headerFields: 'headers',
  pathFields: 'pathParams',
};

/**
 * Merges node-level parameter rows over profile rows per row name: node
 * rows keep their order and win, profile rows with names the node did not
 * set follow.
 */
export function mergeParamRows(nodeRows: ParamRow[], profileRows: ParamRow[]): ParamRow[] {
  const taken = new Set(nodeRows.map((row) => row.name.trim()).filter(Boolean));
  return [...nodeRows, ...profileRows.filter((row) => !row.name.trim() || !taken.has(row.name.trim()))];
}

/**
 * Merges a profile's config under a node's config the way publish does:
 * flat per key, except parameter-group row arrays merge per row name, and
 * a node-level plain target object (query/headers/pathParams/body) drops
 * the profile's rows for that group entirely — the node said exactly what
 * the group is.
 */
export function mergeProfileConfig(
  profileConfig: Record<string, unknown> | undefined,
  nodeConfig: Record<string, unknown> | undefined,
  profileOverrides?: Record<string, boolean>,
): Record<string, unknown> {
  const profile = profileConfig ?? {};
  const node = nodeConfig ?? {};
  const merged: Record<string, unknown> = { ...profile };
  for (const [key, value] of Object.entries(node)) {
    const profileSuppliesKey = Object.prototype.hasOwnProperty.call(profile, key);
    const explicitOverride = profileOverrides?.[key] === true;
    if (profileOverrides === undefined || explicitOverride || !profileSuppliesKey) {
      merged[key] = value;
    }
  }
  for (const [fieldsKey, targetKey] of Object.entries(PARAM_GROUP_TARGETS)) {
    if (!Array.isArray(profile[fieldsKey])) continue;
    if (targetKey in node) {
      delete merged[fieldsKey];
      continue;
    }
    if (Array.isArray(node[fieldsKey])) {
      merged[fieldsKey] = mergeParamRows(toParamRows(node[fieldsKey]), toParamRows(profile[fieldsKey]));
    }
  }
  return merged;
}

/**
 * Mirrors internal/store/profiles.go injectBundleRefs (Phase 1 P0-2):
 * bundle references fill the node's empty reference slots — mappingRef for
 * data.map, mappingRefs groups for http.request — and anything the node set
 * itself wins. Returns a new config object; the input is untouched.
 */
export function injectBundleRefs(
  bundle: ProfileBundle | undefined,
  nodeType: string,
  nodeConfig: Record<string, unknown> | undefined,
): Record<string, unknown> {
  const config: Record<string, unknown> = { ...(nodeConfig ?? {}) };
  if (!bundle) return config;
  // Phase 4 D4-1: the bundle's script fills a code node's empty
  // scriptRef — the node's own reference wins.
  if (
    bundle.script &&
    (nodeType === 'code.run' || nodeType === 'code.python') &&
    !config.scriptRef
  ) {
    config.scriptRef = bundle.script;
  }
  for (const [group, ref] of Object.entries(bundle.mappings ?? {})) {
    if (!ref) continue;
    if (group === 'item') {
      if (nodeType === 'data.map' && !config.mappingRef) config.mappingRef = ref;
      continue;
    }
    // A node-level plain target object owns the whole group.
    if (group in config) continue;
    const refs: Record<string, unknown> = {
      ...((config.mappingRefs ?? {}) as Record<string, unknown>),
    };
    if (refs[group]) continue;
    refs[group] = ref;
    config.mappingRefs = refs;
  }
  return config;
}

/**
 * Effective schema references after bundle injection (Phase 1 P0-2): the
 * node's own ref wins; the bundle fills the empty slot. Mirrors the Go
 * schema-slot half of injectBundleRefs.
 */
export function bundleSchemaRefs(
  bundle: ProfileBundle | undefined,
  node: { inputSchemaRef?: string; outputSchemaRef?: string },
): { input: string; output: string } {
  return {
    input: node.inputSchemaRef || bundle?.schemas?.input || '',
    output: node.outputSchemaRef || bundle?.schemas?.output || '',
  };
}
