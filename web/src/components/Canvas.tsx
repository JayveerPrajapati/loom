import { useEffect, useMemo, useRef, useState } from 'react';
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Position,
  ReactFlow,
  useEdgesState,
  useNodesState,
  useReactFlow,
  type Connection,
  type Edge,
  type Node,
  type NodeProps,
  type OnEdgesChange,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import './Canvas.css';
import { defaultNodeName, edgeId, outputType, PORT_COLORS } from '../catalog';
import { confirmAction } from '../confirm';
import type {
  Endpoint,
  NodeDef,
  PortType,
  ValidationIssue,
  Workflow,
  WorkflowEdge,
} from '../types';

export type NodeStatus = 'queued' | 'running' | 'done' | 'failed' | 'waiting' | 'skipped';

type Port = { name: string; type: PortType; color?: string };

export type LoomNodeData = {
  name: string;
  type: string;
  category: string;
  description: string;
  inputs: Port[];
  outputs: Port[];
  status?: NodeStatus;
  invalid?: boolean;
};

type LoomNode = Node<LoomNodeData, 'loom'>;
type LoomEdgeData = { from: Endpoint; to: Endpoint };
type LoomEdge = Edge<LoomEdgeData>;

const categoryColor: Record<string, string> = {
  trigger: '#bf4800',
  action: '#0071e3',
  logic: '#7d3c98',
  connector: '#0f766e',
};

function LoomNodeView({ data, selected }: NodeProps<LoomNode>) {
  return (
    <div
      className={`loom-node cat-${data.category} ${selected ? 'is-selected' : ''} ${
        data.status ? `status-${data.status}` : ''
      } ${data.invalid ? 'has-error' : ''}`}
    >
      <div className="loom-node-top">
        <span className="loom-node-kind">{data.category}</span>
        {data.status && <span className={`loom-node-state state-${data.status}`}>{data.status}</span>}
      </div>
      <div className="loom-node-name">{data.name}</div>
      <div className="loom-node-type">{data.type}</div>
      <div className="loom-node-ports">
        <div className="port-stack port-stack-in">
          {data.inputs.map((port) => (
            <div className="port-row" key={`in-${port.name}`}>
              <Handle
                id={`in-${port.name}`}
                type="target"
                position={Position.Left}
                className="nodrag loom-handle"
                style={{ background: port.color ?? PORT_COLORS[port.type] }}
              />
              <span className="port-name">{port.name}</span>
              <span className="port-type">{port.type}</span>
            </div>
          ))}
        </div>
        <div className="port-stack port-stack-out">
          {data.outputs.map((port) => (
            <div className="port-row" key={`out-${port.name}`}>
              <span className="port-type">{port.type}</span>
              <span className="port-name">{port.name}</span>
              <Handle
                id={`out-${port.name}`}
                type="source"
                position={Position.Right}
                className="nodrag loom-handle"
                style={{ background: port.color ?? PORT_COLORS[port.type] }}
              />
            </div>
          ))}
        </div>
      </div>
    </div>
  );
}

const nodeTypes = { loom: LoomNodeView };

interface Props {
  workflow: Workflow;
  defs: Record<string, NodeDef>;
  statuses: Record<string, NodeStatus>;
  issues: ValidationIssue[];
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  onMove: (id: string, position: { x: number; y: number }) => void;
  onAutoArrange?: () => void;
  onDeleteNodes: (ids: string[]) => void;
  onDeleteEdges: (ids: string[]) => void;
  onConnect: (connection: Connection) => void;
  onAddNode: (type: string, position: { x: number; y: number }) => void;
  readOnly?: boolean;
}

