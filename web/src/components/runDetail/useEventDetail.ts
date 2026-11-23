import { useEffect, useState } from 'react';
import { api } from '../../api';
import { readEffectsData } from '../runDetailHelpers';
import type { RunEvent } from '../../types';

// Event-detail cluster (Phase 6 D6-2): the input/output/effects/config
// panel behind the selected event, and the input-draft textarea state
// shared with replay overrides.
export function useEventDetail(runId: string, activeEvent: RunEvent | null) {
  const [inputDraft, setInputDraft] = useState<string | null>(null);
  const [detail, setDetail] = useState<{
    loading: boolean;
    input?: unknown;
    output?: unknown;
    effects?: unknown;
    config?: Record<string, unknown>;
    credentials?: Record<string, string>;
    error?: string;
  }>({ loading: false });

  useEffect(() => {
    const event = activeEvent;
    if (!event) {
      setDetail({ loading: false });
      return;
    }
    let cancelled = false;
    setDetail({ loading: true });
    setInputDraft(null);
    const effectsBlob = readEffectsData(event)?.blob;
    Promise.all([
      event.inputHash ? api.getBlob(event.inputHash) : Promise.resolve(undefined),
      event.outputHash ? api.getBlob(event.outputHash) : Promise.resolve(undefined),
      effectsBlob ? api.getBlob(effectsBlob).catch(() => undefined) : Promise.resolve(undefined),
      event.nodeId
        ? api.dataContext(runId, event.nodeId).catch(() => null)
        : Promise.resolve(null),
    ])
      .then(([input, output, effects, context]) => {
        if (cancelled) return;
        const node = context?.nodes.find((item) => item.nodeId === event.nodeId);
        setDetail({
          loading: false,
          input,
          output,
          effects,
          config: node?.config,
          credentials: node?.credentials,
        });
      })
      .catch((reason: Error) => {
        if (!cancelled) setDetail({ loading: false, error: reason.message });
      });
    return () => {
      cancelled = true;
    };
  }, [activeEvent]);

  return { detail, inputDraft, setInputDraft };
}
