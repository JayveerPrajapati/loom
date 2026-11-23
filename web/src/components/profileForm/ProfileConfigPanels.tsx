import { RETRY_FIELDS } from '../../profileForm';
import type {
  CredentialInfo,
  FieldDef,
  Mapping,
  RetryPolicy,
  Script,
} from '../../types';
import { VisibilityChip } from '../assets/VisibilityControl';
import { labelFor } from '../inspectorHelpers';
import { ProfileField, type PaginationState } from './ProfileField';

/** A workflow node that pins this profile (usage-site composition). */
export interface PreviewSite {
  workflowId: string;
  workflowName: string;
  nodeId: string;
  nodeName: string;
  config: Record<string, unknown>;
  profileOverrides?: Record<string, boolean>;
  retry?: RetryPolicy | null;
  inputSchemaRef?: string;
  outputSchemaRef?: string;
}

// The Basics panel (Phase 6 D6-3): identity and governance fields.
export function ProfileBasicsPanel(props: {
  name: string;
  setName: (value: string) => void;
  description: string;
  setDescription: (value: string) => void;
  owner: string;
  setOwner: (value: string) => void;
  tagsText: string;
  setTagsText: (value: string) => void;
  visibility?: string;
  editing: boolean;
}) {
  return (
    <div role="tabpanel" aria-labelledby="profile-tab-identity" data-testid="profile-panel-basics">
      <label className="field" htmlFor="profile-name">
        <span className="field-label">
          Name<span className="required">*</span>
        </span>
        <input
          id="profile-name"
          className="text-input"
          data-testid="profile-name"
          value={props.name}
          onChange={(event) => props.setName(event.target.value)}
          required
        />
      </label>
      <label className="field" htmlFor="profile-description">
        <span className="field-label">Description (optional)</span>
        <input
          id="profile-description"
          className="text-input"
          value={props.description}
          onChange={(event) => props.setDescription(event.target.value)}
        />
      </label>
      <label className="field" htmlFor="profile-owner">
        <span className="field-label">Owner (optional: team or person responsible)</span>
        <input
          id="profile-owner"
          className="text-input"
          placeholder="payments-team"
          value={props.owner}
          onChange={(event) => props.setOwner(event.target.value)}
        />
      </label>
      <label className="field" htmlFor="profile-tags">
        <span className="field-label">Tags (comma-separated, optional)</span>
        <input
          id="profile-tags"
          className="text-input"
          placeholder="payments, prod"
          value={props.tagsText}
          onChange={(event) => props.setTagsText(event.target.value)}
        />
      </label>
      <div className="field" data-testid="profile-identity-visibility">
        <span className="field-label">Visibility</span>
        <div className="visibility-row">
          <VisibilityChip
            visibility={props.editing ? props.visibility : 'private'}
            testId="profile-visibility-chip"
          />
          <span className="field-hint">
            {props.editing
              ? 'Shared profiles are usable by every editor; private profiles only by their creator and admins. Share or unshare from the status bar.'
              : 'New profiles start private. Share from the status bar after the first save.'}
          </span>
        </div>
      </div>
    </div>
  );
}

