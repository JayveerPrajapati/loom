import type { Workflow } from '../../types';
import { closeAncestorDetails } from './menu';

export interface MoreMenuProps {
  workflow: Workflow;
  publishedVersion: number;
  hasDraft: boolean;
  setVersionsOpen: (open: boolean) => void;
  /** Operational-trust campaign: opens the workflow egress dialog. */
  onEditEgress: () => void;
  /** UX Phase 2 D2-6: pending profile-upgrade changes; 0 hides the chip. */
  upgradeCount?: number;
  onUpgradePreview?: () => void;
}

/** The editor's overflow menu: version history, egress and profile
 *  upgrades — the workflow-scoped secondary controls that must not
 *  compete with Run and Publish. Audit 3D. */
export function MoreMenu({
  workflow,
  publishedVersion,
  hasDraft,
  setVersionsOpen,
  onEditEgress,
  upgradeCount,
  onUpgradePreview,
}: MoreMenuProps) {
  return (
    <details className="settings-menu topbar-more" data-testid="topbar-more">
      <summary className="button subtle">More</summary>
      <div className="settings-menu-list" data-testid="topbar-more-menu">
        <button
          className={`version-chip ${hasDraft ? 'draft' : ''}`}
          onClick={(event) => {
            closeAncestorDetails(event.currentTarget);
            setVersionsOpen(true);
          }}
          title="Version history"
        >
          {publishedVersion ? `v${publishedVersion}` : 'Draft'}
          {hasDraft && publishedVersion ? ' · draft' : ''}
        </button>
        <button
          className="egress-chip"
          data-testid="workflow-egress-chip"
          onClick={(event) => {
            closeAncestorDetails(event.currentTarget);
            onEditEgress();
          }}
          title="Workflow egress allowlist"
        >
          Egress{workflow.egress?.allowlist?.length ? ` · ${workflow.egress.allowlist.length}` : ''}
        </button>
        {(upgradeCount ?? 0) > 0 && (
          <button
            className="version-chip draft"
            data-testid="upgrade-available-chip"
            onClick={(event) => {
              closeAncestorDetails(event.currentTarget);
              onUpgradePreview?.();
            }}
            title="Profile upgrades available"
          >
            {upgradeCount} profile upgrade{upgradeCount === 1 ? '' : 's'}
          </button>
        )}
      </div>
    </details>
  );
}
