import type { Dispatch, RefObject, SetStateAction } from 'react';
import type { Job, Run } from '../../types';
import {
  decisionByline,
  duration,
  formatDurationMs,
  formatTime,
  type TimelineRow,
} from '../runDetailHelpers';
import type { DecisionHistoryEntry } from '../RunDock';

/** TimelineTab: node-by-node progress, decisions, run facts, children. */
export function TimelineTab({
  follow,
  setFollow,
  timeline,
  decisionEvents,
  selectedNodeId,
  selectNode,
  timelineEndRef,
  run,
  workflowName,
  job,
  children,
  now,
  onOpenTemplate,
  onOpenJob,
  onOpenRun,
}: {
  follow: boolean;
  setFollow: Dispatch<SetStateAction<boolean>>;
  timeline: TimelineRow[];
  decisionEvents: DecisionHistoryEntry[];
  selectedNodeId: string | null;
  selectNode: (nodeId: string) => void;
  timelineEndRef: RefObject<HTMLDivElement>;
  run: Run;
  workflowName: string;
  job: Job | null;
  children: Run[];
  now: number;
  onOpenTemplate: (id: string) => void;
  onOpenJob: (id: string) => void;
  onOpenRun: (id: string) => void;
}) {
  return (
    <>
      <div className="run-dock-toolbar">
        <label className="run-follow">
          <input
            type="checkbox"
            checked={follow}
            onChange={(event) => setFollow(event.target.checked)}
          />
          Follow live
        </label>
        <span className="muted small">{timeline.length} nodes</span>
      </div>
      {decisionEvents.length > 0 && (
        <div className="decision-history" aria-label="Approval decisions">
          {decisionEvents.map(({ event, data }) => (
            <div
              key={event.seq}
              className={`decision-row ${
                data.decision === 'approved' ? 'approved' : 'rejected'
              }`}
            >
              <span
                className={`decision-badge ${
                  data.decision === 'approved' ? 'approved' : 'rejected'
                }`}
              >
                {data.decision}
              </span>
              <span className="decision-main">
                <strong>{decisionByline(data.actor, data.source).who}</strong>
                {decisionByline(data.actor, data.source).via && (
                  <span className="decision-source">
                    {decisionByline(data.actor, data.source).via}
                  </span>
                )}
                {data.note && <em>{data.note}</em>}
              </span>
              <span className="decision-time">{formatTime(event.at)}</span>
            </div>
          ))}
        </div>
      )}
      {timeline.length === 0 && <p className="muted small">Waiting for node events…</p>}
      {timeline.map((row) => (
        <button
          type="button"
          className={`timeline-row status-${row.status} ${
            selectedNodeId === row.nodeId ? 'active' : ''
          }`}
          key={row.nodeId}
          onClick={() => selectNode(row.nodeId)}
        >
          <span className={`status-dot status-${row.status}`} aria-hidden="true" />
          <span className="timeline-main">
            <strong>{row.name}</strong>
            <em>
              {row.nodeId}
              {row.attempts > 1 ? `, ${row.attempts} attempts` : ''}
            </em>
            {row.error && <span className="timeline-error">{row.error}</span>}
          </span>
          <span className="timeline-duration">
            {formatDurationMs(
              row.status === 'running' && row.start
                ? Math.max(0, now - row.start)
                : row.durationMs,
            )}
          </span>
        </button>
      ))}
      <div ref={timelineEndRef} />
      <details className="run-meta-details">
        <summary>Run details</summary>
        <dl className="job-facts">
          <div>
            <dt>Run id</dt>
            <dd>
              <code>{run.id}</code>
            </dd>
          </div>
          <div>
            <dt>Template</dt>
            <dd>
              <button className="text-link" onClick={() => onOpenTemplate(run.workflowId)}>
                {workflowName}
              </button>
            </dd>
          </div>
          {job && (
            <div>
              <dt>Job</dt>
              <dd>
                <button className="text-link" onClick={() => onOpenJob(job.id)}>
                  {job.name}
                </button>
              </dd>
            </div>
          )}
          <div>
            <dt>Started</dt>
            <dd>{formatTime(run.startedAt)}</dd>
          </div>
          <div>
            <dt>Finished</dt>
            <dd>{formatTime(run.finishedAt)}</dd>
          </div>
          <div>
            <dt>Duration</dt>
            <dd>{duration(run)}</dd>
          </div>
        </dl>
        {run.parentRunId && (
          <button className="button subtle full" onClick={() => onOpenRun(run.parentRunId!)}>
            Open parent run
          </button>
        )}
        <div className="batch-aggregate">
          <div className="batch-aggregate-head">
            <span className="field-label">Batch aggregate</span>
            <span className="muted small meta-list">
              <span>{children.length} runs</span>
              <span>{children.filter((child) => child.status === 'done').length} done</span>
              <span>{children.filter((child) => child.status === 'failed').length} failed</span>
              <span>{children.filter((child) => child.status === 'running').length} running</span>
            </span>
          </div>
          <div className="batch-grid">
            {children.map((child) => (
              <button
                type="button"
                key={child.id}
                className={`batch-cell status-${child.status}`}
                title={`${child.status} · ${child.id}`}
                onClick={() => onOpenRun(child.id)}
              />
            ))}
          </div>
        </div>
        <div className="run-children">
          <span className="field-label">Child runs ({children.length})</span>
          {children.length === 0 && <p className="muted small">No child runs.</p>}
          {children.map((child) => (
            <button className="runs-row" key={child.id} onClick={() => onOpenRun(child.id)}>
              <span className="runs-row-main">
                <strong>{child.status}</strong>
                <span className="meta-list">
                  <span>{child.parentNodeId || 'subflow'}</span>
                  <span>{formatTime(child.startedAt)}</span>
                </span>
              </span>
              <span className="runs-row-meta">
                <code>{child.id.split('_').pop()}</code>
              </span>
            </button>
          ))}
        </div>
      </details>
    </>
  );
}
