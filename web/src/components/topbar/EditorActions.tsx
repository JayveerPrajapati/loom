import { formatCount } from '../../appShared';
import type { ValidationIssue } from '../../types';

export interface EditorActionsProps {
  errorCount: number;
  warningCount: number;
  issues: ValidationIssue[];
  setSelectedId: (id: string | null) => void;
  undo: () => void;
  redo: () => void;
  undoStack: unknown[];
  redoStack: unknown[];
  save: () => void | Promise<boolean>;
  saving: boolean;
  online: boolean;
}

/** The editor's secondary cluster, left of the primary actions:
 *  validation status (always glanceable — it is the workflow's health,
 *  not a command), compact undo/redo, and Save. */
export function EditorActions({
  errorCount,
  warningCount,
  issues,
  setSelectedId,
  undo,
  redo,
  undoStack,
  redoStack,
  save,
  saving,
  online,
}: EditorActionsProps) {
  const focusFirstIssue = () => {
    const issue =
      issues.find((item) => item.severity === 'error' && item.node) ??
      issues.find((item) => item.node);
    setSelectedId(issue?.node ?? null);
  };

  return (
    <div className="editor-actions">
      <button
        className={`validation-chip ${errorCount ? 'bad' : 'good'}`}
        onClick={() => {
          focusFirstIssue();
        }}
        aria-label={
          errorCount
            ? `Show ${formatCount(errorCount, 'validation error')}`
            : warningCount
              ? `Show ${formatCount(warningCount, 'validation warning')}`
              : 'Workflow is valid'
        }
        title={issues.some((item) => item.node) ? 'Show the first issue' : 'Show validation details'}
      >
        {errorCount
          ? formatCount(errorCount, 'error')
          : warningCount
            ? formatCount(warningCount, 'warning')
            : 'Valid'}
      </button>
      <button
        className="icon-button"
        aria-label="Undo"
        title="Undo"
        onClick={undo}
        disabled={undoStack.length === 0}
      >
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
          <path d="M9 7L4 12l5 5M4 12h9a6 6 0 016 6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      <button
        className="icon-button"
        aria-label="Redo"
        title="Redo"
        onClick={redo}
        disabled={redoStack.length === 0}
      >
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
          <path d="M15 7l5 5-5 5M20 12h-9a6 6 0 00-6 6" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>
      <button
        className="button subtle"
        onClick={() => void save()}
        disabled={saving || !online}
        title={!online ? 'Loom server is unreachable' : undefined}
      >
        {saving ? 'Saving…' : 'Save'}
      </button>
    </div>
  );
}
