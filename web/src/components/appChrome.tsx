
import { ActionError } from './assets/ActionError';
import type { RefObject } from 'react';
import { RunHistory } from './RunHistory';
import type { ProfileUpgradeChange, Run, RunEvent, ValidationIssue } from '../types';

// App chrome leaves (Phase 6 D6-1): the boot/error screens, the
// offline banner, the toast, and the profile-upgrade preview dialog —
// all presentational, state stays in App.

export function BootScreen({ label = 'Loading Loom…' }: { label?: string }) {
  return (
    <div className="boot">
      <span className="brand-mark large" aria-hidden="true"><svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" strokeWidth="2.2" aria-hidden="true"><path d="M4 17c4-10 12 10 16 0" strokeLinecap="round" /></svg></span>
      <p>{label}</p>
    </div>
  );
}

export function BootErrorScreen({ bootError, onRetry }: { bootError: string; onRetry: () => void }) {
  return (
    <div className="auth-screen">
      <div className="auth-card">
        <span className="brand-mark large" aria-hidden="true"><svg viewBox="0 0 24 24" width="1em" height="1em" fill="none" stroke="currentColor" strokeWidth="2.2" aria-hidden="true"><path d="M4 17c4-10 12 10 16 0" strokeLinecap="round" /></svg></span>
        <h1>Could not reach Loom</h1>
        <ActionError>{bootError}</ActionError>
        <button className="button primary full" onClick={onRetry}>
          Try again
        </button>
      </div>
    </div>
  );
}

export function OfflineBanner({ onRetry }: { onRetry: () => void }) {
  return (
    <div className="connection-banner" role="status" aria-live="assertive">
      <span>
        <strong>Loom server unreachable.</strong> Edits stay in this tab, but saving, running and
        approvals are paused.
      </span>
      <button className="button subtle" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

export function AppToast({ message, tone, aboveRunbar }: { message: string; tone: 'info' | 'error'; aboveRunbar: boolean }) {
  return (
    <div
      // On editor routes the toast sits above the bottom run bar
      // instead of covering its Run/Approve controls.
      className={`toast toast-${tone}${aboveRunbar ? ' toast-above-runbar' : ''}`}
      role={tone === 'error' ? 'alert' : 'status'}
      aria-live={tone === 'error' ? 'assertive' : 'polite'}
    >
      {message}
    </div>
  );
}

export function UpgradePreviewDialog({
  plan,
  issues,
  applying,
  focusRef,
  onClose,
  onApply,
}: {
  plan: ProfileUpgradeChange[];
  issues: ValidationIssue[];
  applying: boolean;
  focusRef: RefObject<HTMLDivElement>;
  onClose: () => void;
  onApply: () => void;
}) {
  return (
    <div className="dialog-backdrop" data-testid="upgrade-preview-backdrop">
      <div
        className="dialog"
        role="dialog"
        aria-label="Profile upgrades available"
        tabIndex={-1}
        ref={focusRef}
      >
        <header className="dialog-head">
          <h2>Profile upgrades available</h2>
          <button className="button subtle" onClick={onClose}>
            Close
          </button>
        </header>
        <div className="dialog-body" data-testid="upgrade-preview-dialog">
          <p className="field-hint">
            The pinned profiles below have newer versions. Values the profile owns follow it
            forward; your node&rsquo;s own overrides stay. Applying saves the upgrade to the
            draft and publishes a new workflow version.
          </p>
          <table className="diff-table">
            <thead>
              <tr>
                <th scope="col">Node</th>
                <th scope="col">Profile</th>
                <th scope="col">Key</th>
                <th scope="col">Before</th>
                <th scope="col">After</th>
              </tr>
            </thead>
            <tbody>
              {plan.map((change) => (
                <tr key={`${change.nodeId}-${change.key}`} className="diff-changed">
                  <th scope="row">
                    {change.nodeName || change.nodeId}
                    <br />
                    <code>{change.key}</code>
                  </th>
                  <td>
                    {change.profileName} (v{change.pinnedVersion} → v{change.latestVersion})
                  </td>
                  <td>
                    <code>{change.key}</code>
                  </td>
                  <td>{change.before || '—'}</td>
                  <td>{change.after || '—'}</td>
                </tr>
              ))}
            </tbody>
          </table>
          {issues.some((issue) => issue.severity === 'error') && (
            <ActionError>
              The upgraded workflow has validation errors — fix the profiles or node overrides
              first.
            </ActionError>
          )}
          <button
            className="button primary"
            data-testid="upgrade-apply"
            onClick={onApply}
            disabled={applying || issues.some((issue) => issue.severity === 'error')}
          >
            {applying ? 'Publishing…' : 'Save and publish with upgrades'}
          </button>
        </div>
      </div>
    </div>
  );
}

/** PublishWarningsBanner (D6-4): mapping-contract observations recorded
 *  at publish — blocking nothing, shown until dismissed. */
export function PublishWarningsBanner({
  warnings,
  onDismiss,
}: {
  warnings: { node: string; message: string }[];
  onDismiss: () => void;
}) {
  if (warnings.length === 0) return null;
  return (
    <div className="banner notice publish-warnings" role="status" data-testid="publish-mapping-warnings">
      <p>
        <strong>Published with mapping warnings</strong> — the publish stands; these observations
        may deserve a look:
      </p>
      <ul>
        {warnings.map((warning, index) => (
          <li key={index}>
            <code>{warning.node}</code>: {warning.message}
          </li>
        ))}
      </ul>
      <button className="button subtle" type="button" onClick={onDismiss}>
        Dismiss
      </button>
    </div>
  );
}

/** EmptyEditor: the editor route with no workflow open. */
export function EmptyEditor({ onBrowse }: { onBrowse: () => void }) {
  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <div className="empty-panel">
        <h2>No workflow open</h2>
        <p className="muted">Choose a workflow or create a blank one.</p>
        <button className="button primary" onClick={onBrowse}>
          Browse workflows
        </button>
      </div>
    </main>
  );
}

/** RunHistoryHost mounts the shared run-history dialog for App. */
export function RunHistoryHost(props: {
  open: boolean;
  workflowId: string | null;
  currentRunId: string | null;
  onClose: () => void;
  setRun: (run: Run) => void;
  setEvents: (events: RunEvent[]) => void;
  setRunId: (runId: string) => void;
  onOpenRunById: (runId: string) => void;
}) {
  const { open, onClose, setRun, setEvents, setRunId, onOpenRunById, ...rest } = props;
  return (
    <RunHistory
      open={open}
      onClose={onClose}
      onView={(historicalRun, historicalEvents) => {
        setRun(historicalRun);
        setEvents(historicalEvents);
        setRunId(historicalRun.id);
        onClose();
      }}
      onUpdate={(historicalRun, historicalEvents) => {
        setRun(historicalRun);
        setEvents(historicalEvents);
        setRunId(historicalRun.id);
      }}
      onOpenRun={(historicalRunId) => {
        onClose();
        onOpenRunById(historicalRunId);
      }}
      {...rest}
    />
  );
}
