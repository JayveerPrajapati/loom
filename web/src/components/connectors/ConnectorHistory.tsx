import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import { useEffect, useState } from 'react';
import { api } from '../../api';
import { useDialogFocus } from '../../useDialogFocus';
import type { Connector, ConnectorVersion } from '../../types';

/** The connector version history dialog: a wide shell with a loading,
 *  empty and error state — the retry re-runs the fetch for the same
 *  connector. */
export function ConnectorHistory({
  connector,
  onClose,
}: {
  connector: Connector;
  onClose: () => void;
}) {
  const ref = useDialogFocus<HTMLDivElement>(true, onClose);
  const [versions, setVersions] = useState<ConnectorVersion[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  // Bumped by the error state's retry so the load effect re-runs for
  // the same connector id.
  const [reload, setReload] = useState(0);

  useEffect(() => {
    setLoading(true);
    setError('');
    let alive = true;
    api
      .connectorVersions(connector.id)
      .then((result) => {
        if (alive) setVersions(result.versions ?? []);
      })
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : String(reason)),
      )
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [connector.id, reload]);

  return (
    <div className="dialog-backdrop" data-testid="connector-history-backdrop">
      <div
        className="dialog asset-history-dialog"
        role="dialog"
        aria-label={`${connector.name} history`}
        ref={ref}
        tabIndex={-1}
      >
        <header className="dialog-head">
          <h2>{connector.name} — versions</h2>
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
                <p className="muted small">No connector versions recorded yet.</p>
              )}
              <table className="diff-table">
                <thead>
                  <tr>
                    <th scope="col">Version</th>
                    <th scope="col">Actor</th>
                    <th scope="col">Saved</th>
                    <th scope="col">Hash</th>
                  </tr>
                </thead>
                <tbody>
                  {versions.map((version) => (
                    <tr key={version.version}>
                      <td>v{version.version}</td>
                      <td>{version.actor}</td>
                      <td className="muted small">{new Date(version.createdAt).toLocaleString()}</td>
                      <td>
                        <code className="muted small">{version.contentHash.slice(0, 10)}</code>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
