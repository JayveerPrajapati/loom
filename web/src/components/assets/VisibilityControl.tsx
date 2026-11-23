export function VisibilityChip({
  visibility,
  testId = 'asset-visibility-chip',
}: {
  visibility?: string;
  testId?: string;
}) {
  const shared = (visibility ?? 'shared') === 'shared';
  return (
    <span className={`chip ${shared ? 'chip-shared' : 'chip-private'}`} data-testid={testId}>
      {shared ? 'Shared' : 'Private'}
    </span>
  );
}

export function VisibilityControl({
  visibility,
  name,
  labelPrefix = '',
  busy = false,
  onToggle,
}: {
  visibility?: string;
  name: string;
  labelPrefix?: string;
  busy?: boolean;
  onToggle: () => void;
}) {
  const shared = (visibility ?? 'shared') === 'shared';
  return (
    <button
      type="button"
      className="text-button"
      data-testid="visibility-control"
      disabled={busy}
      aria-label={
        labelPrefix
          ? shared
            ? `Make ${labelPrefix}${name} private`
            : `Share ${labelPrefix}${name}`
          : undefined
      }
      onClick={onToggle}
    >
      {busy ? 'Updating…' : shared ? 'Make private' : 'Share'}
    </button>
  );
}
