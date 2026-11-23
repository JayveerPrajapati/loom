import { describe, expect, it } from 'vitest';
import { collectNodeIssues, issueCategory } from '../inspectorIssues';
import type { RunEvent, ValidationIssue, WorkflowNode } from '../types';

function node(extra: Partial<WorkflowNode> = {}): WorkflowNode {
  return {
    id: 'n1',
    type: 'http.request',
    typeVersion: 1,
    name: 'Fetch',
    position: { x: 0, y: 0 },
    config: {},
    ...extra,
  };
}

function issue(partial: Partial<ValidationIssue>): ValidationIssue {
  return { severity: 'error', code: 'missing_config', message: 'problem', ...partial };
}

describe('issueCategory', () => {
  it('maps each publish blocker to its user-facing category', () => {
    expect(issueCategory(issue({ code: 'missing_profile' }), 'http.request')).toBe('profile');
    expect(issueCategory(issue({ code: 'missing_schema' }), 'http.request')).toBe('contract');
    expect(issueCategory(issue({ code: 'missing_mapping' }), 'http.request')).toBe('asset');
    expect(issueCategory(issue({ code: 'missing_credential' }), 'http.request')).toBe('credential');
    expect(issueCategory(issue({ code: 'invalid_config' }), 'http.request')).toBe('config');
  });

  it('separates connector node types from generic graph failures', () => {
    expect(issueCategory(issue({ code: 'unknown_node_type' }), 'connector.crm.list')).toBe(
      'connector',
    );
    expect(issueCategory(issue({ code: 'unknown_node_type' }), 'http.request')).toBe('graph');
  });
});

describe('collectNodeIssues', () => {
  it('keeps only issues that touch the selected node', () => {
    const collected = collectNodeIssues({
      node: node(),
      runEvents: [],
      issues: [
        issue({ code: 'missing_config', node: 'n1', field: 'url', message: 'this node' }),
        issue({ code: 'missing_config', node: 'n2', field: 'url', message: 'another node' }),
        issue({ code: 'unknown_node', node: undefined, message: 'workflow-level' }),
        issue({
          code: 'unknown_port',
          node: 'n1',
          message: 'edge issue',
          edge: {
            from: { node: 'n1', port: 'main' },
            to: { node: 'n2', port: 'main' },
          },
        }),
      ],
    });
    expect(collected.map((entry) => entry.message)).toEqual(['this node', 'edge issue']);
  });

  it('carries the field and labels errors as publish blockers', () => {
    const collected = collectNodeIssues({
      node: node(),
      runEvents: [],
      issues: [
        issue({ code: 'missing_config', node: 'n1', field: 'url', message: 'url required' }),
        issue({
          severity: 'warning',
          code: 'missing_node_name',
          node: 'n1',
          message: 'no display name',
        }),
      ],
    });
    expect(collected[0]).toMatchObject({
      severity: 'error',
      category: 'config',
      field: 'url',
      source: 'Blocks publish',
    });
    expect(collected[1]).toMatchObject({
      severity: 'warning',
      category: 'graph',
      source: 'Warning',
    });
  });

  it('adds the latest runtime failure for this node', () => {
    const events: RunEvent[] = [
      { seq: 1, type: 'node.failed', at: '2026-01-01T00:00:00Z', nodeId: 'n1', error: 'old' },
      { seq: 2, type: 'node.finished', at: '2026-01-01T00:00:01Z', nodeId: 'n1' },
      { seq: 3, type: 'node.failed', at: '2026-01-01T00:00:02Z', nodeId: 'n1', error: 'latest' },
      { seq: 4, type: 'node.failed', at: '2026-01-01T00:00:03Z', nodeId: 'n2', error: 'other' },
    ];
    const collected = collectNodeIssues({ node: node(), issues: [], runEvents: events });
    expect(collected).toHaveLength(1);
    expect(collected[0]).toMatchObject({
      category: 'runtime',
      source: 'Last run',
      message: 'latest',
    });
  });

  it('adds the Python gate only when the server lacks the executor', () => {
    const python = node({ type: 'code.python' });
    expect(
      collectNodeIssues({ node: python, issues: [], runEvents: [], subprocessEnabled: undefined }),
    ).toHaveLength(0);
    expect(
      collectNodeIssues({ node: python, issues: [], runEvents: [], subprocessEnabled: true }),
    ).toHaveLength(0);
    const gated = collectNodeIssues({
      node: python,
      issues: [],
      runEvents: [],
      subprocessEnabled: false,
    });
    expect(gated).toHaveLength(1);
    expect(gated[0]).toMatchObject({ category: 'python', source: 'Server', severity: 'error' });
  });

  it('dedupes identical issues and sorts errors before warnings', () => {
    const collected = collectNodeIssues({
      node: node(),
      runEvents: [],
      issues: [
        issue({
          severity: 'warning',
          code: 'unreachable',
          node: 'n1',
          message: 'unreachable',
        }),
        issue({ code: 'missing_config', node: 'n1', field: 'url', message: 'choose a url' }),
        issue({ code: 'missing_config', node: 'n1', field: 'url', message: 'choose a url' }),
      ],
    });
    expect(collected).toHaveLength(2);
    expect(collected[0].severity).toBe('error');
    expect(collected[1].severity).toBe('warning');
  });
});
