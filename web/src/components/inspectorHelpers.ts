import type { ValidationIssue } from '../types';
import type { ConfigProfile, DataSchema, Mapping, Script } from '../types';

export function labelFor(key: string): string {
  return key
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/^./, (char) => char.toUpperCase());
}

/** Compact display for a config value in hints and previews. */
export function formatConfigValue(value: unknown): string {
  if (value === undefined) return '';
  if (value === null) return 'null';
  if (typeof value === 'string') return value;
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  try {
    const text = JSON.stringify(value);
    return text.length > 90 ? `${text.slice(0, 87)}…` : text;
  } catch {
    return String(value);
  }
}

export function collectFieldPaths(value: unknown, prefix: string, out: string[], depth = 0): void {
  if (depth > 3) return;
  if (value === null || value === undefined) {
    if (prefix) out.push(prefix);
    return;
  }
  if (Array.isArray(value)) {
    if (prefix) out.push(prefix);
    if (value.length > 0) collectFieldPaths(value[0], `${prefix}[0]`, out, depth + 1);
    return;
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length === 0 && prefix) out.push(prefix);
    for (const [key, inner] of entries) {
      const path = prefix ? `${prefix}.${key}` : key;
      collectFieldPaths(inner, path, out, depth + 1);
    }
    return;
  }
  if (prefix) out.push(prefix);
}

export function expressionFor(nodeId: string, port: string, path: string): string {
  const base = port === 'main' ? `$node["${nodeId}"].json` : `$node["${nodeId}"].ports["${port}"]`;
  const suffix = path ? (path.startsWith('[') ? path : `.${path}`) : '';
  return `{{ ${base}${suffix} }}`;
}

export function issueTouchesNode(issue: ValidationIssue, nodeId: string): boolean {
  if (issue.node === nodeId) return true;
  if (issue.edge) return issue.edge.from.node === nodeId || issue.edge.to.node === nodeId;
  return false;
}

/** Human labels for the assets a pinned profile bundle composes. The
 * summary shows names (not opaque ids) so the node is understandable
 * without opening the profile. */
export function bundleAssetLabels(
  profile: ConfigProfile | null,
  mappings: Mapping[],
  schemas: DataSchema[],
  scripts: Script[],
): string[] {
  const bundle = profile?.bundle;
  if (!bundle) return [];
  const labels: string[] = [];
  for (const [slot, id] of Object.entries(bundle.mappings ?? {})) {
    const mapping = mappings.find((candidate) => candidate.id === id);
    labels.push(`${mapping?.name ?? id} (${slot})`);
  }
  for (const [slot, id] of Object.entries(bundle.schemas ?? {})) {
    const schema = schemas.find((candidate) => candidate.id === id);
    labels.push(`${schema?.name ?? id} (${slot})`);
  }
  if (bundle.script) {
    const script = scripts.find((candidate) => candidate.id === bundle.script);
    labels.push(`${script?.name ?? bundle.script} (script)`);
  }
  return labels;
}
