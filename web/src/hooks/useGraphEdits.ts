import { useCallback } from 'react';
import { edgeId, makeNode, outputType, parseHandle, portCompatibility } from '../catalog';
import type { Connection } from '@xyflow/react';
import type { NodeDef, Workflow } from '../types';

// Graph-edit callbacks (Phase 6 D6-1): add/move/delete nodes and edges
// plus the connect validation chain. All flow through commit/apply so
// undo coalescing semantics stay in one place.
export function useGraphEdits(args: {
  workflowRef: React.MutableRefObject<Workflow | null>;
  defsByType: Record<string, NodeDef>;
  commit: (next: Workflow, record?: boolean, coalesceKey?: string) => void;
  apply: (mutator: (current: Workflow) => Workflow, record?: boolean, coalesceKey?: string) => void;
  notify: (message: string, tone?: 'info' | 'error') => void;
  setSelectedId: React.Dispatch<React.SetStateAction<string | null>>;
}) {
  const { workflowRef, defsByType, commit, apply, notify, setSelectedId } = args;

  const addNode = useCallback(
    (def: NodeDef, position?: { x: number; y: number }) => {
      const current = workflowRef.current;
      if (!current) return;
      const node = makeNode(def, current.nodes);
      if (position) node.position = position;
      commit({ ...current, nodes: [...current.nodes, node] });
      setSelectedId(node.id);
    },
    [commit, setSelectedId, workflowRef],
  );

  const moveNode = useCallback(
    (id: string, position: { x: number; y: number }) => {
      const current = workflowRef.current;
      if (!current) return;
      const node = current.nodes.find((item) => item.id === id);
      if (!node || (node.position.x === position.x && node.position.y === position.y)) return;
      commit(
        {
          ...current,
          nodes: current.nodes.map((item) => (item.id === id ? { ...item, position } : item)),
        },
        true,
        `move:${id}`,
      );
    },
    [commit, workflowRef],
  );

  const deleteNodes = useCallback(
    (ids: string[]) => {
      const removed = new Set(ids);
      apply(
        (current) => ({
          ...current,
          nodes: current.nodes.filter((node) => !removed.has(node.id)),
          edges: current.edges.filter(
            (edge) => !removed.has(edge.from.node) && !removed.has(edge.to.node),
          ),
        }),
        true,
        // One Backspace on a selected node arrives as two commits (edge
        // removal via onEdgesChange, then node removal via onNodesDelete);
        // coalescing under one key keeps undo a single step that restores
        // both the node and its edges.
        'delete',
      );
      setSelectedId((id) => (id && removed.has(id) ? null : id));
    },
    [apply, setSelectedId],
  );

  const deleteEdges = useCallback(
    (ids: string[]) => {
      const removed = new Set(ids);
      apply(
        (current) => ({
          ...current,
          edges: current.edges.filter((edge) => !removed.has(edgeId(edge))),
        }),
        true,
        'delete',
      );
    },
    [apply],
  );

  const connect = useCallback(
    (connection: Connection) => {
      const current = workflowRef.current;
      if (!current) return;
      const source = current.nodes.find((node) => node.id === connection.source);
      const target = current.nodes.find((node) => node.id === connection.target);
      if (!source || !target) return;
      if (source.id === target.id) {
        notify('A node cannot connect to itself.', 'error');
        return;
      }
      const sourceDef = defsByType[source.type];
      const targetDef = defsByType[target.type];
      if (!sourceDef || !targetDef) return;
      if (targetDef.category === 'trigger') {
        notify('Triggers cannot have incoming edges.', 'error');
        return;
      }
      const fromPort = parseHandle(connection.sourceHandle, 'out');
      const toPort = parseHandle(connection.targetHandle, 'in');
      if (!sourceDef.outputs.includes(fromPort) || !targetDef.inputs.includes(toPort)) {
        notify('That port is not available on this node.', 'error');
        return;
      }
      if (current.edges.some((edge) => edge.to.node === target.id && edge.to.port === toPort)) {
        notify(`${target.name || target.id}'s “${toPort}” input is already connected.`, 'error');
        return;
      }
      const sourceType = outputType(sourceDef, source.config ?? {});
      const compatibility = portCompatibility(sourceType, targetDef.inputType);
      if (!compatibility.ok) {
        notify(
          `${sourceDef.type} produces ${sourceType}, but ${targetDef.type} expects ${targetDef.inputType}.`,
          'error',
        );
        return;
      }
      if (compatibility.warning) notify(compatibility.warning);
      commit({
        ...current,
        edges: [
          ...current.edges,
          { from: { node: source.id, port: fromPort }, to: { node: target.id, port: toPort } },
        ],
      });
    },
    [commit, defsByType, notify, workflowRef],
  );

  return { addNode, moveNode, deleteNodes, deleteEdges, connect };
}
