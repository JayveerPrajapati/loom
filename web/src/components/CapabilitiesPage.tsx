import { AssetErrorState, AssetLoadingState } from './assets/AssetState';
import { useEffect, useState } from 'react';
import { api } from '../api';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import type { Capabilities } from '../types';

/**
 * CapabilitiesPage: the admin-only posture surface at /capabilities. It
 * reports the authentication, subprocess and egress consequences of the
 * flags this instance started with; /healthz keeps only the editor
 * preflight fields.
 */
export function CapabilitiesPage({ principalRole }: { principalRole?: string }) {
  const admin = principalRole === 'admin';
  const [state, setState] = useState<Capabilities | null>(null);
  const [failure, setFailure] = useState<LoadFailure | null>(null);
  const [reload, setReload] = useState(0);

  useEffect(() => {
    if (!admin) return;
    let cancelled = false;
    api
      .capabilities()
      .then((result) => {
        if (!cancelled) {
          setState(result);
          setFailure(null);
        }
      })
      .catch((reason: unknown) => {
        if (!cancelled) setFailure(classifyCurrentLoadFailure(reason));
      });
    return () => {
      cancelled = true;
    };
  }, [admin, reload]);

  if (!admin) {
    return (
      <main className="page-shell" id="main-stage" tabIndex={-1} data-testid="capabilities-page">
        <header className="page-head">
          <div>
            <h1>Capabilities</h1>
          </div>
        </header>
        <p className="banner notice">Capabilities are admin-only.</p>
      </main>
    );
  }

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1} data-testid="capabilities-page">
      <header className="page-head">
        <div>
          <h1>Capabilities</h1>
          <p className="lede">
            The deployment&rsquo;s authentication and execution posture. This page is
            admin-only; it names the consequences of the flags this instance started with.
          </p>
        </div>
      </header>

      {failure && (
        <AssetErrorState
          failure={failure}
          onRetry={() => setReload((count) => count + 1)}
          testId="capabilities-error"
        />
      )}

      {!state && !failure && <AssetLoadingState label="Loading capabilities…" className="muted" />}

      {state && (
        <div className="dashboard-grid">
          <section className="dashboard-panel" data-testid="capabilities-auth">
            <header className="dashboard-panel-head">
              <div>
                <h2>Authentication</h2>
              </div>
            </header>
            <div className="pad">
              <dl className="capability-list">
                <div>
                  <dt>Production mode</dt>
                  <dd>{state.prod ? 'enabled' : 'not enabled'}</dd>
                </div>
                <div>
                  <dt>Anonymous admin mode</dt>
                  <dd>{state.anonymous ? 'enabled' : 'not enabled'}</dd>
                </div>
                <div>
                  <dt>Service token</dt>
                  <dd>{state.serviceToken ? 'configured' : 'not configured'}</dd>
                </div>
              </dl>
            </div>
          </section>

          <section className="dashboard-panel" data-testid="capabilities-subprocess">
            <header className="dashboard-panel-head">
              <div>
                <h2>Subprocess nodes</h2>
              </div>
            </header>
            <div className="pad">
              {state.subprocess.enabled ? (
                <>
                  <p className="banner notice">
                    Editors can execute arbitrary Python on this host. Run Loom in a
                    dedicated container or VM; OS-level confinement is not a security
                    boundary.
                  </p>
                  <dl className="capability-list">
                    <div>
                      <dt>Package policy</dt>
                      <dd>
                        <code>{state.subprocess.policy || 'default'}</code>
                      </dd>
                    </div>
                    <div>
                      <dt>Environment cap</dt>
                      <dd>{state.subprocess.capMB ?? 0} MiB</dd>
                    </div>
                    <div>
                      <dt>OS-level confinement</dt>
                      <dd>{state.subprocess.osLevel || 'none'}</dd>
                    </div>
                    <div>
                      <dt>Network policy</dt>
                      <dd>
                        {state.subprocess.network === 'deny'
                          ? 'deny (python network is cut)'
                          : 'allow'}
                      </dd>
                    </div>
                    <div>
                      <dt>Environment isolation</dt>
                      <dd>
                        per requirement set, hash-verified; service token and environment
                        are not inherited
                      </dd>
                    </div>
                  </dl>
                </>
              ) : (
                <p className="muted">
                  Subprocess nodes are disabled. Publish and run <code>code.python</code>{' '}
                  with <code>--enable-subprocess-nodes</code> on a live host.
                </p>
              )}
            </div>
          </section>

          <section className="dashboard-panel" data-testid="capabilities-egress">
            <header className="dashboard-panel-head">
              <div>
                <h2>Egress</h2>
              </div>
            </header>
            <div className="pad">
              {state.egress.mode ? (
                <dl className="capability-list">
                  <div>
                    <dt>Mode</dt>
                    <dd>{state.egress.mode}</dd>
                  </div>
                  <div>
                    <dt>Allowlisted destinations</dt>
                    <dd>{state.egress.allowlist ?? 0}</dd>
                  </div>
                </dl>
              ) : (
                <p className="muted">No live egress posture (simulated host).</p>
              )}
            </div>
          </section>

          <section className="dashboard-panel" data-testid="capabilities-warnings">
            <header className="dashboard-panel-head">
              <div>
                <h2>Consequences</h2>
              </div>
            </header>
            <div className="pad">
              {state.warnings.length === 0 ? (
                <p className="muted">No capability warnings.</p>
              ) : (
                <ul className="capability-warnings">
                  {state.warnings.map((warning) => (
                    <li key={warning}>{warning}</li>
                  ))}
                </ul>
              )}
            </div>
          </section>
        </div>
      )}
    </main>
  );
}
