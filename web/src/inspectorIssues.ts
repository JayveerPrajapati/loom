import type { RunEvent, ValidationIssue, WorkflowNode } from './types';
import { issueTouchesNode } from './components/inspectorHelpers';

// One node issue model for the inspector (Apple-Grade Phase 3 D3-4).
// Validation issues carry the field they point at; this module folds them
// together with the Python capability gate and the latest runtime failure
// so the inspector can never claim a clean node while publish would fail.

export type InspectorIssueCategory =
  | 'graph'
  | 'config'
  | 'profile'
  | 'asset'
  | 'credential'
  | 'contract'
  | 'connector'
  | 'runtime'
  | 'python';

export interface InspectorIssue {
  id: string;
  severity: 'error' | 'warning';
  category: InspectorIssueCategory;
  /** Config key, credential slot or ref the issue points at, when known. */
  field?: string;
  source: string;
  message: string;
}

const CATEGORY_ORDER: InspectorIssueCategory[] = [
  'profile',
  'config',
  'credential',
  'contract',
  'asset',
  'connector',
  'python',
  'runtime',
  'graph',
];

export function issueCategory(
  issue: ValidationIssue,
  nodeType: string,
): InspectorIssueCategory {
  switch (issue.code) {
    case 'missing_profile':
      return 'profile';
    case 'missing_schema':
      return 'contract';
    case 'missing_mapping':
      return 'asset';
    case 'missing_credential':
    case 'unknown_credential':
      return 'credential';
    case 'missing_config':
    case 'invalid_config':
    case 'unknown_config':
      return 'config';
    case 'unknown_node_type':
      return nodeType.startsWith('connector.') ? 'connector' : 'graph';
    default:
      return 'graph';
  }
}

export function collectNodeIssues({
  issues,
  node,
  subprocessEnabled,
  runEvents,
}: {
  issues: ValidationIssue[];
  node: WorkflowNode;
  subprocessEnabled?: boolean;
  runEvents: RunEvent[];
}): InspectorIssue[] {
  const collected: InspectorIssue[] = [];

  for (const issue of issues) {
    if (!issueTouchesNode(issue, node.id)) continue;
    const category = issueCategory(issue, node.type);
    collected.push({
      id: `${issue.code}:${category}:${issue.field ?? ''}:${issue.message}`,
      severity: issue.severity,
      category,
      field: issue.field,
      source: issue.severity === 'error' ? 'Blocks publish' : 'Warning',
      message: issue.message,
    });
  }

  const failed = [...runEvents]
    .reverse()
    .find((event) => event.type === 'node.failed' && event.nodeId === node.id);
  if (failed) {
    collected.push({
      id: `runtime:${failed.seq}`,
      severity: 'error',
      category: 'runtime',
      source: 'Last run',
      message: failed.error || 'The last run failed at this node.',
    });
  }

  // The Python gate is a server capability, not draft validation: the
  // published workflow is valid but the node cannot execute here.
  if (node.type === 'code.python' && subprocessEnabled === false) {
    collected.push({
      id: 'python-gate',
      severity: 'error',
      category: 'python',
      source: 'Server',
      message:
        'Python nodes are disabled on this server. Start Loom with --enable-subprocess-nodes (or set LOOM_ENABLE_SUBPROCESS_NODES=1) to run this node.',
    });
  }

  const seen = new Set<string>();
  const unique = collected.filter((issue) => {
    if (seen.has(issue.id)) return false;
    seen.add(issue.id);
    return true;
  });

  return unique.sort((a, b) => {
    if (a.severity !== b.severity) return a.severity === 'error' ? -1 : 1;
    return CATEGORY_ORDER.indexOf(a.category) - CATEGORY_ORDER.indexOf(b.category);
  });
}
