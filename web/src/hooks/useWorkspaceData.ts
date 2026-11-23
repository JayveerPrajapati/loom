import { useCallback, useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import { assetKeys, cachedAsset, invalidateAssets, seedAsset } from '../assetCache';
import { nodeDefMap } from '../catalog';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import type { ConfigProfile, DataSchema, Example, Mapping, NodeDef, WorkflowSummary } from '../types';

// The workspace indexes (Phase 6 D6-1): node catalog, examples, the
// workflow summary list for the switcher, and the reusable-asset
// indexes (profiles, schemas, mappings) that editor validation and the
// Inspector pickers read. Mutations from the management pages flow
// back through the refresh callbacks.
export function useWorkspaceData() {
  const [defs, setDefs] = useState<NodeDef[]>([]);
  const [examples, setExamples] = useState<Example[]>([]);
  const [summaries, setSummaries] = useState<WorkflowSummary[]>([]);
  const [summariesFailure, setSummariesFailure] = useState<LoadFailure | null>(null);
  const [profiles, setProfiles] = useState<ConfigProfile[]>([]);
  const [schemas, setSchemas] = useState<DataSchema[]>([]);
  const [mappings, setMappings] = useState<Mapping[]>([]);

  // Profile mutations from the Configuration page must refresh the
  // App-level profile index: editor validation (required fields arriving
  // via a profile) reads it.
  const refreshProfiles = useCallback(() => {
    invalidateAssets(assetKeys.profiles);
    cachedAsset(assetKeys.profiles, () => api.listConfigProfiles())
      .then((result) => setProfiles(result.profiles ?? []))
      .catch(() => undefined);
  }, []);

  // The schema index serves the same role for data contracts: editor
  // validation (dangling refs) and the Inspector pickers read it.
  const refreshSchemas = useCallback(() => {
    invalidateAssets(assetKeys.schemas);
    cachedAsset(assetKeys.schemas, () => api.listDataSchemas())
      .then((result) => setSchemas(result.schemas ?? []))
      .catch(() => undefined);
  }, []);

  const refreshMappings = useCallback(() => {
    invalidateAssets(assetKeys.mappings);
    cachedAsset(assetKeys.mappings, () => api.listMappings())
      .then((result) => setMappings(result.mappings ?? []))
      .catch(() => undefined);
  }, []);

  const refreshSummaries = useCallback(async () => {
    invalidateAssets(assetKeys.workflowPrefix);
    try {
      const result = await api.listWorkflows();
      setSummaries(result.workflows ?? []);
      setSummariesFailure(null);
    } catch (error) {
      setSummariesFailure(classifyCurrentLoadFailure(error));
    }
  }, []);

  // Boot and every management refresh seed the shared picker cache, so
  // the Inspector reuses the app's complete indexes instead of refetching
  // them per node switch. Empty indexes are not seeded: an empty cache
  // still triggers the Inspector's own read.
  useEffect(() => {
    if (profiles.length > 0) {
      seedAsset(assetKeys.profiles, { profiles, count: profiles.length });
    }
    if (schemas.length > 0) {
      seedAsset(assetKeys.schemas, { schemas, count: schemas.length });
    }
    if (mappings.length > 0) {
      seedAsset(assetKeys.mappings, { mappings, count: mappings.length });
    }
  }, [profiles, schemas, mappings]);

  const defsByType = useMemo(() => nodeDefMap(defs), [defs]);
  const profilesById = useMemo(
    () => Object.fromEntries(profiles.map((p) => [p.id, p])),
    [profiles],
  );
  const schemasById = useMemo(
    () => Object.fromEntries(schemas.map((s) => [s.id, s])),
    [schemas],
  );
  const mappingsById = useMemo(
    () => Object.fromEntries(mappings.map((m) => [m.id, m])),
    [mappings],
  );

  // Stable identity for useWorkflowLifecycle (see useSession).
  const dataHandlers = useMemo(
    () => ({
      setDefs,
      setExamples,
      setSummaries,
      setProfiles,
      setSchemas,
      setMappings,
    }),
    [],
  );

  return {
    dataHandlers,
    defs,
    setDefs,
    examples,
    setExamples,
    summaries,
    setSummaries,
    summariesFailure,
    profiles,
    setProfiles,
    schemas,
    setSchemas,
    mappings,
    setMappings,
    defsByType,
    profilesById,
    schemasById,
    mappingsById,
    refreshProfiles,
    refreshSchemas,
    refreshMappings,
    refreshSummaries,
  };
}
