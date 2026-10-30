import { useEffect, useMemo } from 'react';
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
  onDeleteNodes,
  onDeleteEdges,
  onConnect,
  onAddNode,
  readOnly = false,
}: Props) {
  const [nodes, setNodes, onNodesChange] = useNodesState<LoomNode>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<LoomEdge>([]);
  const { fitView, screenToFlowPosition } = useReactFlow();

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
        void fitView({ padding: 0.24, duration: 220 });
      });
      first = second;
    });
    return () => cancelAnimationFrame(first);
  }, [fitView, workflow.id]);

  const handleEdgesChange: OnEdgesChange<LoomEdge> = (changes) => {
    onEdgesChange(changes);
    const removed = changes.flatMap((change) => (change.type === 'remove' ? [change.id] : []));
    if (removed.length > 0) onDeleteEdges(removed);
  };

  return (
    <div
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
        onNodesDelete={readOnly ? undefined : (deleted) => onDeleteNodes(deleted.map((node) => node.id))}
        nodesDraggable={!readOnly}
        nodesConnectable={!readOnly}
        edgesFocusable={!readOnly}
        connectionRadius={32}
        selectNodesOnDrag={false}
        fitView
        fitViewOptions={{ padding: 0.24 }}
        minZoom={0.25}
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
        <MiniMap
          pannable
          zoomable
          nodeColor={(node) => categoryColor[(node.data as LoomNodeData).category] ?? '#8e8e93'}
          maskColor="var(--minimap-mask)"
        />
      </ReactFlow>
      {workflow.nodes.length === 0 && (
        <div className="canvas-empty">
          <p className="eyebrow">Blank canvas</p>
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
