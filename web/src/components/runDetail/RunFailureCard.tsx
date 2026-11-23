// The failed-run card (Phase 6 D6-2): headline error with jump-to-data
// and retry actions. Props-only.
export function RunFailureCard(props: {
  nodeName: string;
  error: string;
  busy: string;
  onViewData: () => void;
  onRetry: () => void;
}) {
  return (
    <section className="run-failure-card" role="alert">
      <div className="run-failure-main">
        <span className="status-dot status-failed" aria-hidden="true" />
        <div>
          <strong>{props.nodeName} failed</strong>
          <p>{props.error}</p>
        </div>
      </div>
      <div className="run-failure-actions">
        <button className="button subtle" onClick={props.onViewData}>
          View data
        </button>
        <button className="button primary" onClick={props.onRetry} disabled={Boolean(props.busy)}>
          Retry from here
        </button>
      </div>
    </section>
  );
}
