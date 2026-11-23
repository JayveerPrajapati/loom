import { describe, expect, it } from 'vitest';
import { activePublishPhase, newPublishId, publishPhaseLabel } from '../publishProgress';

describe('publish phase labels (audit 4E)', () => {
  it('names every server phase', () => {
    expect(publishPhaseLabel('validate')).toBe('Validating');
    expect(publishPhaseLabel('resolve')).toBe('Resolving assets');
    expect(publishPhaseLabel('environments')).toBe('Building Python environments');
    expect(publishPhaseLabel('publish')).toBe('Writing version');
    expect(publishPhaseLabel('finalize')).toBe('Recording usage');
    expect(publishPhaseLabel('unknown')).toBe('Publishing');
  });

  it('prefers the running phase, else the last one', () => {
    expect(
      activePublishPhase([
        { name: 'validate', status: 'done', ms: 1 },
        { name: 'resolve', status: 'running' },
        { name: 'publish', status: 'done' },
      ]),
    ).toBe('Resolving assets');
    expect(activePublishPhase([{ name: 'finalize', status: 'done', ms: 2 }])).toBe(
      'Recording usage',
    );
    expect(activePublishPhase([])).toBe('');
  });

  it('mints publish ids the server accepts', () => {
    expect(newPublishId()).toMatch(/^[A-Za-z0-9_-]{8,64}$/);
  });
});
