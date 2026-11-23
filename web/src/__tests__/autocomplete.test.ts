import { describe, expect, it } from 'vitest';
import {
  buildExpressionCandidates,
  pathsFromItem,
  pathsFromSchemaFields,
  upstreamNodes,
} from '../autocomplete';
import type { Workflow, WorkflowNode } from '../types';

const node = (id: string, over: Partial<WorkflowNode> = {}): WorkflowNode =>
  ({
    id,
    type: 'data.set',
    typeVersion: 1,
    name: id,
    config: {},
    ...over,
  }) as WorkflowNode;

const flow = (...nodes: WorkflowNode[]): Workflow => ({
  id: 'wf',
  name: 'wf',
  version: 1,
  nodes,
  edges: nodes.slice(1).map((n, i) => ({
    from: { node: nodes[i].id, port: 'main' },
    to: { node: n.id, port: 'main' },
  })),
});

describe('pathsFromSchemaFields', () => {
  it('flattens nested object and array item fields', () => {
    expect(
      pathsFromSchemaFields([
        { name: 'email', type: 'string' },
        {
          name: 'customer',
          type: 'object',
          fields: [
            { name: 'name', type: 'string' },
            { name: 'tags', type: 'array', items: { name: 'tag', type: 'string' } },
          ],
        },
      ]),
    ).toEqual([
      '$json.email',
      '$json.customer',
      '$json.customer.name',
      '$json.customer.tags',
      '$json.customer.tags[0]',
    ]);
  });

  it('brackets keys that are not identifiers', () => {
    expect(pathsFromSchemaFields([{ name: 'first name', type: 'string' }])).toEqual([
      '$json["first name"]',
    ]);
  });
});

describe('upstreamNodes', () => {
  it('returns the direct feeders, deduplicated, in graph order', () => {
    const a = node('a');
    const b = node('b');
    const c = node('c');
    const wf = flow(a, b, c);
    expect(upstreamNodes(wf, 'c')).toEqual([b]);
    expect(upstreamNodes(wf, 'a')).toEqual([]);
    const diamond: Workflow = {
      ...wf,
      edges: [
        { from: { node: 'a', port: 'main' }, to: { node: 'c', port: 'main' } },
        { from: { node: 'b', port: 'main' }, to: { node: 'c', port: 'main' } },
        { from: { node: 'a', port: 'main' }, to: { node: 'c', port: 'main' } },
      ],
    };
    expect(upstreamNodes(diamond, 'c').map((n) => n.id)).toEqual(['a', 'b']);
  });
});

describe('buildExpressionCandidates', () => {
  it('layers contract paths first, then run-only paths, then static', () => {
    const candidates = buildExpressionCandidates(
      [[{ name: 'email', type: 'string' }]],
      [{ email: 'x', observedOnly: 1 }],
    );
    expect(candidates.slice(0, 3)).toEqual([
      { path: '$json.email', source: 'contract' },
      { path: '$json.observedOnly', source: 'run' },
      { path: '$now', source: 'static' },
    ]);
  });

  it('falls back to static hints when no shape is known', () => {
    const paths = buildExpressionCandidates([], []).map((c) => c.path);
    expect(paths.slice(0, 5)).toEqual(['$json', '$json.id', '$json.body', '$now', '$env']);
  });

  it('dedupes across layers and caps the list', () => {
    const candidates = buildExpressionCandidates(
      [Array.from({ length: 20 }, (_, i) => ({ name: `f${i}`, type: 'string' as const }))],
      [],
    );
    expect(candidates).toHaveLength(12);
    expect(new Set(candidates.map((c) => c.path)).size).toBe(12);
  });
});

describe('pathsFromItem', () => {
  it('bounds recursion like collectPaths', () => {
    const deep: Record<string, unknown> = {};
    let cur = deep;
    for (let i = 0; i < 100; i++) {
      cur.next = { i };
      cur = cur.next as Record<string, unknown>;
    }
    expect(pathsFromItem(deep).length).toBeLessThanOrEqual(60);
  });
});
