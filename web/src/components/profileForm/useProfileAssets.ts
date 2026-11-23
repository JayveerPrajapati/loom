import { useEffect, useMemo, useState } from 'react';
import { api } from '../../api';
import type { CredentialInfo, DataSchema, Mapping, Script } from '../../types';

// Asset indexes for the profile form (Phase 6 D6-3): credentials, the
// visible mapping/schema/script lists the bundle pickers offer, and
// id-resolution maps for the preview's ref badges.
export function useProfileAssets() {
  const [credentials, setCredentials] = useState<CredentialInfo[]>([]);
  const [bundleMappingsByKind, setBundleMappingsByKind] = useState<Record<string, Mapping[]>>({});
  const [bundleSchemasList, setBundleSchemasList] = useState<DataSchema[]>([]);
  const [bundleScriptsList, setBundleScriptsList] = useState<Script[]>([]);

  useEffect(() => {
    let cancelled = false;
    api
      .listCredentials()
      .then((result) => {
        if (!cancelled) setCredentials(result.credentials ?? []);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  // Bundle pickers (Phase 1 P0-1): every visible mapping and schema of
  // this node type's kinds. The server filters by visibility already.
  useEffect(() => {
    let cancelled = false;
    api
      .listMappings()
      .then((result) => {
        if (cancelled) return;
        const byKind: Record<string, Mapping[]> = {};
        for (const mapping of result.mappings ?? []) {
          const kind = mapping.kind || 'item';
          (byKind[kind] ??= []).push(mapping);
        }
        setBundleMappingsByKind(byKind);
      })
      .catch(() => undefined);
    api
      .listDataSchemas()
      .then((result) => {
        if (!cancelled) setBundleSchemasList(result.schemas ?? []);
      })
      .catch(() => undefined);
    api
      .listScripts()
      .then((result) => {
        if (!cancelled) setBundleScriptsList(result.scripts ?? []);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  const mappingsById = useMemo(() => {
    const map = new Map<string, Mapping>();
    for (const list of Object.values(bundleMappingsByKind)) {
      for (const mapping of list) map.set(mapping.id, mapping);
    }
    return map;
  }, [bundleMappingsByKind]);
  const schemasById = useMemo(
    () => new Map(bundleSchemasList.map((schema) => [schema.id, schema])),
    [bundleSchemasList],
  );

  return {
    credentials,
    bundleMappingsByKind,
    bundleSchemasList,
    bundleScriptsList,
    mappingsById,
    schemasById,
  };
}
