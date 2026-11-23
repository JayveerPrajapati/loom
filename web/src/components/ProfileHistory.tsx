import { AssetLoadingState } from './assets/AssetState';
import { ActionError } from './assets/ActionError';
import { useEffect, useMemo, useState } from 'react';

import { api } from '../api';
import type {
  ConfigProfile,
  ConfigProfileVersion,
  DataSchema,
  Mapping,
  SchemaField,
} from '../types';
import { useDialogFocus } from '../useDialogFocus';

interface Props {
  profile: ConfigProfile | null;
  onClose: () => void;
}

export interface DiffRow {
  key: string;
  before: string;
  after: string;
  change: 'added' | 'removed' | 'changed';
}

export function ProfileDiffTable({ rows }: { rows: DiffRow[] }) {
  if (rows.length === 0) return <p className="muted small">No content changes.</p>;
  return (
    <table className="diff-table">
      <thead>
        <tr>
          <th scope="col">Field</th>
          <th scope="col">Before</th>
          <th scope="col">After</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => (
          <tr key={row.key} className={`diff-${row.change}`}>
            <th scope="row">{row.key}</th>
            <td>{row.before || '—'}</td>
            <td>{row.after || '—'}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const PARAM_GROUP_KEYS = new Set(['bodyFields', 'queryFields', 'headerFields', 'pathFields']);

interface ParamRowShape {
  name?: string;
  mode?: string;
  type?: string;
  value?: unknown;
}

function paramRowSummary(row: unknown): string {
  if (row === null || typeof row !== 'object') return JSON.stringify(row ?? null);
  const shape = row as ParamRowShape;
  return JSON.stringify({ mode: shape.mode, type: shape.type, value: shape.value });
}

function rowsByName(raw: unknown): Map<string, unknown> {
  const out = new Map<string, unknown>();
  if (!Array.isArray(raw)) return out;
  for (const row of raw) {
    if (row !== null && typeof row === 'object' && typeof (row as ParamRowShape).name === 'string') {
      out.set((row as ParamRowShape).name as string, row);
    }
  }
  return out;
}

// diffProfileDocs compares two profile versions' content at the key level:
// config entries (parameter-group arrays expand to one row per row name),
// credential references, retry policy, bundle references and metadata all
// appear as rows so a reviewer sees exactly what a version changed.
// Shared by the edit page's History tab and the list page's history dialog
// (UX Phase 2 D2-4).
export function diffProfileDocs(before: ConfigProfile | null, after: ConfigProfile): DiffRow[] {
  const rows: DiffRow[] = [];
  const beforeConfig = before?.config ?? {};
  const keys = new Set([...Object.keys(beforeConfig), ...Object.keys(after.config)]);
  for (const key of keys) {
    const had = key in beforeConfig;
    const has = key in after.config;
    // Parameter-group arrays (v4.1 E4) diff per row name — one changed row
    // reads as one diff row instead of an unreadable whole-array blob.
    if (PARAM_GROUP_KEYS.has(key)) {
      const beforeRows = rowsByName(beforeConfig[key]);
      const afterRows = rowsByName(after.config[key]);
      const names = new Set([...beforeRows.keys(), ...afterRows.keys()]);
      for (const name of names) {
        const rowKey = `config.${key}.${name}`;
        const a = beforeRows.has(name) ? paramRowSummary(beforeRows.get(name)) : '';
        const b = afterRows.has(name) ? paramRowSummary(afterRows.get(name)) : '';
        if (!beforeRows.has(name)) rows.push({ key: rowKey, before: '', after: b, change: 'added' });
        else if (!afterRows.has(name))
          rows.push({ key: rowKey, before: a, after: '', change: 'removed' });
        else if (a !== b) rows.push({ key: rowKey, before: a, after: b, change: 'changed' });
      }
      // Rows that could not be parsed by name fall back to the blob diff.
      if (beforeRows.size === 0 && afterRows.size === 0) {
        const blobA = JSON.stringify(beforeConfig[key]);
        const blobB = JSON.stringify(after.config[key]);
        if (blobA !== blobB) {
          if (!had) rows.push({ key: `config.${key}`, before: '', after: blobB, change: 'added' });
          else if (!has)
            rows.push({ key: `config.${key}`, before: blobA, after: '', change: 'removed' });
          else rows.push({ key: `config.${key}`, before: blobA, after: blobB, change: 'changed' });
        }
      }
      continue;
    }
    const a = JSON.stringify(beforeConfig[key]);
    const b = JSON.stringify(after.config[key]);
    if (!had && has) rows.push({ key: `config.${key}`, before: '', after: b, change: 'added' });
    else if (had && !has) rows.push({ key: `config.${key}`, before: a, after: '', change: 'removed' });
    else if (a !== b) rows.push({ key: `config.${key}`, before: a, after: b, change: 'changed' });
  }
  const beforeCred = before?.credentialRefs ?? {};
  const afterCred = after.credentialRefs ?? {};
  const credKeys = new Set([...Object.keys(beforeCred), ...Object.keys(afterCred)]);
  for (const key of credKeys) {
    const a = beforeCred[key] ?? '';
    const b = afterCred[key] ?? '';
    if (!(key in beforeCred)) rows.push({ key: `credentialRefs.${key}`, before: '', after: b, change: 'added' });
    else if (!(key in afterCred)) rows.push({ key: `credentialRefs.${key}`, before: a, after: '', change: 'removed' });
    else if (a !== b) rows.push({ key: `credentialRefs.${key}`, before: a, after: b, change: 'changed' });
  }
  const aRetry = JSON.stringify(before?.retry ?? null);
  const bRetry = JSON.stringify(after.retry ?? null);
  if (aRetry !== bRetry) {
    rows.push({ key: 'retry', before: before?.retry ? aRetry : '', after: after.retry ? bRetry : '', change: 'changed' });
  }
  // Bundle references (Phase 1 P0-1) diff per kind/slot (UX Phase 2 D2-4):
  // one changed ref reads as one row. Old versions without a bundle diff
  // against one that has it as "added" rows — the tolerated legacy shape.
  const beforeMappings = before?.bundle?.mappings ?? {};
  const afterMappings = after.bundle?.mappings ?? {};
  for (const kind of new Set([...Object.keys(beforeMappings), ...Object.keys(afterMappings)])) {
    const a = beforeMappings[kind] ?? '';
    const b = afterMappings[kind] ?? '';
    if (a === b) continue;
    const had = kind in beforeMappings;
    const has = kind in afterMappings;
    if (!had && has) rows.push({ key: `bundle.mappings.${kind}`, before: '', after: b, change: 'added' });
    else if (had && !has)
      rows.push({ key: `bundle.mappings.${kind}`, before: a, after: '', change: 'removed' });
    else rows.push({ key: `bundle.mappings.${kind}`, before: a, after: b, change: 'changed' });
  }
  const beforeSchemas = before?.bundle?.schemas ?? {};
  const afterSchemas = after.bundle?.schemas ?? {};
  for (const slot of new Set([...Object.keys(beforeSchemas), ...Object.keys(afterSchemas)])) {
    const a = beforeSchemas[slot] ?? '';
    const b = afterSchemas[slot] ?? '';
    if (a === b) continue;
    const had = slot in beforeSchemas;
    const has = slot in afterSchemas;
    if (!had && has) rows.push({ key: `bundle.schemas.${slot}`, before: '', after: b, change: 'added' });
    else if (had && !has)
      rows.push({ key: `bundle.schemas.${slot}`, before: a, after: '', change: 'removed' });
    else rows.push({ key: `bundle.schemas.${slot}`, before: a, after: b, change: 'changed' });
  }
  const meta: [string, string, string][] = [
    ['name', before?.name ?? '', after.name],
    ['description', before?.description ?? '', after.description ?? ''],
    ['owner', before?.owner ?? '', after.owner ?? ''],
    ['tags', JSON.stringify(before?.tags ?? []), JSON.stringify(after.tags ?? [])],
  ];
  for (const [key, a, b] of meta) {
    if (a !== b) rows.push({ key, before: a, after: b, change: 'changed' });
  }
  return rows;
}

// diffMappingRows and diffSchemaFields power the bundle content sub-diffs
// (UX Phase 2 D2-4): when a version changed a bundle ref, the reviewer
// also sees what the referenced mapping/contract contains. Profile
// versions pin asset ids (not versions), so the sub-diff shows each
// side's CURRENT content — labeled as such.
export function diffMappingRows(before: Mapping | null, after: Mapping | null): DiffRow[] {
  const rows: DiffRow[] = [];
  const beforeRows = new Map((before?.rows ?? []).map((row) => [row.target, row]));
  const afterRows = new Map((after?.rows ?? []).map((row) => [row.target, row]));
  for (const target of new Set([...beforeRows.keys(), ...afterRows.keys()])) {
    const a = beforeRows.get(target);
    const b = afterRows.get(target);
    const sa = a ? JSON.stringify({ source: a.source, required: a.required }) : '';
    const sb = b ? JSON.stringify({ source: b.source, required: b.required }) : '';
    if (!a) rows.push({ key: target, before: '', after: sb, change: 'added' });
    else if (!b) rows.push({ key: target, before: sa, after: '', change: 'removed' });
    else if (sa !== sb) rows.push({ key: target, before: sa, after: sb, change: 'changed' });
  }
  return rows;
}

function schemaFieldSummary(field: SchemaField): string {
  return JSON.stringify({
    type: field.type,
    required: field.required,
    description: field.description,
  });
}

export function diffSchemaFields(before: DataSchema | null, after: DataSchema | null): DiffRow[] {
  const rows: DiffRow[] = [];
  const beforeFields = new Map((before?.fields ?? []).map((field) => [field.name, field]));
  const afterFields = new Map((after?.fields ?? []).map((field) => [field.name, field]));
  for (const name of new Set([...beforeFields.keys(), ...afterFields.keys()])) {
    const a = beforeFields.get(name);
    const b = afterFields.get(name);
    const sa = a ? schemaFieldSummary(a) : '';
    const sb = b ? schemaFieldSummary(b) : '';
    if (!a) rows.push({ key: name, before: '', after: sb, change: 'added' });
    else if (!b) rows.push({ key: name, before: sa, after: '', change: 'removed' });
    else if (sa !== sb) rows.push({ key: name, before: sa, after: sb, change: 'changed' });
  }
  return rows;
}

interface BundleAssetLookup {
  mappingNames: Map<string, string>;
  schemaNames: Map<string, string>;
}

// Bundle ref ids decorate with the asset's current name (vN) when the
// lookup lists resolve; raw ids show otherwise.
function decorateRef(id: string, names: Map<string, string>): string {
  return names.get(id) ?? id;
}

/** One changed bundle ref plus the before/after asset docs fetched for
 * its content sub-diff. */
interface BundleSubDiff {
  label: string;
  mapping?: { before: Mapping | null; after: Mapping | null };
  schema?: { before: DataSchema | null; after: DataSchema | null };
}

async function loadBundleSubDiff(
  before: ConfigProfile | null,
  after: ConfigProfile,
): Promise<{ diffs: BundleSubDiff[]; errors: string[] }> {
  const diffs: BundleSubDiff[] = [];
  const errors: string[] = [];
  const mappingKinds = new Set([
    ...Object.keys(before?.bundle?.mappings ?? {}),
    ...Object.keys(after.bundle?.mappings ?? {}),
  ]);
  for (const kind of mappingKinds) {
    const beforeID = before?.bundle?.mappings?.[kind] ?? '';
    const afterID = after.bundle?.mappings?.[kind] ?? '';
    if (beforeID === afterID) continue;
    // U6: a failed asset read is reported, not rendered as "no content".
    const load = (id: string): Promise<Mapping | null> =>
      id
        ? api.getMapping(id).catch(() => {
            errors.push(`Could not load the ${kind} mapping (${id}).`);
            return null;
          })
        : Promise.resolve(null);
    diffs.push({
      label: `${kind} mapping`,
      mapping: { before: await load(beforeID), after: await load(afterID) },
    });
  }
  const schemaSlots = new Set([
    ...Object.keys(before?.bundle?.schemas ?? {}),
    ...Object.keys(after.bundle?.schemas ?? {}),
  ]);
  for (const slot of schemaSlots) {
    const beforeID = before?.bundle?.schemas?.[slot] ?? '';
    const afterID = after.bundle?.schemas?.[slot] ?? '';
    if (beforeID === afterID) continue;
    const load = (id: string): Promise<DataSchema | null> =>
      id
        ? api.getDataSchema(id).catch(() => {
            errors.push(`Could not load the ${slot} data contract (${id}).`);
            return null;
          })
        : Promise.resolve(null);
    diffs.push({
      label: `${slot} data contract`,
      schema: { before: await load(beforeID), after: await load(afterID) },
    });
  }
  return { diffs, errors };
}

/**
 * The shared version-history view (UX Phase 2 D2-4): the version list,
 * per-version Diff|Full-config toggle, and the deepened diff — bundle ref
 * changes resolve to asset names and carry content sub-diffs. Rendered
 * inline by the edit page's History tab and inside the dialog by
 * ProfileHistory.
 */
export function ProfileVersionHistory({ profileId }: { profileId: string }) {
  const [versions, setVersions] = useState<ConfigProfileVersion[]>([]);
  const [selected, setSelected] = useState<ConfigProfile | null>(null);
  const [previous, setPrevious] = useState<ConfigProfile | null>(null);
  const [viewMode, setViewMode] = useState<'diff' | 'full'>('diff');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [subDiffs, setSubDiffs] = useState<BundleSubDiff[]>([]);
  // U6: secondary lookups report failures instead of reading as empty
  // or missing content.
  const [subDiffErrors, setSubDiffErrors] = useState<string[]>([]);
  const [previousError, setPreviousError] = useState('');
  const [namesError, setNamesError] = useState(false);
  const [lookup, setLookup] = useState<BundleAssetLookup>({ mappingNames: new Map(), schemaNames: new Map() });
  // Bumped by the error state's retry so the load effect re-runs for
  // the same profile id.
  const [reload, setReload] = useState(0);

  useEffect(() => {
    setLoading(true);
    setError('');
    setSelected(null);
    setPrevious(null);
    setSubDiffs([]);
    setSubDiffErrors([]);
    setNamesError(false);
    setViewMode('diff');
    api
      .listConfigProfileVersions(profileId)
      .then((result) => setVersions(result.versions ?? []))
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : String(reason)),
      )
      .finally(() => setLoading(false));
    // Asset name lookups only decorate bundle rows: on failure the rows
    // fall back to raw ids, and the banner above says why (U6).
    api
      .listMappings()
      .then((result) =>
        setLookup((current) => ({
          ...current,
          mappingNames: new Map(
            (result.mappings ?? []).map((m) => [m.id, `${m.name} (v${m.version})`]),
          ),
        })),
      )
      .catch(() => {
        setNamesError(true);
      });
    api
      .listDataSchemas()
      .then((result) =>
        setLookup((current) => ({
          ...current,
          schemaNames: new Map(
            (result.schemas ?? []).map((s) => [s.id, `${s.name} (v${s.version})`]),
          ),
        })),
      )
      .catch(() => {
        setNamesError(true);
      });
  }, [profileId, reload]);

  const showVersion = (version: number) => {
    setSelected(null);
    setPrevious(null);
    setSubDiffs([]);
    setSubDiffErrors([]);
    setPreviousError('');
    setViewMode('diff');
    setError('');
    const after = api.getConfigProfileVersion(profileId, version);
    const before =
      version > 1
        ? api.getConfigProfileVersion(profileId, version - 1).catch((reason: unknown) => {
            setPreviousError(reason instanceof Error ? reason.message : String(reason));
            return null;
          })
        : Promise.resolve(null);
    Promise.all([after, before])
      .then(async ([a, b]) => {
        setSelected(a);
        setPrevious(b);
        const { diffs, errors } = await loadBundleSubDiff(b, a);
        setSubDiffs(diffs);
        setSubDiffErrors(errors);
      })
      .catch((reason: unknown) =>
        setError(reason instanceof Error ? reason.message : String(reason)),
      );
  };

  // Bundle ref rows in the main diff render decorated ids so the reviewer
  // reads names, not opaque identifiers.
  const decoratedRows = useMemo(() => {
    if (!selected) return [];
    const rows = diffProfileDocs(previous, selected);
    return rows.map((row) => {
      if (!row.key.startsWith('bundle.mappings.') && !row.key.startsWith('bundle.schemas.')) {
        return row;
      }
      const names = row.key.startsWith('bundle.mappings.')
        ? lookup.mappingNames
        : lookup.schemaNames;
      const decorate = (value: string): string => {
        if (!value) return value;
        // Bundle rows carry raw asset ids; JSON-string values (legacy
        // blob rows) resolve through JSON.parse when possible.
        try {
          const parsed = JSON.parse(value) as unknown;
          if (typeof parsed === 'string') return decorateRef(parsed, names);
        } catch {
          // raw id — not JSON
        }
        return decorateRef(value, names);
      };
      return { ...row, before: decorate(row.before), after: decorate(row.after) };
    });
  }, [previous, selected, lookup]);

  return (
    <>
      {loading && <AssetLoadingState label="Loading versions…" className="muted small" />}
      {error && (
        <>
          <ActionError>{error}</ActionError>
          <button
            type="button"
            className="button subtle"
            onClick={() => setReload((count) => count + 1)}
          >
            Try again
          </button>
        </>
      )}
      {namesError && (
        <ActionError testId="profile-history-names-error">
          Could not load mapping and data-contract names. Bundle rows show raw ids.
        </ActionError>
      )}
      {!loading && !error && versions.length === 0 && (
        <p className="muted small">No versions recorded.</p>
      )}
      <ul className="version-list" data-testid="profile-version-list">
        {versions.map((version) => (
          <li key={version.version}>
            <button
              type="button"
              className="button subtle"
              onClick={() => showVersion(version.version)}
              aria-label={`Show version ${version.version}`}
            >
              v{version.version}
            </button>
            <span className="muted small">
              {new Date(version.createdAt).toLocaleString()}
              {version.actor ? ` · ${version.actor}` : ''}
            </span>
          </li>
        ))}
      </ul>
      {selected && (
        <div className="profile-diff">
          <h3>
            {previous
              ? `Changes in v${selected.version} (vs v${previous.version})`
              : previousError
                ? `Version v${selected.version}`
                : `Initial version v${selected.version}`}
          </h3>
          {previousError && (
            <ActionError testId="profile-history-previous-error">
              Could not load the previous version for comparison. {previousError}
            </ActionError>
          )}
          <div className="mode-toggle" role="group" aria-label="Version view mode">
            <button
              type="button"
              className={`mode-toggle-button ${viewMode === 'diff' ? 'active' : ''}`}
              aria-pressed={viewMode === 'diff'}
              data-testid="profile-version-tab-diff"
              onClick={() => setViewMode('diff')}
            >
              Diff
            </button>
            <button
              type="button"
              className={`mode-toggle-button ${viewMode === 'full' ? 'active' : ''}`}
              aria-pressed={viewMode === 'full'}
              data-testid="profile-version-tab-full"
              onClick={() => setViewMode('full')}
            >
              Full config
            </button>
          </div>
          {viewMode === 'diff' ? (
            <div data-testid="profile-version-diff">
              <ProfileDiffTable rows={decoratedRows} />
              {subDiffs.map((sub) => (
                <div className="bundle-subdiff" key={sub.label} data-testid="profile-bundle-subdiff">
                  <h4>
                    {sub.label} — current content of both sides (profile versions pin asset ids,
                    not versions)
                  </h4>
                  <ProfileDiffTable
                    rows={
                      sub.mapping
                        ? diffMappingRows(sub.mapping.before, sub.mapping.after)
                        : diffSchemaFields(sub.schema?.before ?? null, sub.schema?.after ?? null)
                    }
                  />
                </div>
              ))}
              {subDiffErrors.length > 0 && (
                <ActionError testId="profile-bundle-subdiff-error">
                  {subDiffErrors.join(' ')}
                </ActionError>
              )}
            </div>
          ) : (
            <pre className="profile-version-json" data-testid="profile-version-full">
              {JSON.stringify(selected, null, 2)}
            </pre>
          )}
        </div>
      )}
    </>
  );
}

export function ProfileHistory({ profile, onClose }: Props) {
  const dialogRef = useDialogFocus<HTMLDivElement>(profile !== null, onClose);
  if (!profile) return null;
  return (
    <div className="dialog-backdrop" data-testid="profile-history-backdrop">
      <div
        className="dialog asset-history-dialog"
        role="dialog"
        aria-label={`Version history for ${profile.name}`}
        ref={dialogRef}
        tabIndex={-1}
      >
        <header className="dialog-head">
          <h2>Version history: {profile.name}</h2>
          <button className="icon-button" onClick={onClose} aria-label="Close history" type="button">
            ×
          </button>
        </header>
        <div className="dialog-body" data-testid="profile-history-body">
          <ProfileVersionHistory profileId={profile.id} />
        </div>
      </div>
    </div>
  );
}
