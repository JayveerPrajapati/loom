import { formFieldGroups } from '../../formLayout';
import { FormFieldList, type FormFieldsProps } from './shared';

// The fallback form: renders whatever common catalog fields the node
// type declares, in declaration order. Used for every node type without
// a dedicated form module.
export function GenericFormFields({ node, def, onConfigChange, onFocusField, effective, onConfigUnset }: FormFieldsProps) {
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
