import type { FieldOrigin } from '../../effectiveNode';

// One origin language for the inspector (Apple-Grade Phase 3 D3-2).
// Every state carries text — never color alone. Local (node-only) fields
// render nothing so the normal case stays quiet.

export type OriginState = 'inherited' | 'override' | 'bundle' | 'published' | 'local';

const LABEL: Record<OriginState, string> = {
  inherited: 'Inherited',
  override: 'Override',
  bundle: 'From bundle',
  published: 'Resolved at publish',
  local: 'Local',
};

export function originState(
  origin: FieldOrigin | undefined,
  overridden: boolean,
): OriginState | null {
  if (origin === 'profile') return 'inherited';
  if (origin === 'bundle') return 'bundle';
  if (origin === 'node' && overridden) return 'override';
  return null;
}

export function OriginBadge({
  state,
  fieldKey,
  testId,
}: {
  state: OriginState | null;
  fieldKey?: string;
  testId?: string;
}) {
  if (!state) return null;
  return (
    <span
      className={`field-origin origin-${state}`}
      data-origin={state}
      data-testid={testId ?? (fieldKey ? `origin-badge-${fieldKey}` : undefined)}
    >
      {LABEL[state]}
    </span>
  );
}
