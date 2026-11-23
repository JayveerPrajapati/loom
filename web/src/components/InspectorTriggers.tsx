import { useState } from 'react';
import {
  COMMON_TIMEZONES,
  CRON_PRESETS,
  SchedulePreviewPanel,
  cronPresetFor,
} from './dashboard/shared';

export function WebhookTester({ path, method }: { path: string; method: string }) {
  const [requestMethod, setRequestMethod] = useState<'GET' | 'POST'>(
    method === 'GET' ? 'GET' : 'POST',
  );
  const [body, setBody] = useState('{\n  "hello": "world"\n}');
  const [asyncMode, setAsyncMode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState('');

  const send = async () => {
    if (!path.trim()) {
      setError('Set a webhook path first.');
      return;
    }
    if (requestMethod === 'POST') {
      try {
        JSON.parse(body);
      } catch {
        setError('Body must be valid JSON.');
        return;
      }
    }
    setBusy(true);
    setError('');
    setResult('');
    try {
      const response = await fetch(
        `/hook/${path
          .split('/')
          .filter(Boolean)
          .map(encodeURIComponent)
          .join('/')}${asyncMode ? '?async=1' : ''}`,
        {
          method: requestMethod,
          headers: requestMethod === 'POST' ? { 'content-type': 'application/json' } : undefined,
          body: requestMethod === 'POST' ? body : undefined,
        },
      );
      const text = await response.text();
      let formatted = text;
      try {
        formatted = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        // Keep the raw response.
      }
      setResult(`${response.status} ${response.statusText}\n${formatted}`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="inspector-section">
      <h3>Test webhook</h3>
      <p className="field-hint">
        Sends a real request to <code>/hook/{path || '…'}</code>. Use <code>?async=1</code>{' '}
        for an immediate 202.
      </p>
      <div className="webhook-test-row">
        <label className="field" htmlFor="webhook-method">
          <span className="field-label">Method</span>
          <select
            id="webhook-method"
            className="text-input"
            value={requestMethod}
            onChange={(event) => setRequestMethod(event.target.value as 'GET' | 'POST')}
          >
            <option value="POST">POST</option>
            <option value="GET">GET</option>
          </select>
        </label>
        <button className="button primary" onClick={() => void send()} disabled={busy}>
          {busy ? 'Sending…' : 'Send test'}
        </button>
      </div>
      <label className="checkbox-field" htmlFor="webhook-async">
        <input
          id="webhook-async"
          type="checkbox"
          checked={asyncMode}
          onChange={(event) => setAsyncMode(event.target.checked)}
        />
        <span>Return 202 immediately</span>
      </label>
      {requestMethod === 'POST' && (
        <label className="field" htmlFor="webhook-body">
          <span className="field-label">JSON body</span>
          <textarea
            id="webhook-body"
            className="text-input code-input"
            rows={6}
            value={body}
            onChange={(event) => setBody(event.target.value)}
            spellCheck={false}
          />
        </label>
      )}
      {error && <p className="field-error">{error}</p>}
      {result && <pre className="json-view webhook-result">{result}</pre>}
    </section>
  );
}

export function CronTriggerFields({
  schedule,
  timezone,
  onScheduleChange,
  onTimezoneChange,
}: {
  schedule: string;
  timezone: string;
  onScheduleChange: (value: string) => void;
  onTimezoneChange: (value: string) => void;
}) {
  const previewConfig = schedule.trim()
    ? { schedule: schedule.trim(), timezone: timezone.trim() || 'UTC' }
    : undefined;
  return (
    <>
      <label className="field" htmlFor="cron-preset">
        <span className="field-label">Schedule preset</span>
        <select
          id="cron-preset"
          className="text-input"
          value={cronPresetFor(schedule)}
          onChange={(event) => {
            const preset = CRON_PRESETS.find((item) => item.id === event.target.value);
            if (preset?.expression) onScheduleChange(preset.expression);
          }}
        >
          {CRON_PRESETS.map((preset) => (
            <option key={preset.id} value={preset.id}>
              {preset.label}
            </option>
          ))}
        </select>
      </label>
      <label className="field" htmlFor="cron-schedule">
        <span className="field-label">Cron schedule</span>
        <input
          id="cron-schedule"
          className="text-input"
          value={schedule}
          onChange={(event) => onScheduleChange(event.target.value)}
          placeholder="0 9 * * *"
          spellCheck={false}
        />
        <span className="field-hint">
          Five fields: minute hour day-of-month month day-of-week. Use <code>*/10</code> for every 10 minutes.
        </span>
      </label>
      <label className="field" htmlFor="cron-timezone">
        <span className="field-label">Timezone</span>
        <input
          id="cron-timezone"
          className="text-input"
          list="cron-timezone-options"
          value={timezone}
          onChange={(event) => onTimezoneChange(event.target.value)}
          placeholder="UTC"
          spellCheck={false}
        />
        <datalist id="cron-timezone-options">
          {COMMON_TIMEZONES.map((zone) => (
            <option key={zone} value={zone} />
          ))}
        </datalist>
      </label>
      <SchedulePreviewPanel triggerType="cron" config={previewConfig} />
    </>
  );
}
