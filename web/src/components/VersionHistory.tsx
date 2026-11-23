import { AssetLoadingState } from './assets/AssetState';
import { ActionError } from './assets/ActionError';
import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import type {
  Run,
  VerifyNodeStatus,
  VerifyResult,
  Workflow,
  WorkflowNode,
  WorkflowState,
  WorkflowVersion,
} from '../types';
import { useDialogFocus } from '../useDialogFocus';

const VERIFY_STATUS_LABEL: Record<VerifyNodeStatus, string> = {
  match: 'Match',
  drift: 'Drift',
  added: 'Added',
  removed: 'Removed',
  blocked: 'Blocked',
};

function shortRunId(runId: string) {
  return runId.split('_').pop() ?? runId;
}

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
  const dialogRef = useDialogFocus<HTMLElement>(open, onClose);
  const [selected, setSelected] = useState<WorkflowVersion | null>(null);
  const [snapshot, setSnapshot] = useState<Workflow | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [completedRuns, setCompletedRuns] = useState<Run[] | null>(null);
  const [runsError, setRunsError] = useState('');
  const [verifying, setVerifying] = useState(false);
  const [verify, setVerify] = useState<VerifyResult | null>(null);
  const [verifyError, setVerifyError] = useState('');

  useEffect(() => {
    if (!open) {
      setSelected(null);
      setSnapshot(null);
      setError('');
      setCompletedRuns(null);
      setRunsError('');
      setVerify(null);
      setVerifyError('');
      return;
    }
    setSelected(state?.versions[0] ?? null);
  }, [open, state]);

  useEffect(() => {
    if (!open) return;
    let cancelled = false;
    api
      .listRuns(workflowId, 50)
      .then((result) => {
        if (cancelled) return;
        setCompletedRuns(
          (result.runs ?? []).filter(
            (candidate) => !candidate.parentRunId && candidate.status === 'done',
          ),
        );
      })
      .catch((reason) => {
        if (!cancelled) setRunsError(reason instanceof Error ? reason.message : String(reason));
      });
    return () => {
      cancelled = true;
    };
  }, [open, workflowId]);

  useEffect(() => {
    if (!open || !selected) {
      setSnapshot(null);
      return;
    }
    setVerify(null);
    setVerifyError('');
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

  const latestRun = useMemo(() => {
    if (!completedRuns || completedRuns.length === 0) return null;
    return [...completedRuns].sort(
      (a, b) => Date.parse(b.startedAt) - Date.parse(a.startedAt),
    )[0];
  }, [completedRuns]);

  const runVerify = async (version: number) => {
    if (!latestRun) return;
    setVerifying(true);
    setVerifyError('');
    setVerify(null);
    try {
      const result = await api.verifyWorkflow(workflowId, latestRun.id, version);
      setVerify(result);
    } catch (reason) {
      setVerifyError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setVerifying(false);
    }
  };

  if (!open) return null;

  const diff = diffWorkflows(snapshot, current);
  const changed = diff.added.length + diff.removed.length + diff.changed.length;
  const differs = changed > 0 || diff.edgesAdded > 0 || diff.edgesRemoved > 0 || diff.nameChanged;

  return (
    <div className="overlay" onClick={onClose}>
      <section
        ref={dialogRef}
        className="dialog version-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Version history"
        tabIndex={-1}
        onClick={(event) => event.stopPropagation()}
      >
        <header className="history-head">
          <div>
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
                  Publish first version
                </button>
              </div>
            )}
          </div>

          <div className="version-detail">
            {loading && <AssetLoadingState label="Loading version…" className="muted small" />}
            {error && <ActionError>{error}</ActionError>}
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

                <section className="version-verify" aria-label="Verify this version">
                  <span className="field-label">Verify against a run</span>
                  {runsError && <ActionError>{runsError}</ActionError>}
                  {completedRuns === null && !runsError && (
                    <p className="muted small">Looking up runs…</p>
                  )}
                  {completedRuns !== null && !latestRun && (
                    <p className="muted small">
                      No completed runs yet. Run this workflow, then verify this version against
                      the newest run.
                    </p>
                  )}
                  {latestRun && (
                    <p className="muted small">
                      Uses the most recent completed run <code>{shortRunId(latestRun.id)}</code>
                      {` (${new Date(latestRun.startedAt).toLocaleString()}).`}
                    </p>
                  )}
                  <button
                    className="button subtle"
                    onClick={() => void runVerify(selected.version)}
                    disabled={!latestRun || verifying}
                  >
                    {verifying ? 'Verifying…' : `Verify v${selected.version}`}
                  </button>
                  {verifyError && <ActionError>{verifyError}</ActionError>}
                  {verify && (
                    <div className="verify-result">
                      <p className={`verify-verdict ${verify.passed ? 'passed' : 'failed'}`}>
                        {verify.passed
                          ? `Passed — v${selected.version} matches run ${shortRunId(
                              verify.sourceRunId,
                            )}.`
                          : verify.error
                            ? verify.error
                            : `Failed — v${selected.version} does not match run ${shortRunId(
                                verify.sourceRunId,
                              )}.`}
                      </p>
                      <div className="verify-nodes">
                        {verify.nodes.map((node) => (
                          <div className="verify-node" key={node.nodeId}>
                            <span className="verify-node-name">
                              <strong>{node.name || node.nodeId}</strong>
                              <code>{node.nodeId}</code>
                            </span>
                            <span className={`verify-chip ${node.status}`}>
                              {VERIFY_STATUS_LABEL[node.status] ?? node.status}
                            </span>
                            {node.status === 'drift' && (node.recordedHash || node.actualHash) && (
                              <span className="verify-hashes">
                                <code>{node.recordedHash || '—'}</code>
                                <span aria-hidden="true">→</span>
                                <code>{node.actualHash || '—'}</code>
                              </span>
                            )}
                            {node.status === 'blocked' && (
                              <span className="verify-note">
                                The run predates effect recording, or this node never ran.
                              </span>
                            )}
                            {node.status === 'drift' &&
                              ((node.misses?.length ?? 0) > 0 || (node.diffs?.length ?? 0) > 0) && (
                                <span className="verify-node-notes">
                                  {node.misses?.map((miss) => (
                                    <span key={`miss-${miss.kind}-${miss.index}`}>
                                      missing {miss.kind} effect {miss.index}
                                    </span>
                                  ))}
                                  {node.diffs?.map((diff) => (
                                    <span key={`diff-${diff.kind}-${diff.index}`}>
                                      {diff.kind} effect {diff.index} differs
                                    </span>
                                  ))}
                                </span>
                              )}
                          </div>
                        ))}
                      </div>
                    </div>
                  )}
                </section>
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
