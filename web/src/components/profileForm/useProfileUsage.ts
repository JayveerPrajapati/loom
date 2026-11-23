import { useEffect, useState } from 'react';
import { api } from '../../api';
import type {
  ConfigProfileUsage,
  DataSchemaUsage,
  MappingUsage,
  ScriptUsage,
} from '../../types';
import type { PreviewSite } from './ProfileConfigPanels';

// Usage data for the profile form (Phase 6 D6-3): the Used-by tab's own
// + bundled-asset pins (loaded on tab activation), and the usage sites
// the effective preview composes against (D2-3: the latest draft of
// every workflow that pins this profile).
export function useProfileUsage(args: {
  editing: boolean;
  profileId?: string;
  version: number | undefined;
  activeTab: string;
  bundleMappings: Record<string, string>;
  bundleSchemas: Record<'input' | 'output', string>;
  bundleScript: string;
}) {
  const { editing, profileId, version, activeTab, bundleMappings, bundleSchemas, bundleScript } =
    args;

  const [profileUsage, setProfileUsage] = useState<ConfigProfileUsage[] | null>(null);
  const [bundleUsage, setBundleUsage] = useState<
    {
      label: string;
      id: string;
      kind: 'mapping' | 'schema' | 'script';
      usage: MappingUsage[] | DataSchemaUsage[] | ScriptUsage[];
    }[]
  >([]);
  const [usageLoading, setUsageLoading] = useState(false);

  // Phase 4 D4-6: the profile's own usage is needed by the sticky status
  // on every edit page, not only when the Used-by section is open.
  useEffect(() => {
    if (!editing || !profileId) {
      setProfileUsage(null);
      return;
    }
    let cancelled = false;
    api
      .configProfileUsage(profileId)
      .then((own) => {
        if (!cancelled) setProfileUsage(own.usage ?? []);
      })
      .catch(() => {
        if (!cancelled) setProfileUsage(null);
      });
    return () => {
      cancelled = true;
    };
  }, [editing, profileId, version]);

  // Bundled-asset usage is heavier and stays lazy: it loads only when the
  // Used-by section is open.
  useEffect(() => {
    if (activeTab !== 'used-by' || !profileId) return;
    let cancelled = false;
    setUsageLoading(true);
    (async () => {
      try {
        const refs: { label: string; id: string; kind: 'mapping' | 'schema' | 'script' }[] = [];
        for (const [kind, id] of Object.entries(bundleMappings)) {
          if (id) refs.push({ label: `${kind} mapping`, id, kind: 'mapping' });
        }
        for (const [slot, id] of Object.entries(bundleSchemas)) {
          if (id) refs.push({ label: `${slot} data contract`, id, kind: 'schema' });
        }
        if (bundleScript) refs.push({ label: 'script', id: bundleScript, kind: 'script' });
        const sections: typeof bundleUsage = [];
        for (const ref of refs) {
          const result =
            ref.kind === 'mapping'
              ? await api.mappingUsage(ref.id).then((r) => r.usage ?? [])
              : ref.kind === 'script'
                ? await api.scriptUsage(ref.id).then((r) => r.usage ?? [])
                : await api.dataSchemaUsage(ref.id).then((r) => r.usage ?? []);
          if (cancelled) return;
          sections.push({ ...ref, usage: result });
        }
        if (!cancelled) setBundleUsage(sections);
      } catch {
        if (!cancelled) setBundleUsage([]);
      } finally {
        if (!cancelled) setUsageLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [activeTab, profileId, version, bundleMappings, bundleSchemas, bundleScript]);

  const [sites, setSites] = useState<PreviewSite[] | null>(null);
  const [siteKey, setSiteKey] = useState('');

  useEffect(() => {
    if (!editing || !profileId) {
      setSites(null);
      return;
    }
    let cancelled = false;
    api
      .configProfileUsage(profileId)
      .then(async (result) => {
        const workflowNames = new Map<string, string>();
        for (const row of result.usage ?? []) {
          if (!workflowNames.has(row.workflowId)) {
            workflowNames.set(row.workflowId, row.workflowName || row.workflowId);
          }
        }
        const found: PreviewSite[] = [];
        for (const [workflowId, workflowName] of workflowNames) {
          try {
            const workflow = await api.getWorkflow(workflowId);
            for (const node of workflow.nodes ?? []) {
              if (node.profileRef !== profileId) continue;
              found.push({
                workflowId,
                workflowName,
                nodeId: node.id,
                nodeName: node.name,
                config: node.config ?? {},
                retry: node.retry,
                inputSchemaRef: node.inputSchemaRef,
                outputSchemaRef: node.outputSchemaRef,
              });
            }
          } catch {
            // The workflow vanished between the usage query and the
            // fetch — skip it rather than failing the whole preview.
          }
        }
        if (!cancelled) setSites(found);
      })
      .catch(() => {
        if (!cancelled) setSites(null);
      });
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editing, profileId, version]);

  return { profileUsage, bundleUsage, usageLoading, sites, siteKey, setSiteKey };
}
