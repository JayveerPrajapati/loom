import { useState } from 'react';
import { api, ApiError } from '../../api';
import { confirmAction } from '../../confirm';
import type { BundleProfileUse, Script } from '../../types';
import { useCanWrite } from '../../principal';
import { AssetRow } from '../assets/AssetRow';
import { UsagePanel } from '../assets/UsagePanel';
import { VisibilityChip, VisibilityControl } from '../assets/VisibilityControl';
import { AssetRowMenu } from '../assets/AssetRowMenu';
import { ScriptHistory } from './ScriptHistory';
import { ScriptTesterPanel } from './ScriptTesterPanel';

export function ScriptRowView({
  script,
  onEdit,
  onRefresh,
  onNotify,
}: {
  script: Script;
  onEdit: () => void;
  onRefresh: () => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
}) {
  const canWrite = useCanWrite();
  const [usage, setUsage] = useState(false);
  const [usageRows, setUsageRows] = useState<
    { workflowName?: string; workflowId: string; workflowVersion: number; scriptVersion: number }[]
  >([]);
  const [usageProfiles, setUsageProfiles] = useState<BundleProfileUse[]>([]);
  const [history, setHistory] = useState(false);
  const [tester, setTester] = useState(false);

  const shared = (script.visibility ?? 'shared') === 'shared';
  const share = () => {
    (shared ? api.unshareScript(script.id) : api.shareScript(script.id))
      .then(() => {
        onNotify?.(
          shared ? `${script.name} is now private` : `${script.name} is now visible to everyone`,
        );
        onRefresh();
      })
      .catch((error: ApiError) => onNotify?.(error.message, 'error'));
  };

  const loadUsage = () => {
    api
      .scriptUsage(script.id)
      .then((result) => {
        setUsageRows(result.usage ?? []);
        setUsageProfiles(result.profiles ?? []);
        setUsage(true);
      })
      .catch((error: ApiError) => onNotify?.(error.message, 'error'));
  };

  const remove = async () => {
    let result: Awaited<ReturnType<typeof api.scriptUsage>>;
    try {
      result = await api.scriptUsage(script.id);
    } catch (reason) {
      onNotify?.(reason instanceof ApiError ? reason.message : 'Could not load usage', 'error');
      return;
    }
    const pins = result.usage ?? [];
    const profiles = result.profiles ?? [];
    const inUse = pins.length + profiles.length > 0;
    const names = profiles
      .slice(0, 3)
      .map((profile) => profile.name)
      .join(', ');
    await confirmAction({
      title: `Delete the script “${script.name}”?`,
      body: inUse
        ? `${pins.length} published workflow version${pins.length === 1 ? '' : 's'} pin this script and ${profiles.length} profile bundle${profiles.length === 1 ? '' : 's'} reference it. Deletion is blocked until they are removed.${names ? ` Bundle profiles: ${names}.` : ''}`
        : 'No published workflow or profile bundle references this script. This cannot be undone.',
      confirmLabel: 'Delete script',
      secondaryAction: inUse
        ? {
            label: 'View usage',
            run: () => {
              setUsageRows(pins);
              setUsageProfiles(profiles);
              setUsage(true);
            },
          }
        : undefined,
      onConfirm: async () => {
        try {
          await api.deleteScript(script.id);
          onNotify?.(`Deleted ${script.name}.`);
          onRefresh();
        } catch (reason) {
          if (reason instanceof ApiError) {
            return reason.status === 409
              ? `${reason.message} Use View usage to find the pinned versions.`
              : reason.message;
          }
          return 'Delete failed.';
        }
      },
    });
  };

  return (
    <>
    <AssetRow
      identity={
        <>
          <strong>{script.name}</strong>
          <span className="count-pill" data-testid="script-language-badge">
            {script.language}
          </span>
          <VisibilityChip visibility={script.visibility} testId="script-visibility-chip" />
        </>
      }
      description={script.description}
      meta={
        <>
          v{script.version} · {script.usageCount ?? 0} published pin
          {script.usageCount === 1 ? '' : 's'}
          {script.owner ? ` · ${script.owner}` : ''}
        </>
      }
      tags={
        (script.tags ?? []).length > 0 ? (
          <div className="profile-tags">
            {script.tags!.map((tag) => (
              <em key={tag}>{tag}</em>
            ))}
          </div>
        ) : undefined
      }
      actions={
        <>
          {canWrite && (
            <button type="button" className="text-button" onClick={onEdit}>
              Edit
            </button>
          )}
          {canWrite && (
            <button type="button" className="text-button" onClick={() => setTester(true)}>
              Test
            </button>
          )}
          <AssetRowMenu label={`More actions for ${script.name}`}>
            <button
              type="button"
              data-testid="asset-action-history"
              onClick={() => setHistory(true)}
            >
              History
            </button>
            <button type="button" data-testid="asset-action-usage" onClick={loadUsage}>
              Usage
            </button>
            {canWrite && (
              <VisibilityControl
                visibility={script.visibility}
                name={script.name}
                labelPrefix="script "
                onToggle={share}
              />
            )}
            {canWrite && (
              <button
                type="button"
                data-testid="asset-action-duplicate"
                onClick={() => {
                  api
                    .duplicateScript(script.id)
                    .then(() => {
                      onNotify?.(`Duplicated ${script.name}.`);
                      onRefresh();
                    })
                    .catch((error: ApiError) => onNotify?.(error.message, 'error'));
                }}
              >
                Duplicate
              </button>
            )}
            {canWrite && (
              <button
                type="button"
                className="danger"
                data-testid="asset-action-delete"
                onClick={() => void remove()}
              >
                Delete
              </button>
            )}
          </AssetRowMenu>
        </>
      }
    >
      {usage && (
        <div className="mapping-detail">
          <UsagePanel
            title="Pinned by workflows or profile bundles"
            empty={usageRows.length === 0 && usageProfiles.length === 0}
            emptyLabel="No published workflow or profile bundle references this script yet."
            onClose={() => setUsage(false)}
            rows={
              <>
                {usageRows.map((row, index) => (
                  <div className="profile-preview-row" key={index}>
                    <code>{row.workflowName ?? row.workflowId}</code>
                    <span>
                      wf v{row.workflowVersion} · script v{row.scriptVersion}
                    </span>
                  </div>
                ))}
                {usageProfiles.map((profile) => (
                  <div className="profile-preview-row" key={`profile-${profile.id}`}>
                    <code>{profile.name}</code>
                    <span>
                      {profile.nodeType} · profile v{profile.version}
                    </span>
                  </div>
                ))}
              </>
            }
          />
        </div>
      )}
      {tester && (
        <ScriptTesterPanel
          script={script}
          onClose={() => setTester(false)}
          onNotify={onNotify}
        />
      )}
    </AssetRow>
    {/* The history dialog renders outside the row: rows carry a hover
        transform, which would become the containing block for the
        dialog's fixed backdrop and clip the wide shell to the row. */}
    {history && <ScriptHistory script={script} onClose={() => setHistory(false)} />}
    </>
  );
}

