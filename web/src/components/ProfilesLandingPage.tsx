import { useEffect, useState } from 'react';
import { api } from '../api';
import { classifyCurrentLoadFailure, type LoadFailure } from '../loadState';
import type { NodeDef } from '../types';
import { AssetErrorState } from './assets/AssetState';
import { AssetRowMenu } from './assets/AssetRowMenu';

// Configuration profiles landing (v4.1 E1 + UX Phase 2 D2-1): the
// profiles-first primary surface. Every node type with profileable
// catalog fields gets a card with its live profile count; the card
// opens the type's merged page at /hub/:nodeType. Data contracts and
// mappings stay one link away as advanced assets.

const CATEGORY_ORDER = ['trigger', 'action', 'logic'] as const;

const CATEGORY_LABEL: Record<string, string> = {
  trigger: 'Triggers',
  action: 'Actions',
  logic: 'Flow control',
};

export function ProfilesLandingPage({
  defs,
  onNavigate,
}: {
  defs: NodeDef[];
  onNavigate: (path: string) => void;
}) {
  const [counts, setCounts] = useState<Record<string, number> | null>(null);
  const [failure, setFailure] = useState<LoadFailure | null>(null);

  const load = () => {
    setFailure(null);
    api
      .hubOverview()
      .then((result) => setCounts(result.profiles ?? {}))
      .catch((error) => {
        setCounts(null);
        setFailure(classifyCurrentLoadFailure(error));
      });
  };
  useEffect(load, []);

  const profileableDefs = defs.filter((def) =>
    Object.values(def.config ?? {}).some((field) => field.profileable),
  );

  const categories = CATEGORY_ORDER.map((category) => ({
    category,
    defs: profileableDefs.filter((def) => def.category === category),
  })).filter((group) => group.defs.length > 0);

  // One direct create action from the landing: the flagship HTTP node type
  // when it is profileable, otherwise the first profileable type.
  const defaultProfileType =
    profileableDefs.find((def) => def.type === 'http.request')?.type ??
    profileableDefs[0]?.type ??
    'http.request';

  const total = Object.values(counts ?? {}).reduce((sum, count) => sum + count, 0);

  return (
    <main className="page-shell" id="main-stage" tabIndex={-1}>
      <div className="page-body">
      <header className="page-head">
        <div>
          <h1>Profiles</h1>
          <p className="page-sub">
            Reusable, non-secret node configuration, managed on a dedicated page per node type.
            Profiles apply at publish; a node&rsquo;s own fields always win. Secret fields never live
            here.
          </p>
          {counts && (
            <p className="lede" data-testid="profiles-landing-lede">
              {total} profile{total === 1 ? '' : 's'} across {profileableDefs.length} node type
              {profileableDefs.length === 1 ? '' : 's'}
            </p>
          )}
        </div>
        <div className="hub-head-actions">
          <button
            type="button"
            className="button primary"
            data-testid="profiles-new"
            onClick={() => onNavigate(`/hub/${encodeURIComponent(defaultProfileType)}`)}
          >
            New profile
          </button>
          <AssetRowMenu label="Advanced assets" testId="advanced-assets">
            <button type="button" onClick={() => onNavigate('/schemas')}>
              Data contracts
            </button>
            <button type="button" onClick={() => onNavigate('/mappings')}>
              Mappings
            </button>
            <button type="button" onClick={() => onNavigate('/scripts')}>
              Scripts
            </button>
            <button type="button" onClick={() => onNavigate('/connectors')}>
              Connectors
            </button>
          </AssetRowMenu>
        </div>
      </header>

      {failure && <AssetErrorState failure={failure} onRetry={load} />}

      {/* 6.5: first-run guidance — when no profile exists yet, explain
          what reuse buys before the empty per-type counts. */}
      {counts && total === 0 && (
        <div className="empty-panel" data-testid="profiles-first-run">
          <h2>No profiles yet</h2>
          <p className="muted">
            A profile stores one node type&rsquo;s non-secret defaults — a base URL, timeouts,
            mappings — once. Pin it from any node of that type: the node&rsquo;s own fields always
            win, and publishing inlines the pinned version. Pick a node type below to create the
            first one.
          </p>
          <button
            type="button"
            className="button primary"
            onClick={() =>
              onNavigate(`/hub/${encodeURIComponent(profileableDefs[0]?.type ?? 'http.request')}`)
            }
          >
            Create the first profile
          </button>
        </div>
      )}

      {categories.map(({ category, defs: groupDefs }) => (
        <section className="inspector-section" key={category} aria-label={CATEGORY_LABEL[category]}>
          <h2>{CATEGORY_LABEL[category]}</h2>
          <div className="hub-type-grid" data-testid="profiles-landing-grid">
            {groupDefs.map((def) => {
              const count = counts?.[def.type] ?? 0;
              return (
                <button
                  type="button"
                  className="hub-type-card"
                  key={def.type}
                  data-testid={`profiles-type-${def.type}`}
                  onClick={() => onNavigate(`/hub/${encodeURIComponent(def.type)}`)}
                >
                  <span className="hub-type-name">{def.displayName || def.type}</span>
                  <code className="hub-type-id">{def.type}</code>
                  <span className="hub-type-description">{def.description}</span>
                  <span className="hub-type-counts">
                    {counts && (
                      <span className="count-pill">
                        {count} profile{count === 1 ? '' : 's'}
                      </span>
                    )}
                  </span>
                </button>
              );
            })}
          </div>
        </section>
      ))}
      </div>
    </main>
  );
}