// The Node-defaults panel (Phase 6 D6-3): catalog fields (typed or raw
// JSON), the bundle script for code types, credential refs, and retry.
export function ProfileDefaultsPanel(props: {  nodeType: string;
  essential: [string, FieldDef][];
  advanced: [string, FieldDef][];
  groupFieldKeys: Set<string>;
  configMode: 'fields' | 'json';
  switchConfigMode: (next: 'fields' | 'json') => void;
  values: Record<string, unknown>;
  pagination: PaginationState;
  setPagination: React.Dispatch<React.SetStateAction<PaginationState>>;
  setValue: (key: string, value: unknown) => void;
  jsonText: string;
  setJsonText: (value: string) => void;
  credSlots: string[];
  credentials: CredentialInfo[];
  credentialRefs: Record<string, string>;
  setCredentialRefs: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  retryInputs: Record<string, string>;
  setRetryInputs: React.Dispatch<React.SetStateAction<Record<string, string>>>;
}) {
  const fieldProps = (key: string, spec: FieldDef) => ({
    key,
    fieldKey: key,
    spec,
    nodeType: props.nodeType,
    values: props.values,
    pagination: props.pagination,
    setPagination: props.setPagination,
    setValue: props.setValue,
  });
  return (
    <div role="tabpanel" aria-labelledby="profile-section-defaults" data-testid="profile-panel-defaults">
      <div className="field-group-head">
        <h3 className="field-heading">Node configuration</h3>
        <div className="mode-toggle" role="group" aria-label="Configuration editor mode">
          <button
            type="button"
            className={`mode-toggle-button ${props.configMode === 'fields' ? 'active' : ''}`}
            aria-pressed={props.configMode === 'fields'}
            onClick={() => props.switchConfigMode('fields')}
          >
            Fields
          </button>
          <button
            type="button"
            className={`mode-toggle-button ${props.configMode === 'json' ? 'active' : ''}`}
            aria-pressed={props.configMode === 'json'}
            onClick={() => props.switchConfigMode('json')}
          >
            JSON
          </button>
        </div>
      </div>

      {props.configMode === 'fields' ? (
        <>
          {props.essential
            .filter(([key]) => !props.groupFieldKeys.has(key))
            .map(([key, spec]) => (
              <ProfileField {...fieldProps(key, spec)} />
            ))}
          {props.advanced.filter(([key]) => !props.groupFieldKeys.has(key)).length > 0 && (
            <details className="advanced-fields">
              <summary>
                Advanced{' '}
                <span className="count-pill">
                  {props.advanced.filter(([key]) => !props.groupFieldKeys.has(key)).length}
                </span>
              </summary>
              {props.advanced
                .filter(([key]) => !props.groupFieldKeys.has(key))
                .map(([key, spec]) => (
                  <ProfileField {...fieldProps(key, spec)} />
                ))}
            </details>
          )}
        </>
      ) : (
        <label className="field" htmlFor="profile-config-json">
          <span className="field-label">Config (JSON)</span>
          <textarea
            id="profile-config-json"
            className="text-input code"
            rows={12}
            spellCheck={false}
            data-testid="profile-config-json"
            value={props.jsonText}
            onChange={(event) => props.setJsonText(event.target.value)}
          />
          <p className="field-hint">
            Raw JSON for the whole config; every key must be a profileable field.
          </p>
        </label>
      )}

      {props.credSlots.length > 0 && (
        <>
          <h3 className="field-heading">Credentials</h3>
          {props.credSlots.map((slot) => (
            <label className="field" key={slot} htmlFor={`profile-cred-${slot}`}>
              <span className="field-label">{slot} credential</span>
              <select
                id={`profile-cred-${slot}`}
                className="text-input"
                data-testid={`profile-cred-${slot}`}
                value={props.credentialRefs[slot] ?? ''}
                onChange={(event) =>
                  props.setCredentialRefs((current) => ({
                    ...current,
                    [slot]: event.target.value,
                  }))
                }
              >
                <option value="">None</option>
                {props.credentials
                  .filter((credential) => credential.type === slot)
                  .map((credential) => (
                    <option key={credential.id} value={credential.id}>
                      {credential.id}
                    </option>
                  ))}
              </select>
            </label>
          ))}
          <p className="field-hint">
            The profile references credentials by id; secret material never lives here.
          </p>
        </>
      )}

      <h3 className="field-heading">Retry policy</h3>
      <p className="field-hint">
        Applies when a node has no retry of its own. Leave everything empty for no retry.
      </p>
      {RETRY_FIELDS.map((field) => (
        <label className="field" key={field.key} htmlFor={`profile-retry-${field.key}`}>
          <span className="field-label">{field.label}</span>
          <input
            id={`profile-retry-${field.key}`}
            className="text-input"
            type="number"
            min={field.min}
            max={field.max}
            data-testid={`profile-retry-${field.key}`}
            value={props.retryInputs[field.key] ?? ''}
            onChange={(event) =>
              props.setRetryInputs((current) => ({
                ...current,
                [field.key]: event.target.value,
              }))
            }
          />
        </label>
      ))}
    </div>
  );
}

// The Data panel (Phase 6 D6-3): http.request parameter groups and the
// bundle mapping refs that fill a node's empty groups.
export function ProfileDataPanel(props: {
  nodeType: string;
  entries: [string, FieldDef][];
  values: Record<string, unknown>;
  pagination: PaginationState;
  setPagination: React.Dispatch<React.SetStateAction<PaginationState>>;
  setValue: (key: string, value: unknown) => void;
}) {
  const fieldProps = (key: string, spec: FieldDef) => ({
    key,
    fieldKey: key,
    spec,
    nodeType: props.nodeType,
    values: props.values,
    pagination: props.pagination,
    setPagination: props.setPagination,
    setValue: props.setValue,
  });
  return (
    <div role="tabpanel" aria-labelledby="profile-section-data" data-testid="profile-panel-data">
      {props.entries.map(([key, spec]) => (
        <ProfileField {...fieldProps(key, spec)} />
      ))}
    </div>
  );
}

