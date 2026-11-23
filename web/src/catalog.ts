import type { NodeDef, PortType, Workflow, WorkflowEdge, WorkflowNode } from './types';

export const PORT_COLORS: Record<PortType, string> = {
  json: '#6e6e73',
  text: '#0071e3',
  event: '#bf4800',
  order: '#7d3c98',
  message: '#2c8a4b',
  rows: '#0b7285',
  any: '#8e8e93',
};

// Display names come from the backend catalog (Definition.DisplayName,
// unique per type — TestCatalogDisplayNames guards that); the raw type is
// only a last-resort fallback for a server that predates the field.
export function defaultNodeName(def: NodeDef): string {
  return def.displayName || def.type;
}

export function nodeDefMap(defs: NodeDef[]): Record<string, NodeDef> {
  return Object.fromEntries(defs.map((def) => [def.type, def]));
}

export function outputType(def: NodeDef | undefined, config: Record<string, unknown>): PortType {
  if (!def) return 'any';
  if (def.outputTypeKey) {
    const value = config[def.outputTypeKey];
    if (typeof value === 'string' && value) return value as PortType;
  }
  return def.outputType;
}

export function portCompatibility(
  source: PortType,
  target: PortType,
): { ok: boolean; warning?: string } {
  if (source === 'any' || target === 'any') return { ok: true };
  if (source === target) return { ok: true };
  if (source === 'json') return { ok: true, warning: 'JSON narrows into a typed input' };
  if (target === 'json') return { ok: true };
  return { ok: false };
}

export function defaultsFor(def: NodeDef): Record<string, unknown> {
  return Object.fromEntries(
    Object.entries(def.config ?? {}).filter(([, spec]) => spec.default !== undefined)
      .map(([key, spec]) => [key, spec.default]),
  );
}

export function edgeId(edge: WorkflowEdge): string {
  return `${edge.from.node}.${edge.from.port}->${edge.to.node}.${edge.to.port}`;
}

export function parseHandle(handle: string | null | undefined, direction: 'in' | 'out'): string {
  if (!handle) return 'main';
  const prefix = direction === 'in' ? 'in-' : 'out-';
  return handle.startsWith(prefix) ? handle.slice(prefix.length) : handle;
}

export function slug(value: string): string {
  return (
    value
      .toLowerCase()
      .replace(/[^a-z0-9]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .slice(0, 40) || 'node'
  );
}

export function uniqueNodeId(base: string, nodes: WorkflowNode[]): string {
  const root = slug(base);
  const taken = new Set(nodes.map((node) => node.id));
  if (!taken.has(root)) return root;
  let index = 2;
  while (taken.has(`${root}-${index}`)) index += 1;
  return `${root}-${index}`;
}

export function nextPosition(nodes: WorkflowNode[]): { x: number; y: number } {
  if (nodes.length === 0) return { x: 80, y: 120 };
  const maxX = Math.max(...nodes.map((node) => node.position.x));
  const anchor = nodes.find((node) => node.position.x === maxX);
  return { x: maxX + 300, y: anchor ? anchor.position.y + 40 : 120 };
}

export function makeNode(def: NodeDef, nodes: WorkflowNode[], id?: string): WorkflowNode {
  const nodeId = id ?? uniqueNodeId(def.type, nodes);
  const config: Record<string, unknown> = {};
  if (def.type === 'trigger.webhook') config.path = `webhook-${nodeId}`;
  return {
    id: nodeId,
    type: def.type,
    typeVersion: def.typeVersion,
    name: defaultNodeName(def),
    position: nextPosition(nodes),
    config,
    credentials: {},
    profileOverrides: {},
  };
}

export function nodeMap(workflow: Workflow): Record<string, WorkflowNode> {
  return Object.fromEntries(workflow.nodes.map((node) => [node.id, node]));
}

export function isTrigger(def: NodeDef | undefined): boolean {
  return def?.category === 'trigger';
}
