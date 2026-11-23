import { ActionError } from '../assets/ActionError';
import { useState } from 'react';
import { api } from '../../api';
import type { ProfileTestResult } from '../../types';

// The Test panel (Phase 6 D6-3): the saved fixture editor, the run
// button, and the connectivity/execution result rendering (Phase 5:
// code profiles execute their source; the fixture prefills it). The
// test run state lives here; the fixture text stays with the page
// because save persists it.
export function ProfileTestPanel(props: {
  nodeType: string;
  editing: boolean;
  profileId?: string;
  busy: boolean;
  testInput: string;
  setTestInput: (value: string) => void;
}) {
  const { nodeType } = props;
  const [testBusy, setTestBusy] = useState(false);
  const [testError, setTestError] = useState('');
  const [testResult, setTestResult] = useState<ProfileTestResult | null>(null);

  const runTest = async () => {
    if (!props.profileId) return;
    setTestBusy(true);
    setTestError('');
    setTestResult(null);
    try {
      setTestResult(await api.testConfigProfile(props.profileId));
    } catch (reason) {
      setTestError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setTestBusy(false);
    }
  };
  const codeProfile = nodeType === 'code.run' || nodeType === 'code.python';
  return (
    <div role="tabpanel" aria-labelledby="profile-section-test" data-testid="profile-panel-test">
      <div className="field-group-head">
        <h3 className="field-heading">Test this profile</h3>
      </div>
      <p className="field-hint">
        Runs the saved profile&rsquo;s connectivity check and resolves its included assets against
        the current versions — exactly what publish would compose. The result never gates a save:
        a failing test is information, not a verdict.
      </p>
      <label className="field" htmlFor="profile-test-input">
        <span className="field-label">Test fixture (JSON)</span>
        <textarea
          id="profile-test-input"
          className="text-input code"
          rows={4}
          spellCheck={false}
          data-testid="profile-test-input"
          value={props.testInput}
          onChange={(event) => props.setTestInput(event.target.value)}
          placeholder='{ "hello": "world" }'
        />
        <span className="field-hint">
          Saved with the profile and used as the test input. Code profiles execute their source
          against it (JavaScript in-process; Python needs a live server).
        </span>
      </label>
      {props.editing ? (
        <button
          type="button"
          className="button primary"
          data-testid="profile-test-run"
          onClick={() => void runTest()}
          disabled={testBusy || props.busy}
        >
          {testBusy ? 'Testing…' : 'Run test'}
        </button>
      ) : (
        <p className="field-hint">Save the profile first — the test runs the stored version.</p>
      )}
      {testError && (
        <ActionError testId="profile-test-error">{testError}</ActionError>
      )}
      {testResult && (
        <section className="inspector-section" data-testid="profile-test-output">
          <div className="field-group-head">
            <h4 className="field-heading">{codeProfile ? 'Execution' : 'Connectivity'}</h4>
          </div>
          {(() => {
            if (codeProfile) {
              if (testResult.error) {
                return (
                  <p className="profile-preview-row" data-testid="profile-test-connectivity">
                    <code>error</code> <span>{testResult.error}</span>
                  </p>
                );
              }
              if (testResult.skipped) {
                return (
                  <p className="profile-preview-row" data-testid="profile-test-connectivity">
                    <code>skipped</code> <span>{testResult.skipped}</span>
                  </p>
                );
              }
              return (
                <p className="profile-preview-row" data-testid="profile-test-connectivity">
                  <code>{testResult.ok ? 'executed' : 'failed'}</code>{' '}
                  <span>
                    {typeof testResult.exitCode === 'number'
                      ? `exit code ${testResult.exitCode}`
                      : 'returned a result'}
                  </span>
                </p>
              );
            }
            const ok = testResult.ok;
            const skipped = testResult.skipped;
            const error = testResult.error;
            if (error) {
              return (
                <p className="profile-preview-row" data-testid="profile-test-connectivity">
                  <code>error</code> <span>{error}</span>
                </p>
              );
            }
            if (skipped) {
              return (
                <p className="profile-preview-row" data-testid="profile-test-connectivity">
                  <code>skipped</code> <span>{skipped}</span>
                </p>
              );
            }
            const latency =
              typeof testResult.latencyMs === 'number' ? ` in ${testResult.latencyMs} ms` : '';
            const status =
              typeof testResult.status === 'number' ? ` — HTTP ${testResult.status}` : '';
            return (
              <p className="profile-preview-row" data-testid="profile-test-connectivity">
                <code>reachable</code> <span>{`connection succeeded${status}${latency}`}</span>
                {!ok && <em className="from-node">incomplete</em>}
              </p>
            );
          })()}
          {(() => {
            const bundle = testResult.bundle;
            if (!bundle) return null;
            const rows: React.ReactNode[] = [];
            for (const mapping of bundle.mappings ?? []) {
              rows.push(
                <p className="profile-preview-row" key={`mapping-${mapping.id}`}>
                  <code>{mapping.kind} mapping</code>{' '}
                  <span>
                    {mapping.name} (v{mapping.version}), {mapping.rows} rows
                  </span>
                  <em className="from-bundle">resolved</em>
                </p>,
              );
            }
            for (const schema of bundle.schemas ?? []) {
              rows.push(
                <p className="profile-preview-row" key={`schema-${schema.id}`}>
                  <code>{schema.slot} contract</code>{' '}
                  <span>
                    {schema.name} (v{schema.version}), {schema.fields} fields
                  </span>
                  <em className="from-bundle">resolved</em>
                </p>,
              );
            }
            if (bundle.script) {
              rows.push(
                <p className="profile-preview-row" key={`script-${bundle.script.id}`}>
                  <code>script</code>{' '}
                  <span>
                    {bundle.script.name} (v{bundle.script.version}), {bundle.script.language},{' '}
                    {bundle.script.bytes} bytes
                  </span>
                  <em className="from-bundle">resolved</em>
                </p>,
              );
            }
            for (const group of bundle.paramRows ?? []) {
              rows.push(
                <p className="profile-preview-row" key={`rows-${group.group}`}>
                  <code>{group.group}</code> <span>{group.rows} rows compile</span>
                </p>,
              );
            }
            for (const issue of bundle.issues ?? []) {
              rows.push(
                <p className="profile-preview-row" key={`issue-${issue.name}`}>
                  <code>{issue.name}</code> <span>{issue.message}</span>
                  <em className="from-node">issue</em>
                </p>,
              );
            }
            return (
              <>
                <div className="field-group-head">
                  <h4 className="field-heading">
                    Included behavior{testResult.bundleOk === false ? ' — with issues' : ''}
                  </h4>
                </div>
                {rows.length > 0 ? rows : <p className="field-hint">No included assets and no parameter rows.</p>}
              </>
            );
          })()}
          {codeProfile && testResult.result !== undefined && (
            <>
              <div className="field-group-head">
                <h4 className="field-heading">Result</h4>
              </div>
              <pre className="code-preview" data-testid="profile-test-result">
                {typeof testResult.result === 'string'
                  ? testResult.result
                  : JSON.stringify(testResult.result, null, 2)}
              </pre>
            </>
          )}
          {codeProfile && testResult.stdout && <pre className="code-preview">{testResult.stdout}</pre>}
          {codeProfile && testResult.stderr && <pre className="code-preview">{testResult.stderr}</pre>}
        </section>
      )}
    </div>
  );
}
