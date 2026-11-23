import { ActionError } from './assets/ActionError';
import { useEffect, useRef, useState } from 'react';
import { api } from '../api';
import { AssetLoadingState } from './assets/AssetState';

/**
 * OpsPage: the storage-operations surface at /ops (operational-trust
 * campaign 2, D8-5) — the read-only view the API and `loom ops
 * storage` serve. Status-card pattern like PythonStatusCard.
 * Editor-role page; the server enforces the gate and the page renders
 * the honest error for anyone else.
 *
 * The page owns its scroll through .page-shell — the body clips
 * (overflow-y: clip) and the app column is height-locked, so without
 * this wrapper the panels below the fold were unreachable.
 */
type StorageStats = {
  runs: number;
  activeRuns: number;
  events: number;
  blobs: number;
  blobBytes: number;
  databaseBytes: number;
  eventsPerDay: { day: string; events: number }[] | null;
  projectedEvents30d: number;
  sharedBlobs: number;
  sharedBlobBytes: number;
  largestRuns: { runId: string; workflowId: string; workflowName: string; status: string; events: number; bytes: number; at: string }[] | null;
  largestWorkflows: { workflowId: string; name: string; runs: number; events: number; bytes: number; lastActive: string }[] | null;
  reclaimable: { days: number; runs: number; events: number; blobs: number; bytes: number }[] | null;
};

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KiB`;
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MiB`;
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GiB`;
}

/** Coarse relative time for ledger rows; the absolute timestamp rides
 *  along in the title attribute. */
function timeAgo(iso: string): string {
  const ms = Date.now() - new Date(iso).getTime();
  if (!Number.isFinite(ms) || ms < 60_000) return 'just now';
  const minutes = Math.floor(ms / 60_000);
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  if (days < 30) return `${days}d ago`;
  return `${Math.floor(days / 30)}mo ago`;
}

function exportCsv(stats: StorageStats) {
  const lines = ['day,events'];
  for (const day of stats.eventsPerDay ?? []) lines.push(`${day.day},${day.events}`);
  lines.push(`projected-next-30d,${stats.projectedEvents30d}`);
  const blob = new Blob([lines.join('\n') + '\n'], { type: 'text/csv' });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = url;
  anchor.download = 'loom-events-per-day.csv';
  // Headless browsers only fire the download for anchors attached to
  // the document.
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/** One labeled metric with tabular numerals. */
function Metric({ label, value }: { label: string; value: string }) {
  return (
    <span className="ops-metric">
      <span className="ops-metric-label">{label}</span>
      <span className="ops-metric-value">{value}</span>
    </span>
  );
}

// L7 — a linear projection over a single day is a straight line to
// zero-or-noise; below three recorded days the card reports the actual
// history instead of dressing it up as a forecast.
const PROJECTION_MIN_DAYS = 3;

export function OpsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const [stats, setStats] = useState<StorageStats | null>(null);
  const [error, setError] = useState('');
  const [loadedAt, setLoadedAt] = useState('');
  const [loading, setLoading] = useState(true);
  // U6: the clipboard write's outcome is visible — "Copied" on success,
  // "Copy failed" on rejection, resetting after the same short window.
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  // Guards the fetch against stale/unmounted updates, so a response
  // landing after unmount (or a superseded refresh) never touches
  // state.
  const alive = useRef(true);

  const load = () => {
    setLoading(true);
    api
      .storageOps()
      .then((result) => {
        if (!alive.current) return;
        setStats(result.stats);
        setError('');
        setLoadedAt(new Date().toLocaleTimeString());
      })
      .catch((reason: Error) => {
        if (alive.current) setError(reason.message);
      })
      .finally(() => {
        if (alive.current) setLoading(false);
      });
  };

  useEffect(() => {
    alive.current = true;
    load();
    return () => {
      alive.current = false;
    };
  }, []);

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1} data-testid="ops-storage-page">
      <div className="dashboard-grid">
        <header className="page-head">
          <div>
            <h1>Storage &amp; operations</h1>
            <p className="lede">
              The append-only ledger&rsquo;s footprint, computed on demand. Reclaimable
              forecasts use the prune rules: terminal runs older than the horizon, plus
              blobs nothing surviving references.
            </p>
          </div>
        </header>

        {error && (
          <ActionError testId="ops-error">{error}</ActionError>
        )}

        {loading && !stats && !error && (
          <AssetLoadingState label="Loading storage & operations…" />
        )}

        {stats && (
          <>
            <section className="dashboard-panel" data-testid="ops-totals-card">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Totals</h2>
                  <p className="muted">
                    {stats.activeRuns} active run{stats.activeRuns === 1 ? '' : 's'}
                    {loadedAt ? ` · read ${loadedAt}` : ''}
                  </p>
                </div>
                <button className="button subtle" type="button" onClick={load} disabled={loading}>
                  Refresh
                </button>
              </header>
              <dl className="stat-row">
                <div>
                  <dt>Runs</dt>
                  <dd>{stats.runs.toLocaleString()}</dd>
                </div>
                <div>
                  <dt>Events</dt>
                  <dd>{stats.events.toLocaleString()}</dd>
                </div>
                <div>
                  <dt>Database</dt>
                  <dd>{formatBytes(stats.databaseBytes)}</dd>
                </div>
                <div>
                  <dt>Blobs</dt>
                  <dd>
                    {stats.blobs.toLocaleString()} · {formatBytes(stats.blobBytes)}
                  </dd>
                </div>
              </dl>
            </section>

            <section className="dashboard-panel" data-testid="ops-growth-card">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Growth &amp; projection</h2>
                  <p className="muted">
                    Events per day, last 14 days. The projection extends the trend
                    linearly — a trend, not a promise.
                  </p>
                </div>
                {stats.eventsPerDay && stats.eventsPerDay.length > 0 && (
                  <button
                    className="button subtle"
                    type="button"
                    data-testid="ops-growth-csv"
                    onClick={() => exportCsv(stats)}
                  >
                    Download CSV
                  </button>
                )}
              </header>
              {stats.eventsPerDay && stats.eventsPerDay.length > 0 ? (
                (() => {
                  const max = Math.max(...stats.eventsPerDay.map((day) => day.events), 1);
                  return (
                    <div
                      className="ops-chart"
                      data-testid="ops-growth-chart"
                      role="img"
                      aria-label={`Events per day: ${stats.eventsPerDay
                        .map((day) => `${day.day} ${day.events.toLocaleString()}`)
                        .join(', ')}`}
                    >
                      {stats.eventsPerDay.map((day) => (
                        <div
                          className="ops-chart-bar"
                          key={day.day}
                          title={`${day.day}: ${day.events.toLocaleString()} events`}
                        >
                          <span className="ops-chart-value">{day.events.toLocaleString()}</span>
                          <span
                            className="ops-chart-fill"
                            style={{ height: `${Math.round((day.events / max) * 100)}%` }}
                          />
                          <span className="ops-chart-day">{day.day.slice(5)}</span>
                        </div>
                      ))}
                    </div>
                  );
                })()
              ) : (
                <div className="empty-panel" data-testid="ops-growth-empty">
                  <h2>No events recorded yet</h2>
                  <p className="muted">
                    Every run appends signed events to the ledger — run a workflow and the daily
                    growth appears here.
                  </p>
                </div>
              )}
              {stats.eventsPerDay && stats.eventsPerDay.length > 0 && stats.eventsPerDay.length < PROJECTION_MIN_DAYS && (
                <p className="muted small" data-testid="ops-growth-sparse">
                  {stats.eventsPerDay.reduce((sum, day) => sum + day.events, 0).toLocaleString()}{' '}
                  event{stats.eventsPerDay.reduce((sum, day) => sum + day.events, 0) === 1 ? '' : 's'} recorded over{' '}
                  {stats.eventsPerDay.length} day{stats.eventsPerDay.length === 1 ? '' : 's'}. A
                  projection needs at least {PROJECTION_MIN_DAYS} days of history.
                </p>
              )}
              {stats.eventsPerDay && stats.eventsPerDay.length >= PROJECTION_MIN_DAYS ? (
                <p className="muted" data-testid="ops-growth-projection">
                  Projected events, next 30 days:{' '}
                  <strong>{stats.projectedEvents30d.toLocaleString()}</strong>
                </p>
              ) : (
                <p className="muted" data-testid="ops-growth-projection">
                  Projection unavailable — at least {PROJECTION_MIN_DAYS} days of history is
                  needed.
                </p>
              )}
            </section>

            <section className="dashboard-panel" data-testid="ops-dedup-card">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Content-addressed dedup</h2>
                  <p className="muted">
                    Blobs referenced by two or more events — the bytes the shared
                    store saved over copies.
                  </p>
                </div>
              </header>
              <dl className="stat-row">
                <div>
                  <dt>Shared blobs</dt>
                  <dd>{stats.sharedBlobs.toLocaleString()}</dd>
                </div>
                <div>
                  <dt>Bytes stored once</dt>
                  <dd>{formatBytes(stats.sharedBlobBytes)}</dd>
                </div>
              </dl>
            </section>

            <section className="dashboard-panel" data-testid="ops-reclaim-card">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Reclaimable at retention horizons</h2>
                  <p className="muted">
                    What pruning terminal runs older than each horizon would free today.
                    Enable <code>--retention-days</code> on serve to prune automatically.
                  </p>
                </div>
              <button
                className="button subtle"
                type="button"
                data-testid="ops-retention-copy"
                onClick={() => {
                  navigator.clipboard
                    .writeText('loom serve --retention-days N')
                    .then(() => {
                      setCopyState('copied');
                      window.setTimeout(() => setCopyState('idle'), 2000);
                    })
                    .catch(() => {
                      setCopyState('failed');
                      window.setTimeout(() => setCopyState('idle'), 2000);
                    });
                }}
              >
                {copyState === 'copied' ? 'Copied' : copyState === 'failed' ? 'Copy failed' : 'Copy command'}
              </button>
              {/* The clipboard result is asynchronous: the button label
                 shows it sighted, this persistent live region announces
                 it. It rides the same copyState, so the same 2s reset
                 clears both. */}
              <span
                className="visually-hidden"
                role="status"
                aria-live="polite"
                data-testid="ops-retention-copy-status"
              >
                {copyState === 'copied' ? 'Command copied' : copyState === 'failed' ? 'Copy failed' : ''}
              </span>
              </header>
              <table className="diff-table">
                <thead>
                  <tr>
                    <th scope="col">Horizon</th>
                    <th scope="col">Runs</th>
                    <th scope="col">Events</th>
                    <th scope="col">Blobs</th>
                    <th scope="col">Bytes</th>
                  </tr>
                </thead>
                <tbody>
                  {(stats.reclaimable ?? []).map((horizon) => (
                    <tr key={horizon.days}>
                      <td>{horizon.days} days</td>
                      <td>{horizon.runs.toLocaleString()}</td>
                      <td>{horizon.events.toLocaleString()}</td>
                      <td>{horizon.blobs.toLocaleString()}</td>
                      <td>{formatBytes(horizon.bytes)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </section>

            <section className="dashboard-panel ops-ledger-panel" data-testid="ops-workflows-card">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Storage by workflow</h2>
                  <p className="muted">Top 10 by referenced blob bytes.</p>
                </div>
              </header>
              {(stats.largestWorkflows ?? []).length === 0 ? (
                <div className="pad">
                  <p className="muted" data-testid="ops-workflows-empty">No blobs recorded yet.</p>
                </div>
              ) : (
                (() => {
                  const rows = stats.largestWorkflows ?? [];
                  const maxBytes = Math.max(...rows.map((wf) => wf.bytes), 1);
                  return (
                    <ul className="ops-ledger" data-testid="ops-workflows-ledger">
                      {rows.map((wf) => (
                        <li className="ops-ledger-row" key={wf.workflowId}>
                          <span className="ops-ledger-main">
                            <code className="ops-ledger-name" title={wf.name || wf.workflowId}>
                              {wf.name || wf.workflowId}
                            </code>
                          </span>
                          <span className="ops-ledger-metrics">
                            <Metric label="runs" value={wf.runs.toLocaleString()} />
                            <Metric label="events" value={wf.events.toLocaleString()} />
                            <Metric label="bytes" value={formatBytes(wf.bytes)} />
                          </span>
                          <span
                            className="ops-bar"
                            role="img"
                            aria-label={`${formatBytes(wf.bytes)} of ${formatBytes(maxBytes)}`}
                          >
                            <span
                              className="ops-bar-fill"
                              style={{ width: `${Math.max(Math.round((wf.bytes / maxBytes) * 100), 2)}%` }}
                            />
                          </span>
                          <span
                            className="ops-ledger-recency"
                            title={new Date(wf.lastActive).toLocaleString()}
                          >
                            {timeAgo(wf.lastActive)}
                          </span>
                        </li>
                      ))}
                    </ul>
                  );
                })()
              )}
            </section>

            <section className="dashboard-panel ops-ledger-panel" data-testid="ops-runs-card">
              <header className="dashboard-panel-head">
                <div>
                  <h2>Largest runs</h2>
                  <p className="muted">Top 10 by unique referenced blob bytes.</p>
                </div>
              </header>
              {(stats.largestRuns ?? []).length === 0 ? (
                <div className="pad">
                  <p className="muted" data-testid="ops-runs-empty">No runs recorded yet.</p>
                </div>
              ) : (
                (() => {
                  const rows = stats.largestRuns ?? [];
                  const maxBytes = Math.max(...rows.map((run) => run.bytes), 1);
                  return (
                    <ul className="ops-ledger" data-testid="ops-runs-ledger">
                      {rows.map((run) => (
                        <li className="ops-ledger-row" key={run.runId}>
                          <span className={`ops-chip status-${run.status}`}>{run.status}</span>
                          <span className="ops-ledger-main">
                            <button
                              type="button"
                              className="text-button ops-run-link"
                              title={run.runId}
                              onClick={() => onNavigate(`/runs/${encodeURIComponent(run.runId)}`)}
                            >
                              <code>{run.runId.slice(0, 8)}</code>
                            </button>
                            <span
                              className="ops-ledger-workflow"
                              title={run.workflowName ? `${run.workflowName} (${run.workflowId})` : run.workflowId}
                            >
                              {run.workflowName || run.workflowId}
                            </span>
                          </span>
                          <span className="ops-ledger-metrics">
                            <Metric label="events" value={run.events.toLocaleString()} />
                            <Metric label="bytes" value={formatBytes(run.bytes)} />
                          </span>
                          <span
                            className="ops-bar"
                            role="img"
                            aria-label={`${formatBytes(run.bytes)} of ${formatBytes(maxBytes)}`}
                          >
                            <span
                              className="ops-bar-fill"
                              style={{ width: `${Math.max(Math.round((run.bytes / maxBytes) * 100), 2)}%` }}
                            />
                          </span>
                          <span
                            className="ops-ledger-recency"
                            title={new Date(run.at).toLocaleString()}
                          >
                            {timeAgo(run.at)}
                          </span>
                        </li>
                      ))}
                    </ul>
                  );
                })()
              )}
            </section>
          </>
        )}
      </div>
    </main>
  );
}
