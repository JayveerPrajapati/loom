import { createContext, useContext, type ReactNode } from 'react';
import type { ExpressionCandidate } from '../../autocomplete';
import type { Mapping, NodeDef, Script, WorkflowNode } from '../../types';
import type { NodeEffective } from '../../effectiveNode';
import { ConfigField } from '../InspectorFields';
import { OriginBadge } from '../assets/OriginBadge';
import type { FormFieldGroups } from '../../formLayout';

// Shared plumbing for the per-node-type form modules (Profile-Centric UX
// Phase 3, D3-4): one props shape for Basics-area forms, one for
// Data-area forms, and the field-list renderer both use.

/** Node issue text keyed by config field (Apple-Grade Phase 3 D3-4).
 * Provided once by the Inspector so every form module renders inline
 * validation without threading a prop through each module. */
export const FieldErrorsContext = createContext<
  ((key: string) => string | undefined) | undefined
>(undefined);

export interface FormFieldsProps {
  node: WorkflowNode;
  def?: NodeDef;
  onConfigChange: (key: string, value: unknown) => void;
  onFocusField: (key: string) => void;
  /** Phase 4 slot: CodeForm/PythonForm render it above the source
   * field. The Inspector passes the script picker (D3-8/D4-1). */
  scriptRefSlot?: ReactNode;
  /** Item mappings, for the friendly data.map mapping picker (D3-6).
   * Only MapForm consumes this. */
  mappings?: Mapping[];
  /** Scripts of this form's language, for the scriptRef picker's
   * options (Phase 4 D4-1). Only the code forms consume this. */
  scripts?: Script[];
  /** Effective values under the pinned profile (UI-audit B1): fields
   * display inherited values with their origin; edits still write the
   * node's own config. */
  effective?: NodeEffective | null;
  /** Removes a node-level config key, restoring the inherited value. */
  onConfigUnset?: (key: string) => void;
}

export interface FormDataProps {
  node: WorkflowNode;
  onConfigChange: (key: string, value: unknown) => void;
  candidates: ExpressionCandidate[];
  mappings: Mapping[];
  onMappingsChanged: () => void;
  onOpenHub: (nodeType: string) => void;
  /** Effective values under the pinned profile (UI-audit B2). */
  effective?: NodeEffective | null;
}

export function FormFieldList({
  groups,
  config,
  onConfigChange,
  onFocusField,
  only,
  effective,
  onConfigUnset,
}: {
  groups: FormFieldGroups;
  config: Record<string, unknown>;
  onConfigChange: (key: string, value: unknown) => void;
  onFocusField: (key: string) => void;
  only: 'common' | 'advanced';
  effective?: NodeEffective | null;
  onConfigUnset?: (key: string) => void;
}) {
  const entries = only === 'common' ? groups.common : groups.advanced;
  const fieldErrors = useContext(FieldErrorsContext);
  return (
    <>
      {entries.map(([key, spec]) => {
        const info = effective?.field(key) ?? null;
        const fallback = spec.default;
        return (
          <ConfigField
            key={key}
            fieldKey={key}
            spec={spec}
            value={info ? info.value : config[key] ?? fallback}
            onChange={(value) => onConfigChange(key, value)}
            onFocus={() => onFocusField(key)}
            origin={info ? info.origin : undefined}
            overridden={info?.overridden === true}
            profileValue={
              info?.overridden && effective ? effective.inheritedValue(key) : undefined
            }
            issue={fieldErrors?.(key)}
            onReset={
              onConfigUnset && info?.overridden && effective?.canReset(key) === true
                ? () => onConfigUnset(key)
                : undefined
            }
          />
        );
      })}
    </>
  );
}

/** The language a code node's scripts must carry (Phase 4 D4-1). */
export function codeLanguageFor(nodeType: string): string {
  return nodeType === 'code.python' ? 'python' : 'javascript';
}

/** True when the node has no authored source for its referenced
 * script: the stored source is empty (the script owns it) or matches
 * the script's current source (publish pinned it there). Either way
 * the form hides the textarea and keeps the script binding visible;
 * a diverging source is authored and stays editable. */
export function scriptSourceInlined(
  node: WorkflowNode,
  scripts: Script[] | undefined,
): boolean {
  const ref = typeof node.config?.scriptRef === 'string' ? node.config.scriptRef : '';
  if (!ref) return false;
  const source = String(node.config?.source ?? '');
  if (source === '') return true;
  const script = (scripts ?? []).find((s) => s.id === ref);
  return !!script && source === script.source;
}

/** The script picker for CodeForm/PythonForm (Phase 4 D4-1): a friendly
 * select over the language's scripts, rendered through the scriptRefSlot
 * the forms have carried since Phase 3 (D3-8). Keeps the
 * field-scriptRef anchor id so the freeze spec stays stable. When the
 * profile bundle fills the slot, the picker shows the inherited value
 * with its origin badge (UI-audit B1). */
export function ScriptRefPicker({
  node,
  scripts,
  onConfigChange,
  effective,
}: {
  node: WorkflowNode;
  scripts: Script[] | undefined;
  onConfigChange: (key: string, value: unknown) => void;
  effective?: { field(key: string): { value: unknown; origin: string } | null } | null;
}) {
  const language = codeLanguageFor(node.type);
  const ref = typeof node.config?.scriptRef === 'string' ? node.config.scriptRef : '';
  const inherited = effective?.field('scriptRef') ?? null;
  const options = (scripts ?? []).filter((script) => script.language === language);
  return (
    <label className="field" htmlFor="field-scriptRef">
      <span className="field-label">
        Script
        {inherited && inherited.origin !== 'node' && (
          <OriginBadge
            state={inherited.origin === 'bundle' ? 'bundle' : 'inherited'}
            fieldKey="scriptRef"
          />
        )}
      </span>
      <select
        id="field-scriptRef"
        className="text-input"
        data-testid="script-ref-picker"
        value={typeof inherited?.value === 'string' ? inherited.value : ref}
        onChange={(event) => onConfigChange('scriptRef', event.target.value)}
      >
        <option value="">(local source below)</option>
        {options.map((script) => (
          <option key={script.id} value={script.id}>
            {script.name} (v{script.version})
          </option>
        ))}
      </select>
      <span className="field-hint">
        Publish inlines the pinned version{ref || inherited?.value ? '' : '; a local source is required without one'}.
      </span>
    </label>
  );
}