export function Canvas({
  workflow,
  defs,
  statuses,
  issues,
  selectedId,
  onSelect,
  onMove,
  onAutoArrange,
  onDeleteNodes,
  onDeleteEdges,
  onConnect,
  onAddNode,
  readOnly = false,
}: Props) {
  const [nodes, setNodes, onNodesChange] = useNodesState<LoomNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<LoomEdge>([]);
  const { fitView, getViewport, screenToFlowPosition, setViewport } = useReactFlow();
  const shellRef = useRef<HTMLDivElement>(null);

  const nodeById = useMemo(
    () => Object.fromEntries(workflow.nodes.map((node) => [node.id, node])),
    [workflow.nodes],
  );
  const nodeErrors = useMemo(
    () =>
      new Set(
        issues
          .filter((issue) => issue.severity === 'error' && issue.node)
          .map((issue) => issue.node as string),
      ),
    [issues],
  );
  const edgeErrors = useMemo(
    () =>
      new Set(
        issues
          .filter((issue) => issue.severity === 'error' && issue.edge)
          .map((issue) => edgeId(issue.edge as WorkflowEdge)),
      ),
    [issues],
  );

  // L5 — minimap visibility. A minimap earns its corner only once the
  // graph outgrows the viewport; on a two-node pipeline it is a blank
  // box covering canvas for no information. The rule is measured from
  // the laid-out content (node cards are ~240px wide, ~120px tall):
  // show the map when the graph spans more than one desktop viewport
  // horizontally (1200px) or vertically (760px). Content span beats a
  // node count — eight nodes stacked in one column still fit the view,
  // while three far-apart nodes already need the map. Every consumer
  // (editor, template view, run detail) shares this component and so
  // shares the rule.
  const showMinimap = useMemo(() => {
    if (workflow.nodes.length === 0) return false;
    const xs = workflow.nodes.map((node) => node.position?.x ?? 0);
    const ys = workflow.nodes.map((node) => node.position?.y ?? 0);
    const spanX = Math.max(...xs) + 240 - Math.min(...xs);
    const spanY = Math.max(...ys) + 120 - Math.min(...ys);
    return spanX > 1200 || spanY > 760;
  }, [workflow.nodes]);

  useEffect(() => {
    setNodes(
      workflow.nodes.map((node) => {
        const def = defs[node.type];
        const config = node.config ?? {};
        const inputs: Port[] = (def?.inputs ?? []).map((port) => ({
          name: port,
          type: def?.inputType ?? 'any',
        }));
        const outputs: Port[] = (def?.outputs ?? []).map((port) => ({
          name: port,
          type: outputType(def, config),
        }));
        if (node.onError === 'branch') {
          outputs.push({ name: 'error', type: 'json', color: '#c0392b' });
        }
        return {
          id: node.id,
          type: 'loom',
          position: node.position,
          selected: node.id === selectedId,
          data: {
            name: node.name || defaultNodeName(def ?? { type: node.type } as NodeDef),
            type: node.type,
            category: def?.category ?? 'action',
            description: def?.description ?? '',
            inputs,
            outputs,
            status: statuses[node.id],
            invalid: nodeErrors.has(node.id),
          },
        };
      }),
    );
  }, [workflow, defs, statuses, selectedId, nodeErrors, setNodes]);

  useEffect(() => {
    setEdges(
      workflow.edges.map((edge) => {
        const source = nodeById[edge.from.node];
        const def = source ? defs[source.type] : undefined;
        const type = outputType(def, source?.config ?? {});
        const stroke = edge.from.port === 'error' ? '#c0392b' : PORT_COLORS[type];
        const sourceStatus = statuses[edge.from.node];
        const targetStatus = statuses[edge.to.node];
        const traversed =
          (sourceStatus === 'done' || sourceStatus === 'running') &&
          targetStatus !== undefined &&
          targetStatus !== 'queued' &&
          targetStatus !== 'skipped';
        const classes = [
          edgeErrors.has(edgeId(edge)) ? 'edge-invalid' : '',
          traversed ? 'edge-active' : '',
        ]
          .filter(Boolean)
          .join(' ');
        return {
          id: edgeId(edge),
          source: edge.from.node,
          target: edge.to.node,
          sourceHandle: `out-${edge.from.port}`,
          targetHandle: `in-${edge.to.port}`,
          type: 'smoothstep',
          label: edge.from.port === 'main' ? undefined : edge.from.port,
          labelStyle: { fill: '#6e6e73', fontSize: 10, fontWeight: 600 },
          labelBgStyle: { fill: 'var(--panel)', fillOpacity: 0.92 },
          labelBgPadding: [5, 3] as [number, number],
          labelBgBorderRadius: 4,
          markerEnd: { type: MarkerType.ArrowClosed, color: stroke, width: 16, height: 16 },
          style: { stroke, strokeWidth: 1.6 },
          className: classes || undefined,
          animated: statuses[edge.from.node] === 'running',
          data: { from: edge.from, to: edge.to },
        };
      }),
    );
  }, [workflow, defs, statuses, nodeById, edgeErrors, setEdges]);

  useEffect(() => {
    let first = requestAnimationFrame(() => {
      const second = requestAnimationFrame(() => {
        void fitView({ padding: 0.24, maxZoom: 1, duration: 220 });
      });
      first = second;
    });
    return () => cancelAnimationFrame(first);
  }, [fitView, workflow.id]);

  // A node added from the palette is placed beside the current nodes with
  // no viewport awareness (catalog.ts nextPosition cascades rightward), and
  // fitView only runs at mount, so the new node can render off-canvas with
  // its ports unreachable. Pan the minimum amount that brings newly added
  // nodes back inside the canvas; drag-drop adds land under the cursor and
  // are already inside, so they produce no pan. A pending pan survives a
  // workflow identity re-init, and a node stays pending until it is
  // verifiably inside the pane, so a raced or cancelled animation cannot
  // leave it off-canvas.
  const seenIdsRef = useRef<Set<string>>(new Set());
  const lastWorkflowIdRef = useRef<string>('');
  const pendingPanRef = useRef<string[]>([]);
  const panFrameRef = useRef<number | null>(null);
  const panTimerRef = useRef<number | null>(null);
  const [resizeTick, setResizeTick] = useState(0);
  const lastResizeTickRef = useRef(0);

  // The worklist lives in a ref and the loop is cancelled only on unmount: an
  // unrelated re-render between the add and the paint used to cancel the
  // pending pan, which left new nodes off-canvas.
  useEffect(() => {
    return () => {
      if (panFrameRef.current !== null) cancelAnimationFrame(panFrameRef.current);
      if (panTimerRef.current !== null) clearTimeout(panTimerRef.current);
      panFrameRef.current = null;
      panTimerRef.current = null;
    };
  }, []);

  // A canvas resize is not a workflow change: opening the inspector after
  // adding a node narrows the pane *after* the add-time pan, so the node can
  // end up under the inspector. Re-check every node on a shell resize and
  // pan only the ones that fall outside the visible pane.
  useEffect(() => {
    if (readOnly) return;
    const shell = shellRef.current;
    if (!shell || typeof ResizeObserver === 'undefined') return;
    let frame = 0;
    let lastWidth = -1;
    let lastHeight = -1;
    const observer = new ResizeObserver((entries) => {
      const rect = entries[0]?.contentRect;
      if (!rect) return;
      // The first callback is the initial observe, not a resize. Ignore it
      // so the mount-time fitView remains the only initial viewport move.
      if (lastWidth < 0) {
        lastWidth = rect.width;
        lastHeight = rect.height;
        return;
      }
      if (rect.width === lastWidth && rect.height === lastHeight) return;
      lastWidth = rect.width;
      lastHeight = rect.height;
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => setResizeTick((tick) => tick + 1));
    });
    observer.observe(shell);
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame);
    };
  }, [readOnly]);

  useEffect(() => {
    if (lastWorkflowIdRef.current !== workflow.id) {
      lastWorkflowIdRef.current = workflow.id;
      seenIdsRef.current = new Set(workflow.nodes.map((node) => node.id));
      // Keep pan candidates that still exist after an identity re-init so
      // a pan in flight across the switch is not silently dropped.
      const ids = new Set(workflow.nodes.map((node) => node.id));
      pendingPanRef.current = pendingPanRef.current.filter((id) => ids.has(id));
    } else {
      const added = workflow.nodes.filter((node) => !seenIdsRef.current.has(node.id));
      workflow.nodes.forEach((node) => seenIdsRef.current.add(node.id));
      if (!readOnly) {
        for (const node of added) {
          if (!pendingPanRef.current.includes(node.id)) pendingPanRef.current.push(node.id);
        }
      }
    }
    if (resizeTick !== lastResizeTickRef.current) {
      lastResizeTickRef.current = resizeTick;
      if (!readOnly) {
        for (const node of workflow.nodes) {
          if (!pendingPanRef.current.includes(node.id)) pendingPanRef.current.push(node.id);
        }
      }
    }
    if (readOnly || pendingPanRef.current.length === 0) return;
    if (panFrameRef.current !== null || panTimerRef.current !== null) return;

    let missAttempts = 0;
    let panAttempts = 0;
    const step = () => {
      panFrameRef.current = null;
      panTimerRef.current = null;
      const shell = shellRef.current;
      if (!shell) {
        missAttempts += 1;
        if (missAttempts >= 40) return;
        panFrameRef.current = requestAnimationFrame(step);
        return;
      }
      const pane = shell.querySelector<HTMLElement>('.react-flow__pane');
      const bounds = (pane ?? shell).getBoundingClientRect();
      const { x, y, zoom } = getViewport();
      let dx = 0;
      let dy = 0;
      const missing: string[] = [];
      const outside: string[] = [];
      for (const id of pendingPanRef.current) {
        const rect = shell
          .querySelector(`.react-flow__node[data-id="${id}"]`)
          ?.getBoundingClientRect();
        if (!rect) {
          missing.push(id);
          continue;
        }
        const left = rect.left - bounds.left;
        const top = rect.top - bounds.top;
        const right = left + rect.width;
        const bottom = top + rect.height;
        let off = false;
        if (right > bounds.width - 24) {
          dx = Math.max(dx, right - (bounds.width - 24));
          off = true;
        }
        if (left < 24) {
          dx = Math.min(dx, left - 24);
          off = true;
        }
        if (bottom > bounds.height - 24) {
          dy = Math.max(dy, bottom - (bounds.height - 24));
          off = true;
        }
        if (top < 24) {
          dy = Math.min(dy, top - 24);
          off = true;
        }
        if (off) outside.push(id);
      }
      pendingPanRef.current = missing.concat(outside);
      if ((dx !== 0 || dy !== 0) && panAttempts < 8) {
        panAttempts += 1;
        void setViewport({ x: x - dx, y: y - dy, zoom }, { duration: 200 });
        // Re-check after the pan animation settles: a single pass could
        // compute its delta from a mid-animation viewport and land short.
        panTimerRef.current = window.setTimeout(step, 260);
        return;
      }
      if (missing.length > 0 && missAttempts < 40) {
        missAttempts += 1;
        panFrameRef.current = requestAnimationFrame(step);
        return;
      }
      if (outside.length > 0) {
        // Diagnostic breadcrumb: if a pan ever gives up with nodes still
        // off-canvas, this lands in the browser console and any Playwright
        // trace captured for the failure.
        console.warn('[canvas] pan gave up; nodes still off-canvas:', outside);
      }
    };
    panFrameRef.current = requestAnimationFrame(step);
  }, [workflow, readOnly, getViewport, setViewport, resizeTick]);

  const handleEdgesChange: OnEdgesChange<LoomEdge> = (changes) => {
    onEdgesChange(changes);
    const removed = changes.flatMap((change) => (change.type === 'remove' ? [change.id] : []));
    if (removed.length > 0) onDeleteEdges(removed);
  };

  return (
    <div
      ref={shellRef}
      className={`canvas-shell ${readOnly ? 'canvas-readonly' : ''}`}
      onDragOver={(event) => {
        if (readOnly) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = 'move';
      }}
      onDrop={(event) => {
        if (readOnly) return;
        event.preventDefault();
        const type = event.dataTransfer.getData('application/loom-node');
        if (!type) return;
        onAddNode(type, screenToFlowPosition({ x: event.clientX, y: event.clientY }));
      }}
    >
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={readOnly ? undefined : onNodesChange}
        onEdgesChange={readOnly ? undefined : handleEdgesChange}
        onConnect={readOnly ? undefined : onConnect}
        onNodeClick={(_, node) => onSelect(node.id)}
        onPaneClick={() => onSelect(null)}
        onNodeDragStop={readOnly ? undefined : (_, node) => onMove(node.id, node.position)}
        onBeforeDelete={
          readOnly
            ? undefined
            : async ({ nodes: deleting }) => {
                if (deleting.length === 0) return true;
                return confirmAction({
                  title:
                    deleting.length === 1
                      ? 'Delete this node?'
                      : `Delete ${deleting.length} nodes?`,
                  body: 'The selected nodes and their edges are removed from the draft. Undo restores them.',
                  confirmLabel: deleting.length === 1 ? 'Delete node' : 'Delete nodes',
                  cancelLabel: 'Cancel',
                });
              }
        }
        onNodesDelete={readOnly ? undefined : (deleted) => onDeleteNodes(deleted.map((node) => node.id))}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        edgesFocusable={!readOnly}
        connectionRadius={32}
        selectNodesOnDrag={false}
        fitView
        fitViewOptions={{ padding: 0.24, maxZoom: 1 }}
        /* L6 — legibility floor. Node type sizes are --text-md 13px
         * (titles), --text-sm 12px (kind/state) and --text-xs 11px
         * (port rows). At the old 0.25 floor fit-to-view rendered those
         * at ~3.3–8.7px, unreadable and the audit's complaint. At 0.85
         * the guaranteed effective sizes are 11.05 / 10.2 / 9.35px —
         * every label stays legible. maxZoom stays 1 for the initial
         * fit (small graphs never blow up); graphs too large to fit
         * at 0.85 open partial and require panning, which is the
         * accepted trade — the L5 minimap covers exactly that case. */
        minZoom={0.85}
        maxZoom={1.8}
        deleteKeyCode={readOnly ? null : ['Backspace', 'Delete']}
        proOptions={{ hideAttribution: true }}
        defaultEdgeOptions={{
          type: 'smoothstep',
          animated: true,
          style: { strokeWidth: 2 },
        }}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1.4} color="var(--canvas-dot)" />
        <Controls showInteractive={false} />
        {!readOnly && onAutoArrange && (
          <button type="button" className="canvas-arrange" onClick={onAutoArrange}>
            Auto-arrange
          </button>
        )}
        {showMinimap && (
          <MiniMap
            pannable
            zoomable
            nodeColor={(node) => categoryColor[(node.data as LoomNodeData).category] ?? '#8e8e93'}
            maskColor="var(--minimap-mask)"
          />
        )}
      </ReactFlow>
      {workflow.nodes.length === 0 && (
        <div className="canvas-empty">
          <h2>Add a node to begin</h2>
          <p className="muted">Drag one in from the library, or press ⌘K to search.</p>
        </div>
      )}
    </div>
  );
}

export function edgeSignature(edge: WorkflowEdge): string {
  return edgeId(edge);
}
