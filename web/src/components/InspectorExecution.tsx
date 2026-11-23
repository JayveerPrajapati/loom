import { OriginBadge } from './assets/OriginBadge';
import type { RetryPolicy, WorkflowNode } from '../types';

interface Props {
  node: WorkflowNode;
  onNodePatch: (patch: Partial<WorkflowNode>) => void;
  /** The pinned profile's retry policy (UI-audit B2): applies when the
   * node sets none — displayed as inherited values with a badge. */
  profileRetry?: RetryPolicy | null;
}

function updateRetryState(
  node: WorkflowNode,
  onNodePatch: (patch: Partial<WorkflowNode>) => void,
  patch: Record<string, unknown>,
) {
  const retry = node.retry ?? {};
  const next = { ...retry, ...patch };
  const hasValue = Object.values(next).some((value) => typeof value === 'number' && value > 0);
  onNodePatch({ retry: hasValue ? next : undefined });
}

function updateBatchState(
  node: WorkflowNode,
  onNodePatch: (patch: Partial<WorkflowNode>) => void,
  patch: Record<string, unknown>,
) {
  const batch = node.batch ?? {};
  const next = { ...batch, ...patch };
  const hasValue = (next.itemsPath ?? '').trim() !== '' || next.continueOnItemError === true;
  onNodePatch({ batch: hasValue ? next : undefined });
}

// The Retry / Failure handling / Batch items inspector sections, extracted
// verbatim from Inspector. The has-a-value semantics (dropping the policy
// object when nothing is set) move with the JSX.
export function InspectorExecution({ node, onNodePatch, profileRetry }: Props) {
  const retryInherited = !node.retry && !!profileRetry;
  const retry = node.retry ?? (retryInherited ? profileRetry : {}) ?? {};
  const batch = node.batch ?? {};
  const updateRetry = (patch: Record<string, unknown>) =>
    updateRetryState(node, onNodePatch, patch);
  const updateBatch = (patch: Record<string, unknown>) =>
    updateBatchState(node, onNodePatch, patch);
  return (
    <>
      <section className="inspector-section">
        <h3>
          Retry
          {retryInherited && <OriginBadge state="inherited" />}
        </h3>
        <div className="retry-fields">
          <label className="field" htmlFor="retry-attempts">
            <span className="field-label">Max attempts</span>
            <input
              id="retry-attempts"
              className="text-input"
              type="number"
              min={0}
              max={20}
              value={retry.maxAttempts ?? ''}
              onChange={(event) =>
                updateRetry({ maxAttempts: Math.max(0, Number(event.target.value) || 0) })
              }
            />
          </label>
          <label className="field" htmlFor="retry-backoff">
            <span className="field-label">Backoff (ms)</span>
            <input
              id="retry-backoff"
              className="text-input"
              type="number"
              min={0}
              max={600000}
              value={retry.backoffMs ?? ''}
              onChange={(event) =>
                updateRetry({ backoffMs: Math.max(0, Number(event.target.value) || 0) })
              }
            />
          </label>
          <label className="field" htmlFor="retry-max-delay">
            <span className="field-label">Max delay (ms)</span>
            <input
              id="retry-max-delay"
              className="text-input"
              type="number"
              min={0}
              max={3600000}
              value={retry.maxDelayMs ?? ''}
              onChange={(event) =>
                updateRetry({ maxDelayMs: Math.max(0, Number(event.target.value) || 0) })
              }
            />
          </label>
        </div>
        <p className="field-hint">
          Leave at 0 to use the engine default (3 attempts, 100 ms quadratic
          backoff). A policy uses doubling backoff capped at the max delay.
        </p>
      </section>

      <section className="inspector-section">
        <h3>Failure handling</h3>
        <label className="field" htmlFor="on-error">
          <span className="field-label">When this node fails</span>
          <select
            id="on-error"
            className="text-input"
            value={node.onError ?? 'stop'}
            onChange={(event) =>
              onNodePatch({
                onError:
                  event.target.value === 'stop'
                    ? undefined
                    : (event.target.value as 'continue' | 'branch'),
              })
            }
          >
            <option value="stop">Stop the run</option>
            <option value="continue">Continue with an error item</option>
            <option value="branch">Route to the error output</option>
          </select>
        </label>
        <p className="field-hint">
          Continue passes <code>{'{ error, nodeId, attempts }'}</code> on the main
          output. Branch sends it to the <code>error</code> output instead and
          skips main-output nodes.
        </p>
      </section>

      <section className="inspector-section">
        <h3>Batch items</h3>
        <label className="field" htmlFor="batch-path">
          <span className="field-label">Array path</span>
          <input
            id="batch-path"
            className="text-input"
            placeholder="items"
            value={batch.itemsPath ?? ''}
            onChange={(event) => updateBatch({ itemsPath: event.target.value })}
          />
        </label>
        <label className="checkbox-field" htmlFor="batch-continue">
          <input
            id="batch-continue"
            type="checkbox"
            checked={batch.continueOnItemError === true}
            onChange={(event) => updateBatch({ continueOnItemError: event.target.checked })}
          />
          <span>Continue after item failures</span>
        </label>
        <p className="field-hint">
          Runs this node once per object in the array (default <code>items</code>).
          The output is <code>{'{ items, count, failed }'}</code>; each failure
          appends an <code>item.failed</code> event.
        </p>
      </section>
    </>
  );
}
