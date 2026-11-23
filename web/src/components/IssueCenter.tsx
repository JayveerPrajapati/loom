import type { InspectorIssue, InspectorIssueCategory } from '../inspectorIssues';

const CATEGORY_LABEL: Record<InspectorIssueCategory, string> = {
  graph: 'Graph',
  config: 'Configuration',
  profile: 'Profile',
  asset: 'Asset',
  credential: 'Credential',
  contract: 'Contract',
  connector: 'Connector',
  runtime: 'Runtime',
  python: 'Python',
};

// The inspector's single issue center (Apple-Grade Phase 3 D3-4): every
// category the node can fail on, with severity, source and a fix action.
export function IssueCenter({
  issues,
  onFix,
}: {
  issues: InspectorIssue[];
  onFix: (issue: InspectorIssue) => void;
}) {
  const errors = issues.filter((issue) => issue.severity === 'error').length;
  const warnings = issues.length - errors;
  return (
    <section
      className="inspector-section inspector-issues"
      id="inspector-issues"
      data-testid="inspector-issues"
    >
      <div className="section-head-with-hint">
        <h3>Issues</h3>
        {issues.length > 0 && (
          <span className="issue-count" data-testid="inspector-issue-count">
            {errors} blocker{errors === 1 ? '' : 's'} · {warnings} warning
            {warnings === 1 ? '' : 's'}
          </span>
        )}
      </div>
      {issues.length === 0 ? (
        <p className="inspector-ok" data-testid="inspector-issues-clear">
          No issues for this node.
        </p>
      ) : (
        <div className="issue-list">
          {issues.map((issue) => (
            <div
              className={`issue issue-${issue.severity}`}
              key={issue.id}
              data-testid={`inspector-issue-${issue.category}`}
              data-category={issue.category}
            >
              <span className="issue-badge">
                {issue.severity === 'error' ? 'Error' : 'Warning'}
              </span>
              <div className="issue-body">
                <p>{issue.message}</p>
                <p className="issue-meta">
                  {CATEGORY_LABEL[issue.category]} · {issue.source}
                  {issue.field ? (
                    <>
                      {' '}
                      · <code>{issue.field}</code>
                    </>
                  ) : null}
                </p>
              </div>
              {issue.field && (
                <button type="button" className="issue-link" onClick={() => onFix(issue)}>
                  Fix field
                </button>
              )}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}
