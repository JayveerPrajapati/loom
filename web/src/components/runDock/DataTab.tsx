import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import type { Dispatch, SetStateAction } from 'react';
import type { RunEvent, Workflow } from '../../types';
import { STATUS_LABEL, JsonPaths, pretty } from '../runDetailHelpers';
import type { DockTab } from '../runDetailHelpers';
import type { ReplayState, StepDetail } from '../RunDock';

/** DataTab: the selected step's recorded data plus replay controls. */
export function DataTab({
  setSelectedEvent,
  setSelectedNodeId,
  setDock,
  activeEvent,
  selectedNode,
  nodeName,
  activeStatus,
  detail,
  request,
  response,
  inputDraft,
  setInputDraft,
  replay,
  busy,
  hermetic,
  setHermetic,
  replayStep,
  replayStepLive,
  replayFrom,
  replayIsStep,
}: {
  setSelectedEvent: Dispatch<SetStateAction<RunEvent | null>>;
  setSelectedNodeId: Dispatch<SetStateAction<string | null>>;
  setDock: Dispatch<SetStateAction<DockTab>>;
  activeEvent: RunEvent | null;
  selectedNode: Workflow['nodes'][number] | null;
  nodeName: (nodeId?: string) => string;
  activeStatus: string | undefined;
  detail: StepDetail;
  request: unknown;
  response: unknown;
  inputDraft: string | null;
  setInputDraft: Dispatch<SetStateAction<string | null>>;
  replay: ReplayState;
  busy: string;
  hermetic: boolean;
  setHermetic: Dispatch<SetStateAction<boolean>>;
  replayStep: (event: RunEvent) => Promise<void>;
  replayStepLive: (event: RunEvent) => Promise<void>;
  replayFrom: (event: RunEvent) => Promise<void>;
  replayIsStep: boolean;
}) {
  return (
    <>
      {!activeEvent && !selectedNode && (
        <p className="muted small">
          Select a node on the canvas or a step in the timeline to inspect its data.
        </p>
      )}
      {(activeEvent || selectedNode) && (
        <div className="event-detail" role="region" aria-label="Step data">
          <div className="run-node-head">
            <div>
              <h3>{selectedNode?.name ?? nodeName(activeEvent?.nodeId)}</h3>
              <code>{selectedNode?.type ?? activeEvent?.type ?? 'run'}</code>
            </div>
            <div className="run-node-head-actions">
              {activeStatus && (
                <span className={`status-text status-${activeStatus}`}>
                  {STATUS_LABEL[activeStatus] ?? activeStatus}
                </span>
              )}
              <button
                type="button"
                className="icon-button"
                onClick={() => {
                  setSelectedEvent(null);
                  setSelectedNodeId(null);
                  setDock('raw');
                }}
                aria-label="Close step data"
              >
                ×
              </button>
            </div>
          </div>
          {activeEvent?.error && <ActionError>{activeEvent.error}</ActionError>}
          {(() => {
            // Output contract verdict (v2 D16): recorded on the
            // node.finished event, surfaced here — never a gate.
            const raw = activeEvent?.data?.contract as
              | { valid: boolean; violations?: { path: string; message: string }[] }
              | undefined;
            if (!raw) return null;
            return (
              <div className="event-detail-block" data-testid="contract-verdict">
                <span className="field-label">Output contract</span>
                {raw.valid ? (
                  <p className="banner notice">Output conforms to the contract.</p>
                ) : (
                  <ul className="contract-violations">
                    {(raw.violations ?? []).map((violation, index) => (
                      <li key={index}>
                        <code>{violation.path || '(root)'}</code>: {violation.message}
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            );
          })()}
          {detail.loading && <AssetLoadingState label="Loading data…" className="muted small" />}
          {detail.error && <ActionError>{detail.error}</ActionError>}
          {!detail.loading &&
            !detail.error &&
            detail.input === undefined &&
            detail.output === undefined &&
            detail.effects === undefined &&
            detail.config === undefined &&
            (detail.credentials === undefined ||
              Object.keys(detail.credentials).length === 0) &&
            request === undefined &&
            response === undefined && (
              <p className="muted small">No data recorded for this step.</p>
            )}
          {detail.config !== undefined && (
            <div className="event-detail-block">
              <span className="field-label">Configuration</span>
              <pre className="json-view">{pretty(detail.config)}</pre>
            </div>
          )}
          {detail.credentials !== undefined &&
            Object.keys(detail.credentials).length > 0 && (
              <div className="event-detail-block">
                <span className="field-label">Credentials</span>
                <pre className="json-view">{pretty(detail.credentials)}</pre>
              </div>
            )}
          {request !== undefined && (
            <div className="event-detail-block">
              <span className="field-label">Request</span>
              <pre className="json-view">{pretty(request)}</pre>
            </div>
          )}
          {response !== undefined && (
            <div className="event-detail-block">
              <span className="field-label">Response</span>
              <pre className="json-view">{pretty(response)}</pre>
            </div>
          )}
          {detail.effects !== undefined && (
            <div className="event-detail-block">
              <span className="field-label">External effects</span>
              <pre className="json-view">{pretty(detail.effects)}</pre>
            </div>
          )}
          {detail.input !== undefined && (
            <div className="event-detail-block">
              <span className="field-label">Input item</span>
              <textarea
                className="json-input"
                spellCheck={false}
                aria-label="Node input JSON, editable before replay"
                value={inputDraft !== null ? inputDraft : pretty(detail.input)}
                onChange={(change) => setInputDraft(change.target.value)}
              />
              <span className="field-hint">
                Edit this before replaying to re-execute with a changed input.
              </span>
            </div>
          )}
          {detail.output !== undefined && (
            <div className="event-detail-block">
              <span className="field-label">Output item</span>
              <pre className="json-view">{pretty(detail.output)}</pre>
              <JsonPaths value={detail.output} />
            </div>
          )}
          {activeEvent &&
            (activeEvent.type === 'node.finished' || activeEvent.type === 'node.failed') &&
            activeEvent.nodeId && (
              <>
                <div className="event-detail-actions">
                  {activeEvent.type === 'node.finished' && (
                    <>
                      <button
                        className="button subtle"
                        onClick={() => void replayStep(activeEvent)}
                        disabled={replay.busy || Boolean(busy)}
                      >
                        Verify step{hermetic ? ' (no network)' : ' (compare only)'}
                      </button>
                      <button
                        className="button subtle"
                        onClick={() => void replayStepLive(activeEvent)}
                        disabled={replay.busy || Boolean(busy) || hermetic}
                        title={hermetic ? 'Turn off "No network" to re-run live.' : undefined}
                      >
                        Re-run live
                      </button>
                    </>
                  )}
                  <button
                    className={`button ${
                      activeEvent.type === 'node.failed' ? 'primary' : 'subtle'
                    }`}
                    onClick={() => void replayFrom(activeEvent)}
                    disabled={replay.busy || Boolean(busy)}
                  >
                    {activeEvent.type === 'node.failed'
                      ? 'Retry from failed node'
                      : 'Replay from here'}
                    {hermetic ? ' (no network)' : ' (live)'}
                  </button>
                  <label
                    className={`hermetic-toggle ${hermetic ? 'checked' : ''}`}
                    title="Replays use the recorded effects instead of live external calls."
                  >
                    <input
                      type="checkbox"
                      checked={hermetic}
                      onChange={(change) => setHermetic(change.target.checked)}
                    />
                    <span>No network</span>
                  </label>
                </div>
                <div className={`replay-mode-note ${hermetic ? 'safe' : 'live'}`}>
                  {hermetic ? (
                    <span>
                      No network: replays re-run against the recorded effects. Nothing
                      external is touched and the recorded run is never modified.
                    </span>
                  ) : (
                    <span>
                      Live: replays re-execute nodes for real (requests, emails and
                      other external effects repeat). The recorded run is never modified.
                    </span>
                  )}
                </div>
              </>
            )}
          {replay.error && <ActionError>{replay.error}</ActionError>}
          {replay.result && (
            <div
              className={`replay-result ${
                replayIsStep ? (replay.result.match ? 'match' : 'diverge') : ''
              }`}
            >
              <strong>
                {replay.result.mode === 'step'
                  ? replay.result.match
                    ? 'Step verification matched the recorded output.'
                    : 'Step verification diverged from the recorded output.'
                  : replay.result.mode === 'step-execute'
                    ? replay.result.match
                      ? 'Live step re-run matched the recorded output.'
                      : 'Live step re-run diverged from the recorded output.'
                    : `Replay started as run ${replay.result.runId.split('_').pop()}.`}
              </strong>
              {replay.result.mode !== 'step' && (
                <span className="replay-effects-note">
                  The recorded run is unchanged; the replay is recorded as its own run.
                </span>
              )}
              {replay.hermetic && replayIsStep && (
                <span className="replay-effects-note">
                  {typeof replay.result.effects === 'number' && replay.result.effects > 0
                    ? `Replayed from ${replay.result.effects} recorded effect${
                        replay.result.effects === 1 ? '' : 's'
                      } — no network calls.`
                    : 'Replayed from the recording — no network calls.'}
                </span>
              )}
              {(replay.result.misses?.length ?? 0) > 0 && (
                <span className="replay-effect-list">
                  {replay.result.misses?.map((miss) => (
                    <span key={`miss-${miss.kind}-${miss.index}`}>
                      Missing {miss.kind} effect {miss.index}.
                    </span>
                  ))}
                </span>
              )}
              {(replay.result.diffs?.length ?? 0) > 0 && (
                <span className="replay-effect-list">
                  {replay.result.diffs?.map((diff) => (
                    <span key={`diff-${diff.kind}-${diff.index}`}>
                      {diff.kind} effect {diff.index} differs from the recording.
                    </span>
                  ))}
                </span>
              )}
            </div>
          )}
        </div>
      )}
    </>
  );
}
