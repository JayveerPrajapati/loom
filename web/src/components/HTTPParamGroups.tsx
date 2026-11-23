import { useState } from 'react';
import { api, ApiError } from '../api';
import { FieldGroupEditor } from './FieldGroupEditor';
import { OriginBadge } from './assets/OriginBadge';
import {
  compileMappingRows,
  paramRowsToMappingRows,
  toParamRows,
} from '../paramRows';
import type { ExpressionCandidate } from '../autocomplete';
import type { Mapping } from '../types';
import type { NodeEffective } from '../effectiveNode';

// HTTP parameter groups (Configuration Hub v3 plan D2/D4/D5): the
// Inspector section that replaces the raw query/headers/pathParams/body
// inputs. Each group is a shared FieldGroupEditor plus a mapping picker
// (rows resolve under the node's own at publish), save-as-mapping, and an
// open-in-hub link. Body group only appears for JSON bodies.

interface GroupSpec {
  target: 'body' | 'query' | 'headers' | 'pathParams';
  fields: 'bodyFields' | 'queryFields' | 'headerFields' | 'pathFields';
  label: string;
  kind: string;
}

const GROUPS: GroupSpec[] = [
  { target: 'query', fields: 'queryFields', label: 'Query parameters', kind: 'query' },
  { target: 'headers', fields: 'headerFields', label: 'Headers', kind: 'headers' },
  { target: 'pathParams', fields: 'pathFields', label: 'Path parameters', kind: 'pathParams' },
];

// How the group reaches the request. Path values are placeholders in the
// URL, never appended; query values are appended automatically. The hint
// shows under the editor so the placeholder rule is visible while editing.
const GROUP_HINTS: Record<GroupSpec['target'], string> = {
  body: 'Typed rows compile into the JSON body at publish.',
  query: 'Appended to the URL query string automatically — e.g. postId = 1 becomes ?postId=1.',
  headers: 'Sent as request headers; explicit values win over credential headers.',
  pathParams:
    'Replaces {name} or :name in the URL — add the placeholder first, e.g. /posts/{postId}. Path values are never appended automatically.',
};

interface Props {
  config: Record<string, unknown>;
  bodyType: string;
  onConfigChange: (key: string, value: unknown) => void;
  candidates: ExpressionCandidate[];
  mappings: Mapping[];
  onMappingsChanged: () => void;
  onOpenHub: (nodeType: string) => void;
  /** Effective values under the pinned profile (UI-audit B2): rows
   * display the merged set with per-row origins; edits still write
   * the node's own row array. */
  effective?: NodeEffective | null;
}

