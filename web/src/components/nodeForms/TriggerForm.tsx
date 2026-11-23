import { formFieldGroups } from '../../formLayout';
import { CronTriggerFields } from '../InspectorTriggers';
import { FormFieldList, type FormFieldsProps } from './shared';

// Trigger form module (every trigger.* type). Common fields render
// generically; the cron type additionally gets its dedicated
// schedule/timezone editor below them (schedule and timezone are
// excluded from the generic loop by fieldVisibility).
export function TriggerFormFields({ node, def, onConfigChange, onFocusField, effective, onConfigUnset }: FormFieldsProps) {
  const groups = formFieldGroups(node.type, node.config, def);
  const config = node.config ?? {};
  return (
    <>
      <FormFieldList
        groups={groups}
        config={config}
        onConfigChange={onConfigChange}
        onFocusField={onFocusField}
        effective={effective}
        onConfigUnset={onConfigUnset}
        only="common"
      />
      {node.type === 'trigger.cron' && (
        <CronTriggerFields
          schedule={String(config.schedule ?? '')}
          timezone={String(config.timezone ?? '')}
          onScheduleChange={(value) => onConfigChange('schedule', value)}
          onTimezoneChange={(value) => onConfigChange('timezone', value)}
        />
      )}
    </>
  );
}
