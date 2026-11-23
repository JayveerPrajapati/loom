import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import type { ReplayResult, RunEvent } from '../../types';
import { pretty } from './helpers';

/** HistoryEventDetail: one event's recorded data plus the replay
 *  actions available from the history dialog. */
export function HistoryEventDetail({
  detailEvent,
  detail,
  request,
  response,
  replay,
  onDetailEventChange,
  onReplayNode,
}: {
  detailEvent: RunEvent;
  detail: {
    loading: boolean;
    input?: unknown;
    output?: unknown;
    error?: string;
  };
  request: unknown;
  response: unknown;
  replay: { busy: boolean; result?: ReplayResult; error?: string };
  onDetailEventChange: (event: RunEvent | null) => void;
  onReplayNode: (mode: 'step' | 'from') => void;
}) {
  return (
    <div className="event-detail" role="region" aria-label="Step data">
      <div className="event-detail-head">
        <div>
          <strong>{detailEvent.type}</strong>
          {detailEvent.nodeId && <span className="event-node">{detailEvent.nodeId}</span>}
        </div>
        <button className="icon-button" onClick={() => onDetailEventChange(null)} aria-label="Close step data">
          ×
        </button>
      </div>
      {detail.loading && <AssetLoadingState label="Loading data…" className="muted small" />}
      {detail.error && <ActionError>{detail.error}</ActionError>}
      {!detail.loading &&
        !detail.error &&
        detail.input === undefined &&
        detail.output === undefined &&
        request === undefined &&
        response === undefined && <p className="muted small">No data recorded for this step.</p>}
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
      {detail.input !== undefined && (
        <div className="event-detail-block">
          <span className="field-label">Input item</span>
          <pre className="json-view">{pretty(detail.input)}</pre>
        </div>
      )}
      {detail.output !== undefined && (
        <div className="event-detail-block">
          <span className="field-label">Output item</span>
          <pre className="json-view">{pretty(detail.output)}</pre>
        </div>
      )}
      {(detailEvent.type === 'node.finished' || detailEvent.type === 'node.failed') &&
        detailEvent.nodeId && (
          <div className="event-detail-actions">
            {detailEvent.type === 'node.finished' && (
              <button
                type="button"
                className="button subtle"
                onClick={() => onReplayNode('step')}
                disabled={replay.busy}
              >
                Replay step
              </button>
            )}
            <button
              type="button"
              className={`button ${detailEvent.type === 'node.failed' ? 'primary' : 'subtle'}`}
              onClick={() => onReplayNode('from')}
              disabled={replay.busy}
            >
              {detailEvent.type === 'node.failed' ? 'Retry from failed node' : 'Replay from here'}
            </button>
          </div>
        )}
      {replay.busy && <p className="muted small">Replaying…</p>}
      {replay.error && <ActionError>{replay.error}</ActionError>}
      {replay.result && (
        <div
          className={`replay-result ${
            replay.result.mode === 'step' ? (replay.result.match ? 'match' : 'diverge') : ''
          }`}
        >
          <strong>
            {replay.result.mode === 'step'
              ? replay.result.match
                ? 'Step replay matched the recorded output.'
                : 'Step replay diverged from the recorded output.'
              : `Replay from ${replay.result.nodeId ?? 'node'}: ${replay.result.status} · new run ${replay.result.runId.split('_').pop()}`}
          </strong>
          {replay.result.mode === 'step' && (
            <span className="replay-hashes">
              <code>{replay.result.recordedHash ?? '—'}</code>
              {' → '}
              <code>{replay.result.actualHash ?? '—'}</code>
            </span>
          )}
        </div>
      )}
    </div>
  );
}
