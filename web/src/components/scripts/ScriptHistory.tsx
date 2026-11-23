import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import { useEffect, useState } from 'react';
import { api } from '../../api';
import { useDialogFocus } from '../../useDialogFocus';
import type { Script } from '../../types';

export function ScriptHistory({ script, onClose }: { script: Script; onClose: () => void }) {
  const ref = useDialogFocus<HTMLDivElement>(true, onClose);
  const [versions, setVersions] = useState<{ version: number; actor: string; createdAt: string }[]>(
    [],
  );
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // Bumped by the error state's retry so the load effect re-runs for
  // the same script id.
  const [reload, setReload] = useState(0);

  useEffect(() => {
    setLoading(true);
    setError('');
    api
      .scriptVersions(script.id)
      .then((result) => setVersions(result.versions ?? []))
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : String(reason)),
      )
      .finally(() => setLoading(false));
  }, [script.id, reload]);

  return (
    <div className="dialog-backdrop" role="presentation">
      <div
        className="dialog asset-history-dialog"
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label="Script history"
        data-testid="script-history"
      >
        <header className="dialog-head">
          <h3>History: {script.name}</h3>
          <button className="icon-button" onClick={onClose} aria-label="Close history" type="button">
            ×
          </button>
        </header>
        <div className="dialog-body">
          {loading && <AssetLoadingState label="Loading versions…" className="muted small" />}
          {error ? (
            <>
              <ActionError>{error}</ActionError>
              <button
                type="button"
                className="button subtle"
                onClick={() => setReload((count) => count + 1)}
              >
                Try again
              </button>
            </>
          ) : (
            <>
              {!loading && versions.length === 0 && (
                <p className="muted small">No script versions recorded yet.</p>
              )}
              {versions.map((version) => (
                <div className="profile-preview-row" key={version.version}>
                  <span>v{version.version}</span>
                  <span>{new Date(version.createdAt).toLocaleString()}</span>
                  <span>{version.actor || 'unknown'}</span>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
