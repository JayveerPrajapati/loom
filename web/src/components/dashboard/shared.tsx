import { useEffect, useMemo, useState } from 'react';
import { api } from '../../api';
import type {
  Example,
  Job,
  Run,
  SchedulePreview,
  Workflow,
} from '../../types';

export function formatTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    hour: 'numeric',
    minute: '2-digit',
  }).format(date);
}

export function formatDurationText(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  const minutes = Math.floor(ms / 60_000);
  const seconds = Math.round((ms % 60_000) / 1000);
  return seconds === 0 ? `${minutes}m` : `${minutes}m ${seconds}s`;
}

export function duration(run: Run): string {
  if (!run.finishedAt) return '—';
  const ms = Date.parse(run.finishedAt) - Date.parse(run.startedAt);
  if (!Number.isFinite(ms) || ms < 0) return '—';
  return formatDurationText(ms);
}

export function nodeCount(example: Example): string {
  return `${example.workflow.nodes.length} nodes`;
}

export function triggerName(example: Example): string {
  return (
    example.workflow.nodes.find((node) => node.type.startsWith('trigger.'))?.type ?? 'trigger'
  );
}

export function workflowDiff(from: Workflow, to: Workflow) {
  const before = new Map(from.nodes.map((node) => [node.id, JSON.stringify(node)]));
  const after = new Map(to.nodes.map((node) => [node.id, JSON.stringify(node)]));
  let added = 0;
  let removed = 0;
  let changed = 0;
  for (const id of after.keys()) {
    if (!before.has(id)) added += 1;
    else if (before.get(id) !== after.get(id)) changed += 1;
  }
  for (const id of before.keys()) if (!after.has(id)) removed += 1;
  return {
    added,
    removed,
    changed,
    edges: to.edges.length - from.edges.length,
  };
}

export function WorkflowDiffBadges({ from, to }: { from: Workflow; to: Workflow }) {
  const diff = useMemo(() => workflowDiff(from, to), [from, to]);
  const parts = [
    diff.added ? `+${diff.added} node${diff.added === 1 ? '' : 's'}` : '',
    diff.removed ? `−${diff.removed} node${diff.removed === 1 ? '' : 's'}` : '',
    diff.changed ? `~${diff.changed} changed` : '',
    diff.edges ? `${diff.edges > 0 ? '+' : ''}${diff.edges} edge${Math.abs(diff.edges) === 1 ? '' : 's'}` : '',
  ].filter(Boolean);
  if (parts.length === 0) return <p className="muted small">No structural changes.</p>;
  return (
    <div className="diff-badges" aria-label="Changes in the newer version">
      {parts.map((part) => (
        <span className="diff-badge" key={part}>{part}</span>
      ))}
    </div>
  );
}

export function parseJSONObject(text: string): Record<string, unknown> {
  if (!text.trim()) return {};
  const value: unknown = JSON.parse(text);
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Expected a JSON object.');
  }
  return value as Record<string, unknown>;
}

export function localTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
}

