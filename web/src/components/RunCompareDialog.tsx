import { AssetLoadingState } from './assets/AssetState';
import { ActionError } from './assets/ActionError';
import type { RefObject } from 'react';
import { durationLabel, formatTime, pretty } from './runDetailHelpers';
import type { Run, RunDiff } from '../types';

export interface CompareState {
  open: boolean;
  loading: boolean;
  error: string;
  runs: Run[];
  against: string;
  diff: RunDiff | null;
}

// The "compare runs" overlay, extracted verbatim from RunDetailPage. The
// compare state and loadCompare stay in the page; this is presentation only.
export function RunCompareDialog({
  compare,
  setCompare,
  compareRef,
  loadCompare,
}: {
  compare: CompareState;
  setCompare: React.Dispatch<React.SetStateAction<CompareState>>;
  compareRef: RefObject<HTMLDivElement>;
  loadCompare: (runId: string) => Promise<void>;
}) {
  if (!compare.open) return null;
  return (
    <div
      className="overlay"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) {
          setCompare((current) => ({ ...current, open: false }));
        }
      }}
    >
      <div
        ref={compareRef}
        className="dialog run-compare-dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Compare runs"
        tabIndex={-1}
      >
        <header className="run-compare-head">
          <div>
            <h2>Run differences</h2>
          </div>
          <button
            type="button"
            className="icon-button"
            onClick={() => setCompare((current) => ({ ...current, open: false }))}
          >
            Close
          </button>
        </header>
        <p className="muted small">
          Compare this run against another completed run of the same workflow.
        </p>
        {compare.loading && <AssetLoadingState label="Loading runs…" className="muted small" />}
        {compare.error && <ActionError>{compare.error}</ActionError>}
        {!compare.loading && compare.runs.length === 0 && (
          <p className="muted small">No other completed runs yet.</p>
        )}
        {compare.runs.length > 0 && (
          <label className="field" htmlFor="compare-run">
            <span className="field-label">Against</span>
            <select
              id="compare-run"
              className="text-input"
              value={compare.against}
              onChange={(event) => void loadCompare(event.target.value)}
            >
              {compare.runs.map((candidate) => (
                <option key={candidate.id} value={candidate.id}>
                  {candidate.id} · {candidate.status} · {formatTime(candidate.startedAt)}
                </option>
              ))}
            </select>
          </label>
        )}
        {compare.diff && (
          <div className="run-diff" role="region" aria-label="Run diff">
            <div className="run-diff-head">
              <strong>
                {compare.diff.changed ? `${compare.diff.changed} changed` : 'No differences'}
              </strong>
              <span className="muted small">{compare.diff.nodes.length} nodes compared</span>
            </div>
            <div className="diff-rows">
              {compare.diff.nodes.map((node) => {
                const changed =
                  !node.inputMatch ||
                  !node.outputMatch ||
                  node.baseStatus !== node.againstStatus;
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
                        {durationLabel(node.baseDurationMs)} →{' '}
                        {durationLabel(node.againstDurationMs)}
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
        )}
      </div>
    </div>
  );
}
