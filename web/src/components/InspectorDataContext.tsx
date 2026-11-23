import { useEffect, useState } from 'react';
import { api } from '../api';
import { collectFieldPaths, expressionFor, labelFor } from './inspectorHelpers';
import type { DataContext, WorkflowNode } from '../types';

interface Props {
  workflowId: string;
  node: WorkflowNode;
  runId: string | null | undefined;
  runEventsCount: number;
  activeField: string;
  canInsert: boolean;
  insertExpression: (expression: string) => void;
}

// The "Data from upstream" inspector section plus the state machine behind
// it: loading the node's data context (from the live run or the latest run of
// the workflow), picking the source node, live-previewing the focused
// field's expression, and the debug-evaluate box. Extracted verbatim from
// Inspector — the parent still owns activeField and insertExpression
// because the Config section shares them.
export function InspectorDataContextPanel({
  workflowId,
  node,
  runId,
  runEventsCount,
  activeField,
  canInsert,
  insertExpression,
}: Props) {
  const [dataContext, setDataContext] = useState<DataContext | null>(null);
  const [contextRunId, setContextRunId] = useState<string | null>(null);
  const [pickedNode, setPickedNode] = useState('');
  const [previewValue, setPreviewValue] = useState('');
  const [previewError, setPreviewError] = useState('');
  const [debugExpr, setDebugExpr] = useState('');
  const [debugResult, setDebugResult] = useState<string | null>(null);
  const [debugError, setDebugError] = useState('');
  const [debugBusy, setDebugBusy] = useState(false);

  useEffect(() => {
    let cancelled = false;
    if (!node) {
      setDataContext(null);
      setContextRunId(null);
      return;
    }
    const nodeId = node.id;
    void (async () => {
      let id = runId ?? null;
      if (!id) {
        try {
          const result = await api.listRuns(workflowId, 1);
          id = result.runs?.[0]?.id ?? null;
        } catch {
          id = null;
        }
      }
      if (!id) {
        if (!cancelled) {
          setDataContext(null);
          setContextRunId(null);
        }
        return;
      }
      try {
        const context = await api.dataContext(id, nodeId);
        if (!cancelled) {
          setDataContext(context);
          setContextRunId(id);
        }
      } catch {
        if (!cancelled) {
          setDataContext(null);
          setContextRunId(null);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [node?.id, runId, runEventsCount, workflowId]);

  const evaluate = async () => {
    if (!contextRunId || !debugExpr.trim()) return;
    setDebugBusy(true);
    setDebugError('');
    setDebugResult(null);
    try {
      const result = await api.previewExpression(workflowId, {
        runId: contextRunId,
        nodeId: node?.id,
        expression: debugExpr,
      });
      setDebugResult(result.value);
    } catch (reason) {
      setDebugError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setDebugBusy(false);
    }
  };

  useEffect(() => {
    if (!dataContext) {
      setPickedNode('');
      return;
    }
    setPickedNode((current) =>
      dataContext.upstream.includes(current) ? current : (dataContext.upstream[0] ?? ''),
    );
  }, [dataContext]);

  const activeValue = activeField ? node.config?.[activeField] : undefined;

  useEffect(() => {
    setPreviewValue('');
    setPreviewError('');
    if (!contextRunId || !activeField || typeof activeValue !== 'string' || !activeValue.includes('{{')) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      api
        .previewExpression(workflowId, {
          runId: contextRunId,
          nodeId: node?.id,
          expression: activeValue,
        })
        .then((result) => {
          if (!cancelled) setPreviewValue(result.value);
        })
        .catch((error: unknown) => {
          if (!cancelled) setPreviewError(error instanceof Error ? error.message : String(error));
        });
    }, 350);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [activeField, activeValue, contextRunId, node?.id, workflowId]);

  const upstream = dataContext
    ? dataContext.upstream
        .map((id) => dataContext.nodes.find((item) => item.nodeId === id))
        .filter((item): item is NonNullable<typeof item> => Boolean(item))
    : [];
  const picked = upstream.find((item) => item.nodeId === pickedNode) ?? upstream[0];

  return (
    <section className="inspector-section">
      <h3>Data from upstream</h3>
      {!dataContext && (
        <p className="field-hint">Run this workflow once to load fields from upstream nodes.</p>
      )}
      {dataContext && upstream.length === 0 && (
        <p className="field-hint">This node has no upstream nodes yet.</p>
      )}
      {dataContext && upstream.length > 0 && (
        <>
          <label className="field" htmlFor="data-source">
            <span className="field-label">Source node</span>
            <select
              id="data-source"
              className="text-input"
              value={picked?.nodeId ?? ''}
              onChange={(event) => setPickedNode(event.target.value)}
            >
              {upstream.map((item) => (
                <option key={item.nodeId} value={item.nodeId}>
                  {item.name} ({item.nodeId}) · {item.status}
                </option>
              ))}
            </select>
          </label>
          {!picked?.outputs && <p className="field-hint">No output recorded for this node.</p>}
          {picked && picked.outputs && (
            <div className="data-fields">
              {Object.entries(picked.outputs).map(([port, item]) => {
                const paths: string[] = [];
                collectFieldPaths(item, '', paths);
                return (
                  <div className="data-port" key={port}>
                    <span className="field-label">{port}</span>
                    {paths.length === 0 && <span className="muted small">empty</span>}
                    {paths.map((path) => {
                      const expression = expressionFor(picked.nodeId, port, path);
                      return (
                        <button
                          type="button"
                          className="data-field"
                          key={path}
                          disabled={!canInsert}
                          title={canInsert ? `Insert ${expression}` : 'Focus a text field first'}
                          onClick={() => insertExpression(expression)}
                        >
                          {path}
                        </button>
                      );
                    })}
                  </div>
                );
              })}
            </div>
          )}
          <p className="field-hint">
            {canInsert
              ? `Click a field to insert it into “${labelFor(activeField)}”.`
              : 'Focus a text field, then click a field to insert it.'}
          </p>
          {previewValue && (
            <div className="data-preview">
              <span className="field-label">Preview</span>
              <code>{previewValue}</code>
            </div>
          )}
          {previewError && <p className="field-error">{previewError}</p>}
          {contextRunId && (
            <div className="debug-eval">
              <label className="field" htmlFor="debug-expression">
                <span className="field-label">Evaluate expression</span>
                <input
                  id="debug-expression"
                  className="text-input code-input"
                  value={debugExpr}
                  onChange={(event) => setDebugExpr(event.target.value)}
                  placeholder='{{ $json.email }}'
                  spellCheck={false}
                  onKeyDown={(event) => {
                    if (event.key === 'Enter') void evaluate();
                  }}
                />
              </label>
              <button
                type="button"
                className="button subtle"
                onClick={() => void evaluate()}
                disabled={debugBusy || !debugExpr.trim()}
              >
                {debugBusy ? 'Evaluating…' : 'Evaluate'}
              </button>
              {debugError && <p className="field-error">{debugError}</p>}
              {debugResult !== null && (
                <div className="data-preview">
                  <span className="field-label">Result</span>
                  <code>{debugResult}</code>
                </div>
              )}
            </div>
          )}
        </>
      )}
    </section>
  );
}