export function toDateTimeLocal(value: string): string {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

export const CRON_PRESETS: { id: string; label: string; expression: string }[] = [
  { id: 'custom', label: 'Custom expression', expression: '' },
  { id: 'every-5', label: 'Every 5 minutes', expression: '*/5 * * * *' },
  { id: 'every-10', label: 'Every 10 minutes', expression: '*/10 * * * *' },
  { id: 'every-15', label: 'Every 15 minutes', expression: '*/15 * * * *' },
  { id: 'every-30', label: 'Every 30 minutes', expression: '*/30 * * * *' },
  { id: 'hourly', label: 'Every hour', expression: '0 * * * *' },
  { id: 'daily-9', label: 'Every day at 09:00', expression: '0 9 * * *' },
  { id: 'weekdays-9', label: 'Weekdays at 09:00', expression: '0 9 * * 1-5' },
  { id: 'weekly-mon-9', label: 'Mondays at 09:00', expression: '0 9 * * 1' },
  { id: 'monthly-1-9', label: '1st of the month at 09:00', expression: '0 9 1 * *' },
];

export const COMMON_TIMEZONES = [
  'UTC',
  'America/New_York',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'Europe/London',
  'Europe/Berlin',
  'Europe/Paris',
  'Asia/Kolkata',
  'Asia/Calcutta',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Australia/Sydney',
];

export function cronPresetFor(expression: string): string {
  return CRON_PRESETS.find((preset) => preset.expression === expression)?.id ?? 'custom';
}

export function schedulePreviewConfig(
  triggerType: string,
  schedule: string,
  timezone: string,
  runAt: string,
): Record<string, unknown> | undefined {
  if (triggerType === 'cron') {
    const expression = schedule.trim();
    if (!expression) return undefined;
    return { schedule: expression, timezone: timezone.trim() || 'UTC' };
  }
  if (triggerType === 'once') {
    if (!runAt) return undefined;
    const at = new Date(runAt);
    if (Number.isNaN(at.getTime())) return undefined;
    return { runAt: at.toISOString() };
  }
  return undefined;
}

export interface SchedulePreviewPanelProps {
  triggerType: string;
  config?: Record<string, unknown>;
}

export function SchedulePreviewPanel({ triggerType, config }: SchedulePreviewPanelProps) {
  const [loading, setLoading] = useState(false);
  const [preview, setPreview] = useState<SchedulePreview | null>(null);
  const configKey = config ? JSON.stringify(config) : '';

  useEffect(() => {
    if (triggerType === 'manual' || !config) {
      setPreview(null);
      setLoading(false);
      return;
    }
    let cancelled = false;
    setLoading(true);
    const timer = window.setTimeout(() => {
      api
        .previewSchedule(triggerType, config, 3)
        .then((result) => {
          if (!cancelled) setPreview(result);
        })
        .catch((reason) => {
          if (!cancelled) {
            setPreview({
              valid: false,
              next: [],
              error: reason instanceof Error ? reason.message : String(reason),
            });
          }
        })
        .finally(() => {
          if (!cancelled) setLoading(false);
        });
    }, 250);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [triggerType, configKey]);

  if (triggerType === 'manual' || !config) return null;
  if (loading && !preview) return <p className="field-hint">Checking schedule…</p>;
  if (!preview) return null;
  if (!preview.valid) {
    return <p className="field-error">{preview.error ?? 'Invalid schedule.'}</p>;
  }
  if (preview.next.length === 0) {
    return <p className="field-hint">No upcoming run for this schedule.</p>;
  }
  return (
    <div className="schedule-preview">
      <span className="field-label">Next runs</span>
      <ul>
        {preview.next.map((value) => (
          <li key={value}>{formatTime(value)}</li>
        ))}
      </ul>
    </div>
  );
}

export function triggerLabel(triggerType: string): string {
  if (triggerType === 'cron') return 'Schedule';
  if (triggerType === 'once') return 'One time';
  return 'Manual';
}

export function nextRunLabel(job: Job): string {
  if (!job.enabled) return 'Paused';
  if (job.nextRunAt) return formatTime(job.nextRunAt);
  if (job.triggerType === 'manual') return 'Manual';
  return '—';
}

export function batchItemLabel(nodeId?: string): string {
  if (!nodeId) return 'Item';
  const match = /^batch:(\d+)$/.exec(nodeId);
  return match ? `Item ${Number(match[1]) + 1}` : nodeId;
}

export interface BatchRunRowProps {
  run: Run;
  onOpenRun: (runId: string) => void;
  onRetried: () => void;
}

export function BatchRunRow({ run, onOpenRun, onRetried }: BatchRunRowProps) {
  const [children, setChildren] = useState<Run[]>([]);
  const [status, setStatus] = useState(run.status);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    let timer: ReturnType<typeof setInterval> | undefined;
    const load = () => {
      Promise.all([api.getRun(run.id), api.listChildRuns(run.id, 100)])
        .then(([parent, result]) => {
          if (cancelled) return;
          const next = result.runs ?? [];
          setStatus(parent.status);
          setChildren(next);
          setError('');
          setLoading(false);
          const active = next.some((child) =>
            ['queued', 'running', 'waiting'].includes(child.status),
          );
          if (['done', 'failed', 'cancelled'].includes(parent.status) && !active && timer) {
            clearInterval(timer);
            timer = undefined;
          }
        })
        .catch((reason) => {
          if (!cancelled) {
            setError(reason instanceof Error ? reason.message : String(reason));
            setLoading(false);
          }
        });
    };
    load();
    timer = setInterval(load, 600);
    return () => {
      cancelled = true;
      if (timer) clearInterval(timer);
    };
  }, [run.id]);

  const done = children.filter((child) => child.status === 'done').length;
  const failed = children.filter((child) => child.status === 'failed').length;
  const active = children.filter((child) => ['queued', 'running', 'waiting'].includes(child.status)).length;
  const progress = children.length === 0 ? 0 : Math.round((done / children.length) * 100);

  const retry = async () => {
    setBusy(true);
    setError('');
    try {
      await api.retryRun(run.id);
      onRetried();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="batch-run" role="listitem">
      <div className="batch-run-head">
        <span className={`status-dot status-${status}`} aria-hidden="true" />
        <span className="runs-row-main">
          <strong>Batch · {status}</strong>
          <span>
            {formatTime(run.startedAt)} · {children.length} items · {done} done
            {failed > 0 ? ` · ${failed} failed` : ''}
            {active > 0 ? ` · ${active} active` : ''}
          </span>
        </span>
        <span className="runs-row-meta">
          <code>{run.id.split('_').pop()}</code>
          <em>{duration(run)}</em>
        </span>
      </div>
      <div className="batch-progress" aria-label={`Batch progress ${progress}%`}>
        <div className="batch-progress-track">
          <span style={{ width: `${progress}%` }} />
        </div>
        <span className="muted small">{progress}%</span>
      </div>
      {error && <p className="banner error">{error}</p>}
      {loading && <p className="muted small pad">Loading batch items…</p>}
      {!loading && children.length > 0 && (
        <div className="batch-children">
          {children.map((child) => (
            <button className="batch-child" key={child.id} onClick={() => onOpenRun(child.id)}>
              <span className={`status-dot status-${child.status}`} aria-hidden="true" />
              <span>
                <strong>{batchItemLabel(child.parentNodeId)}</strong>
                <em>
                  {child.status}
                  {child.error ? ` · ${child.error}` : ''}
                </em>
              </span>
              <code>{child.id.split('_').pop()}</code>
            </button>
          ))}
        </div>
      )}
      {failed > 0 && !['queued', 'running'].includes(status) && (
        <button className="button subtle" onClick={() => void retry()} disabled={busy}>
          {busy ? 'Retrying…' : `Retry ${failed} failed item${failed === 1 ? '' : 's'}`}
        </button>
      )}
    </div>
  );
}
