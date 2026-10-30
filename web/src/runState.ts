import type { NodeStatus } from './components/Canvas';
import type { RunEvent } from './types';

export function deriveRunStatus(events: RunEvent[], fallback: string): string {
  for (let index = events.length - 1; index >= 0; index -= 1) {
    switch (events[index].type) {
      case 'run.finished':
        return 'done';
      case 'run.failed':
        return 'failed';
      case 'run.cancelled':
        return 'cancelled';
      case 'run.waiting':
        return 'waiting';
      case 'run.started':
      case 'run.resumed':
        return 'running';
      default:
        break;
    }
  }
  return fallback;
}

export function deriveNodeStatuses(
  events: RunEvent[],
  nodeIds?: string[],
  runStatus?: string,
): Record<string, NodeStatus> {
  const statuses: Record<string, NodeStatus> = {};
  for (const event of events) {
    if (!event.nodeId) continue;
    switch (event.type) {
      case 'node.started':
      case 'node.retried':
      case 'run.resumed':
        statuses[event.nodeId] = 'running';
        break;
      case 'node.finished':
        statuses[event.nodeId] = event.error ? 'failed' : 'done';
        break;
      case 'node.failed':
        statuses[event.nodeId] = 'failed';
        break;
      case 'run.waiting':
        statuses[event.nodeId] = 'waiting';
        break;
      default:
        break;
    }
  }
  if (nodeIds) {
    const active =
      runStatus === 'running' || runStatus === 'queued' || runStatus === 'waiting';
    const terminal =
      runStatus === 'done' || runStatus === 'failed' || runStatus === 'cancelled';
    for (const nodeId of nodeIds) {
      if (statuses[nodeId]) continue;
      if (active) statuses[nodeId] = 'queued';
      else if (terminal) statuses[nodeId] = 'skipped';
    }
  }
  return statuses;
}
