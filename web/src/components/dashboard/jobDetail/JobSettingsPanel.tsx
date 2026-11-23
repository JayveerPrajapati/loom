import {
  COMMON_TIMEZONES,
  CRON_PRESETS,
  SchedulePreviewPanel,
  cronPresetFor,
  schedulePreviewConfig,
  triggerLabel,
} from '../shared';
import type { Job } from '../../../types';

/**
 * JobSettingsPanel: the job's editable settings form. Controlled inputs
 * owned by the page; every field is disabled unless editing.
 */
export function JobSettingsPanel({
  job,
  editing,
  onDirty,
  name,
  triggerType,
  schedule,
  cronPreset,
  timezone,
  runAt,
  maxConcurrency,
  inputText,
  previewConfig,
  onNameChange,
  onTriggerTypeChange,
  onScheduleChange,
  onCronPresetChange,
  onTimezoneChange,
  onRunAtChange,
  onMaxConcurrencyChange,
  onInputTextChange,
}: {
  job: Job;
  editing: boolean;
  onDirty: () => void;
  name: string;
  triggerType: string;
  schedule: string;
  cronPreset: string;
  timezone: string;
  runAt: string;
  maxConcurrency: string;
  inputText: string;
  previewConfig: ReturnType<typeof schedulePreviewConfig>;
  onNameChange: (next: string) => void;
  onTriggerTypeChange: (next: string) => void;
  onScheduleChange: (next: string) => void;
  onCronPresetChange: (next: string) => void;
  onTimezoneChange: (next: string) => void;
  onRunAtChange: (next: string) => void;
  onMaxConcurrencyChange: (next: string) => void;
  onInputTextChange: (next: string) => void;
}) {
  return (
    <section className="dashboard-panel">
      <header className="dashboard-panel-head">
        <div>
          <h2>Job settings</h2>
        </div>
        <span className={`job-state ${job.enabled ? '' : 'paused'}`}>
          {job.enabled ? 'Active' : 'Paused'}
        </span>
      </header>
      <div className="field-stack" onInput={onDirty}>
        <label className="field" htmlFor="detail-job-name">
          <span className="field-label">Name</span>
          <input
            id="detail-job-name"
            className="text-input"
            value={name}
            disabled={!editing}
            onChange={(event) => onNameChange(event.target.value)}
          />
        </label>
        <label className="field" htmlFor="detail-job-trigger">
          <span className="field-label">Trigger</span>
          <select
            id="detail-job-trigger"
            className="text-input"
            value={triggerType}
            disabled={!editing}
            onChange={(event) => onTriggerTypeChange(event.target.value)}
          >
            <option value="manual">Manual</option>
            <option value="cron">Schedule (cron)</option>
            <option value="once">One time</option>
          </select>
        </label>
        {triggerType === 'cron' && (
          <>
            <label className="field" htmlFor="detail-job-cron-preset">
              <span className="field-label">Schedule preset</span>
              <select
                id="detail-job-cron-preset"
                className="text-input"
                value={cronPreset}
                disabled={!editing}
                onChange={(event) => {
                  const nextPreset = event.target.value;
                  onCronPresetChange(nextPreset);
                  const preset = CRON_PRESETS.find((item) => item.id === nextPreset);
                  if (preset?.expression) onScheduleChange(preset.expression);
                }}
              >
                {CRON_PRESETS.map((preset) => (
                  <option key={preset.id} value={preset.id}>{preset.label}</option>
                ))}
              </select>
            </label>
            <label className="field" htmlFor="detail-job-schedule">
              <span className="field-label">Cron schedule</span>
              <input
                id="detail-job-schedule"
                className="text-input"
                value={schedule}
                disabled={!editing}
                onChange={(event) => {
                  onScheduleChange(event.target.value);
                  onCronPresetChange(cronPresetFor(event.target.value));
                }}
                placeholder="0 9 * * *"
                spellCheck={false}
              />
              <span className="field-hint">
                Five fields: minute hour day-of-month month day-of-week. Use <code>*/10</code> for every 10 minutes.
              </span>
            </label>
            <label className="field" htmlFor="detail-job-timezone">
              <span className="field-label">Timezone</span>
              <input
                id="detail-job-timezone"
                className="text-input"
                list="detail-job-timezone-options"
                value={timezone}
                disabled={!editing}
                onChange={(event) => onTimezoneChange(event.target.value)}
                placeholder="UTC"
                spellCheck={false}
              />
              <datalist id="detail-job-timezone-options">
                {COMMON_TIMEZONES.map((zone) => (
                  <option key={zone} value={zone} />
                ))}
              </datalist>
            </label>
          </>
        )}
        {triggerType === 'once' && (
          <label className="field" htmlFor="detail-job-run-at">
            <span className="field-label">Run at</span>
            <input
              id="detail-job-run-at"
              className="text-input"
              type="datetime-local"
              value={runAt}
              disabled={!editing}
              onChange={(event) => onRunAtChange(event.target.value)}
            />
          </label>
        )}
        <SchedulePreviewPanel triggerType={triggerType} config={previewConfig} />
        <label className="field" htmlFor="detail-job-max-concurrency">
          <span className="field-label">Max concurrency</span>
          <input
            id="detail-job-max-concurrency"
            className="text-input"
            type="number"
            min={1}
            value={maxConcurrency}
            disabled={!editing}
            onChange={(event) => onMaxConcurrencyChange(event.target.value)}
            placeholder="Engine default"
          />
        </label>
        <label className="field" htmlFor="detail-job-input">
          <span className="field-label">Input defaults (JSON)</span>
          <textarea
            id="detail-job-input"
            className="text-input code-input"
            value={inputText}
            disabled={!editing}
            onChange={(event) => onInputTextChange(event.target.value)}
            rows={8}
            spellCheck={false}
          />
        </label>
        <div className="job-inline-actions">
          <span className="muted small">Trigger: {triggerLabel(job.triggerType)}</span>
        </div>
      </div>
    </section>
  );
}
