import { describe, expect, it } from 'vitest';
import { validateWorkflow } from '../validate';
import type { NodeDef, Workflow, WorkflowNode } from '../types';

const trigger: NodeDef = {
  type: 'trigger.manual',
  typeVersion: 1,
  category: 'trigger',
  description: '',
  inputs: [],
  outputs: ['out'],
  inputType: 'any',
  outputType: 'json',
};

const echo: NodeDef = {
  type: 'data.echo',
  typeVersion: 2,
  category: 'data',
  description: '',
  inputs: ['in'],
  outputs: ['out'],
  inputType: 'json',
  outputType: 'json',
  config: { message: { type: 'string', required: true } },
};

const typed: NodeDef = {
  ...echo,
  type: 'data.typed',
  inputType: 'text',
  config: undefined,
};

const defs: Record<string, NodeDef> = {
  'trigger.manual': trigger,
  'data.echo': echo,
  'data.typed': typed,
};

let seq = 0;
function node(id: string, type: string, config: Record<string, unknown> = {}): WorkflowNode {
  seq += 1;
  return {
    id,
    type,
    typeVersion: defs[type]?.typeVersion ?? 1,
    name: id,
    position: { x: seq * 10, y: 0 },
    config,
  };
}

function workflow(nodes: WorkflowNode[], edges: Workflow['edges'] = []): Workflow {
  return { id: 'w', name: 'w', version: 1, nodes, edges };
}

function codes(issues: ReturnType<typeof validateWorkflow>): string[] {
  return issues.map((issue) => issue.code);
}

