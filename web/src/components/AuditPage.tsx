import { AssetErrorState, AssetLoadingState } from './assets/AssetState';
import { useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import type { AuthEvent } from '../types';

/**
 * AuditPage: the admin-only view of the latest auth/governance events
 * (auth_events). This is operational audit, separate from the run chain.
 */
export function AuditPage({ principalRole }: { principalRole?: string }) {
  const admin = principalRole === 'admin';
  const [events, setEvents] = useState<AuthEvent[]>([]);
  const [eventType, setEventType] = useState('');
  const [failure, setFailure] = useState<LoadFailure | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    if (!admin) return;
    let cancelled = false;
    api
      .listAuthEvents()
      .then((result) => {
        if (!cancelled) {
          setEvents(result.events ?? []);
          setFailure(null);
          setLoaded(true);
        }
      })
      .catch((reason: unknown) => {
        if (!cancelled) {
          setFailure(classifyCurrentLoadFailure(reason));
          setLoaded(true);
        }
      });
    return () => {
      cancelled = true;
    };
  }, [admin, reload]);

  const types = useMemo(
    () => Array.from(new Set(events.map((event) => event.type))).sort(),
    [events],
  );
  const filtered = eventType ? events.filter((event) => event.type === eventType) : events;

  if (!admin) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1} data-testid="audit-page">
        <header className="page-head">
          <div>
            <h1>Audit</h1>
          </div>
        </header>
        <p className="banner notice">Audit events are admin-only.</p>
      </main>
    );
  }

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1} data-testid="audit-page">
      <header className="page-head">
        <div>
          <h1>Audit</h1>
          <p className="lede">
            The latest 200 authentication, governance and publish events. Admin-only;
            this is operational audit, separate from the run event chain.
          </p>
        </div>
      </header>

      {failure && (
        <AssetErrorState
          failure={failure}
          onRetry={() => setReload((count) => count + 1)}
          testId="audit-error"
        />
      )}

      <section className="dashboard-panel">
        <header className="dashboard-panel-head">
          <div>
            <h2>Events</h2>
          </div>
        </header>
        <div className="pad">
          <label className="audit-filter" htmlFor="audit-type-filter">
            <span>Event type</span>
            <select
              id="audit-type-filter"
              className="text-input"
              data-testid="audit-type-filter"
              value={eventType}
              onChange={(event) => setEventType(event.target.value)}
            >
              <option value="">All events</option>
              {types.map((type) => (
                <option key={type} value={type}>
                  {type}
                </option>
              ))}
            </select>
          </label>

          {!loaded && !failure && <AssetLoadingState label="Loading events…" className="muted small" />}
          {loaded && filtered.length === 0 && (
            <p className="muted small">
              {events.length === 0 ? 'No audit events yet.' : 'No events match this filter.'}
            </p>
          )}
          {loaded && filtered.length > 0 && (
            <ul className="audit-list" role="list">
              {filtered.map((event) => (
                <li
                  className="audit-row"
                  key={event.id}
                  data-testid="audit-event"
                  data-type={event.type}
                  aria-label={`${event.actor || event.userId || 'system'} ${event.type} ${new Date(
                    event.at,
                  ).toLocaleString()}`}
                >
                  <span className="audit-type">
                    <code>{event.type}</code>
                  </span>
                  <span className="audit-detail">{event.detail || '—'}</span>
                  <span className="audit-meta">
                    <span className="meta-list meta-strict">
                      <span title={event.actor || event.userId || 'system'}>
                        {event.actor || event.userId || 'system'}
                      </span>
                      <span title={new Date(event.at).toLocaleString()}>
                        {new Date(event.at).toLocaleString()}
                      </span>
                      {event.ip ? (
                        <span title={event.ip}>{event.ip}</span>
                      ) : null}
                    </span>
                  </span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </section>
    </main>
  );
}
