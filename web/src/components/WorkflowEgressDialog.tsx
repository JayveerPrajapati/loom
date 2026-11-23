import { useEffect, useMemo, useState } from 'react';
import { useDialogFocus } from '../useDialogFocus';
import { validateEgressEntry } from '../egress';
import type { Workflow } from '../types';

/**
 * WorkflowEgressDialog: edits the workflow doc's own egress allowlist
 * (operational-trust campaign, D7-2). One destination per line
 * (https://host[:port], leading "*.host" wildcards). Empty = the
 * workflow imposes no restriction of its own; the operator's global
 * policy still applies. Entries are validated per line with the same
 * rules as the server, so an invalid entry can never be silently
 * dropped by a rejected save.
 */
export function WorkflowEgressDialog(props: {
  workflow: Workflow;
  notify: (message: string, tone?: 'info' | 'error') => void;
  onClose: () => void;
  apply: (mutator: (current: Workflow) => Workflow, record?: boolean) => void;
}) {
  const dialogRef = useDialogFocus<HTMLDivElement>(true, props.onClose);
  const [text, setText] = useState(() =>
    ((props.workflow.egress?.allowlist as string[] | undefined) ?? []).join('\n'),
  );

  useEffect(() => {
    setText(((props.workflow.egress?.allowlist as string[] | undefined) ?? []).join('\n'));
    // Seed once per open; typing must not be clobbered.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const entries = useMemo(
    () =>
      text
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean),
    [text],
  );
  const problems = useMemo(
    () =>
      entries
        .map((entry) => ({ entry, problem: validateEgressEntry(entry) }))
        .filter((result) => result.problem !== null),
    [entries],
  );

  const save = () => {
    if (problems.length > 0) return;
    props.apply((current) => ({
      ...current,
      egress: entries.length > 0 ? { allowlist: entries } : undefined,
    }));
    props.notify(
      entries.length > 0
        ? `Egress allowlist updated (${entries.length} destination${entries.length === 1 ? '' : 's'}).`
        : 'Egress allowlist cleared.',
    );
    props.onClose();
  };

  return (
    <div className="dialog-backdrop" onMouseDown={(event) => {
      if (event.target === event.currentTarget) props.onClose();
    }}>
      <div
        className="dialog"
        role="dialog"
        aria-modal="true"
        aria-label="Workflow egress allowlist"
        ref={dialogRef}
        data-testid="workflow-egress-dialog"
      >
        <h2>Egress allowlist</h2>
        <p className="dialog-note">
          Destinations this workflow may call, one per line — for example
          {' '}https://api.github.com or https://*.slack.com. Leave empty to impose no
          workflow-level restriction; the server&rsquo;s global egress policy always
          applies as well.
        </p>
        <label className="field" htmlFor="workflow-egress-entries">
          <span className="field-label">Allowed destinations</span>
          <textarea
            id="workflow-egress-entries"
            className="text-input"
            rows={7}
            spellCheck={false}
            placeholder={'https://api.github.com\nhttps://*.slack.com'}
            value={text}
            data-testid="workflow-egress-entries"
            onChange={(event) => setText(event.target.value)}
          />
        </label>
        {problems.length > 0 && (
          <div data-testid="workflow-egress-errors" role="alert" aria-live="assertive">
            {problems.map((result) => (
              <div className="issue issue-error" key={result.entry}>
                <span className="issue-badge">Error</span>
                <p>{result.problem}</p>
              </div>
            ))}
          </div>
        )}
        <div className="dialog-actions">
          <button className="button subtle" type="button" onClick={props.onClose}>
            Cancel
          </button>
          <button className="button primary" type="button" onClick={save} disabled={problems.length > 0}>
            Save
          </button>
        </div>
      </div>
    </div>
  );
}
