import { useCallback, useEffect, useMemo, useState } from 'react';
import { api } from '../api';
import {
  assetKeys,
  cachedAsset,
  cachedBlob,
  invalidateAssets,
  useAssetCacheVersion,
} from '../assetCache';
import {
  buildExpressionCandidates,
  upstreamNodes,
  type ExpressionCandidate,
} from '../autocomplete';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import type {
  ConfigProfile,
  CredentialInfo,
  DataSchema,
  Mapping,
  NodeDef,
  RunEvent,
  SchemaField,
  Script,
  Workflow,
  WorkflowNode,
} from '../types';

// Asset fetching + derived candidates for the node inspector.
// Phase 5: lists come from the shared asset cache (seeded by the app
// indexes), picker failures keep their Retry state, and the blob effect is
// keyed by node id and output hash so unrelated edits do not refetch it.
export function useInspectorAssets(
  workflow: Workflow,
  node: WorkflowNode | null,
  def: NodeDef | undefined,
  runEvents: RunEvent[],
) {
  const [credentials, setCredentials] = useState<CredentialInfo[]>([]);
  const [profiles, setProfiles] = useState<ConfigProfile[]>([]);
  const [schemas, setSchemas] = useState<DataSchema[]>([]);
  const [mappings, setMappings] = useState<Mapping[]>([]);
  const [scripts, setScripts] = useState<Script[]>([]);
  const [calleeContract, setCalleeContract] = useState<DataSchema | null>(null);
  const [upstreamOutput, setUpstreamOutput] = useState<unknown>(null);
  const [failures, setFailures] = useState<Partial<Record<string, LoadFailure>>>({});
  const cacheVersion = useAssetCacheVersion();
  const profileable = Object.values(def?.config ?? {}).some((spec) => spec.profileable);

  const markFailure = useCallback((resource: string, error: unknown) => {
    setFailures((current) => ({ ...current, [resource]: classifyCurrentLoadFailure(error) }));
  }, []);

  const clearFailure = useCallback((resource: string) => {
    setFailures((current) => {
      if (!(resource in current)) return current;
      const next = { ...current };
      delete next[resource];
      return next;
    });
  }, []);

  const retryAssets = useCallback(() => {
    setFailures({});
    invalidateAssets();
  }, []);

  // Shape-aware candidates (v2 D15): the upstream nodes' output contracts
  // first, then what their last run actually emitted, then static hints.
  const candidates: ExpressionCandidate[] = useMemo(() => {
    if (!node) return [];
    const upstream = upstreamNodes(workflow, node.id);
    const contracts = upstream
      .map((up) => schemas.find((s) => s.id === up.outputSchemaRef)?.fields)
      .filter((fields): fields is SchemaField[] => !!fields && fields.length > 0);
    return buildExpressionCandidates(contracts, upstreamOutput ? [upstreamOutput] : []);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [node?.id, JSON.stringify(workflow.edges), schemas, upstreamOutput]);

  // The latest upstream output hash is the only blob input. Deriving it
  // separately keeps unrelated run events and workflow edits from
  // restarting the fetch.
  const latestOutputHash = useMemo(() => {
    if (!node) return '';
    const upstreamIds = upstreamNodes(workflow, node.id).map((up) => up.id);
    const latest = [...runEvents]
      .reverse()
      .find((event) => event.outputHash && upstreamIds.includes(event.nodeId ?? ''));
    return latest?.outputHash ?? '';
  }, [node?.id, runEvents, workflow]);

  useEffect(() => {
    setUpstreamOutput(null);
    if (!node || !latestOutputHash) return;
    let cancelled = false;
    cachedBlob(latestOutputHash, () => api.getBlob(latestOutputHash))
      .then((value) => {
        if (!cancelled) setUpstreamOutput(value);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [node?.id, latestOutputHash]);

  const refreshMappings = useCallback(() => {
    invalidateAssets(assetKeys.mappings);
  }, []);

  useEffect(() => {
    let cancelled = false;
    cachedAsset(assetKeys.credentials, () => api.listCredentials())
      .then((result) => {
        if (cancelled) return;
        setCredentials(result.credentials ?? []);
        clearFailure('credentials');
      })
      .catch((error) => {
        if (!cancelled) markFailure('credentials', error);
      });
    cachedAsset(assetKeys.schemas, () => api.listDataSchemas())
      .then((result) => {
        if (cancelled) return;
        setSchemas(result.schemas ?? []);
        clearFailure('schemas');
      })
      .catch((error) => {
        if (!cancelled) markFailure('schemas', error);
      });
    cachedAsset(assetKeys.mappings, () => api.listMappings())
      .then((result) => {
        if (cancelled) return;
        setMappings(result.mappings ?? []);
        clearFailure('mappings');
      })
      .catch((error) => {
        if (!cancelled) markFailure('mappings', error);
      });
    cachedAsset(assetKeys.scripts, () => api.listScripts())
      .then((result) => {
        if (cancelled) return;
        setScripts(result.scripts ?? []);
        clearFailure('scripts');
      })
      .catch((error) => {
        if (!cancelled) markFailure('scripts', error);
      });
    return () => {
      cancelled = true;
    };
  }, [cacheVersion, markFailure, clearFailure]);

  // Profiles are filtered client-side so one cached list serves every node
  // type (the app index is already complete).
  useEffect(() => {
    setProfiles([]);
    if (!profileable || !node?.type) return;
    let cancelled = false;
    cachedAsset(assetKeys.profiles, () => api.listConfigProfiles())
      .then((result) => {
        if (cancelled) return;
        setProfiles((result.profiles ?? []).filter((profile) => profile.nodeType === node.type));
        clearFailure('profiles');
      })
      .catch((error) => {
        if (!cancelled) markFailure('profiles', error);
      });
    return () => {
      cancelled = true;
    };
  }, [node?.type, profileable, cacheVersion, markFailure, clearFailure]);

  // Subflow: show the callee's input contract so the author knows the
  // shape the child expects. Skipped when the id is itself a template.
  useEffect(() => {
    setCalleeContract(null);
    if (node?.type !== 'flow.subflow') return;
    const workflowId = String(node.config?.workflowId ?? '');
    if (!workflowId || workflowId.includes('{{')) return;
    let cancelled = false;
    cachedAsset(assetKeys.workflow(workflowId), () => api.getWorkflow(workflowId), 10_000)
      .then((callee) => {
        const ref = callee.inputSchemaRef;
        if (!ref) return null;
        return cachedAsset(assetKeys.schema(ref), () => api.getDataSchema(ref));
      })
      .then((schema) => {
        if (cancelled) return;
        setCalleeContract(schema ?? null);
        clearFailure('callee');
      })
      .catch((error) => {
        if (!cancelled) markFailure('callee', error);
      });
    return () => {
      cancelled = true;
    };
  }, [node?.type, node?.config?.workflowId, cacheVersion, markFailure, clearFailure]);

  return {
    credentials,
    profiles,
    schemas,
    mappings,
    scripts,
    calleeContract,
    candidates,
    failures,
    retryAssets,
    refreshMappings,
  };
}
