import type { RunDiff } from '../../types';
import { durationLabel, pretty } from './helpers';

/** RunDiffView: per-node base-vs-compared verdicts for two runs. */
export function RunDiffView({ diff }: { diff: RunDiff }) {
  return (
    <div className="run-diff" role="region" aria-label="Run diff">
      <div className="run-diff-head">
        <strong>{diff.changed ? `${diff.changed} changed` : 'No differences'}</strong>
        <span className="muted small">{diff.nodes.length} nodes compared</span>
      </div>
      <div className="diff-rows">
        {diff.nodes.map((node) => {
          const changed =
            !node.inputMatch || !node.outputMatch || node.baseStatus !== node.againstStatus;
          return (
            <details key={node.nodeId} className={`diff-row ${changed ? 'diff-changed' : ''}`}>
              <summary>
                <span className="diff-node">
                  <strong>{node.name || node.nodeId}</strong>
                  <code>{node.nodeId}</code>
                </span>
                <span className="diff-statuses">
                  {node.baseStatus || 'missing'} → {node.againstStatus || 'missing'}
                </span>
                <span className="diff-durations">
                  {durationLabel(node.baseDurationMs)} → {durationLabel(node.againstDurationMs)}
                </span>
                <span className={`diff-badge ${node.inputMatch ? 'same' : 'changed'}`}>
                  input {node.inputMatch ? 'same' : 'changed'}
                </span>
                <span className={`diff-badge ${node.outputMatch ? 'same' : 'changed'}`}>
                  output {node.outputMatch ? 'same' : 'changed'}
                </span>
              </summary>
              <div className="diff-detail">
                <div className="diff-outputs">
                  <div>
                    <span className="field-label">Base output</span>
                    <pre className="json-view">{pretty(node.baseOutput)}</pre>
                  </div>
                  <div>
                    <span className="field-label">Compared output</span>
                    <pre className="json-view">{pretty(node.againstOutput)}</pre>
                  </div>
                </div>
                <div className="diff-hashes">
                  <span>
                    input: <code>{node.baseInputHash || '—'}</code> →{' '}
                    <code>{node.againstInputHash || '—'}</code>
                  </span>
                  <span>
                    output: <code>{node.baseOutputHash || '—'}</code> →{' '}
                    <code>{node.againstOutputHash || '—'}</code>
                  </span>
                </div>
              </div>
            </details>
          );
        })}
      </div>
    </div>
  );
}
