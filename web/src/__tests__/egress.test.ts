import { describe, expect, it } from 'vitest';
import { validateEgressEntry } from '../egress';
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
const defs: Record<string, NodeDef> = { 'trigger.manual': trigger };

function workflow(egress?: { allowlist: string[] }): Workflow {
  const nodes: WorkflowNode[] = [
    {
      id: 't',
      type: 'trigger.manual',
      typeVersion: 1,
      name: 't',
      position: { x: 0, y: 0 },
      config: {},
    },
  ];
  return { id: 'w', name: 'w', version: 1, nodes, edges: [], egress };
}

describe('validateEgressEntry (mirror of egress.ValidateEntry)', () => {
  it('accepts every valid form', () => {
    expect(validateEgressEntry('https://api.github.com')).toBeNull();
    expect(validateEgressEntry('http://insecure.internal:8080')).toBeNull();
    expect(validateEgressEntry('https://*.slack.com')).toBeNull();
    expect(validateEgressEntry('postgres://db.internal:5432')).toBeNull();
    expect(validateEgressEntry('postgres://db.internal:5432/')).toBeNull();
    expect(validateEgressEntry('  https://api.github.com  ')).toBeNull();
  });

  it('rejects entries the server rejects, naming the entry', () => {
    expect(validateEgressEntry('api.github.com')).toMatch(/must start with https/);
    expect(validateEgressEntry('')).toMatch(/empty/);
    expect(validateEgressEntry('ftp://files.example.com')).toMatch(/scheme must be http/);
    expect(validateEgressEntry('https://api.github.com/repos/loom')).toMatch(
      /paths are not allowed/,
    );
    expect(validateEgressEntry('https://*.slack.com:443/base')).toMatch(/paths are not allowed/);
    expect(validateEgressEntry('https://x.*.example.com')).toMatch(
      /wildcard only allowed as a leading/,
    );
    expect(validateEgressEntry('https://a.*.b.*.example.com')).toMatch(
      /wildcard only allowed as a leading/,
    );
  });
});

describe('validateWorkflow egress wiring', () => {
  it('flags invalid egress entries as invalid_egress errors', () => {
    const issues = validateWorkflow(
      workflow({ allowlist: ['https://api.github.com', 'no-scheme.example.com'] }),
      defs,
    );
    expect(issues).toHaveLength(1);
    expect(issues[0].code).toBe('invalid_egress');
    expect(issues[0].severity).toBe('error');
    expect(issues[0].message).toContain('no-scheme.example.com');
  });

  it('accepts a fully valid allowlist', () => {
    expect(validateWorkflow(workflow({ allowlist: ['https://a.example.com', 'https://*.b.example.com'] }), defs)).toEqual([]);
  });
});
