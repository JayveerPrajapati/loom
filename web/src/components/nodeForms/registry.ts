import type { ComponentType } from 'react';
import { CodeFormFields } from './CodeForm';
import { GenericFormFields } from './GenericForm';
import { HttpFormData, HttpFormFields } from './HttpForm';
import { MapFormFields } from './MapForm';
import { PostgresFormFields } from './PostgresForm';
import { PythonFormFields } from './PythonForm';
import { SubflowFormFields } from './SubflowForm';
import { TriggerFormFields } from './TriggerForm';
import type { FormDataProps, FormFieldsProps } from './shared';

// Node-type → form module registry (Profile-Centric UX Phase 3). The
// Basics area dispatches through formFieldsFor; the Data area renders
// type-specific content through formDataFor (null = the shared
// data-contract section is the whole Data tab). A node type without a
// dedicated module falls back to the generic field loop.

const FIELDS: Record<string, ComponentType<FormFieldsProps>> = {
  'http.request': HttpFormFields,
  'db.postgres': PostgresFormFields,
  'code.run': CodeFormFields,
  'code.python': PythonFormFields,
  'data.map': MapFormFields,
  'flow.subflow': SubflowFormFields,
};

const DATA: Record<string, ComponentType<FormDataProps>> = {
  'http.request': HttpFormData,
};

export function formFieldsFor(nodeType: string): ComponentType<FormFieldsProps> {
  if (nodeType.startsWith('trigger.')) return TriggerFormFields;
  return FIELDS[nodeType] ?? GenericFormFields;
}

export function formDataFor(nodeType: string): ComponentType<FormDataProps> | null {
  return DATA[nodeType] ?? null;
}
