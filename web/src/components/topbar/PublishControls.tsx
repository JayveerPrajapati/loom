import type { ReactNode } from 'react';

export interface PublishControlsProps {
  startRun: (input?: Record<string, unknown>) => void | Promise<unknown>;
  starting: boolean;
  runBlocked: string | undefined;
  publish: () => void | Promise<unknown>;
  publishing: boolean;
  /** Audit 4E: the live phase label; rendered only while publishing. */
  publishPhase?: string;
  saving: boolean;
  online: boolean;
  /** The More menu, passed in by the composition root. */
  more: ReactNode;
}

/** The editor's primary cluster: Run, Publish, More — always visible
 *  together. The .publish-slot wrapper hosts the audit 4E live publish
 *  phase beside the Publish button. */
export function PublishControls({
  startRun,
  starting,
  runBlocked,
  publish,
  publishing,
  publishPhase,
  saving,
  online,
  more,
}: PublishControlsProps) {
  return (
    <div className="publish-controls">
      <button
        className="button primary"
        onClick={() => void startRun()}
        disabled={starting || Boolean(runBlocked)}
        title={runBlocked}
      >
        {starting ? 'Starting…' : 'Run'}
      </button>
      {/* Audit 4E: the live publish-progress lane. Kept inside the slot
          beside the button so Run/Publish/More never reflow. */}
      <div className="publish-slot">
        <button
          className="button subtle"
          onClick={() => void publish()}
          disabled={publishing || saving || !online}
        >
          {publishing ? 'Publishing…' : 'Publish'}
        </button>
        {publishing && publishPhase && (
          <span
            className="publish-phase muted small"
            role="status"
            aria-live="polite"
            data-testid="publish-phase"
          >
            {publishPhase}
          </span>
        )}
      </div>
      {more}
    </div>
  );
}
