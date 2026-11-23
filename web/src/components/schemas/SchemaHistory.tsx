import { AssetLoadingState } from '../assets/AssetState';
import { ActionError } from '../assets/ActionError';
import { useEffect, useState } from 'react';
import { api } from '../../api';
import type { DataSchema, DataSchemaVersion } from '../../types';
import { useDialogFocus } from '../../useDialogFocus';

/** SchemaHistory lists a contract's append-only version history. */
export function SchemaHistory({
  schema,
  onClose,
}: {
  schema: DataSchema | null;
  onClose: () => void;
}) {
  const [versions, setVersions] = useState<DataSchemaVersion[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [reload, setReload] = useState(0);
  const ref = useDialogFocus<HTMLDivElement>(schema !== null, onClose);

  useEffect(() => {
    if (!schema) return;
    setVersions([]);
    setLoading(true);
    setError('');
    api
      .listDataSchemaVersions(schema.id)
      .then((result) => setVersions(result.versions ?? []))
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : String(reason)),
      )
      .finally(() => setLoading(false));
  }, [schema, reload]);

  if (!schema) return null;
  return (
    <div className="dialog-backdrop" data-testid="schema-history-backdrop">
      <div
        className="dialog asset-history-dialog"
        role="dialog"
        aria-modal="true"
        aria-label={`Version history for ${schema.name}`}
        ref={ref}
        tabIndex={-1}
      >
        <header className="dialog-head">
          <h2>Version history: {schema.name}</h2>
          <button className="icon-button" onClick={onClose} aria-label="Close history" type="button">
            ×
          </button>
        </header>
        <div className="dialog-body" data-testid="schema-history-body">
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
                <p className="muted small">No contract versions recorded yet.</p>
              )}
              {versions.map((version) => (
                <div className="schema-history-row" key={version.version}>
                  <strong>v{version.version}</strong>
                  <span>{new Date(version.createdAt).toLocaleString()}</span>
                  <em>{version.actor}</em>
                </div>
              ))}
            </>
          )}
        </div>
      </div>
    </div>
  );
}
