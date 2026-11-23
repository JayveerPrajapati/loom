import { useState } from 'react';
import { formFieldGroups } from '../formLayout';
import { collectNodeIssues } from '../inspectorIssues';
import { labelFor } from './inspectorHelpers';
import { ConfigField } from './InspectorFields';
import { IssueCenter } from './IssueCenter';
import type {
  NodeDef,
  RunEvent,
  ValidationIssue,
  Workflow,
  WorkflowNode,
} from '../types';

interface Props {
  workflow: Workflow;
  selectedId: string | null;
  selectedNode: WorkflowNode | null;
  defsByType: Record<string, NodeDef>;
  issues: ValidationIssue[];
  runEvents: RunEvent[];
  subprocessEnabled?: boolean;
  onSelect: (id: string | null) => void;
  onNodePatch: (patch: Partial<WorkflowNode>) => void;
  onConfigChange: (key: string, value: unknown) => void;
}

const SIMPLE_TYPES = new Set(['string', 'number', 'boolean']);

// The phone editing subset (Apple-Grade Phase 3 D3-5). The graph canvas
// stays read-only below 720px and destructive/layout actions stay on
// desktop; this surface still lets an operator pick a node, rename it,
// change simple text/number/boolean/select values, copy its id and read
// its issues. Approvals and run inspection live on the mobile tasks and
// run routes.
export function MobileInspector({
  workflow,
  selectedId,
  selectedNode,
  defsByType,
  issues,
  runEvents,
  subprocessEnabled,
  onSelect,
  onNodePatch,
  onConfigChange,
}: Props) {
  const [copied, setCopied] = useState(false);
  const node = selectedNode;
  const def = node ? defsByType[node.type] : undefined;
  const groups = node ? formFieldGroups(node.type, node.config, def) : { common: [], advanced: [] };
  const simpleFields = groups.common.filter(
    ([key, spec]) =>
      SIMPLE_TYPES.has(spec.type) &&
      !spec.secret &&
      key !== 'source' &&
      key !== 'mappingRows',
  );
  const nodeIssues = node
    ? collectNodeIssues({ issues, node, subprocessEnabled, runEvents })
    : [];

  const copyId = () => {
    if (!node) return;
    navigator.clipboard
      .writeText(node.id)
      .then(() => {
        setCopied(true);
        window.setTimeout(() => setCopied(false), 1500);
      })
      .catch(() => setCopied(false));
  };

  const focusField = (field: string | undefined) => {
    if (!field || field.startsWith('profileRef') || field.startsWith('credential:')) return;
    document.getElementById(`field-${field}`)?.focus();
  };

  return (
    <section className="mobile-inspector" data-testid="mobile-inspector" aria-label="Node settings">
      <div className="mobile-inspector-head">
        <h2>Node settings</h2>
        <p className="field-hint">
          Graph editing stays on desktop. Rename nodes and change simple settings here.
        </p>
      </div>
      <label className="field" htmlFor="mobile-node-select">
        <span className="field-label">Node</span>
        <select
          id="mobile-node-select"
          className="text-input"
          data-testid="mobile-node-select"
          value={selectedId ?? ''}
          onChange={(event) => onSelect(event.target.value || null)}
        >
          <option value="">Choose a node…</option>
          {workflow.nodes.map((candidate) => (
            <option key={candidate.id} value={candidate.id}>
              {candidate.name || labelFor(candidate.type)} ({candidate.type})
            </option>
          ))}
        </select>
      </label>

      {!node && (
        <p className="muted small" data-testid="mobile-inspector-empty">
          Pick a node to edit its simple settings. The workflow graph is view-only on a phone.
        </p>
      )}

      {node && (
        <>
          <label className="field" htmlFor="mobile-node-name">
            <span className="field-label">Name</span>
            <input
              id="mobile-node-name"
              className="text-input"
              value={node.name}
              onChange={(event) => onNodePatch({ name: event.target.value })}
            />
          </label>
          <div className="node-identity">
            <span className={`node-dot dot-${def?.category ?? 'action'}`} aria-hidden="true" />
            <code>{node.type}</code>
            <span className="muted">v{node.typeVersion}</span>
            <button
              type="button"
              className="button subtle mobile-copy-id"
              data-testid="mobile-copy-node-id"
              onClick={copyId}
            >
              {copied ? 'Copied' : 'Copy id'}
            </button>
          </div>

          {simpleFields.map(([key, spec]) => (
            <ConfigField
              key={key}
              fieldKey={key}
              spec={spec}
              value={node.config?.[key] ?? spec.default}
              onChange={(value) => onConfigChange(key, value)}
              issue={nodeIssues.find((issue) => issue.field === key && issue.severity === 'error')?.message}
            />
          ))}
          {simpleFields.length === 0 && (
            <p className="muted small">This node has no simple settings to edit on a phone.</p>
          )}

          <IssueCenter issues={nodeIssues} onFix={(issue) => focusField(issue.field)} />
        </>
      )}
    </section>
  );
}