describe('validateWorkflow', () => {
  it('accepts a minimal valid workflow', () => {
    const wf = workflow(
      [node('t', 'trigger.manual'), node('e', 'data.echo', { message: 'hi' })],
      [{ from: { node: 't', port: 'out' }, to: { node: 'e', port: 'in' } }],
    );
    expect(validateWorkflow(wf, defs)).toEqual([]);
  });

  it('flags missing identity fields and an empty graph', () => {
    const issues = validateWorkflow(
      { ...workflow([]), id: ' ', name: '', version: 0 },
      defs,
    );
    expect(codes(issues)).toEqual(
      expect.arrayContaining(['missing_id', 'missing_name', 'invalid_version', 'no_nodes']),
    );
  });

  it('rejects unknown types and wrong typeVersions', () => {
    const bad = node('x', 'nope.nope');
    const wrongVersion = { ...node('t', 'data.echo'), typeVersion: 1 };
    const issues = validateWorkflow(workflow([bad, wrongVersion]), defs);
    expect(codes(issues)).toEqual(
      expect.arrayContaining(['unknown_node_type', 'unsupported_type_version']),
    );
  });

  it('enforces config presence, types, enums and unknown keys', () => {
    const missing = node('e', 'data.echo');
    const wrongType = node('e2', 'data.echo', { message: 7 });
    const extra = node('e3', 'data.echo', { message: 'ok', bogus: 1 });
    const issues = validateWorkflow(
      workflow([missing, wrongType, extra]),
      defs,
    );
    expect(codes(issues)).toEqual(
      expect.arrayContaining(['missing_config', 'invalid_config', 'unknown_config']),
    );
  });

  it('catches duplicate node ids, self edges, unknown endpoints and duplicate inputs', () => {
    const a = node('a', 'trigger.manual');
    const b = node('b', 'data.echo', { message: 'x' });
    const b2 = node('b', 'data.echo', { message: 'y' });
    const edges = [
      { from: { node: 'a', port: 'out' }, to: { node: 'b', port: 'in' } },
      { from: { node: 'a', port: 'out' }, to: { node: 'b', port: 'in' } },
      { from: { node: 'a', port: 'out' }, to: { node: 'a', port: 'in' } },
      { from: { node: 'ghost', port: 'out' }, to: { node: 'b', port: 'in' } },
    ];
    const issues = validateWorkflow(workflow([a, b, b2], edges), defs);
    expect(codes(issues)).toEqual(
      expect.arrayContaining([
        'duplicate_node',
        'duplicate_input',
        'self_edge',
        'unknown_node',
        'trigger_input',
        'unknown_port',
      ]),
    );
  });

  it('flags type mismatches and untyped json edges distinctly', () => {
    const t = node('t', 'trigger.manual');
    const e = node('e', 'data.typed');
    const issues = validateWorkflow(
      workflow([t, e], [{ from: { node: 't', port: 'out' }, to: { node: 'e', port: 'in' } }]),
      defs,
    );
    // json into a typed input is allowed with a warning, not an error.
    expect(codes(issues)).toContain('untyped_edge');
    expect(codes(issues)).not.toContain('type_mismatch');

    const stringish = {
      ...defs,
      'trigger.manual': { ...trigger, outputType: 'text' as const },
      'data.typed': { ...typed, inputType: 'rows' as const },
    };
    const issues2 = validateWorkflow(
      workflow([t, e], [{ from: { node: 't', port: 'out' }, to: { node: 'e', port: 'in' } }]),
      stringish,
    );
    expect(codes(issues2)).toContain('type_mismatch');
  });

  it('requires exactly one trigger', () => {
    const a = node('a', 'trigger.manual');
    const b = node('b', 'trigger.manual');
    const e = node('e', 'data.typed');
    expect(codes(validateWorkflow(workflow([e]), defs))).toContain('no_trigger');
    expect(
      codes(
        validateWorkflow(
          workflow(
            [a, b, e],
            [
              { from: { node: 'a', port: 'out' }, to: { node: 'e', port: 'in' } },
              { from: { node: 'b', port: 'out' }, to: { node: 'e', port: 'in' } },
            ],
          ),
          defs,
        ),
      ),
    ).toContain('multiple_triggers');
  });

  it('rejects cycles and non-upstream expression references', () => {
    const a = node('a', 'trigger.manual');
    const b = node('b', 'data.echo', { message: '{{$node["a"].output}}' });
    const c = node('c', 'data.echo', { message: '{{$node["a"].output}}' });
    const cycleEdges = [
      { from: { node: 'a', port: 'out' }, to: { node: 'b', port: 'in' } },
      { from: { node: 'b', port: 'out' }, to: { node: 'c', port: 'in' } },
      { from: { node: 'c', port: 'out' }, to: { node: 'b', port: 'in' } },
    ];
    expect(codes(validateWorkflow(workflow([a, b, c], cycleEdges), defs))).toContain('cycle');

    const selfRef = node('s', 'data.echo', { message: '{{$node["s"].output}}' });
    expect(codes(validateWorkflow(workflow([selfRef]), defs))).toContain('node_self_reference');
    expect(codes(validateWorkflow(workflow([node('x', 'data.echo', { message: '{{$node["ghost"].output}}' })]), defs))).toContain(
      'unknown_node_reference',
    );

    // b references c, but c is downstream of b in a→b→c.
    const upstream = [
      node('a', 'trigger.manual'),
      node('b', 'data.typed'),
      node('c', 'data.typed'),
    ];
    const straight = [
      { from: { node: 'a', port: 'out' }, to: { node: 'b', port: 'in' } },
      { from: { node: 'b', port: 'out' }, to: { node: 'c', port: 'in' } },
    ];
    const downstreamRef = { ...upstream[1], config: { message: '{{$node["c"].output}}' } };
    const wf = workflow([upstream[0], downstreamRef, upstream[2]], straight);
    expect(codes(validateWorkflow(wf, defs))).toContain('node_reference_not_upstream');
  });

  it('warns about unreachable nodes', () => {
    const a = node('a', 'trigger.manual');
    const orphan = node('orphan', 'data.typed');
    const issues = validateWorkflow(workflow([a, orphan]), defs);
    const unreachable = issues.find((issue) => issue.code === 'unreachable');
    expect(unreachable?.severity).toBe('warning');
    expect(unreachable?.node).toBe('orphan');
  });
});

