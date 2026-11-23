import type { Run, RunEvent } from '../../types';

// Run-history formatting helpers (extracted verbatim from RunHistory —
// deliberately NOT runDetailHelpers: this dialog's time format is the
// short month/day variant).

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

export function duration(run: Run): string {
  if (!run.finishedAt) return '—';
  const ms = Date.parse(run.finishedAt) - Date.parse(run.startedAt);
  if (!Number.isFinite(ms) || ms < 0) return '—';
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(1)} s`;
}

export function durationLabel(value?: number): string {
  if (value == null) return '—';
  if (value < 1000) return `${value} ms`;
  return `${(value / 1000).toFixed(1)} s`;
}

export function pretty(value: unknown): string {
  if (value === undefined) return '—';
  try {
    return JSON.stringify(value, null, 2) ?? String(value);
  } catch {
    return String(value);
  }
}

export function traceData(event: RunEvent | null, key: 'request' | 'response'): unknown {
  const data = event?.data;
  if (!data || typeof data !== 'object') return undefined;
  return (data as Record<string, unknown>)[key];
}
