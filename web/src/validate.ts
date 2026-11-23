import type { Mapping } from './types';
import { defaultsFor, outputType } from './catalog';
import { validateEgressEntry } from './egress';
import { bundleSchemaRefs, injectBundleRefs, mergeProfileConfig } from './paramRows';
import type {
  ConfigProfile,
  DataSchema,
  NodeDef,
  PortType,
  ValidationIssue,
  Workflow,
  WorkflowEdge,
} from './types';

function compatible(source: PortType, target: PortType): { ok: boolean; warning?: string } {
  if (source === 'any' || target === 'any') return { ok: true };
  if (source === target) return { ok: true };
  if (source === 'json') return { ok: true, warning: 'JSON narrows into a typed input; add a typed producer' };
  if (target === 'json') return { ok: true };
  return { ok: false };
}

function fieldMatches(value: unknown, type: string): boolean {
  switch (type) {
    case 'string':
      return typeof value === 'string';
    case 'number':
      return typeof value === 'number' && Number.isFinite(value);
    case 'boolean':
      return typeof value === 'boolean';
    case 'array':
      return Array.isArray(value);
    case 'object':
      return value !== null && typeof value === 'object' && !Array.isArray(value);
    default:
      return true;
  }
}

export function validateWorkflow(
  workflow: Workflow,
  defs: Record<string, NodeDef>,
  profiles: Record<string, ConfigProfile> = {},
  schemas: Record<string, DataSchema> = {},
  mappings: Record<string, Mapping> = {},
): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const error = (
    code: string,
    message: string,
    node?: string,
    edge?: WorkflowEdge,
    field?: string,
  ) => issues.push({ severity: 'error', code, message, node, edge, field });
  const warning = (
    code: string,
    message: string,
    node?: string,
    edge?: WorkflowEdge,
    field?: string,
  ) => issues.push({ severity: 'warning', code, message, node, edge, field });

  if (!workflow.id.trim()) error('missing_id', 'Workflow id is required');
  if (!workflow.name.trim()) error('missing_name', 'Workflow name is required');
  if (workflow.version < 1) error('invalid_version', 'Workflow version must be at least 1');
  if (workflow.nodes.length === 0) error('no_nodes', 'Workflow must contain at least one node');

  // Egress allowlist entries must mirror the server's egress.ValidateEntry,
  // so autosave skips an invalid doc and a manual save names the bad entry
  // instead of the server 400 being swallowed silently.
  const egressEntries = (workflow.egress?.allowlist as string[] | undefined) ?? [];
  for (const entry of egressEntries) {
    const problem = validateEgressEntry(entry);
    if (problem) error('invalid_egress', problem);
  }

  // Workflow-level contracts (subflow surface): reference integrity like
  // every other schema ref.
  for (const [ref, label] of [
    [workflow.inputSchemaRef, 'input'],
    [workflow.outputSchemaRef, 'output'],
  ] as const) {
    if (ref && !schemas[ref]) {
      error('missing_schema', `Workflow references missing ${label} data contract “${ref}”`);
    }
  }

  const byId = new Map<string, (typeof workflow.nodes)[number]>();
  for (const node of workflow.nodes) {
    if (!node.id.trim()) {
      error('missing_node_id', 'Every node needs an id');
      continue;
    }
    if (byId.has(node.id)) error('duplicate_node', `Node id “${node.id}” is used more than once`, node.id);
    byId.set(node.id, node);

    const def = defs[node.type];
    if (!def) {
      error('unknown_node_type', `Unknown node type “${node.type}”`, node.id);
      continue;
    }
    if (node.typeVersion !== def.typeVersion) {
      error(
        'unsupported_type_version',
        `${def.type} supports typeVersion ${def.typeVersion}, got ${node.typeVersion}`,
        node.id,
      );
    }
    if (!node.name.trim()) warning('missing_node_name', 'Node has no display name', node.id);

    // Configuration Hub: a node with a profile carries required config via
    // its profile, so validation runs against the resolved (profile +
    // override) config — the same contract the server enforces at save and
    // publish (plan §18 D2). Parameter-group rows merge per row name
    // (v4.1 E4), exactly like the server's ResolveProfileRefs.
    const profile = node.profileRef ? profiles[node.profileRef] : undefined;
    if (node.profileRef && !profile) {
      error('missing_profile', `Node references missing config profile “${node.profileRef}”`, node.id, undefined, 'profileRef');
    }
    if (profile && profile.nodeType !== node.type) {
      error(
        'missing_profile',
        `Node type “${node.type}” cannot use profile “${node.profileRef}” (for “${profile.nodeType}”)`,
        node.id,
        undefined,
        'profileRef',
      );
    }
    const config = mergeProfileConfig(profile?.config, node.config ?? {}, node.profileOverrides);
    for (const [key, value] of Object.entries(defaultsFor(def))) {
      if (!Object.prototype.hasOwnProperty.call(config, key)) config[key] = value;
    }
    // Phase 1 P0-2: bundle references fill the node's empty slots — the
    // effective refs are what the server validates at save and publish.
    const bundleRefs = injectBundleRefs(profile?.bundle, node.type, node.config ?? {});
    // Data contracts: a dangling schema reference is a named error, the
    // same reference integrity the server enforces at save and publish.
    const effectiveSchemas = bundleSchemaRefs(profile?.bundle, node);
    for (const [ref, label] of [
      [effectiveSchemas.input, 'input'],
      [effectiveSchemas.output, 'output'],
    ] as const) {
      if (ref && !schemas[ref]) {
        error('missing_schema', `Node references missing ${label} data contract “${ref}”`, node.id, undefined, `${label}SchemaRef`);
      }
    }
    // Mappings: a data.map node's mappingRef must resolve (reference
    // integrity, the same contract the server enforces at save and publish).
    if (node.type === 'data.map') {
      const ref = typeof bundleRefs.mappingRef === 'string' ? bundleRefs.mappingRef : '';
      if (!ref) {
        error('missing_mapping', 'data.map requires a mapping', node.id, undefined, 'mappingRef');
      } else if (!mappings[ref]) {
        error('missing_mapping', `Node references missing mapping “${ref}”`, node.id, undefined, 'mappingRef');
      }
    }
    // http.request parameter groups: every effective mapping ref — the
    // node's own or the bundle-injected one — must resolve with a
    // kind match.
    for (const [group, rawRef] of Object.entries(
      (bundleRefs.mappingRefs ?? {}) as Record<string, unknown>,
    )) {
      if (typeof rawRef !== 'string' || !rawRef) continue;
      const mapping = mappings[rawRef];
      if (!mapping) {
        error('missing_mapping', `Node references missing mapping “${rawRef}”`, node.id, undefined, `mappingRefs.${group}`);
      } else if (mapping.kind && mapping.kind !== group) {
        error(
          'missing_mapping',
          `Mapping “${mapping.name}” is kind ${mapping.kind} but slot “${group}” needs ${group}`,
          node.id,
          undefined,
          `mappingRefs.${group}`,
        );
      }
    }
    for (const [key, spec] of Object.entries(def.config ?? {})) {
      const present = Object.prototype.hasOwnProperty.call(config, key);
      if (!present) {
        if (spec.required) error('missing_config', `${def.type} requires config “${key}”`, node.id, undefined, key);
        continue;
      }
      const value = config[key];
      if (!fieldMatches(value, spec.type)) {
        error('invalid_config', `Config “${key}” must be a ${spec.type}`, node.id, undefined, key);
        continue;
      }
      if (spec.enum && !spec.enum.includes(String(value))) {
        error(
          'invalid_config',
          `Config “${key}” must be one of ${spec.enum.join(', ')}; got “${String(value)}”`,
          node.id,
          undefined,
          key,
        );
      }
    }
    for (const key of Object.keys(config)) {
      if (def.config && !(key in def.config)) {
        error('unknown_config', `${def.type} has no config field “${key}”`, node.id, undefined, key);
      }
    }
    for (const credential of def.credentials ?? []) {
      if ((def.optionalCredentials ?? []).includes(credential)) continue;
      if (!node.credentials?.[credential]?.trim()) {
        error('missing_credential', `${def.type} requires a “${credential}” credential`, node.id, undefined, `credential:${credential}`);
      }
    }
    for (const key of Object.keys(node.credentials ?? {})) {
      if (!(def.credentials ?? []).includes(key) && !(def.optionalCredentials ?? []).includes(key)) {
        warning('unknown_credential', `${def.type} does not use credential “${key}”`, node.id, undefined, `credential:${key}`);
      }
    }
  }

  const incoming = new Map<string, number>();
  for (const edge of workflow.edges) {
    const source = byId.get(edge.from.node);
    const target = byId.get(edge.to.node);
    if (!source) error('unknown_node', `Edge source “${edge.from.node}” does not exist`, undefined, edge);
    if (!target) error('unknown_node', `Edge target “${edge.to.node}” does not exist`, undefined, edge);
    if (edge.from.node === edge.to.node) {
      error('self_edge', 'A node cannot connect to itself', edge.from.node, edge);
    }
    const sourceDef = source ? defs[source.type] : undefined;
    const targetDef = target ? defs[target.type] : undefined;
    if (!sourceDef || !targetDef) continue;

    if (!sourceDef.outputs.includes(edge.from.port)) {
      error(
        'unknown_port',
        `${sourceDef.type} has no output port “${edge.from.port}” (have ${sourceDef.outputs.join(', ')})`,
        edge.from.node,
        edge,
      );
    }
    if (!targetDef.inputs.includes(edge.to.port)) {
      error(
        'unknown_port',
        `${targetDef.type} has no input port “${edge.to.port}” (have ${targetDef.inputs.join(', ')})`,
        edge.to.node,
        edge,
      );
    }
    const key = `${edge.to.node}\u0000${edge.to.port}`;
    const count = (incoming.get(key) ?? 0) + 1;
    incoming.set(key, count);
    if (count > 1) {
      error(
        'duplicate_input',
        `Input port “${edge.to.port}” already has a connection; use a merge node`,
        edge.to.node,
        edge,
      );
    }
    if (targetDef.category === 'trigger') {
      error('trigger_input', 'Trigger nodes cannot have incoming edges', edge.to.node, edge);
    }

    const sourceType = outputType(sourceDef, source?.config ?? {});
    const result = compatible(sourceType, targetDef.inputType);
    if (!result.ok) {
      error(
        'type_mismatch',
        `${sourceDef.type} produces ${sourceType} but ${targetDef.type} expects ${targetDef.inputType}`,
        edge.to.node,
        edge,
      );
    } else if (result.warning) {
      warning('untyped_edge', result.warning, undefined, edge);
    }
  }

  const triggers = workflow.nodes.filter((node) => defs[node.type]?.category === 'trigger');
  if (triggers.length === 0) error('no_trigger', 'Workflow needs exactly one trigger node');
  if (triggers.length > 1) {
    error(
      'multiple_triggers',
      `Workflow needs exactly one trigger node; found ${triggers.map((node) => node.id).join(', ')}`,
    );
  }

  const nodeRef = /\$node\s*\[\s*["']([^"']+)["']\s*\]/g;
  const ancestors = new Map<string, Set<string>>();
  for (const node of workflow.nodes) {
    const seen = new Set<string>();
    const stack = workflow.edges.filter((edge) => edge.to.node === node.id).map((edge) => edge.from.node);
    while (stack.length > 0) {
      const id = stack.pop() as string;
      if (seen.has(id)) continue;
      seen.add(id);
      for (const edge of workflow.edges) if (edge.to.node === id) stack.push(edge.from.node);
    }
    ancestors.set(node.id, seen);
  }
  for (const node of workflow.nodes) {
    const refs = new Set<string>();
    const visit = (value: unknown) => {
      if (typeof value === 'string') {
        for (const match of value.matchAll(nodeRef)) refs.add(match[1]);
      } else if (Array.isArray(value)) {
        value.forEach(visit);
      } else if (value && typeof value === 'object') {
        Object.values(value).forEach(visit);
      }
    };
    visit(node.config);
    for (const ref of refs) {
      if (ref === node.id) {
        error('node_self_reference', `Node “${node.name}” cannot reference its own output`, node.id);
      } else if (!byId.has(ref)) {
        error('unknown_node_reference', `Expression references unknown node “${ref}”`, node.id);
      } else if (!ancestors.get(node.id)?.has(ref)) {
        error('node_reference_not_upstream', `Expression references “${ref}”, which is not upstream of this node`, node.id);
      }
    }
  }

  if (hasCycle(workflow)) error('cycle', 'Workflow graph must be a DAG; a cycle was found');

  if (triggers.length > 0) {
    const reachable = new Set<string>();
    const queue = triggers.map((node) => node.id);
    while (queue.length > 0) {
      const id = queue.shift() as string;
      if (reachable.has(id)) continue;
      reachable.add(id);
      for (const edge of workflow.edges) {
        if (edge.from.node === id) queue.push(edge.to.node);
      }
    }
    for (const node of workflow.nodes) {
      if (!reachable.has(node.id)) {
        warning('unreachable', 'Node is not reachable from a trigger', node.id);
      }
    }
  }

  return issues;
}

function hasCycle(workflow: Workflow): boolean {
  const adjacency = new Map<string, string[]>();
  for (const edge of workflow.edges) {
    const list = adjacency.get(edge.from.node) ?? [];
    list.push(edge.to.node);
    adjacency.set(edge.from.node, list);
  }
  const visiting = new Set<string>();
  const visited = new Set<string>();
  const visit = (id: string): boolean => {
    if (visiting.has(id)) return true;
    if (visited.has(id)) return false;
    visiting.add(id);
    for (const next of adjacency.get(id) ?? []) {
      if (visit(next)) return true;
    }
    visiting.delete(id);
    visited.add(id);
    return false;
  };
  return workflow.nodes.some((node) => visit(node.id));
}