export function HTTPParamGroups({
  config,
  bodyType,
  onConfigChange,
  candidates,
  mappings,
  onMappingsChanged,
  onOpenHub,
  effective,
}: Props) {
  const [savingGroup, setSavingGroup] = useState<string | null>(null);
  const [saveName, setSaveName] = useState('');
  const [notice, setNotice] = useState<{ ok: boolean; message: string } | null>(null);

  const groups = bodyType === 'json'
    ? [{ target: 'body', fields: 'bodyFields', label: 'JSON body fields', kind: 'body' } as GroupSpec, ...GROUPS]
    : GROUPS;

  const mappingRefs = (config.mappingRefs as Record<string, unknown> | undefined) ?? {};
  const setMappingRef = (group: GroupSpec, id: string | undefined) => {
    const next = { ...mappingRefs };
    if (id) next[group.target] = id;
    else delete next[group.target];
    onConfigChange('mappingRefs', Object.keys(next).length > 0 ? next : undefined);
  };

  const saveAsMapping = (group: GroupSpec) => {
    const name = saveName.trim();
    if (!name) return;
    const rows = toParamRows(config[group.fields]);
    if (rows.length === 0 || rows.some((row) => !row.name.trim())) {
      setNotice({ ok: false, message: 'Add at least one named row before saving a mapping.' });
      return;
    }
    api
      .createMapping({ name, kind: group.kind, rows: paramRowsToMappingRows(rows) })
      .then((created) => {
        setMappingRef(group, created.id);
        setSavingGroup(null);
        setSaveName('');
        setNotice({ ok: true, message: `Saved mapping “${created.name}” (v${created.version}) and attached it to ${group.label.toLowerCase()}.` });
        onMappingsChanged();
      })
      .catch((error: ApiError) => setNotice({ ok: false, message: error.message }));
  };

  return (
    <section className="inspector-section" data-testid="http-param-groups">
      <div className="section-head-with-hint">
        <h3>Parameters</h3>
        <button
          type="button"
          className="text-button"
          data-testid="open-in-hub"
          onClick={() => onOpenHub('http.request')}
        >
          Open in hub
        </button>
      </div>

      {notice && (
        <div className={`banner ${notice.ok ? 'notice' : 'error'}`} role="status" data-testid="param-group-notice">
          <code>{notice.message}</code>
        </div>
      )}

      {groups.map((group) => {
        // The mapping-attach select and the row editor work from the
        // effective view when a profile is pinned: bundle-filled refs
        // and merged rows show, with per-row origin badges. Edits
        // write the node's own config (override semantics).
        const refInfo = effective?.field('mappingRefs') ?? null;
        const effectiveRefs =
          (refInfo?.value as Record<string, unknown> | undefined) ??
          ((config.mappingRefs as Record<string, unknown> | undefined) ?? {});
        const ref = typeof effectiveRefs[group.target] === 'string' ? (effectiveRefs[group.target] as string) : '';
        const refOrigin =
          (config.mappingRefs as Record<string, unknown> | undefined)?.[group.target] !== undefined
            ? 'node'
            : refInfo?.origin === 'bundle'
              ? 'bundle'
              : null;
        const selected = mappings.find((m) => m.id === ref && (m.kind ?? 'item') === group.kind);
        const kindMappings = mappings.filter((m) => (m.kind ?? 'item') === group.kind);
        const rows = toParamRows(
          effective ? effective.groupValue(group.fields) : config[group.fields],
        );
        return (
          <div className="param-group" key={group.target} data-testid={`param-group-${group.target}`}>
            <FieldGroupEditor
              label={group.label}
              hint={GROUP_HINTS[group.target]}
              rows={rows}
              rowOrigin={
                effective
                  ? (name) => effective.rowOrigin(group.fields, name)
                  : undefined
              }
              onRowsChange={(next) =>
                onConfigChange(group.fields, next.length > 0 ? next : undefined)
              }
              jsonValue={config[group.target] ?? {}}
              onJsonChange={(value) => onConfigChange(group.target, value)}
              candidates={candidates}
              testIdPrefix={`param-${group.target}`}
            />
            <div className="mapping-attach">
              <label className="field" htmlFor={`mapping-ref-${group.target}`}>
                <span className="field-label">
                  Attach {group.kind} mapping
                  {refOrigin && refOrigin !== 'node' && (
                    <OriginBadge
                      state={refOrigin === 'bundle' ? 'bundle' : 'inherited'}
                      fieldKey={`mappingRefs-${group.target}`}
                    />
                  )}
                </span>
                <select
                  id={`mapping-ref-${group.target}`}
                  className="text-input"
                  value={ref}
                  onChange={(event) => setMappingRef(group, event.target.value || undefined)}
                >
                  <option value="">None (inline rows only)</option>
                  {kindMappings.map((mapping) => (
                    <option key={mapping.id} value={mapping.id}>
                      {mapping.name} (v{mapping.version})
                    </option>
                  ))}
                </select>
                <span className="field-hint">
                  Mapping rows resolve at publish under this node&rsquo;s own rows.
                </span>
              </label>
              {selected && (
                <div className="profile-preview" data-testid={`mapping-preview-${group.target}`}>
                  <p className="field-label">
                    {selected.name} · v{selected.version}
                  </p>
                  {Object.entries(compileMappingRows(group.target, selected.rows)).map(([name, value]) => (
                    <div className="profile-preview-row" key={name}>
                      <code>{name}</code>
                      <span>{typeof value === 'string' ? value : JSON.stringify(value)}</span>
                    </div>
                  ))}
                </div>
              )}
              {savingGroup === group.target ? (
                <div className="save-as-mapping" data-testid={`save-as-mapping-${group.target}`}>
                  <input
                    className="text-input"
                    placeholder="Mapping name"
                    aria-label="New mapping name"
                    value={saveName}
                    autoFocus
                    onChange={(event) => setSaveName(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key === 'Enter') saveAsMapping(group);
                      if (event.key === 'Escape') setSavingGroup(null);
                    }}
                  />
                  <button type="button" className="button primary" onClick={() => saveAsMapping(group)}>
                    Save
                  </button>
                  <button type="button" className="text-button" onClick={() => setSavingGroup(null)}>
                    Cancel
                  </button>
                </div>
              ) : (
                rows.length > 0 && (
                  <button
                    type="button"
                    className="text-button"
                    data-testid={`save-as-mapping-trigger-${group.target}`}
                    onClick={() => {
                      setSavingGroup(group.target);
                      setSaveName('');
                      setNotice(null);
                    }}
                  >
                    Save as mapping
                  </button>
                )
              )}
            </div>
          </div>
        );
      })}
    </section>
  );
}
