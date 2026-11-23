import { useState } from 'react';
import { api } from '../../api';
import type { DataSchema } from '../../types';

/**
 * SchemaSampleTester: paste a sample payload and check it against the
 * contract with the exact production validator. The result never gates
 * a save.
 */
export function SchemaSampleTester({ schema }: { schema: DataSchema }) {
  const [sampleText, setSampleText] = useState('{\n  \n}');
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<
    { ok: boolean; violations: { path: string; message: string }[] } | null
  >(null);

  const testSample = async () => {
    setTesting(true);
    setTestResult(null);
    let sample: unknown;
    try {
      sample = JSON.parse(sampleText);
    } catch {
      setTestResult({ ok: false, violations: [{ path: '', message: 'Sample is not valid JSON.' }] });
      setTesting(false);
      return;
    }
    try {
      const result = await api.testDataSchema(schema.id, sample);
      setTestResult(result);
    } catch {
      setTestResult({ ok: false, violations: [{ path: '', message: 'Test request failed.' }] });
    } finally {
      setTesting(false);
    }
  };

  return (
    <section className="dashboard-panel pad" data-testid="schema-tester">
      <header className="dashboard-panel-head">
        <div>
          <h2>Test a sample against {schema.name}</h2>
        </div>
      </header>
      <p className="field-hint">
        Paste a sample payload: the exact production validator checks it and names every
        violation. The result never gates a save.
      </p>
      <label className="field" htmlFor="schema-sample">
        <span className="field-label">Sample payload (JSON)</span>
        <textarea
          id="schema-sample"
          className="text-input code"
          rows={6}
          spellCheck={false}
          value={sampleText}
          onChange={(event) => setSampleText(event.target.value)}
        />
      </label>
      <button className="button" onClick={() => void testSample()} disabled={testing}>
        {testing ? 'Testing…' : 'Test sample'}
      </button>
      {testResult && (
        <div
          className={`banner ${testResult.ok ? 'notice' : 'error'}`}
          role="status"
          data-testid="schema-test-result"
        >
          {testResult.ok ? (
            <p>The sample conforms to the contract.</p>
          ) : (
            <ul>
              {testResult.violations.map((violation, index) => (
                <li key={index}>
                  <code>{violation.path || '(root)'}</code>: {violation.message}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  );
}
