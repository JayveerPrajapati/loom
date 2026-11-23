import { decisionByline } from '../runDetailHelpers';

export type DecisionEvent = {
  event: { nodeId?: string; at: string };
  data: { decision: string; note: string; actor: string; source: string };
};

/** RunDecisionSummary: the last approval outcome of the run, shown
 *  under the header once a decision event exists. */
export function RunDecisionSummary({ decision }: { decision: DecisionEvent }) {
  const byline = decisionByline(decision.data.actor, decision.data.source);
  return (
    <p
      className={`run-decision-summary ${
        decision.data.decision === 'approved' ? 'approved' : 'rejected'
      }`}
    >
      <span
        className={`decision-badge ${
          decision.data.decision === 'approved' ? 'approved' : 'rejected'
        }`}
      >
        {decision.data.decision}
      </span>
      <span>
        {byline?.who}
        {byline?.via ? ` · ${byline.via}` : ''}
        {decision.data.note ? ` · ${decision.data.note}` : ''}
      </span>
    </p>
  );
}
