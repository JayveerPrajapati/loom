import { bodyTypeFor } from '../../fieldVisibility';
import { formFieldGroups } from '../../formLayout';
import { HTTPParamGroups } from '../HTTPParamGroups';
import { FormFieldList, type FormDataProps, type FormFieldsProps } from './shared';

// http.request form module (Profile-Centric UX Phase 3). Basics renders
// the common fields (method/url/auth/bodyType); the type-specific Data
// content is HTTPParamGroups — the friendly row editors that own the
// raw query/headers/pathParams/body config and the mapping attach
// pickers.
export function HttpFormFields({
  node,
  def,
  onConfigChange,
  onFocusField,
  effective,
  onConfigUnset,
}: FormFieldsProps) {
  const groups = formFieldGroups(node.type, node.config, def);
  return (
    <FormFieldList
      groups={groups}
      config={node.config ?? {}}
      onConfigChange={onConfigChange}
      onFocusField={onFocusField}
      effective={effective}
      onConfigUnset={onConfigUnset}
      only="common"
    />
  );
}

export function HttpFormData({
  node,
  onConfigChange,
  candidates,
  mappings,
  onMappingsChanged,
  onOpenHub,
  effective,
}: FormDataProps) {
  return (
    <HTTPParamGroups
      config={node.config ?? {}}
      bodyType={bodyTypeFor(node.type, node.config)}
      onConfigChange={onConfigChange}
      candidates={candidates}
      mappings={mappings}
      onMappingsChanged={onMappingsChanged}
      onOpenHub={onOpenHub}
      effective={effective}
    />
  );
}