describe('validateWorkflow with configuration profiles', () => {
  const profile = {
    id: 'profile-1',
    name: 'Echo defaults',
    nodeType: 'data.echo',
    version: 1,
    config: { message: 'from profile' },
    createdAt: '2026-11-11T00:00:00Z',
    updatedAt: '2026-11-11T00:00:00Z',
  };

  it('accepts a node whose required field arrives via its profile', () => {
    const wf = workflow(
      [node('t', 'trigger.manual'), { ...node('e', 'data.echo'), profileRef: 'profile-1' }],
      [{ from: { node: 't', port: 'out' }, to: { node: 'e', port: 'in' } }],
    );
    expect(validateWorkflow(wf, defs, { 'profile-1': profile })).toEqual([]);
  });

  it('flags a missing profile reference by id', () => {
    const wf = workflow([
      node('t', 'trigger.manual'),
      { ...node('e', 'data.echo', { message: 'x' }), profileRef: 'profile-gone' },
    ]);
    const issues = validateWorkflow(wf, defs, {});
    expect(codes(issues)).toContain('missing_profile');
    expect(issues.find((issue) => issue.code === 'missing_profile')?.node).toBe('e');
  });

  it('flags a profile pinned to a different node type', () => {
    const wf = workflow([
      node('t', 'trigger.manual'),
      { ...node('d', 'data.typed'), profileRef: 'profile-1' },
    ]);
    expect(codes(validateWorkflow(wf, defs, { 'profile-1': profile }))).toContain('missing_profile');
  });

  it('lets the node override the profile and still validates the merged value', () => {
    const wf = workflow([
      node('t', 'trigger.manual'),
      { ...node('e', 'data.echo', { message: 42 }), profileRef: 'profile-1' },
    ]);
    expect(codes(validateWorkflow(wf, defs, { 'profile-1': profile }))).toContain('invalid_config');
  });
});

describe('validateWorkflow with data contracts', () => {
  const schema = {
    id: 'schema-1',
    name: 'Order',
    version: 1,
    fields: [{ name: 'email', type: 'string' as const, required: true }],
    createdAt: '2026-11-11T00:00:00Z',
    updatedAt: '2026-11-11T00:00:00Z',
  };

  it('accepts node- and workflow-level contract refs that resolve', () => {
    const wf = workflow(
      [
        node('t', 'trigger.manual'),
        { ...node('e', 'data.echo', { message: 'x' }), outputSchemaRef: 'schema-1' },
      ],
      [{ from: { node: 't', port: 'out' }, to: { node: 'e', port: 'in' } }],
    );
    wf.inputSchemaRef = 'schema-1';
    wf.outputSchemaRef = 'schema-1';
    expect(validateWorkflow(wf, defs, {}, { 'schema-1': schema })).toEqual([]);
  });

  it('flags dangling node input/output refs by id and slot', () => {
    const wf = workflow([
      node('t', 'trigger.manual'),
      {
        ...node('e', 'data.echo', { message: 'x' }),
        inputSchemaRef: 'schema-gone',
        outputSchemaRef: 'schema-1',
      },
    ]);
    const issues = validateWorkflow(wf, defs, {}, { 'schema-1': schema });
    const missing = issues.filter((issue) => issue.code === 'missing_schema');
    expect(missing).toHaveLength(1);
    expect(missing[0].message).toContain('input');
    expect(missing[0].node).toBe('e');
  });

  it('flags dangling workflow-level refs', () => {
    const wf = workflow([node('t', 'trigger.manual')]);
    wf.outputSchemaRef = 'schema-gone';
    const issues = validateWorkflow(wf, defs, {}, {});
    expect(issues.find((issue) => issue.code === 'missing_schema')?.message).toContain('output');
  });
});