// Phase 4 D4-5: all included assets in one place. The visible language
// is "included behavior"; the advanced disclosure explains that it is
// stored as a bundle on the profile version.
export function ProfileIncludedPanel(props: {
  nodeType: string;
  mappingKinds: string[];
  bundleMappings: Record<string, string>;
  setBundleMappings: React.Dispatch<React.SetStateAction<Record<string, string>>>;
  bundleMappingsByKind: Record<string, Mapping[]>;
  bundleSchemas: Record<'input' | 'output', string>;
  setBundleSchemas: React.Dispatch<React.SetStateAction<Record<'input' | 'output', string>>>;
  bundleSchemasList: { id: string; name: string; version: number }[];
  bundleScript: string;
  setBundleScript: (value: string) => void;
  bundleScriptsList: Script[];
}) {
  const scripts = props.bundleScriptsList.filter(
    (script) =>
      script.language === (props.nodeType === 'code.python' ? 'python' : 'javascript'),
  );
  return (
    <div
      role="tabpanel"
      aria-labelledby="profile-section-included"
      data-testid="profile-panel-included"
    >
      <div className="field-group-head">
        <h3 className="field-heading">Included behavior</h3>
      </div>
      <p className="field-hint">
        These assets travel with the profile and fill a node&rsquo;s empty slots at publish — the
        node&rsquo;s own value always wins.
      </p>

      {props.mappingKinds.length > 0 && (
        <section className="inspector-section" data-testid="profile-bundle">
          <div className="field-group-head">
            <h4 className="field-heading">Mappings</h4>
          </div>
          {props.mappingKinds.map((kind) => (
            <label className="field" key={kind} htmlFor={`bundle-mapping-${kind}`}>
              <span className="field-label">{labelFor(kind)} mapping</span>
              <select
                id={`bundle-mapping-${kind}`}
                className="text-input"
                data-testid={`bundle-mapping-${kind}`}
                value={props.bundleMappings[kind] ?? ''}
                onChange={(event) =>
                  props.setBundleMappings((current) => {
                    const next = { ...current };
                    if (event.target.value) next[kind] = event.target.value;
                    else delete next[kind];
                    return next;
                  })
                }
              >
                <option value="">(none)</option>
                {(props.bundleMappingsByKind[kind] ?? []).map((mapping) => (
                  <option key={mapping.id} value={mapping.id}>
                    {mapping.name} (v{mapping.version})
                  </option>
                ))}
              </select>
              <span className="field-hint">
                Applied when a node&rsquo;s own {labelFor(kind).toLowerCase()} parameters are empty.
              </span>
            </label>
          ))}
        </section>
      )}

      <section className="inspector-section" data-testid="profile-bundle-contracts">
        <div className="field-group-head">
          <h4 className="field-heading">Contracts</h4>
        </div>
        {(['input', 'output'] as const).map((slot) => (
          <label className="field" key={slot} htmlFor={`bundle-schema-${slot}`}>
            <span className="field-label">{labelFor(slot)} contract</span>
            <select
              id={`bundle-schema-${slot}`}
              className="text-input"
              data-testid={`bundle-schema-${slot}`}
              value={props.bundleSchemas[slot]}
              onChange={(event) =>
                props.setBundleSchemas((current) => ({ ...current, [slot]: event.target.value }))
              }
            >
              <option value="">(none)</option>
              {props.bundleSchemasList.map((schema) => (
                <option key={schema.id} value={schema.id}>
                  {schema.name} (v{schema.version})
                </option>
              ))}
            </select>
            <span className="field-hint">
              Fills a node&rsquo;s empty {slot} contract slot — the node&rsquo;s own ref wins.
            </span>
          </label>
        ))}
      </section>

      {(props.nodeType === 'code.run' || props.nodeType === 'code.python') && (
        <section className="inspector-section" data-testid="profile-bundle-script">
          <div className="field-group-head">
            <h4 className="field-heading">Script</h4>
          </div>
          <label className="field" htmlFor="bundle-script">
            <span className="field-label">Script</span>
            <select
              id="bundle-script"
              className="text-input"
              data-testid="bundle-script"
              value={props.bundleScript}
              onChange={(event) => props.setBundleScript(event.target.value)}
            >
              <option value="">(none)</option>
              {scripts.map((script) => (
                <option key={script.id} value={script.id}>
                  {script.name} (v{script.version})
                </option>
              ))}
            </select>
            <span className="field-hint">
              Fills a node&rsquo;s empty script slot — the node&rsquo;s own reference or diverging
              source wins.
            </span>
          </label>
        </section>
      )}

      <details className="advanced-fields">
        <summary>How this is stored</summary>
        <p className="field-hint">
          These references are stored as a bundle on the profile version. Publish inlines the
          pinned versions into each node that leaves the slot empty.
        </p>
      </details>
    </div>
  );
}
