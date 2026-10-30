import { outputType } from './catalog';
import type { NodeDef, PortType, ValidationIssue, Workflow, WorkflowEdge } from './types';

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

export function validateWorkflow(workflow: Workflow, defs: Record<string, NodeDef>): ValidationIssue[] {
  const issues: ValidationIssue[] = [];
  const error = (code: string, message: string, node?: string, edge?: WorkflowEdge) =>
    issues.push({ severity: 'error', code, message, node, edge });
  const warning = (code: string, message: string, node?: string, edge?: WorkflowEdge) =>
    issues.push({ severity: 'warning', code, message, node, edge });

  if (!workflow.id.trim()) error('missing_id', 'Workflow id is required');
  if (!workflow.name.trim()) error('missing_name', 'Workflow name is required');
  if (workflow.version < 1) error('invalid_version', 'Workflow version must be at least 1');
  if (workflow.nodes.length === 0) error('no_nodes', 'Workflow must contain at least one node');

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

    const config = node.config ?? {};
    for (const [key, spec] of Object.entries(def.config ?? {})) {
      const present = Object.prototype.hasOwnProperty.call(config, key);
      if (!present) {
        if (spec.required) error('missing_config', `${def.type} requires config “${key}”`, node.id);
        continue;
      }
      const value = config[key];
      if (!fieldMatches(value, spec.type)) {
        error('invalid_config', `Config “${key}” must be a ${spec.type}`, node.id);
        continue;
      }
      if (spec.enum && !spec.enum.includes(String(value))) {
        error(
          'invalid_config',
          `Config “${key}” must be one of ${spec.enum.join(', ')}; got “${String(value)}”`,
          node.id,
        );
      }
    }
    for (const key of Object.keys(config)) {
      if (def.config && !(key in def.config)) {
        error('unknown_config', `${def.type} has no config field “${key}”`, node.id);
      }
    }
    for (const credential of def.credentials ?? []) {
      if ((def.optionalCredentials ?? []).includes(credential)) continue;
      if (!node.credentials?.[credential]?.trim()) {
        error('missing_credential', `${def.type} requires a “${credential}” credential`, node.id);
      }
    }
    for (const key of Object.keys(node.credentials ?? {})) {
      if (!(def.credentials ?? []).includes(key) && !(def.optionalCredentials ?? []).includes(key)) {
        warning('unknown_credential', `${def.type} does not use credential “${key}”`, node.id);
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
