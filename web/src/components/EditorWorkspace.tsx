import { useEffect, useState } from 'react';
import type { Connection } from '@xyflow/react';
import { Canvas, type NodeStatus } from './Canvas';
import { Inspector } from './Inspector';
import { MobileInspector } from './MobileInspector';
import { NodePanel } from './NodePanel';
import { RunBar } from './RunBar';
import { autoArrange } from '../layout';
import { useDialogFocus } from '../useDialogFocus';
import type { NodeDef, Run, RunEvent, ValidationIssue, Workflow } from '../types';

interface Props {
  workflow: Workflow;
  defs: NodeDef[];
  defsByType: Record<string, NodeDef>;
  statuses: Record<string, NodeStatus>;
  issues: ValidationIssue[];
  selectedId: string | null;
  selectedNode: Workflow['nodes'][number] | null;
  run: Run | null;
  status: string;
  events: RunEvent[];
  busy: boolean;
  online: boolean;
  runBlocked: string | undefined;
  subprocessEnabled?: boolean;
  onSelect: (id: string | null) => void;
  onAddNode: (def: NodeDef, position?: { x: number; y: number }) => void;
  onMoveNode: (id: string, position: { x: number; y: number }) => void;
  onDeleteNodes: (ids: string[]) => void;
  onDeleteEdges: (ids: string[]) => void;
  onConnect: (connection: Connection) => void;
  onRun: () => void;
  onInput: () => void;
  onRetry: () => void;
  onCancel: () => void;
  onResume: (decision: 'approved' | 'rejected') => void;
  onNotify: (message: string, tone?: 'info' | 'error') => void;
  onOpenHistory: () => void;
  onOpenRun: (runID: string) => void;
  onOpenRunbook: () => void;
  onOpenWorkflows: () => void;
  /** Deep-links into the Configuration Hub page for a node type (v3 D5). */
  onOpenHub: (nodeType: string) => void;
  /** Propagates save-as-mapping creation so the app's mappings cache —
   * and with it the draft validator — stays current (Phase 1). */
  onMappingsChanged?: () => void;
  onApply: (
    transform: (current: Workflow) => Workflow,
    record?: boolean,
    coalesceKey?: string,
  ) => void;
  onDeleteSelected: () => void;
}

