import { formFieldGroups } from '../../formLayout';
import { FormFieldList, type FormFieldsProps } from './shared';

// db.postgres form module: operation + SQL. The postgres credential
// itself rides the shared Credentials section driven by
// def.credentials.
export function PostgresFormFields({ node, def, onConfigChange, onFocusField, effective, onConfigUnset }: FormFieldsProps) {
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
