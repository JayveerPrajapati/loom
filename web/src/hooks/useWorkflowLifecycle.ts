import { useCallback, useEffect, useState } from 'react';
import { api, ApiError } from '../api';
import { assetKeys, cachedAsset, clearAssetCache } from '../assetCache';
import { parseRoute, type Route } from '../appShared';
import type {
  AuthUser,
  ConfigProfile,
  DataSchema,
  Example,
  Mapping,
  NodeDef,
  ProfileUpgradeChange,
  SubprocessNodes,
  ValidationIssue,
  Workflow,
  WorkflowSummary,
  WorkflowState,
} from '../types';

// Workflow lifecycle effects (Phase 6 D6-1): boot loading, route-driven
// workflow loads, the published-version poll, and the profile-upgrade
// plan fetch. All state changes flow through the host's callbacks.
export function useWorkflowLifecycle(args: {
  route: Route;
  workflowRef: React.MutableRefObject<Workflow | null>;
  adoptWorkflow: (next: Workflow, markDirty?: boolean) => void;
  clearEditor: () => void;
  notify: (message: string, tone?: 'info' | 'error') => void;
  session: {
    setPhase: (phase: 'loading' | 'ready' | 'error') => void;
    setBootError: (message: string) => void;
    setPrincipal: (user: AuthUser) => void;
    principalRef: React.MutableRefObject<AuthUser | null>;
    setAuthChecked: (value: boolean) => void;
    authExpiredRef: React.MutableRefObject<boolean>;
    setAuthRequired: (value: boolean) => void;
    setAuthError: (message: string) => void;
    setAuthExpired: (value: boolean) => void;
    setSubprocess: (nodes: SubprocessNodes | null) => void;
  };
  data: {
    setDefs: (defs: NodeDef[]) => void;
    setExamples: (examples: Example[]) => void;
    setSummaries: (summaries: WorkflowSummary[]) => void;
    setProfiles: (profiles: ConfigProfile[]) => void;
    setSchemas: (schemas: DataSchema[]) => void;
    setMappings: (mappings: Mapping[]) => void;
  };
}) {
  const { route, workflowRef, adoptWorkflow, clearEditor, notify, session, data } = args;

  const [versionState, setVersionState] = useState<WorkflowState | null>(null);
  // UX Phase 2 D2-6: the upgrade plan for this workflow's profile pins.
  const [upgradePlan, setUpgradePlan] = useState<ProfileUpgradeChange[] | null>(null);
  const [upgradeIssues, setUpgradeIssues] = useState<ValidationIssue[]>([]);

  // Opening a workflow URL loads that workflow.
  useEffect(() => {
    if ((route.name !== 'editor' && route.name !== 'workflow') || !route.id) return;
    if (workflowRef.current?.id === route.id) return;
    let cancelled = false;
    api
      .getWorkflow(route.id)
      .then((next) => {
        if (!cancelled) adoptWorkflow(next);
      })
      .catch((error: Error) => {
        if (!cancelled) notify(error.message, 'error');
      });
    return () => {
      cancelled = true;
    };
  }, [adoptWorkflow, notify, route, workflowRef]);

  const refreshWorkflowState = useCallback(async (id: string) => {
    try {
      setVersionState(await api.getWorkflowState(id));
    } catch {
      setVersionState(null);
    }
  }, []);

  useEffect(() => {
    if (!workflowRef.current?.id) {
      setVersionState(null);
      return;
    }
    let cancelled = false;
    api
      .getWorkflowState(workflowRef.current.id)
      .then((state) => {
        if (!cancelled) setVersionState(state);
      })
      .catch(() => {
        if (!cancelled) setVersionState(null);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflowRef.current?.id]);

  // UX Phase 2 D2-6: fetch the profile-upgrade plan for the open
  // workflow whenever its published version changes — the indicator
  // disappears once every pin is current. Drafts and errors leave it off.
  useEffect(() => {
    const wf = workflowRef.current;
    if (!wf?.id || !(versionState?.publishedVersion ?? 0)) {
      setUpgradePlan(null);
      setUpgradeIssues([]);
      return;
    }
    if (!wf.nodes.some((node) => node.profileRef)) {
      setUpgradePlan(null);
      setUpgradeIssues([]);
      return;
    }
    let cancelled = false;
    api
      .workflowUpgradePreview(wf.id)
      .then((result) => {
        if (!cancelled) {
          setUpgradePlan(result.plan ?? []);
          setUpgradeIssues(result.issues ?? []);
        }
      })
      .catch(() => {
        if (!cancelled) setUpgradePlan(null);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [workflowRef.current?.id, versionState?.publishedVersion]);

  // Boot: probe the session, then load the workspace indexes and the
  // workflow the URL names (or the first one).
  const loadInitial = useCallback(async () => {
    session.setPhase('loading');
    session.setBootError('');
    // A boot is a session boundary: never serve picker data cached for a
    // previous principal.
    clearAssetCache();
    try {
      const me = await api.me();
      session.setPrincipal(me.user);
      session.principalRef.current = me.user;
      session.setAuthChecked(true);
      session.setAuthRequired(false);
      session.setAuthError('');
      session.setAuthExpired(false);
      session.authExpiredRef.current = false;
      const [nodeTypes, exampleList, workflowList, health, profileList, schemaList, mappingList] =
        await Promise.all([
          api.nodeTypes(),
          api.examples(),
          api.listWorkflows(),
          api.healthz(),
          cachedAsset(assetKeys.profiles, () => api.listConfigProfiles()),
          cachedAsset(assetKeys.schemas, () => api.listDataSchemas()),
          cachedAsset(assetKeys.mappings, () => api.listMappings()),
        ]);
      data.setDefs(nodeTypes.nodeTypes ?? []);
      data.setExamples(exampleList.examples ?? []);
      data.setSummaries(workflowList.workflows ?? []);
      session.setSubprocess(health.subprocessNodes ?? null);
      data.setProfiles(profileList.profiles ?? []);
      data.setSchemas(schemaList.schemas ?? []);
      data.setMappings(mappingList.mappings ?? []);
      const initialRoute = parseRoute();
      const requestedId =
        initialRoute.name === 'editor' || initialRoute.name === 'workflow'
          ? initialRoute.id
          : undefined;
      const workflowId = requestedId ?? workflowList.workflows?.[0]?.id;
      if ((initialRoute.name === 'editor' || initialRoute.name === 'workflow') && workflowId) {
        const selected = await api.getWorkflow(workflowId);
        adoptWorkflow(selected);
      } else {
        clearEditor();
      }
      session.setPhase('ready');
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        // The api layer already dispatched loom:auth-required, whose
        // handler owns all sign-in state (including the expiry notice);
        // re-setting it here would clobber the message.
        return;
      }
      session.setBootError(error instanceof Error ? error.message : String(error));
      session.setPhase('error');
    }
  }, [adoptWorkflow, clearEditor, data, session]);

  return {
    versionState,
    upgradePlan,
    upgradeIssues,
    refreshWorkflowState,
    loadInitial,
  };
}
