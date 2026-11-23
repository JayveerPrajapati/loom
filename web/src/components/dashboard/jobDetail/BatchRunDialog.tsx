import { ActionError } from '../../assets/ActionError';
import { useState } from 'react';
import { api } from '../../../api';
import { useDialogFocus } from '../../../useDialogFocus';
import type { Job } from '../../../types';

/**
 * BatchRunDialog: run a job over many items — paste or upload a JSON
 * array or CSV with a header row. Owns its form state; reports back via
 * onStarted (reload + notify) and onClose.
 */
export function BatchRunDialog({
  job,
  onClose,
  onStarted,
}: {
  job: Job;
  onClose: () => void;
  onStarted: () => Promise<void> | void;
}) {
  const [batchText, setBatchText] = useState('');
  const [batchConcurrency, setBatchConcurrency] = useState(3);
  const [batchBusy, setBatchBusy] = useState(false);
  const [batchError, setBatchError] = useState('');
  const batchRef = useDialogFocus<HTMLDivElement>(true, onClose);

  const startBatch = async () => {
    const text = batchText.trim();
    if (!text) {
      setBatchError('Paste a JSON array or CSV with a header row.');
      return;
    }
    let payload: { items?: unknown[]; csv?: string; concurrency: number };
    if (text.startsWith('[')) {
      try {
        const parsed: unknown = JSON.parse(text);
        if (!Array.isArray(parsed) || parsed.length === 0) {
          throw new Error('Expected a non-empty JSON array.');
        }
        payload = { items: parsed, concurrency: batchConcurrency };
      } catch (reason) {
        setBatchError(reason instanceof Error ? reason.message : String(reason));
        return;
      }
    } else {
      payload = { csv: text, concurrency: batchConcurrency };
    }
    setBatchBusy(true);
    setBatchError('');
    try {
      await api.startBatch(job.id, payload);
      onClose();
      await onStarted();
    } catch (reason) {
      setBatchError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBatchBusy(false);
    }
  };

  return (
    <div
      className="batch-modal-backdrop"
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <section
        ref={batchRef}
        className="batch-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby="batch-modal-title"
      >
        <header className="batch-modal-head">
          <div>
            <h2 id="batch-modal-title">Run {job.name} over many items</h2>
          </div>
          <button className="button subtle" onClick={onClose}>
            Close
          </button>
        </header>
        <p className="muted small">
          Paste a JSON array of objects, or CSV with a header row. Each item runs as a child
          run; job input defaults are merged into every item.
        </p>
        <label className="field" htmlFor="batch-file">
          <span className="field-label">Upload JSON or CSV</span>
          <input
            id="batch-file"
            className="text-input"
            type="file"
            accept=".json,.csv,application/json,text/csv"
            onChange={(event) => {
              const file = event.target.files?.[0];
              if (!file) return;
              file
                .text()
                .then((text) => {
                  setBatchText(text);
                  setBatchError('');
                })
                .catch((reason) => setBatchError(reason instanceof Error ? reason.message : String(reason)));
            }}
          />
        </label>
        <label className="field" htmlFor="batch-text">
          <span className="field-label">Items</span>
          <textarea
            id="batch-text"
            className="text-input code-input"
            value={batchText}
            onChange={(event) => setBatchText(event.target.value)}
            rows={10}
            spellCheck={false}
            placeholder={'[{"name":"Ada"},{"name":"Grace"}]\n\nor\n\nname,city\nAda,London\nGrace,Paris'}
          />
        </label>
        <label className="field batch-concurrency" htmlFor="batch-concurrency">
          <span className="field-label">Concurrency</span>
          <input
            id="batch-concurrency"
            className="text-input"
            type="number"
            min={1}
            max={20}
            value={batchConcurrency}
            onChange={(event) => setBatchConcurrency(Number(event.target.value) || 1)}
          />
          <span className="muted small">At most 20 items run at once.</span>
        </label>
        {batchError && <ActionError>{batchError}</ActionError>}
        <footer className="batch-modal-actions">
          <button className="button subtle" onClick={onClose}>
            Cancel
          </button>
          <button className="button primary" onClick={() => void startBatch()} disabled={batchBusy}>
            {batchBusy ? 'Starting…' : 'Start batch'}
          </button>
        </footer>
      </section>
    </div>
  );
}
