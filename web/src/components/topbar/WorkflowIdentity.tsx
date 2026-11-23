import type { Workflow, WorkflowSummary } from '../../types';

export interface WorkflowIdentityProps {
  workflow: Workflow;
  selectOptions: WorkflowSummary[];
  switchWorkflow: (id: string, mode: 'view' | 'edit') => void | Promise<void>;
  dirty: boolean;
  autosaving: boolean;
  busyNew: boolean;
  createBlank: () => void | Promise<void>;
}

/** The editor's heading zone. Audit 3D: the select doubles as the
 *  workflow title (no second page title competes with it), the badge
 *  carries the unsaved state, and the + button starts a new workflow. */
export function WorkflowIdentity({
  workflow,
  selectOptions,
  switchWorkflow,
  dirty,
  autosaving,
  busyNew,
  createBlank,
}: WorkflowIdentityProps) {
  return (
    <div className="topbar-heading">
      <select
        className="workflow-select"
        value={workflow.id}
        aria-label="Current workflow"
        onChange={(event) => void switchWorkflow(event.target.value, 'edit')}
      >
        {selectOptions.map((summary) => (
          <option key={summary.id} value={summary.id}>
            {summary.name}
          </option>
        ))}
      </select>
      {dirty && <span className="dirty-badge">{autosaving ? 'Saving…' : 'Unsaved'}</span>}
      <button className="icon-button" onClick={() => void createBlank()} disabled={busyNew} title="New workflow">
        <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" strokeWidth="1.9" aria-hidden="true">
          <path d="M12 5v14M5 12h14" strokeLinecap="round" />
        </svg>
      </button>
    </div>
  );
}
