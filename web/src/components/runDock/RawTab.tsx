import { ActionError } from '../assets/ActionError';
import { useState, type Dispatch, type SetStateAction } from 'react';
import type { Run, RunEvent } from '../../types';
import { eventSummary, formatTime, readEffectsData, type Filter } from '../runDetailHelpers';

/** RawTab: the raw event list with filter, search, copy and download. */
export function RawTab({
  run,
  filter,
  setFilter,
  query,
  setQuery,
  copyLog,
  copied,
  downloadLog,
  visibleEvents,
  selectedEvent,
  selectEvent,
}: {
  run: Run;
  filter: Filter;
  setFilter: Dispatch<SetStateAction<Filter>>;
  query: string;
  setQuery: Dispatch<SetStateAction<string>>;
  copyLog: () => Promise<void>;
  copied: boolean;
  downloadLog: () => void;
  visibleEvents: RunEvent[];
  selectedEvent: RunEvent | null;
  selectEvent: (event: RunEvent) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  return (
    <>
      <div className="run-log-toolbar">
        <label className="field inline-field" htmlFor="run-log-filter">
          <span className="field-label">Filter</span>
          <select
            id="run-log-filter"
            className="text-input"
            value={filter}
            onChange={(event) => setFilter(event.target.value as Filter)}
          >
            <option value="all">All events</option>
            <option value="errors">Errors</option>
            <option value="warnings">Warnings</option>
          </select>
        </label>
        <label className="field inline-field run-log-search" htmlFor="run-log-search">
          <span className="field-label">Search</span>
          <input
            id="run-log-search"
            className="text-input"
            value={query}
            placeholder="Node or message"
            onChange={(event) => setQuery(event.target.value)}
          />
        </label>
        <div className="run-log-actions">
          <button
            type="button"
            className="text-button"
            data-testid="run-event-expand"
            aria-expanded={expanded}
            onClick={() => setExpanded((value) => !value)}
            disabled={visibleEvents.length === 0}
          >
            {expanded ? 'Collapse' : 'Expand'}
          </button>
          <button
            type="button"
            className="text-button"
            onClick={() => void copyLog()}
            disabled={visibleEvents.length === 0}
          >
            {copied ? 'Copied' : 'Copy'}
          </button>
          <button
            type="button"
            className="text-button"
            onClick={downloadLog}
            disabled={visibleEvents.length === 0}
          >
            Download
          </button>
        </div>
      </div>
      {run.error && <ActionError>{run.error}</ActionError>}
      <div
        className={`event-list run-event-list${expanded ? ' expanded' : ''}`}
        data-testid="run-event-list"
      >
        {visibleEvents.length === 0 && <p className="muted small">No events match this view.</p>}
        {visibleEvents.map((event) => {
          const summary = eventSummary(event);
          const effectsBlob = readEffectsData(event)?.blob;
          return (
            <div
              className={`event-row event-${event.type.replace('.', '-')} ${
                selectedEvent?.seq === event.seq ? 'active' : ''
              }`}
              key={`${event.seq}-${event.type}`}
            >
              <button
                type="button"
                className="event-row-main"
                onClick={() => selectEvent(event)}
                aria-expanded={selectedEvent?.seq === event.seq}
              >
                <span className="event-seq">{event.seq}</span>
                <span className="event-main">
                  <strong>{summary.title}</strong>
                  {event.nodeId && <span className="event-node">{event.nodeId}</span>}
                  {summary.notes.length > 0 && (
                    <span className="event-effect-notes">
                      {summary.notes.map((note) => (
                        <span className="event-effect-note" key={note}>
                          {note}
                        </span>
                      ))}
                    </span>
                  )}
                  {event.error && <span className="event-error">{event.error}</span>}
                </span>
                <span className="event-time">
                  {event.durationMs != null ? `${event.durationMs} ms` : formatTime(event.at)}
                </span>
              </button>
              {(event.inputHash || event.outputHash || effectsBlob) && (
                <button
                  type="button"
                  className={`event-inspect ${selectedEvent?.seq === event.seq ? 'active' : ''}`}
                  onClick={() => selectEvent(event)}
                  aria-expanded={selectedEvent?.seq === event.seq}
                  aria-label={`Inspect ${event.type}${event.nodeId ? ` for ${event.nodeId}` : ''}`}
                >
                  Inspect
                </button>
              )}
            </div>
          );
        })}
      </div>
    </>
  );
}
