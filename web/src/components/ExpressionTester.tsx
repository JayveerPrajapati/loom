import { useState } from 'react';
import { api, ApiError } from '../api';

// The "Test an expression" disclosure: evaluates a sample payload against
// the expression endpoint. Owns its own state; extracted verbatim from
// Inspector's Configuration section (Profile-Centric UX Phase 3 item 3.2).
export function ExpressionTester() {
  const [tester, setTester] = useState({ expression: '', sample: '{}' });
  const [testerResult, setTesterResult] = useState<{ ok: boolean; message: string } | null>(null);
  const [testing, setTesting] = useState(false);

  return (
    <details className="inspector-section expression-tester" data-testid="expression-tester">
      <summary>Test an expression</summary>
      <label className="field" htmlFor="tester-expression">
        <span className="field-label">Expression</span>
        <input
          id="tester-expression"
          className="text-input"
          placeholder="Hello {{ $json.name }}"
          value={tester.expression}
          onChange={(event) => setTester({ ...tester, expression: event.target.value })}
        />
      </label>
      <label className="field" htmlFor="tester-sample">
        <span className="field-label">Sample item (JSON)</span>
        <textarea
          id="tester-sample"
          className="text-input"
          rows={4}
          spellCheck={false}
          value={tester.sample}
          onChange={(event) => setTester({ ...tester, sample: event.target.value })}
        />
      </label>
      <div className="form-actions">
        <button
          type="button"
          className="text-button"
          disabled={testing}
          onClick={() => {
            setTesting(true);
            let sample: Record<string, unknown> = {};
            try {
              sample = tester.sample.trim() ? JSON.parse(tester.sample) : {};
            } catch {
              setTesterResult({ ok: false, message: 'Sample is not valid JSON.' });
              setTesting(false);
              return;
            }
            api
              .evalExpression(tester.expression, sample)
              .then((result) =>
                setTesterResult(
                  result.ok
                    ? { ok: true, message: JSON.stringify(result.value) }
                    : { ok: false, message: result.error ?? 'evaluation failed' },
                ),
              )
              .catch((error: ApiError) => setTesterResult({ ok: false, message: error.message }))
              .finally(() => setTesting(false));
          }}
        >
          {testing ? 'Testing…' : 'Test'}
        </button>
      </div>
      {testerResult && (
        <div
          className={`banner ${testerResult.ok ? 'notice' : 'error'}`}
          role="status"
          data-testid="expression-test-result"
        >
          <code>{testerResult.message}</code>
        </div>
      )}
    </details>
  );
}