// The editor route's workspace — NodePanel, Canvas, RunBar and Inspector
// laid out — extracted verbatim from App. All state and the apply/commit
// machinery stay in App; this component only renders and reports.
export function EditorWorkspace({
  workflow,
  defs,
  defsByType,
  statuses,
  issues,
  selectedId,
  selectedNode,
  run,
  status,
  events,
  busy,
  online,
  runBlocked,
  subprocessEnabled,
  onSelect,
  onAddNode,
  onMoveNode,
  onDeleteNodes,
  onDeleteEdges,
  onConnect,
  onRun,
  onInput,
  onRetry,
  onCancel,
  onResume,
  onNotify,
  onOpenHistory,
  onOpenRun,
  onOpenRunbook,
  onOpenWorkflows,
  onOpenHub,
  onMappingsChanged,
  onApply,
  onDeleteSelected,
}: Props) {
  // UX Phase 0 decision — read-only mobile: below 720px the editor is for
  // viewing and following runs. The same query drives the CSS that hides
  // the node library and inspector, so the two can never disagree.
  const [phoneViewport, setPhoneViewport] = useState(
    () => typeof window !== 'undefined' && window.matchMedia('(max-width: 720px)').matches,
  );
  useEffect(() => {
    const query = window.matchMedia('(max-width: 720px)');
    const onChange = (event: MediaQueryListEvent) => setPhoneViewport(event.matches);
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [importError, setImportError] = useState('');
  const importDialogRef = useDialogFocus<HTMLDivElement>(importOpen, () => setImportOpen(false));

  const applyImport = () => {
    setImportError('');
    let parsed: unknown;
    try {
      parsed = JSON.parse(importText);
    } catch {
      setImportError('That is not valid JSON.');
      return;
    }
    const source = parsed as Partial<Workflow> | null;
    if (!source || typeof source !== 'object' || !Array.isArray(source.nodes)) {
      setImportError('Expected a workflow object with a "nodes" array.');
      return;
    }
    onApply(
      (current) => ({
        ...current,
        nodes: source.nodes ?? [],
        edges: Array.isArray(source.edges) ? source.edges : [],
      }),
      true,
      'workflow:import',
    );
    setImportOpen(false);
    setImportText('');
    onNotify(`Imported ${(source.nodes ?? []).length} nodes — press ⌘Z to undo.`);
  };

  return (
    <div className="workspace" id="main-stage" tabIndex={-1}>
      <h1 className="sr-only">{workflow.name}</h1>
      <NodePanel defs={defs} onAdd={(def) => onAddNode(def)} onDragStart={() => undefined} />
      <main className="stage">
        {phoneViewport && (
          <div className="mobile-desktop-only" role="note" data-testid="mobile-desktop-only">
            <h2>Open on desktop to edit</h2>
            <p className="muted small">
              The graph editor needs a larger screen. From this phone you can still rename
              nodes, change simple settings and save your work.
            </p>
          </div>
        )}
        {subprocessEnabled === false && workflow.nodes.some((node) => node.type === 'code.python') && (
          <div className="banner notice python-gate-notice" role="note" data-testid="python-gate-notice">
            <strong>Python nodes are disabled on this server.</strong> Runs will fail at the Python
            node until the operator starts Loom with <code>--enable-subprocess-nodes</code> (or
            sets <code>LOOM_ENABLE_SUBPROCESS_NODES=1</code>). See <code>docs/security.md</code> for
            the isolation model.
          </div>
        )}
        {workflow.nodes.length === 0 && !phoneViewport && (
          <div className="canvas-start" data-testid="canvas-empty">
            <h2>Start your workflow</h2>
            <p className="muted">Add a first node, pick a bundled workflow, or paste one as JSON.</p>
            <div className="canvas-start-actions">
              <button
                className="button primary"
                onClick={() => {
                  const def = defsByType['trigger.manual'];
                  if (def) onAddNode(def, { x: 80, y: 160 });
                }}
              >
                Add first node
              </button>
              <button className="button subtle" onClick={onOpenWorkflows}>
                Start from template
              </button>
              <button
                className="button subtle"
                onClick={() => {
                  setImportText('');
                  setImportError('');
                  setImportOpen(true);
                }}
              >
                Import JSON
              </button>
            </div>
          </div>
        )}
        {importOpen && (
          <div className="overlay import-dialog-backdrop" onMouseDown={(event) => event.target === event.currentTarget && setImportOpen(false)}>
            <div
              ref={importDialogRef}
              className="dialog import-dialog"
              role="dialog"
              aria-modal="true"
              aria-label="Import workflow JSON"
            >
              <h3>Import workflow JSON</h3>
              <p className="muted small">
                Paste a workflow definition (nodes and edges). The current name and settings are
                kept; press ⌘Z afterwards to undo.
              </p>
              <textarea
                className="text-input code"
                data-autofocus
                rows={10}
                spellCheck={false}
                placeholder={'{\n  "nodes": [...],\n  "edges": [...]\n}'}
                value={importText}
                onChange={(event) => setImportText(event.target.value)}
              />
              {importError && <p className="field-error" role="alert">{importError}</p>}
              <div className="dialog-actions">
                <button className="button subtle" onClick={() => setImportOpen(false)}>
                  Cancel
                </button>
                <button className="button primary" onClick={applyImport}>
                  Import
                </button>
              </div>
            </div>
          </div>
        )}
        {phoneViewport ? null : (
          <Canvas
            workflow={workflow}
            defs={defsByType}
            statuses={statuses}
            issues={issues}
            selectedId={selectedId}
            onSelect={onSelect}
            readOnly={false}
            onMove={onMoveNode}
            onDeleteNodes={onDeleteNodes}
            onDeleteEdges={onDeleteEdges}
            onConnect={onConnect}
            onAddNode={(type, position) => {
              const def = defsByType[type];
              if (def) onAddNode(def, position);
            }}
            onAutoArrange={() => {
              onApply((current) => autoArrange(current));
              onNotify('Graph auto-arranged — press ⌘Z to undo.');
            }}
          />
        )}
        <RunBar
          run={run}
          status={status}
          events={events}
          nodeCount={workflow.nodes.length}
          busy={busy}
          online={online}
          runBlocked={runBlocked}
          onRun={onRun}
          onInput={onInput}
          onRetry={onRetry}
          onCancel={onCancel}
          onResume={onResume}
          onNotify={onNotify}
          onOpenHistory={onOpenHistory}
          onOpenRun={() => {
            if (run) onOpenRun(run.id);
          }}
        />
        {phoneViewport && (
          <MobileInspector
            workflow={workflow}
            selectedId={selectedId}
            selectedNode={selectedNode}
            defsByType={defsByType}
            issues={issues}
            runEvents={events}
            subprocessEnabled={subprocessEnabled}
            onSelect={onSelect}
            onNodePatch={(patch) =>
              onApply(
                (current) => ({
                  ...current,
                  nodes: current.nodes.map((node) =>
                    node.id === selectedId ? { ...node, ...patch } : node,
                  ),
                }),
                true,
                `node:${selectedId}`,
              )
            }
            onConfigChange={(key, value) =>
              onApply(
                (current) => ({
                  ...current,
                  nodes: current.nodes.map((node) =>
                    node.id === selectedId
                      ? {
                          ...node,
                          config: { ...(node.config ?? {}), [key]: value },
                          profileOverrides: { ...(node.profileOverrides ?? {}), [key]: true },
                        }
                      : node,
                  ),
                }),
                true,
                `config:${selectedId}:${key}`,
              )
            }
          />
        )}
      </main>
      {!phoneViewport && (
        <Inspector
          workflow={workflow}
          node={selectedNode}
          def={selectedNode ? defsByType[selectedNode.type] : undefined}
          issues={issues}
          runEvents={events}
          runId={run?.id ?? null}
          subprocessEnabled={subprocessEnabled}
          onWorkflowName={(name) => onApply((current) => ({ ...current, name }), true, 'workflow:name')}
          onWorkflowSettings={(patch) =>
            onApply(
              (current) => ({
                ...current,
                settings: { ...(current.settings ?? {}), ...patch },
              }),
              true,
              'workflow:settings',
            )
          }
          onOpenRunbook={onOpenRunbook}
          onOpenHub={onOpenHub}
          onMappingsChanged={onMappingsChanged}
          onNodePatch={(patch) =>
            onApply(
              (current) => ({
                ...current,
                nodes: current.nodes.map((node) =>
                  node.id === selectedId ? { ...node, ...patch } : node,
                ),
              }),
              true,
              `node:${selectedId}`,
            )
          }
          onConfigChange={(key, value) =>
            onApply(
              (current) => ({
                ...current,
                nodes: current.nodes.map((node) =>
                  node.id === selectedId
                    ? {
                        ...node,
                        config: { ...(node.config ?? {}), [key]: value },
                        profileOverrides: { ...(node.profileOverrides ?? {}), [key]: true },
                      }
                    : node,
                ),
              }),
              true,
              `config:${selectedId}:${key}`,
            )
          }
          onCredentialChange={(key, value) =>
            onApply(
              (current) => ({
                ...current,
                nodes: current.nodes.map((node) =>
                  node.id === selectedId
                    ? { ...node, credentials: { ...(node.credentials ?? {}), [key]: value } }
                    : node,
                ),
              }),
              true,
              `credential:${selectedId}:${key}`,
            )
          }
          onDeleteNode={onDeleteSelected}
          onSelectNode={onSelect}
        />
      )}
    </div>
  );
}
