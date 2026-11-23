import type { SchemaField, Workflow, WorkflowNode } from './types';

// Shape-aware expression candidates (Configuration Hub v2, plan D15).
// Pills are layered, most trustworthy first:
//   1. contract  — fields declared on the upstream node's output contract
//   2. run       — paths observed in the upstream node's last run output
//   3. static    — the always-available scope hints ($json, $now, $env…)
// A path appears once, at its highest layer. The insert target is the
// selected node's active string field.

export interface ExpressionCandidate {
  path: string;
  source: 'contract' | 'run' | 'static';
}

/** Flatten declared contract fields into $json paths (dot/bracket notation). */
export function pathsFromSchemaFields(fields: SchemaField[], prefix = '$json'): string[] {
  const out: string[] = [];
  const walk = (list: SchemaField[], current: string) => {
    for (const field of list) {
      const path = /^[A-Za-z_$][\w$]*$/.test(field.name)
        ? `${current}.${field.name}`
        : `${current}[${JSON.stringify(field.name)}]`;
      out.push(path);
      if (field.type === 'array' && field.items) {
        out.push(`${path}[0]`);
      } else if (field.type === 'object' && field.fields) {
        walk(field.fields, path);
      }
    }
  };
  walk(fields, prefix);
  return out;
}

/** Flatten an observed output value into $json paths, bounded like collectPaths. */
export function pathsFromItem(value: unknown, prefix = '$json', out: string[] = []): string[] {
  if (out.length >= 60 || value === null || typeof value !== 'object') return out;
  const entries = Array.isArray(value)
    ? value.slice(0, 10).map((item, index) => [String(index), item] as const)
    : Object.entries(value);
  for (const [key, child] of entries) {
    const path = Array.isArray(value)
      ? `${prefix}[${key}]`
      : /^[A-Za-z_$][\w$]*$/.test(key)
        ? `${prefix}.${key}`
        : `${prefix}[${JSON.stringify(key)}]`;
    out.push(path);
    pathsFromItem(child, path, out);
    if (out.length >= 60) break;
  }
  return out;
}

/** Immediate upstream nodes feeding the selected node (edges into it). */
export function upstreamNodes(workflow: Workflow, nodeId: string): WorkflowNode[] {
  const incoming = workflow.edges
    .filter((edge) => edge.to.node === nodeId)
    .map((edge) => edge.from.node);
  const seen = new Set<string>();
  return workflow.nodes.filter((node) => {
    if (!incoming.includes(node.id) || seen.has(node.id)) return false;
    seen.add(node.id);
    return true;
  });
}

/**
 * Build the layered candidate list. Contract paths rank first, then paths
 * observed in the last run output that no contract already covers, then
 * the static scope hints. The list is capped so the pill row stays a row.
 */
export function buildExpressionCandidates(
  upstreamContracts: SchemaField[][],
  upstreamOutputs: unknown[],
  cap = 12,
): ExpressionCandidate[] {
  const seen = new Set<string>();
  const out: ExpressionCandidate[] = [];
  const push = (path: string, source: ExpressionCandidate['source']) => {
    if (seen.has(path)) return;
    seen.add(path);
    out.push({ path, source });
  };
  for (const fields of upstreamContracts) {
    for (const path of pathsFromSchemaFields(fields)) push(path, 'contract');
  }
  for (const output of upstreamOutputs) {
    for (const path of pathsFromItem(output)) push(path, 'run');
  }
  if (out.length === 0) {
    // No shape known at all: the raw scope hints carry the most signal.
    push('$json', 'static');
    push('$json.id', 'static');
    push('$json.body', 'static');
  }
  push('$now', 'static');
  push('$env', 'static');
  return out.slice(0, cap);
}
