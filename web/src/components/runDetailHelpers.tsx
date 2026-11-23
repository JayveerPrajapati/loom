import { useMemo } from 'react';
import type {
  EffectDiff,
  NodeEffectsData,
  NodeEffectsDriftData,
  Run,
  RunEvent,
  Workflow,
} from '../types';

export type Filter = 'all' | 'errors' | 'warnings';
export type DockTab = 'timeline' | 'data' | 'raw';

export function formatTime(value?: string) {
  if (!value) return '—';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}

export function duration(run: Run) {
  const start = Date.parse(run.startedAt);
  const end = run.finishedAt ? Date.parse(run.finishedAt) : 0;
  if (!start || !end || end < start) return '—';
  const ms = end - start;
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export function formatDurationMs(ms?: number) {
  if (!ms || ms < 0) return '—';
  if (ms < 1000) return `${Math.round(ms)} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`;
}

export function pretty(value: unknown) {
  if (value === undefined) return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function durationLabel(value?: number) {
  return value === undefined ? '—' : formatDurationMs(value);
}

export function traceData(event: RunEvent | null, key: 'request' | 'response') {
  const value = event?.data?.[key];
  return value === undefined ? undefined : value;
}

export function readCount(value: unknown): number | undefined {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

export function readEffectRefs(value: unknown): EffectDiff[] {
  if (!Array.isArray(value)) return [];
  const refs: EffectDiff[] = [];
  for (const item of value) {
    if (typeof item !== 'object' || item === null) continue;
    const record = item as Record<string, unknown>;
    refs.push({
      kind: typeof record.kind === 'string' ? record.kind : 'effect',
      index: readCount(record.index) ?? 0,
      recordedHash: typeof record.recordedHash === 'string' ? record.recordedHash : undefined,
      actualHash: typeof record.actualHash === 'string' ? record.actualHash : undefined,
    });
  }
  return refs;
}

export function readEffectsData(event: RunEvent): NodeEffectsData | null {
  if (event.type !== 'node.effects' || !event.data) return null;
  const count = readCount(event.data.count);
  if (count === undefined) return null;
  return {
    blob: typeof event.data.blob === 'string' ? event.data.blob : undefined,
    count,
    itemIndex: readCount(event.data.itemIndex),
  };
}

export function readDriftData(event: RunEvent): NodeEffectsDriftData | null {
  if (event.type !== 'node.effects.drift' || !event.data) return null;
  return {
    diffs: readEffectRefs(event.data.diffs),
    misses: readEffectRefs(event.data.misses).map((ref) => ({
      kind: ref.kind,
      index: ref.index,
    })),
    itemIndex: readCount(event.data.itemIndex),
  };
}

export function readDecisionData(
  event: RunEvent,
): { decision: string; note: string; actor: string; source: string } | null {
  if (event.type !== 'run.resumed' || !event.data) return null;
  const decision = event.data.decision;
  if (typeof decision !== 'string') return null;
  return {
    decision,
    note: typeof event.data.note === 'string' ? event.data.note : '',
    actor:
      typeof event.data.actor === 'string'
        ? event.data.actor
        : typeof event.actor === 'string'
          ? event.actor
          : '',
    source: typeof event.data.source === 'string' ? event.data.source : '',
  };
}

// actorLabel turns the stored attribution into something a person can read.
// New decisions store an email; older ones carry principal ids or system tags.
export function actorLabel(actor: string): string {
  const value = actor.trim();
  if (!value) return 'Unknown';
  if (value.includes('@')) return value;
  if (value.startsWith('user:')) return 'A workspace member';
  if (value.startsWith('token:')) return 'An API token';
  if (value === 'service:loom-token') return 'The service token';
  if (value === 'system:approval-link') return 'Approval link';
  if (value === 'system:slack') return 'Slack';
  if (value === 'system:timeout') return 'Timeout';
  if (value === 'cli') return 'The CLI';
  if (value === 'anonymous') return 'Anonymous';
  return value;
}

// decisionSourceLabel names where a decision came from, but only when it was
// not a plain call to this app's API.
export function decisionSourceLabel(source: string): string {
  switch (source) {
    case 'slack':
      return 'via Slack';
    case 'link':
      return 'via approval link';
    case 'cli':
      return 'via CLI';
    case 'timeout':
      return 'by timeout';
    default:
      return '';
  }
}

// decisionByline keeps the deciding person visible. A system actor (an
// approval link, Slack, the timeout) is already named by its source, so the
// attribution is not stated twice.
export function decisionByline(actor: string, source: string): { who: string; via: string } {
  const via = decisionSourceLabel(source);
  if (actor.trim().startsWith('system:')) {
    return { who: via || `by ${actorLabel(actor)}`, via: '' };
  }
  return { who: `by ${actorLabel(actor)}`, via };
}

// linkStateLabel reports the approval link's remaining life next to the
// waiting call to action. It describes the link, never an SLA.
export function linkStateLabel(expiresAt: string, now: number): string {
  const remaining = Date.parse(expiresAt) - now;
  if (!Number.isFinite(remaining)) return '';
  if (remaining <= 0) return 'Link expired';
  const minutes = Math.floor(remaining / 60_000);
  const hours = Math.floor(minutes / 60);
  const days = Math.floor(hours / 24);
  if (days >= 1) return `Link valid for ${days}d${hours % 24 ? ` ${hours % 24}h` : ''}`;
  if (hours >= 1) return `Link expires in ${hours}h${minutes % 60 ? ` ${minutes % 60}m` : ''}`;
  if (minutes >= 1) return `Link expires in ${minutes}m`;
  return `Link expires in ${Math.ceil(remaining / 1000)}s`;
}

export function eventSummary(event: RunEvent): { title: string; notes: string[] } {
  if (event.type === 'node.effects') {
    const count = readEffectsData(event)?.count ?? 0;
    return { title: `Recorded ${count} external effect${count === 1 ? '' : 's'}`, notes: [] };
  }
  if (event.type === 'node.effects.drift') {
    const data = readDriftData(event);
    const notes: string[] = [];
    for (const miss of data?.misses ?? []) notes.push(`missing ${miss.kind} effect ${miss.index}`);
    for (const diff of data?.diffs ?? [])
      notes.push(`${diff.kind} effect ${diff.index} differs from the recording`);
    return { title: 'Effect drift on replay', notes };
  }
  return { title: event.type, notes: [] };
}

export function isTerminal(status: string) {
  return ['done', 'failed', 'cancelled'].includes(status);
}

export function latestNodeEvent(events: RunEvent[], nodeId: string): RunEvent | null {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    if (events[index].nodeId === nodeId) return events[index];
  }
  return null;
}

export function collectPaths(value: unknown, prefix = '$json', out: string[] = []): string[] {
  if (out.length >= 60 || value === null || typeof value !== 'object') return out;
  const entries = Array.isArray(value)
    ? value.slice(0, 10).map((item, index) => [String(index), item] as const)
    : Object.entries(value);
  for (const [key, child] of entries) {
    const path = Array.isArray(value)
      ? `${prefix}[${key}]`
      : /^[A-Za-z_$][\w$]*$/.test(key)
        ? `${prefix}.${key}`
        : `${prefix}[${JSON.stringify(key)}]`;
    out.push(path);
    collectPaths(child, path, out);
    if (out.length >= 60) break;
  }
  return out;
}

export function JsonPaths({ value }: { value: unknown }) {
  const paths = useMemo(() => collectPaths(value), [value]);
  if (paths.length === 0) return null;
  const copy = (path: string) => {
    void navigator.clipboard?.writeText(`{{ ${path} }}`);
  };
  return (
    <details className="json-paths">
      <summary>{paths.length} expression paths</summary>
      <ul>
        {paths.map((path) => (
          <li key={path}>
            <code>{path}</code>
            <button
              type="button"
              className="icon-button"
              onClick={() => copy(path)}
              title="Copy expression"
            >
              Copy
            </button>
          </li>
        ))}
      </ul>
    </details>
  );
}

export type TimelineStatus = 'running' | 'done' | 'failed' | 'waiting';

export interface TimelineRow {
  nodeId: string;
  name: string;
  status: TimelineStatus;
  durationMs: number;
  error?: string;
  attempts: number;
  start: number;
}

export function buildTimeline(events: RunEvent[], workflow: Workflow | null): TimelineRow[] {
  const rows = new Map<string, TimelineRow>();
  const ensure = (nodeId: string) => {
    let row = rows.get(nodeId);
    if (!row) {
      row = {
        nodeId,
        name: workflow?.nodes.find((node) => node.id === nodeId)?.name ?? nodeId,
        status: 'running',
        durationMs: 0,
        attempts: 1,
        start: 0,
      };
      rows.set(nodeId, row);
    }
    return row;
  };
  for (const event of events) {
    if (!event.nodeId || event.type.startsWith('item.')) continue;
    const row = ensure(event.nodeId);
    const at = Date.parse(event.at);
    if (Number.isFinite(at) && (!row.start || at < row.start)) row.start = at;
    switch (event.type) {
      case 'node.started':
        row.status = 'running';
        break;
      case 'node.retried':
        row.status = 'running';
        row.attempts = Math.max(row.attempts, event.attempt ?? row.attempts + 1);
        break;
      case 'node.finished':
        row.status = event.error ? 'failed' : 'done';
        row.error = event.error;
        row.durationMs = event.durationMs ?? row.durationMs;
        break;
      case 'node.failed':
        row.status = 'failed';
        row.error = event.error;
        row.durationMs = event.durationMs ?? row.durationMs;
        break;
      case 'run.waiting':
        row.status = 'waiting';
        break;
      default:
        break;
    }
  }
  return [...rows.values()].sort((a, b) => a.start - b.start || a.nodeId.localeCompare(b.nodeId));
}

export const STATUS_LABEL: Record<string, string> = {
  queued: 'Queued',
  running: 'Running',
  waiting: 'Waiting',
  done: 'Complete',
  failed: 'Failed',
  cancelled: 'Cancelled',
  skipped: 'Skipped',
};

export function readRunView(): { node: string | null; tab: DockTab } {
  const params = new URLSearchParams(window.location.search);
  const tab = params.get('tab');
  return {
    node: params.get('node'),
    tab: tab === 'data' || tab === 'raw' || tab === 'timeline' ? tab : 'raw',
  };
}
