import { useEffect, useState } from 'react';
import { api } from '../api';
import type { Workflow, WorkflowNode, WorkflowState, WorkflowVersion } from '../types';

interface Props {
  open: boolean;
  workflowId: string;
  current: Workflow | null;
  state: WorkflowState | null;
  onClose: () => void;
  onPublish: () => void;
}

interface Diff {
  nameChanged: boolean;
  added: WorkflowNode[];
  removed: WorkflowNode[];
  changed: WorkflowNode[];
  edgesAdded: number;
  edgesRemoved: number;
}

function nodeSignature(node: WorkflowNode): string {
  return JSON.stringify({
    type: node.type,
    typeVersion: node.typeVersion,
    name: node.name,
    config: node.config,
    credentials: node.credentials,
  });
}

function diffWorkflows(base: Workflow | null, against: Workflow | null): Diff {
  const empty: Diff = { nameChanged: false, added: [], removed: [], changed: [], edgesAdded: 0, edgesRemoved: 0 };
  if (!base || !against) return empty;
  const before = new Map(base.nodes.map((node) => [node.id, node]));
  const after = new Map(against.nodes.map((node) => [node.id, node]));
  const added: WorkflowNode[] = [];
  const removed: WorkflowNode[] = [];
  const changed: WorkflowNode[] = [];
  for (const node of against.nodes) {
    const previous = before.get(node.id);
    if (!previous) added.push(node);
    else if (nodeSignature(previous) !== nodeSignature(node)) changed.push(node);
  }
  for (const node of base.nodes) {
    if (!after.has(node.id)) removed.push(node);
  }
  const edgeSignature = (edge: { from: { node: string; port: string }; to: { node: string; port: string } }) =>
    `${edge.from.node}:${edge.from.port}->${edge.to.node}:${edge.to.port}`;
  const beforeEdges = new Set(base.edges.map(edgeSignature));
  const afterEdges = new Set(against.edges.map(edgeSignature));
  let edgesAdded = 0;
  let edgesRemoved = 0;
  for (const edge of afterEdges) if (!beforeEdges.has(edge)) edgesAdded += 1;
  for (const edge of beforeEdges) if (!afterEdges.has(edge)) edgesRemoved += 1;
  return { nameChanged: base.name !== against.name, added, removed, changed, edgesAdded, edgesRemoved };
}

export function VersionHistory({ open, workflowId, current, state, onClose, onPublish }: Props) {
  const [selected, setSelected] = useState<WorkflowVersion | null>(null);
  const [snapshot, setSnapshot] = useState<Workflow | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    if (!open) {
      setSelected(null);
      setSnapshot(null);
      setError('');
      return;
    }
    setSelected(state?.versions[0] ?? null);
  }, [open, state]);

  useEffect(() => {
    if (!open || !selected) {
      setSnapshot(null);
      return;
    }
    let cancelled = false;
    setLoading(true);
    setError('');
    api
      .getWorkflowVersion(workflowId, selected.version)
      .then((workflow) => {
        if (!cancelled) setSnapshot(workflow);
      })
      .catch((reason) => {
        if (!cancelled) setError(reason instanceof Error ? reason.message : String(reason));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [open, selected, workflowId]);

  if (!open) return null;

  const diff = diffWorkflows(snapshot, current);
  const changed = diff.added.length + diff.removed.length + diff.changed.length;
  const differs = changed > 0 || diff.edgesAdded > 0 || diff.edgesRemoved > 0 || diff.nameChanged;

  return (
    <div className="overlay" onClick={onClose}>
      <section
        className="dialog version-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Version history"
        onClick={(event) => event.stopPropagation()}
      >
        <header className="history-head">
          <div>
            <p className="eyebrow">Template versions</p>
            <h2>{current?.name ?? 'Version history'}</h2>
          </div>
          <button className="icon-button" onClick={onClose} aria-label="Close version history">
            ×
          </button>
        </header>

        <div className="version-body">
          <div className="version-list" aria-label="Published versions">
            {state?.versions.length ? (
              state.versions.map((version) => (
                <button
                  key={version.version}
                  className={`version-row ${selected?.version === version.version ? 'selected' : ''}`}
                  onClick={() => setSelected(version)}
                >
                  <strong>v{version.version}</strong>
                  <span>{new Date(version.publishedAt).toLocaleString()}</span>
                  <span>{version.nodeCount} nodes</span>
                  {version.version === state.publishedVersion && <em>Current</em>}
                </button>
              ))
            ) : (
              <div className="empty-panel">
                <p className="muted small">No published versions yet.</p>
                <button className="button primary" onClick={onPublish}>
                  Publish the first version
                </button>
              </div>
            )}
          </div>

          <div className="version-detail">
            {loading && <p className="muted small">Loading version…</p>}
            {error && <p className="banner error">{error}</p>}
            {selected && !loading && !error && (
              <>
                <div className="version-summary">
                  <span>Published v{selected.version}</span>
                  <span>{selected.nodeCount} nodes</span>
                  <span>
                    {!differs
                      ? 'Matches current draft'
                      : changed > 0 || diff.edgesAdded > 0 || diff.edgesRemoved > 0
                        ? `${changed} node changes`
                        : 'Template name changed'}
                  </span>
                </div>
                <div className="version-diff">
                  {diff.nameChanged && snapshot && current && (
                    <div>
                      <strong>Template name</strong>
                      <p className="muted small">{snapshot.name} → {current.name}</p>
                    </div>
                  )}
                  {diff.added.length > 0 && (
                    <div>
                      <strong>Added</strong>
                      <ul>
                        {diff.added.map((node) => (
                          <li key={node.id}>{node.name} <code>{node.type}</code></li>
                        ))}
                      </ul>
                    </div>
                  )}
                  {diff.removed.length > 0 && (
                    <div>
                      <strong>Removed</strong>
                      <ul>
                        {diff.removed.map((node) => (
                          <li key={node.id}>{node.name} <code>{node.type}</code></li>
                        ))}
                      </ul>
                    </div>
                  )}
                  {diff.changed.length > 0 && (
                    <div>
                      <strong>Changed</strong>
                      <ul>
                        {diff.changed.map((node) => (
                          <li key={node.id}>{node.name} <code>{node.type}</code></li>
                        ))}
                      </ul>
                    </div>
                  )}
                  {(diff.edgesAdded > 0 || diff.edgesRemoved > 0) && (
                    <div>
                      <strong>Connections</strong>
                      <p className="muted small">
                        {diff.edgesAdded} added · {diff.edgesRemoved} removed
                      </p>
                    </div>
                  )}
                  {!differs && (
                    <p className="muted small">The current draft is identical to this version.</p>
                  )}
                </div>
              </>
            )}
            {!selected && state?.versions.length === 0 && (
              <p className="muted small">Publish the current draft to create an immutable version.</p>
            )}
          </div>
        </div>

        <footer className="dialog-actions">
          <span className="muted small">
            {state?.draft ? 'Draft changes are not published.' : 'Draft matches the published version.'}
          </span>
          <button className="button subtle" onClick={onClose}>Close</button>
          <button className="button primary" onClick={onPublish}>Publish draft</button>
        </footer>
      </section>
    </div>
  );
}
