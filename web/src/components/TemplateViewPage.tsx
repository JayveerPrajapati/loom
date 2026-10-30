import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import { defaultNodeName } from '../catalog';
import type { NodeDef, Run, ValidationIssue, Workflow, WorkflowState } from '../types';
import { Canvas, type NodeStatus } from './Canvas';

interface Props {
  workflow: Workflow;
  defs: Record<string, NodeDef>;
  issues: ValidationIssue[];
  statuses: Record<string, NodeStatus>;
  selectedId: string | null;
  online: boolean;
  onSelect: (id: string | null) => void;
  onBack: () => void;
  onEdit: () => void;
  onRun: () => void;
  onOpenRun: (runId: string) => void;
}

function formatTime(value?: string) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

function duration(run: Run) {
  const start = Date.parse(run.startedAt);
  const end = run.finishedAt ? Date.parse(run.finishedAt) : 0;
  if (!start || !end || end < start) return '—';
  const ms = end - start;
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export function TemplateViewPage({
  workflow,
  defs,
  issues,
  statuses,
  selectedId,
  online,
  onSelect,
  onBack,
  onEdit,
  onRun,
  onOpenRun,
}: Props) {
  const [runs, setRuns] = useState<Run[]>([]);
  const [state, setState] = useState<WorkflowState | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    setLoading(true);
    setError('');
    Promise.all([
      api.listRuns(workflow.id, 20),
      api.getWorkflowState(workflow.id),
    ])
      .then(([runResult, workflowState]) => {
        if (cancelled) return;
        setRuns(runResult.runs ?? []);
        setState(workflowState);
      })
      .catch((reason: Error) => {
        if (!cancelled) setError(reason.message);
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [workflow.id]);

  const selectedNode = workflow.nodes.find((node) => node.id === selectedId) ?? null;
  const selectedDef = selectedNode ? defs[selectedNode.type] : undefined;
  const trigger = workflow.nodes.find((node) => node.type.startsWith('trigger.'));
  const errorCount = issues.filter((issue) => issue.severity === 'error').length;
  const published = state?.publishedVersion ?? 0;
  const configEntries = useMemo(
    () => Object.entries(selectedNode?.config ?? {}),
    [selectedNode],
  );

  return (
    <main className="page-shell template-view-page" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <button className="breadcrumb-button" onClick={onBack}>
            ← Templates
          </button>
          <p className="eyebrow">Template</p>
          <h1>{workflow.name}</h1>
          <p className="lede">
            {published ? `Published v${published}` : 'Draft'}
            {state?.draft && published ? ' · draft changes' : ''} · {workflow.nodes.length} nodes ·{' '}
            {workflow.edges.length} edges
          </p>
        </div>
        <div className="job-actions">
          <span className={`validation-chip ${errorCount ? 'bad' : 'good'}`}>
            {errorCount ? `${errorCount} errors` : 'Valid'}
          </span>
          <button className="button subtle" onClick={onEdit}>
            Edit
          </button>
          <button className="button primary" onClick={onRun} disabled={!online || errorCount > 0}>
            Run
          </button>
        </div>
      </header>

      {error && <p className="banner error">{error}</p>}

      <div className="template-view-layout">
        <section className="stage template-view-stage" aria-label="Workflow preview">
          <Canvas
            workflow={workflow}
            defs={defs}
            statuses={statuses}
            issues={issues}
            selectedId={selectedId}
            onSelect={onSelect}
            onMove={() => undefined}
            onDeleteNodes={() => undefined}
            onDeleteEdges={() => undefined}
            onConnect={() => undefined}
            onAddNode={() => undefined}
            readOnly
          />
        </section>

        <aside className="panel template-view-side">
          <div className="panel-head">
            <div>
              <p className="eyebrow">{selectedNode ? 'Node' : 'Overview'}</p>
              <h2>
                {selectedNode
                  ? selectedNode.name || defaultNodeName(selectedDef ?? ({ type: selectedNode.type } as NodeDef))
                  : 'Template'}
              </h2>
            </div>
            {selectedNode && (
              <button className="button subtle" onClick={() => onSelect(null)}>
                Overview
              </button>
            )}
          </div>
          <div className="inspector-scroll">
            {!selectedNode && (
              <>
                <div className="meta-grid">
                  <div>
                    <span>Id</span>
                    <code>{workflow.id}</code>
                  </div>
                  <div>
                    <span>Version</span>
                    <code>{published || workflow.version}</code>
                  </div>
                  <div>
                    <span>Nodes</span>
                    <code>{workflow.nodes.length}</code>
                  </div>
                  <div>
                    <span>Edges</span>
                    <code>{workflow.edges.length}</code>
                  </div>
                </div>
                {trigger && (
                  <div className="callout">
                    <p className="eyebrow">Trigger</p>
                    <strong>{trigger.name || trigger.type}</strong>
                    <span>
                      {String(
                        trigger.config.path ??
                          trigger.config.schedule ??
                          trigger.type.replace('trigger.', ''),
                      )}
                    </span>
                  </div>
                )}
                <section className="inspector-section">
                  <h3>Validation</h3>
                  {issues.length === 0 && <p className="muted small">No validation issues.</p>}
                  {issues.map((issue, index) => (
                    <div className={`issue issue-${issue.severity}`} key={`${issue.code}-${index}`}>
                      <span className="issue-badge">{issue.severity}</span>
                      <span>{issue.message}</span>
                    </div>
                  ))}
                </section>
              </>
            )}

            {selectedNode && (
              <>
                <div className="node-identity">
                  <span className={`node-dot dot-${selectedDef?.category ?? 'action'}`} aria-hidden="true" />
                  <code>{selectedNode.type}</code>
                  <span className="muted">v{selectedNode.typeVersion}</span>
                  <code className="node-id">{selectedNode.id}</code>
                </div>
                {selectedDef?.description && <p className="node-description">{selectedDef.description}</p>}
                <section className="inspector-section">
                  <h3>Configuration</h3>
                  {configEntries.length === 0 && <p className="muted small">No configuration.</p>}
                  {configEntries.map(([key, value]) => (
                    <div className="field" key={key}>
                      <span className="field-label">{key}</span>
                      <pre className="json-view">{typeof value === 'string' ? value : JSON.stringify(value, null, 2)}</pre>
                    </div>
                  ))}
                </section>
                {selectedNode.credentials && Object.keys(selectedNode.credentials).length > 0 && (
                  <section className="inspector-section">
                    <h3>Credentials</h3>
                    {Object.entries(selectedNode.credentials).map(([key, value]) => (
                      <div className="field" key={key}>
                        <span className="field-label">{key}</span>
                        <code>{value}</code>
                      </div>
                    ))}
                  </section>
                )}
              </>
            )}

            <section className="inspector-section">
              <h3>Recent runs</h3>
              {loading && <p className="muted small">Loading runs…</p>}
              {!loading && runs.length === 0 && <p className="muted small">No runs yet.</p>}
              {runs.map((run) => (
                <button className="runs-row" key={run.id} onClick={() => onOpenRun(run.id)}>
                  <span className={`status-dot status-${run.status}`} aria-hidden="true" />
                  <span className="runs-row-main">
                    <strong>{run.status}</strong>
                    <span>{run.trigger} · {formatTime(run.startedAt)}</span>
                  </span>
                  <span className="runs-row-meta">
                    <code>{run.id.split('_').pop()}</code>
                    <em>{duration(run)}</em>
                  </span>
                </button>
              ))}
            </section>
          </div>
        </aside>
      </div>
    </main>
  );
}
