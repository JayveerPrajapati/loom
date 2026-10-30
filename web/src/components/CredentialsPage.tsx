import { useEffect, useState, type FormEvent } from 'react';
import { api } from '../api';
import type { CredentialInfo } from '../types';

export function CredentialsPage() {
  const [credentials, setCredentials] = useState<CredentialInfo[]>([]);
  const [type, setType] = useState('http');
  const [data, setData] = useState('{\n  "headers": {\n    "Authorization": "Bearer …"\n  }\n}');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [loading, setLoading] = useState(true);

  const load = () => {
    setLoading(true);
    api
      .listCredentials()
      .then((result) => setCredentials(result.credentials ?? []))
      .catch((reason: Error) => setError(reason.message))
      .finally(() => setLoading(false));
  };

  useEffect(() => {
    load();
  }, []);

  const create = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    let parsed: unknown;
    try {
      parsed = JSON.parse(data);
    } catch {
      setError('Credential data must be a valid JSON object.');
      return;
    }
    if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
      setError('Credential data must be a JSON object.');
      return;
    }
    setBusy(true);
    try {
      await api.createCredential({
        type: type.trim(),
        data: parsed as Record<string, unknown>,
      });
      setData('{}');
      load();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  const remove = async (credential: CredentialInfo) => {
    if (!window.confirm(`Delete the ${credential.type} credential “${credential.id}”?`)) return;
    setError('');
    try {
      await api.deleteCredential(credential.id);
      load();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  };

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <header className="page-head">
        <div>
          <p className="eyebrow">Workspace</p>
          <h1>Credentials</h1>
          <p className="lede">
            Secrets are encrypted at rest. Nodes reference a credential by id; values never leave
            the server.
          </p>
        </div>
      </header>

      {error && <p className="banner error">{error}</p>}

      <div className="dashboard-grid">
        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">Stored</p>
              <h2>Credential ids</h2>
            </div>
            <button className="button subtle" onClick={load} disabled={loading}>
              Refresh
            </button>
          </header>
          {loading && <p className="muted small pad">Loading credentials…</p>}
          {!loading && credentials.length === 0 && (
            <p className="muted small pad">No credentials yet. Add one on the right.</p>
          )}
          {!loading && credentials.length > 0 && (
            <div className="dashboard-run-list">
              {credentials.map((credential) => (
                <div className="dashboard-run-row" key={credential.id}>
                  <span className="dashboard-run-main">
                    <strong>{credential.type}</strong>
                    <span>
                      <code>{credential.id}</code>
                    </span>
                  </span>
                  <span className="dashboard-run-meta">
                    <em>{new Date(credential.createdAt).toLocaleString()}</em>
                    <button
                      className="button subtle"
                      onClick={() => void remove(credential)}
                      aria-label={`Delete credential ${credential.id}`}
                    >
                      Delete
                    </button>
                  </span>
                </div>
              ))}
            </div>
          )}
        </section>

        <section className="dashboard-panel">
          <header className="dashboard-panel-head">
            <div>
              <p className="eyebrow">New</p>
              <h2>Add a credential</h2>
            </div>
          </header>
          <form className="pad" onSubmit={(event) => void create(event)}>
            <label className="field" htmlFor="credential-type">
              <span className="field-label">Type</span>
              <input
                id="credential-type"
                className="text-input"
                list="credential-types"
                value={type}
                onChange={(event) => setType(event.target.value)}
                required
              />
              <datalist id="credential-types">
                <option value="http" />
                <option value="slack" />
                <option value="postgres" />
                <option value="sheets" />
                <option value="discord" />
                <option value="teams" />
                <option value="resend" />
                <option value="hubspot" />
                <option value="jira" />
                <option value="openai" />
                <option value="hmac" />
              </datalist>
            </label>
            <label className="field" htmlFor="credential-data">
              <span className="field-label">Data (JSON)</span>
              <textarea
                id="credential-data"
                className="text-input code"
                rows={9}
                spellCheck={false}
                value={data}
                onChange={(event) => setData(event.target.value)}
              />
            </label>
            <button className="button primary" type="submit" disabled={busy}>
              {busy ? 'Saving…' : 'Save credential'}
            </button>
          </form>
        </section>
      </div>
    </main>
  );
}
