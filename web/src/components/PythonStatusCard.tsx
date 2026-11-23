import { AssetLoadingState } from './assets/AssetState';
import { useEffect, useState } from 'react';
import { api } from '../api';
import type { PythonEnv, SubprocessNodes } from '../types';

// Python runtime status: /scripts carries the operator-facing truth
// about code.python execution in one compact strip — state, policy
// summary, environment count — with a Details disclosure for the full
// posture. The state comes from /healthz, which reports only what the
// engine host can actually do (a -sim server reads as disabled even
// with the flag set). Enabling never happens from here: it is a
// server-side, operator-controlled opt-in.
export function PythonStatusCard({ subprocess }: { subprocess: SubprocessNodes | null }) {
  // The envs listing rides the enabled card only — a disabled server
  // builds nothing, so there is nothing to list.
  const [envs, setEnvs] = useState<PythonEnv[] | null>(null);
  const enabled = !!subprocess?.enabled;
  useEffect(() => {
    if (!enabled) return;
    let alive = true;
    api
      .listPythonEnvs()
      .then((result) => {
        if (alive) setEnvs(result.envs ?? []);
      })
      .catch(() => undefined);
    return () => {
      alive = false;
    };
  }, [enabled]);
  if (!subprocess) return null;
  if (!subprocess.enabled) {
    return (
      <section
        className="python-status-strip"
        aria-label="Python runtime status"
        data-testid="python-status-card"
      >
        <div className="python-status-line">
          <strong>Python runtime</strong>
          <span className="count-pill" data-testid="python-status-disabled">
            disabled
          </span>
          <span className="python-status-summary muted small">
            code.python stays off unless an operator enables it — JavaScript tests and hermetic
            replay keep working.
          </span>
          <details className="python-status-details">
            <summary>Details</summary>
            <div className="python-status-body">
              <p>
                <strong>Python execution is disabled on this server.</strong> code.python runs
                an arbitrary Python subprocess — the single largest security surface in Loom —
                so it stays off unless an operator deliberately enables it.
              </p>
              <p className="field-label">Enabling (operator-controlled, never silent)</p>
              <p className="muted">
                Start <code>loom serve</code> with{' '}
                <code>--enable-subprocess-nodes</code> (or set{' '}
                <code>LOOM_ENABLE_SUBPROCESS_NODES=1</code>) and <code>uv</code> on PATH. The
                subprocess package policy lives at <code>subprocess-policy.json</code> in the
                data directory. See <code>docs/security.md</code> for the isolation model.
              </p>
              <p className="muted">
                While disabled: JavaScript scripts still test in-process here, and published
                workflows that used Python still replay hermetically — replay verifies the
                recorded tape instead of re-running anything.
              </p>
            </div>
          </details>
        </div>
      </section>
    );
  }
  const unsandboxed = subprocess.osLevel === 'none';
  return (
    <section
      className="python-status-strip"
      aria-label="Python runtime status"
      data-testid="python-status-card"
    >
      <div className="python-status-line">
        <strong>Python runtime</strong>
        <span className="count-pill" data-testid="python-status-enabled">
          enabled
        </span>
        <span className="python-status-summary muted small">
          {subprocess.policy || 'default subprocess policy'}
          {' · env cap '}
          {subprocess.capMB} MiB
          {envs === null ? '' : ` · ${envs.length} built environment${envs.length === 1 ? '' : 's'}`}
        </span>
        <details className="python-status-details">
          <summary>Details</summary>
          <div className="python-status-body">
            <p>
              <strong>code.python execution is enabled.</strong> Python is the largest security
              surface in Loom — keep this instance isolated from anything it should not read.
              Environments are built at publish time only; runs never install packages.
            </p>
            <div className="mapping-detail">
              <p className="profile-preview-row">
                <code>policy</code>
                <span>
                  {subprocess.policy || 'subprocess-policy.json (default, all pinned packages)'}
                </span>
              </p>
              <p className="profile-preview-row">
                <code>env cap</code>
                <span>{subprocess.capMB} MiB per dependency environment</span>
              </p>
              <p className="profile-preview-row">
                <code>os-level</code>
                <span>{subprocess.osLevel || 'unknown'}</span>
              </p>
              <p className="profile-preview-row">
                <code>network</code>
                <span
                  data-testid="python-network-posture"
                  className={subprocess.network === 'deny' ? undefined : 'field-error'}
                >
                  {subprocess.network === 'deny'
                    ? 'denied — the OS cuts python\u2019s network (fail-closed if unavailable)'
                    : 'allowed — scripts may open sockets; start with --subprocess-network deny to cut it'}
                </span>
              </p>
            </div>
            {unsandboxed && (
              <p className="field-error" data-testid="python-unsandboxed-warning">
                <strong>No OS-level sandbox on this host.</strong> The memory/time/output caps
                are resource limits, not a security boundary — editors are effectively admins.{' '}
                {subprocess.containerRecommended || 'Run subprocess-capable instances in a container.'}
              </p>
            )}
            <p className="muted">
              A subprocess-capable server refuses to also run with a service token: that
              combination would let Python read service data and keys.
            </p>
            <div className="field-group-head">
              <h3 className="field-heading">Built environments</h3>
            </div>
            {envs === null && <AssetLoadingState label="Loading environments…" className="muted" />}
            {envs !== null && envs.length === 0 && (
              <p className="muted" data-testid="python-envs-empty">
                None yet — publishing a workflow with code.python requirements builds them.
              </p>
            )}
            {envs !== null && envs.length > 0 && (
              <div className="mapping-detail" data-testid="python-envs-list">
                {envs.map((env) => (
                  <p className="profile-preview-row" key={env.key}>
                    <code>{env.key.slice(0, 12)}</code>
                    <span>
                      {env.requirements.join(', ')} · {env.sizeMB} MiB
                    </span>
                  </p>
                ))}
              </div>
            )}
          </div>
        </details>
      </div>
    </section>
  );
}
