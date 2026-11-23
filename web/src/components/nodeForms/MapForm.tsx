import { useContext } from 'react';
import { labelFor } from '../inspectorHelpers';
import { formFieldGroups } from '../../formLayout';
import { OriginBadge, originState } from '../assets/OriginBadge';
import { FieldErrorsContext, FormFieldList, type FormFieldsProps } from './shared';

// data.map form module: a friendly mapping picker (D3-6 — the raw
// mappingRef string is replaced by a select over the item mappings,
// keeping the `field-mappingRef` id so the freeze spec keeps capturing
// it) plus per-target overrides and output type. The referenced
// mapping's row preview lives in the shared data-contract section.
export function MapFormFields({
  node,
  def,
  onConfigChange,
  onFocusField,
  mappings,
  effective,
  onConfigUnset,
}: FormFieldsProps) {
  const groups = formFieldGroups(node.type, node.config, def);
  const config = node.config ?? {};
  const itemMappings = (mappings ?? []).filter((m) => (m.kind ?? 'item') === 'item');
  const spec = def?.config?.mappingRef;
  const mappingRefInfo = effective?.field('mappingRef') ?? null;
  const mappingRef = typeof config.mappingRef === 'string' ? config.mappingRef : '';
  const fieldErrors = useContext(FieldErrorsContext);
  const mappingIssue = fieldErrors?.('mappingRef');
  return (
    <>
      <label className="field" htmlFor="field-mappingRef">
        <span className="field-label">
          {spec?.label || labelFor('mappingRef')}
          {spec?.required && <span className="required">*</span>}
          <OriginBadge
            state={originState(mappingRefInfo?.origin, mappingRefInfo?.overridden === true)}
            fieldKey="mappingRef"
          />
        </span>
        <select
          id="field-mappingRef"
          className="text-input"
          value={typeof mappingRefInfo?.value === 'string' ? mappingRefInfo.value : mappingRef}
          aria-invalid={mappingIssue ? true : undefined}
          onFocus={() => onFocusField('mappingRef')}
          onChange={(event) => onConfigChange('mappingRef', event.target.value)}
        >
          {!mappingRefInfo?.value && <option value="">Choose…</option>}
          {itemMappings.map((mapping) => (
            <option key={mapping.id} value={mapping.id}>
              {mapping.name} (v{mapping.version})
            </option>
          ))}
        </select>
        {mappingIssue && (
          <p className="field-error" role="alert" data-testid="field-error-mappingRef">
            {mappingIssue}
          </p>
        )}
        {spec?.description && <p className="field-hint">{spec.description}</p>}
      </label>
      <FormFieldList
        groups={{ ...groups, common: groups.common.filter(([key]) => key !== 'mappingRef') }}
        config={config}
        onConfigChange={onConfigChange}
        onFocusField={onFocusField}
        effective={effective}
        onConfigUnset={onConfigUnset}
        only="common"
      />
    </>
  );
}
