import { useState } from 'react';
import { api, ApiError } from '../../api';
import { useDialogFocus } from '../../useDialogFocus';
import type { RecordedScriptTest, Script, ScriptTestResult } from '../../types';

export function ScriptTesterPanel({
  script,
  onClose,
  onNotify,
}: {
  script: Script;
  onClose: () => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}) {
  const ref = useDialogFocus<HTMLDivElement>(true, onClose);
  const [input, setInput] = useState('{ "hello": "world" }');
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<ScriptTestResult | null>(null);
  const [recorded, setRecorded] = useState<RecordedScriptTest | null>(null);
  const [recordedBusy, setRecordedBusy] = useState(false);

  const run = () => {
    setRunning(true);
    setResult(null);
    let parsed: unknown;
    try {
      parsed = JSON.parse(input);
    } catch {
      // Send the raw string; the server shapes non-JSON strings.
      parsed = input;
    }
    api
      .testScript(script.id, parsed)
      .then((outcome) => {
        setRunning(false);
        setResult(outcome);
        if (!outcome.ok && outcome.error) onNotify?.(outcome.error, 'error');
      })
      .catch((error: ApiError) => {
        setRunning(false);
        setResult({ ok: false, error: error.message });
        onNotify?.(error.message, 'error');
      });
  };

  // The recorded mode reads the effect tape of a finished run — nothing
  // executes, so it works on any server (Phase 5).
  const runRecorded = () => {
    setRecordedBusy(true);
    setRecorded(null);
    api
      .recordedScriptTest(script.id)
      .then((outcome) => {
        setRecordedBusy(false);
        setRecorded(outcome);
      })
      .catch((error: ApiError) => {
        setRecordedBusy(false);
        setRecorded({ found: false, message: error.message });
      });
  };

  return (
    <div className="dialog-backdrop" role="presentation">
      <div
        className="dialog script-tester-dialog"
        ref={ref}
        role="dialog"
        aria-modal="true"
        aria-label={`Test script ${script.name}`}
        data-testid="script-tester"
      >
        <header className="dialog-head">
          <h3>Test: {script.name}</h3>
          <button type="button" className="text-button" onClick={onClose}>
            Close
          </button>
        </header>
        <div className="dialog-body">
          <div className="tester-grid">
            <div className="tester-pane" data-testid="script-test-input">
              <label className="field" htmlFor={`script-test-input-${script.id}`}>
                <span className="field-label">Input (JSON)</span>
                <textarea
                  id={`script-test-input-${script.id}`}
                  className="text-input code"
                  rows={8}
                  spellCheck={false}
                  value={input}
                  onChange={(event) => setInput(event.target.value)}
                />
              </label>
              <div className="form-actions">
                <button type="button" className="button primary" onClick={run} disabled={running}>
                  {running ? 'Running…' : 'Run test'}
                </button>
                <button type="button" className="button" onClick={runRecorded} disabled={recordedBusy}>
                  {recordedBusy ? 'Reading tape…' : 'Recorded result'}
                </button>
              </div>
            </div>
            <div className="tester-pane" data-testid="script-test-output">
              <p className="field-label">Output</p>
              {result && (
                <div data-testid="script-test-result" className="mapping-detail">
                  <p className="field-label">Result {result.ok ? '(ok)' : '(failed)'}</p>
                  {result.result !== undefined && (
                    <pre className="code-preview">
                      {typeof result.result === 'string'
                        ? result.result
                        : JSON.stringify(result.result, null, 2)}
                    </pre>
                  )}
                  {typeof result.exitCode === 'number' && (
                    <p className="muted">exit code {result.exitCode}</p>
                  )}
                  {result.stdout && <pre className="code-preview">{result.stdout}</pre>}
                  {result.stderr && <pre className="code-preview">{result.stderr}</pre>}
                  {result.logs && result.logs.length > 0 && (
                    <pre className="code-preview">{result.logs.join('\n')}</pre>
                  )}
                  {result.error && <p className="field-error">{result.error}</p>}
                </div>
              )}
              {recorded && (
                <div data-testid="script-recorded-result" className="mapping-detail">
                  <p className="field-label">Recorded execution {recorded.found ? '' : '(none yet)'}</p>
                  {recorded.found ? (
                    <>
                      <p className="profile-preview-row">
                        <code>run</code>{' '}
                        <span>
                          {recorded.workflowName ?? recorded.runId} · wf v{recorded.workflowVersion} ·
                          node {recorded.nodeName ?? recorded.nodeId} · script v
                          {recorded.scriptVersion}
                          {recorded.recordedAt
                            ? ` · ${new Date(recorded.recordedAt).toLocaleString()}`
                            : ''}
                        </span>
                      </p>
                      <p className="profile-preview-row">
                        <code>exit</code> <span>{recorded.exitCode}</span>
                      </p>
                      {recorded.stdout && <pre className="code-preview">{recorded.stdout}</pre>}
                      {recorded.stderr && <pre className="code-preview">{recorded.stderr}</pre>}
                      {recorded.result !== undefined && (
                        <pre className="code-preview">
                          {typeof recorded.result === 'string'
                            ? recorded.result
                            : JSON.stringify(recorded.result, null, 2)}
                        </pre>
                      )}
                      <p className="field-hint">
                        Read from the run&rsquo;s effect tape — the same evidence hermetic replay
                        verifies. Nothing was re-run.
                      </p>
                    </>
                  ) : (
                    <p className="muted">{recorded.message}</p>
                  )}
                </div>
              )}
              {!result && !recorded && (
                <p className="muted small">Run the script or read a recorded execution.</p>
              )}
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
