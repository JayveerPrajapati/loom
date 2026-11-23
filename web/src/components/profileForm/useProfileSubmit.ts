import { useState, type FormEvent } from 'react';
import { api } from '../../api';
import {
  RETRY_FIELDS,
  mandatoryError,
  paginationToObject,
  profilePayload,
  stripUnset,
  validateProfileConfig,
} from '../../profileForm';
import type { ConfigProfile, NodeDef } from '../../types';
import type { PaginationState } from './ProfileField';

// Submit + config-composition cluster (Phase 6 D6-3): what the form
// currently represents (currentConfig / bundleForPayload /
// retryFromInputsSafe — also used by the preview), and the save path
// with mandatory/field validation, stale-save 409 recovery, and the
// Phase 5 test-fixture parse gate.
export function useProfileSubmit(args: {
  nodeType: string;
  def: NodeDef | undefined;
  editing: boolean;
  profile: ConfigProfile | null;
  typePath: string;
  name: string;
  description: string;
  owner: string;
  tagsText: string;
  configMode: 'fields' | 'json';
  jsonText: string;
  values: Record<string, unknown>;
  pagination: PaginationState;
  retryInputs: Record<string, string>;
  credentialRefs: Record<string, string>;
  bundleMappings: Record<string, string>;
  bundleSchemas: Record<'input' | 'output', string>;
  bundleScript: string;
  testInput: string;
  onNavigate: (path: string) => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onProfilesChanged?: () => void;
}) {
  const {
    nodeType,
    def,
    editing,
    profile,
    typePath,
    name,
    description,
    owner,
    tagsText,
    configMode,
    jsonText,
    values,
    pagination,
    retryInputs,
    credentialRefs,
    bundleMappings,
    bundleSchemas,
    bundleScript,
    testInput,
    onNavigate,
    onNotify,
    onProfilesChanged,
  } = args;

  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [stale, setStale] = useState(false);

  const clearError = () => setError('');
  const resetErrors = () => {
    setError('');
    setStale(false);
  };

  // Phase 1 P0-1: the bundle payload — only refs that are actually set.
  const bundleForPayload = (): ConfigProfile['bundle'] => {
    const mappings = Object.fromEntries(Object.entries(bundleMappings).filter(([, id]) => id));
    const schemas = Object.fromEntries(
      (['input', 'output'] as const)
        .filter((slot) => bundleSchemas[slot])
        .map((slot) => [slot, bundleSchemas[slot]]),
    );
    const script = (nodeType === 'code.run' || nodeType === 'code.python') && bundleScript
      ? bundleScript
      : '';
    return Object.keys(mappings).length > 0 || Object.keys(schemas).length > 0 || script
      ? { mappings, schemas, ...(script ? { script } : {}) }
      : undefined;
  };

  /** The config object the form currently represents. */
  const currentConfig = (): Record<string, unknown> => {
    if (configMode === 'json') {
      try {
        const parsed = JSON.parse(jsonText) as unknown;
        if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') return {};
        return parsed as Record<string, unknown>;
      } catch {
        return {};
      }
    }
    const config = stripUnset(values);
    const pag = paginationToObject(pagination.mode, pagination.strings, pagination.numbers);
    if (pag) {
      config.pagination = pag;
      if (pag.mode === 'offset' && !pag.limit) {
        // offset mode requires a positive limit; surfaced as a validation error too.
      }
    } else {
      delete config.pagination;
    }
    return config;
  };

  const retryFromInputsSafe = () => {
    const retry: Record<string, number> = {};
    for (const field of RETRY_FIELDS) {
      const text = (retryInputs[field.key] ?? '').trim();
      if (text === '') continue;
      const num = Number(text);
      if (Number.isFinite(num)) retry[field.key] = num;
    }
    return Object.keys(retry).length > 0 ? retry : null;
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    setError('');
    setStale(false);
    if (!name.trim()) {
      setError('Name is required.');
      return;
    }
    let config: Record<string, unknown>;
    if (configMode === 'json') {
      try {
        const parsed = JSON.parse(jsonText) as unknown;
        if (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object') {
          throw new Error('object');
        }
        config = parsed as Record<string, unknown>;
      } catch {
        setError('Config must be a valid JSON object.');
        return;
      }
    } else {
      config = currentConfig();
    }
    const retry = retryFromInputsSafe();
    const bundle = bundleForPayload();
    // Phase 5: the fixture must parse before it can save.
    let fixture: unknown;
    if (testInput.trim() !== '') {
      try {
        fixture = JSON.parse(testInput);
      } catch (issue) {
        setError(`Test fixture is not valid JSON: ${issue instanceof Error ? issue.message : String(issue)}`);
        return;
      }
    }
    const mandatory = mandatoryError(config, retry, bundle);
    if (mandatory) {
      setError(mandatory);
      return;
    }
    const fieldErrors = def ? validateProfileConfig(def, config) : [];
    if (fieldErrors.length > 0) {
      setError(fieldErrors.join(' '));
      return;
    }
    setBusy(true);
    try {
      if (editing && profile) {
        const updated = await api.updateConfigProfile(profile.id, {
          ...profilePayload({
            name,
            nodeType,
            description,
            owner,
            tags: tagsText,
            config,
            credentialRefs,
            retry,
            bundle: bundleForPayload(),
            testInput: fixture,
          }),
          version: profile.version,
        });
        onNotify?.(`Saved ${updated.name} as v${updated.version}.`);
        onProfilesChanged?.();
        onNavigate(typePath);
      } else {
        const created = await api.createConfigProfile({
          ...profilePayload({
            name,
            nodeType,
            description,
            owner,
            tags: tagsText,
            config,
            credentialRefs,
            retry,
            bundle: bundleForPayload(),
            testInput: fixture,
          }),
        });
        onNotify?.(`Created ${created.name}.`);
        onProfilesChanged?.();
        onNavigate(`${typePath}/${encodeURIComponent(created.id)}/edit`);
      }
    } catch (reason) {
      if (reason instanceof Error && reason.message.includes('concurrently')) {
        setStale(true);
        setError('This profile changed while you were editing. Reload to pick up the latest version.');
      } else {
        setError(reason instanceof Error ? reason.message : String(reason));
      }
    } finally {
      setBusy(false);
    }
  };

  return {
    save,
    busy,
    setBusy,
    error,
    clearError,
    resetErrors,
    stale,
    currentConfig,
    retryFromInputsSafe,
    bundleForPayload,
  };
}
