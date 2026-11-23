import type { ValidationIssue, Workflow } from '../types';
import { IssueList } from './InspectorFields';

interface Props {
  workflow: Workflow;
  issues: ValidationIssue[];
  onWorkflowName: (name: string) => void;
  onWorkflowSettings: (patch: Record<string, unknown>) => void;
  onOpenRunbook: () => void;
  onSelectNode: (id: string | null) => void;
}

// The inspector's no-selection view: workflow identity, settings, the
// trigger summary and the workflow-level validation list. Extracted
// verbatim from Inspector (Profile-Centric UX Phase 3 item 3.2).
export function InspectorWorkflowPanel({
  workflow,
  issues,
  onWorkflowName,
  onWorkflowSettings,
  onOpenRunbook,
  onSelectNode,
}: Props) {
  const errors = issues.filter((issue) => issue.severity === 'error').length;
  const trigger = workflow.nodes.find((item) => item.type.startsWith('trigger.'));
  return (
    <aside className="panel inspector" aria-label="Workflow inspector">
      <div className="panel-head">
        <div>
          <h2>Workflow</h2>
        </div>
        <span className={`count-pill ${errors ? 'bad' : 'good'}`}>{errors ? `${errors} errors` : 'Valid'}</span>
      </div>
      <div className="inspector-scroll">
        <label className="field" htmlFor="workflow-name">
          <span className="field-label">Name</span>
          <input
            id="workflow-name"
            className="text-input"
            value={workflow.name}
            onChange={(event) => onWorkflowName(event.target.value)}
          />
        </label>
        <label className="field" htmlFor="workflow-max-runs">
          <span className="field-label">Max parallel runs</span>
          <input
            id="workflow-max-runs"
            className="text-input"
            type="number"
            min={1}
            value={String(workflow.settings?.maxParallelRuns ?? '')}
            placeholder="No limit"
            onChange={(event) =>
              onWorkflowSettings(
                event.target.value.trim() === ''
                  ? { maxParallelRuns: undefined }
                  : { maxParallelRuns: Number(event.target.value) },
              )
            }
          />
          <span className="field-hint">Extra runs wait in the queue until a slot frees.</span>
        </label>
        <div className="meta-grid">
          <div>
            <span>Id</span>
            <code>{workflow.id}</code>
          </div>
          <div>
            <span>Version</span>
            <code>{workflow.version}</code>
          </div>
          <div>
            <span>Nodes</span>
            <code>{workflow.nodes.length}</code>
          </div>
          <div>
            <span>Edges</span>
            <code>{workflow.edges.length}</code>
          </div>
        </div>
        <button className="button subtle runbook-button" onClick={onOpenRunbook}>
          Download runbook
        </button>
        {trigger && (
          <div className="callout">
            <p className="field-label">Trigger</p>
            <strong>{trigger.name || trigger.type}</strong>
            <span>
              {String(trigger.config.path ?? trigger.config.schedule ?? trigger.type.replace('trigger.', ''))}
            </span>
          </div>
        )}
        <section className="inspector-section">
          <h3>Validation</h3>
          <IssueList issues={issues} onSelectNode={onSelectNode} />
        </section>
        <section className="inspector-section">
          <h3>Shortcuts</h3>
          <dl className="shortcut-list">
            <div>
              <dt>Run</dt>
              <dd>⌘ ⏎</dd>
            </div>
            <div>
              <dt>Save</dt>
              <dd>⌘ S</dd>
            </div>
            <div>
              <dt>Undo</dt>
              <dd>⌘ Z</dd>
            </div>
            <div>
              <dt>Command palette</dt>
              <dd>⌘ K</dd>
            </div>
          </dl>
        </section>
      </div>
    </aside>
  );
}
