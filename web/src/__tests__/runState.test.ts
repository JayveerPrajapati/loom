import { describe, expect, it } from 'vitest';
import { deriveNodeStatuses, deriveRunStatus } from '../runState';
import type { RunEvent } from '../types';

function ev(seq: number, type: string, nodeId?: string, error?: string): RunEvent {
  return { seq, type, at: '2026-01-01T00:00:00Z', nodeId, error };
}

describe('deriveRunStatus', () => {
  it('uses the latest run-level event, scanning backwards', () => {
    const events = [
      ev(1, 'run.started'),
      ev(2, 'run.waiting'),
      ev(3, 'run.resumed'),
      ev(4, 'node.finished', 'a'),
    ];
    expect(deriveRunStatus(events, 'queued')).toBe('running');
  });

  it('prefers a terminal event over later node events', () => {
    const events = [ev(1, 'run.started'), ev(2, 'run.failed'), ev(3, 'node.finished', 'a')];
    expect(deriveRunStatus(events, 'running')).toBe('failed');
  });

  it('falls back when no run-level event exists', () => {
    expect(deriveRunStatus([ev(1, 'node.started', 'a')], 'queued')).toBe('queued');
    expect(deriveRunStatus([], 'whatever')).toBe('whatever');
  });
});

describe('deriveNodeStatuses', () => {
  it('tracks started then finished, failed on error', () => {
    const events = [
      ev(1, 'node.started', 'a'),
      ev(2, 'node.started', 'b'),
      ev(3, 'node.finished', 'a'),
      ev(4, 'node.finished', 'b', 'boom'),
    ];
    expect(deriveNodeStatuses(events)).toEqual({ a: 'done', b: 'failed' });
  });

  it('marks the waiting node on run.waiting', () => {
    const events = [ev(1, 'run.waiting', 'approve')];
    expect(deriveNodeStatuses(events)).toEqual({ approve: 'waiting' });
  });

  it('defaults unknown nodes to queued while active, skipped when terminal', () => {
    const events = [ev(1, 'node.finished', 'a')];
    expect(deriveNodeStatuses(events, ['a', 'b', 'c'], 'running')).toEqual({
      a: 'done',
      b: 'queued',
      c: 'queued',
    });
    expect(deriveNodeStatuses(events, ['a', 'b'], 'done')).toEqual({ a: 'done', b: 'skipped' });
  });
});
