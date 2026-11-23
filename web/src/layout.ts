import type { Workflow, WorkflowNode } from './types';

// Deterministic auto-arrange (UX Phase 1.3). The grid contract matches the
// bundled-example geometry test: 360px columns, 170px rows, triggers
// leftmost via longest-path leveling. Nodes keep a stable order (the
// workflow's node order), so the same graph always arranges identically.
const COLUMN_STEP = 360;
const ROW_STEP = 170;

/**
 * Computes one deterministic layout for the graph: every node lands on the
 * grid cell (level, row); levels come from longest-path layering so triggers
 * sit left and fan-outs fan right; rows stack same-level nodes top to bottom
 * in stable node order. Back edges (loops) pull their source down one extra
 * row so the loop path reads separately instead of crossing every edge.
 */
export function computeLayout(workflow: Workflow): Map<string, { x: number; y: number }> {
  const ids = workflow.nodes.map((node) => node.id);
  const succ = new Map<string, string[]>(ids.map((id) => [id, []]));
  const indeg = new Map<string, number>(ids.map((id) => [id, 0]));
  for (const edge of workflow.edges) {
    const from = edge.from.node;
    const to = edge.to.node;
    if (!succ.has(from) || !succ.has(to)) continue;
    succ.get(from)!.push(to);
    indeg.set(to, (indeg.get(to) ?? 0) + 1);
  }

  // Kahn longest-path leveling; cycle members keep the level their
  // non-cycle predecessors give them, so loops never block termination.
  const level = new Map<string, number>(ids.map((id) => [id, 0]));
  const queue = ids.filter((id) => (indeg.get(id) ?? 0) === 0);
  const pending = new Map(indeg);
  while (queue.length > 0) {
    const id = queue.shift()!;
    for (const next of succ.get(id) ?? []) {
      level.set(next, Math.max(level.get(next) ?? 0, (level.get(id) ?? 0) + 1));
      const left = (pending.get(next) ?? 1) - 1;
      pending.set(next, left);
      if (left === 0) queue.push(next);
    }
  }

  const positions = new Map<string, { x: number; y: number }>();
  const rowsUsed = new Map<number, number>();
  const placedIds: string[] = [];
  for (const node of workflow.nodes) {
    const lvl = level.get(node.id) ?? 0;
    const row = rowsUsed.get(lvl) ?? 0;
    rowsUsed.set(lvl, row + 1);
    positions.set(node.id, { x: lvl * COLUMN_STEP, y: row * ROW_STEP });
    placedIds.push(node.id);
  }

  // Loop separation: a node with an edge to an equal-or-earlier level is a
  // back edge; drop its source one extra row below its level's stack so
  // the return path stays visually distinct.
  const backEdgeSources = new Set<string>();
  for (const edge of workflow.edges) {
    const from = edge.from.node;
    const to = edge.to.node;
    if (!level.has(from) || !level.has(to)) continue;
    if ((level.get(to) ?? 0) <= (level.get(from) ?? 0)) backEdgeSources.add(from);
  }
  if (backEdgeSources.size > 0) {
    const extra = new Map<string, { x: number; y: number }>();
    for (const node of workflow.nodes) {
      if (!backEdgeSources.has(node.id)) continue;
      const base = positions.get(node.id)!;
      extra.set(node.id, { x: base.x, y: base.y + (rowsUsed.get(level.get(node.id) ?? 0) ?? 1) * ROW_STEP });
    }
    for (const [id, pos] of extra) positions.set(id, pos);
  }

  return positions;
}

/** Returns a copy of the workflow with every node placed on the grid. */
export function autoArrange(workflow: Workflow): Workflow {
  const positions = computeLayout(workflow);
  return {
    ...workflow,
    nodes: workflow.nodes.map((node) => ({
      ...node,
      position: positions.get(node.id) ?? { ...node.position },
    })) as WorkflowNode[],
  };
}
