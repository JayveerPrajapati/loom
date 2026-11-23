import { WorkflowDiffBadges, formatTime, nextRunLabel } from '../shared';
import type { Job, Workflow } from '../../../types';

/**
 * JobPinnedVersionPanel: what the job pins — template facts, the
 * upgrade affordance when a newer version exists, and the pinned
 * template's node steps.
 */
export function JobPinnedVersionPanel({
  job,
  latest,
  templateWorkflow,
  latestWorkflow,
  busy,
  onOpenTemplate,
  onUpgrade,
}: {
  job: Job;
  latest: number;
  templateWorkflow: Workflow | null;
  latestWorkflow: Workflow | null;
  busy: string;
  onOpenTemplate: (id: string) => void;
  onUpgrade: (version: number) => void;
}) {
  return (
    <section className="dashboard-panel">
      <header className="dashboard-panel-head">
        <div>
          <h2>Pinned version</h2>
        </div>
      </header>
      <dl className="job-facts">
        <div>
          <dt>Template</dt>
          <dd>
            <button className="text-link" onClick={() => onOpenTemplate(job.templateId)}>
              {job.templateName || job.templateId}
            </button>
          </dd>
        </div>
        <div>
          <dt>Version</dt>
          <dd>v{job.templateVersion}</dd>
        </div>
        <div>
          <dt>Last run</dt>
          <dd>{job.lastRunAt ? formatTime(job.lastRunAt) : 'Never'}</dd>
        </div>
        <div>
          <dt>Next run</dt>
          <dd>{nextRunLabel(job)}</dd>
        </div>
        <div>
          <dt>Created</dt>
          <dd>{formatTime(job.createdAt)}</dd>
        </div>
      </dl>
      {latest > job.templateVersion && (
        <>
          <button
            className="button subtle full"
            onClick={() => onUpgrade(latest)}
            disabled={Boolean(busy)}
          >
            Upgrade to v{latest}
          </button>
          {templateWorkflow && latestWorkflow && (
            <WorkflowDiffBadges from={templateWorkflow} to={latestWorkflow} />
          )}
        </>
      )}
      {templateWorkflow && (
        <div className="job-template-steps">
          <div className="job-template-steps-head">
            <h3>Template steps</h3>
            <span className="muted small">
              {templateWorkflow.nodes.length} node{templateWorkflow.nodes.length === 1 ? '' : 's'}
            </span>
          </div>
          <ol>
            {templateWorkflow.nodes.map((node) => (
              <li key={node.id}>
                <span className="step-index" aria-hidden="true" />
                <span>
                  <strong>{node.name || node.id}</strong>
                  <em>{node.type}</em>
                </span>
              </li>
            ))}
          </ol>
        </div>
      )}
    </section>
  );
}
