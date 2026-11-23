// Audit 4E: the editor-side view of a publish's live phase progress.
// Publish stays one synchronous POST; the server exposes the phases it is
// walking through an in-memory registry the editor polls.

export type PublishPhase = {
  name: string;
  status: 'running' | 'done' | 'error';
  ms?: number;
};

export function newPublishId(): string {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID();
  }
  return `pub-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 10)}`;
}

const PHASE_LABELS: Record<string, string> = {
  validate: 'Validating',
  resolve: 'Resolving assets',
  environments: 'Building Python environments',
  publish: 'Writing version',
  finalize: 'Recording usage',
};

export function publishPhaseLabel(name: string): string {
  return PHASE_LABELS[name] ?? 'Publishing';
}

export function activePublishPhase(phases: PublishPhase[]): string {
  const running = phases.find((phase) => phase.status === 'running');
  const current = running ?? phases[phases.length - 1];
  return current ? publishPhaseLabel(current.name) : '';
}
