import { describe, expect, it } from 'vitest';
import { autoArrange, computeLayout } from '../layout';
import type { Workflow } from '../types';

function wf(nodes: [string, string][], edges: [string, string][]): Workflow {
  return {
    id: 't',
    name: 't',
    version: 1,
    settings: {},
    nodes: nodes.map(([id, type]) => ({
      id,
      type,
      typeVersion: 1,
      name: id,
      position: { x: 999, y: 999 },
      config: {},
    })),
    edges: edges.map(([from, to]) => ({
      from: { node: from, port: 'main' },
      to: { node: to, port: 'main' },
    })),
  } as unknown as Workflow;
}

function overlaps(workflow: Workflow): boolean {
  const boxes = workflow.nodes.map((node) => ({
    id: node.id,
    x: node.position.x,
    y: node.position.y,
    w: 240,
    h: 120,
  }));
  for (let i = 0; i < boxes.length; i += 1) {
    for (let j = i + 1; j < boxes.length; j += 1) {
      const a = boxes[i];
      const b = boxes[j];
      if (Math.abs(a.x - b.x) < a.w && Math.abs(a.y - b.y) < a.h) return true;
    }
  }
  return false;
}

describe('autoArrange', () => {
  it('lays out a linear graph left to right with triggers first', () => {
    const out = autoArrange(wf([['t', 'trigger.manual'], ['a', 'http.request'], ['b', 'http.request']], [['t', 'a'], ['a', 'b']]));
    expect(out.nodes.map((n) => n.position.x)).toEqual([0, 360, 720]);
    expect(out.nodes.every((n) => n.position.y === 0)).toBe(true);
  });

  it('separates branch rows vertically', () => {
    const out = autoArrange(
      wf(
        [['t', 'trigger.manual'], ['x', 'code.python'], ['a', 'http.request'], ['b', 'http.request'], ['m', 'code.python']],
        [['t', 'x'], ['x', 'a'], ['x', 'b'], ['a', 'm'], ['b', 'm']],
      ),
    );
    const a = out.nodes.find((n) => n.id === 'a')!;
    const b = out.nodes.find((n) => n.id === 'b')!;
    expect(Math.abs(a.position.y - b.position.y)).toBe(170);
    expect(overlaps(out)).toBe(false);
  });

  it('is deterministic and stable across repeated runs', () => {
    const graph = wf(
      [['t', 'trigger.manual'], ['x', 'code.python'], ['a', 'http.request'], ['b', 'http.request']],
      [['t', 'x'], ['x', 'a'], ['x', 'b'], ['b', 'a']],
    );
    const first = computeLayout(graph);
    const second = computeLayout(autoArrange(graph));
    for (const [id, pos] of first) {
      expect(second.get(id)).toEqual(pos);
    }
    const out1 = JSON.stringify(autoArrange(graph).nodes.map((n) => [n.id, n.position]));
    const out2 = JSON.stringify(autoArrange(graph).nodes.map((n) => [n.id, n.position]));
    expect(out1).toBe(out2);
  });

  it('never overlaps on a deliberately overlapping input graph', () => {
    const out = autoArrange(
      wf(
        [['t', 'trigger.manual'], ['a', 'http.request'], ['b', 'http.request'], ['c', 'code.python']],
        [['t', 'a'], ['t', 'b'], ['t', 'c'], ['a', 'b']],
      ),
    );
    expect(overlaps(out)).toBe(false);
  });

  it('keeps loop back-edge sources visually separated below their level', () => {
    // a -> b, b loops back to a: the back-edge source must not share the
    // exact cell of its level and must not overlap the target.
    const out = autoArrange(wf([['a', 'http.request'], ['b', 'code.python']], [['a', 'b'], ['b', 'a']]));
    expect(overlaps(out)).toBe(false);
    const a = out.nodes.find((n) => n.id === 'a')!;
    const b = out.nodes.find((n) => n.id === 'b')!;
    expect(Math.abs(a.position.y - b.position.y)).toBeGreaterThanOrEqual(170);
  });

  it('leaves isolated nodes on the grid without overlapping', () => {
    const out = autoArrange(
      wf([['solo', 'code.python'], ['t', 'trigger.manual'], ['a', 'http.request']], [['t', 'a']]),
    );
    expect(overlaps(out)).toBe(false);
    expect(out.nodes.every((n) => n.position.x % 360 === 0 && n.position.y % 170 === 0)).toBe(true);
  });
});
