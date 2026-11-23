import type { ReactNode } from 'react';
import { api } from '../../api';
import type { ConfigProfile, NodeDef } from '../../types';
import { ProfileVersionHistory } from '../ProfileHistory';
import { AREA_LABEL, type ProfileArea } from '../../profileForm';

/** ProfileFormHeader: breadcrumb, title and version pill. */
export function ProfileFormHeader({
  nodeType,
  typePath,
  def,
  editing,
  profile,
  onNavigate,
}: {
  nodeType: string;
  typePath: string;
  def: NodeDef;
  editing: boolean;
  profile: ConfigProfile | null;
  onNavigate: (path: string) => void;
}) {
  return (
    <header className="page-head">
      <div>
        <p className="breadcrumb">
          <button className="text-button" onClick={() => onNavigate('/profiles')}>
            Profiles
          </button>{' '}
          /{' '}
          <button className="text-button" onClick={() => onNavigate(typePath)}>
            {def.displayName || nodeType}
          </button>{' '}
          / <code>{editing ? `v${profile?.version ?? ''}` : 'new'}</code>
        </p>
        <h1>
          {editing ? `Edit ${profile?.name ?? 'profile'}` : `New ${def.displayName || nodeType} profile`}
        </h1>
        <p className="page-sub">
          Fields you leave empty stay out of the profile: nodes keep their own values and the
          profile fills the rest.
        </p>
      </div>
      {editing && profile && (
        <span className="hub-head-actions">
          <span className="count-pill" data-testid="profile-version-pill">
            v{profile.version}
          </span>
        </span>
      )}
    </header>
  );
}

/** ProfileAreaStrip (Phase 4 D4-1): Identity / Configuration / Trust. */
export function ProfileAreaStrip({
  area,
  onSelect,
}: {
  area: ProfileArea;
  onSelect: (next: ProfileArea) => void;
}) {
  return (
    <div className="profile-tabs profile-area-tabs" role="tablist" aria-label="Profile areas">
      {(['identity', 'configuration', 'trust'] as ProfileArea[]).map((id) => (
        <button
          type="button"
          role="tab"
          key={id}
          id={`profile-tab-${id}`}
          data-testid={`profile-tab-${id}`}
          aria-selected={area === id}
          className={area === id ? 'active' : ''}
          onClick={() => onSelect(id)}
        >
          {AREA_LABEL[id]}
        </button>
      ))}
    </div>
  );
}

/** ProfileSectionStrip: the secondary sections inside one primary area. */
export function ProfileSectionStrip<T extends string>({
  sections,
  active,
  labels,
  label,
  onSelect,
}: {
  sections: readonly T[];
  active: T;
  labels: Record<T, string>;
  label: string;
  onSelect: (next: T) => void;
}) {
  return (
    <div className="profile-sections" role="tablist" aria-label={label}>
      {sections.map((id) => (
        <button
          type="button"
          role="tab"
          key={id}
          id={`profile-section-${id}`}
          data-testid={`profile-section-${id}`}
          aria-selected={active === id}
          className={active === id ? 'active' : ''}
          onClick={() => onSelect(id)}
        >
          {labels[id]}
        </button>
      ))}
    </div>
  );
}

/** ProfileHistoryTabPanel: the History tab body (edit pages only). */
export function ProfileHistoryTabPanel({ profileId }: { profileId: string }) {
  return (
    <div role="tabpanel" aria-labelledby="profile-section-history" data-testid="profile-panel-history">
      <div className="field-group-head">
        <h3 className="field-heading">Version history</h3>
      </div>
      <p className="field-hint">
        Every save appends a version. Diff shows what changed against the previous version —
        bundle ref changes also show the referenced assets&rsquo; current content.
      </p>
      <ProfileVersionHistory profileId={profileId} />
    </div>
  );
}

/** ProfileStickyBar (Phase 4 D4-3): status and save stay reachable
 *  while the form scrolls. Save submits the single profile form. */
export function ProfileStickyBar({
  title,
  meta,
  dirty,
  busy,
  saveLabel,
  onCancel,
  children,
}: {
  title: string;
  meta: string;
  dirty: boolean;
  busy: boolean;
  saveLabel: string;
  onCancel: () => void;
  children?: ReactNode;
}) {
  return (
    <div className="profile-sticky" data-testid="profile-sticky">
      <div className="profile-sticky-main">
        <span className="profile-sticky-name" data-testid="profile-sticky-name">
          {title}
        </span>
        <span className="profile-sticky-meta" data-testid="profile-sticky-meta">
          {meta}
        </span>
      </div>
      <span
        className={`profile-sticky-state${dirty ? ' dirty' : ''}`}
        data-testid="profile-dirty-state"
      >
        {dirty ? 'Unsaved changes' : 'No unsaved changes'}
      </span>
      <div className="profile-sticky-actions">
        {children}
        <button className="button primary" type="submit" disabled={busy} data-testid="profile-save">
          {saveLabel}
        </button>
        <button
          className="button subtle"
          type="button"
          onClick={onCancel}
          data-testid="profile-cancel"
        >
          Cancel
        </button>
      </div>
    </div>
  );
}

/** ProfileShareButton (Phase 1 D-P0b): promote/demote visibility.
 *  Creator or admin only — the server enforces it and this can 403. */
export function ProfileShareButton({
  profile,
  busy,
  setBusy,
  onNotify,
  onReload,
}: {
  profile: ConfigProfile;
  busy: boolean;
  setBusy: (value: boolean) => void;
  onNotify?: (message: string, tone?: 'info' | 'error') => void;
  onReload: () => void;
}) {
  return (
    <button
      className="button subtle"
      type="button"
      data-testid={profile.visibility === 'private' ? 'profile-share' : 'profile-unshare'}
      disabled={busy}
      onClick={() => {
        setBusy(true);
        const action =
          profile.visibility === 'private'
            ? api.shareConfigProfile(profile.id)
            : api.unshareConfigProfile(profile.id);
        action
          .then(() => {
            onNotify?.(
              profile.visibility === 'private'
                ? `Shared ${profile.name} — every editor can now use it.`
                : `${profile.name} is private again.`,
            );
            onReload();
          })
          .catch((reason: Error) => onNotify?.(reason.message, 'error'))
          .finally(() => setBusy(false));
      }}
    >
      {profile.visibility === 'private' ? 'Share' : 'Unshare'}
    </button>
  );
}
