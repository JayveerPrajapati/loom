import { formFieldGroups } from '../../formLayout';
import { FormFieldList, scriptSourceInlined, type FormFieldsProps } from './shared';

// code.run form module. The scriptRefSlot (D3-8) carries the Phase 4
// script picker (D4-1). A source that publish inlined from the
// referenced script hides so the binding stays the visible truth; a
// diverging source is authored and stays editable (the escape hatch).
export function CodeFormFields({
  node,
  def,
  onConfigChange,
  onFocusField,
  scriptRefSlot,
  scripts,
  effective,
  onConfigUnset,
}: FormFieldsProps) {
  const groups = formFieldGroups(node.type, node.config, def);
  const inlined = scriptSourceInlined(node, scripts);
  const common = groups.common.filter(
    ([key]) => key !== 'scriptRef' && !(key === 'source' && inlined),
  );
  return (
    <>
      {scriptRefSlot}
      <FormFieldList
        groups={{ ...groups, common }}
        config={node.config ?? {}}
        onConfigChange={onConfigChange}
        onFocusField={onFocusField}
        effective={effective}
        onConfigUnset={onConfigUnset}
        only="common"
      />
      {inlined && (
        <p className="field-hint">
          Source is pinned from the selected script at publish. Pick a different version on the
          Scripts page, or choose (local source below) to author it here.
        </p>
      )}
    </>
  );
}
