import { formFieldGroups } from '../../formLayout';
import { FormFieldList, type FormFieldsProps } from './shared';

// flow.subflow form module: the callee workflow id and the wait toggle.
// The callee's declared input contract renders in the shared
// data-contract section.
export function SubflowFormFields({ node, def, onConfigChange, onFocusField, effective, onConfigUnset }: FormFieldsProps) {
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
