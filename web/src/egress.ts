// Client mirror of egress.ValidateEntry (internal/egress/egress.go).
// Keep the two in lockstep: the egress dialog and validateWorkflow use
// this so an invalid entry is rejected before the server ever sees it,
// instead of the save silently failing on a 400.
export function validateEgressEntry(entry: string): string | null {
  const trimmed = entry.trim();
  if (!trimmed) return 'Entry is empty';
  let parsed: URL;
  try {
    parsed = new URL(trimmed);
  } catch {
    return `Invalid entry “${entry}”: it must start with https://, http:// or postgres://`;
  }
  const scheme = parsed.protocol.replace(/:$/, '');
  if (!['http', 'https', 'postgres'].includes(scheme)) {
    return `Invalid entry “${entry}”: scheme must be http, https or postgres`;
  }
  if (!parsed.hostname) {
    return `Invalid entry “${entry}”: missing host`;
  }
  if (parsed.pathname && parsed.pathname !== '/') {
    return `Invalid entry “${entry}”: paths are not allowed (destination-level only)`;
  }
  if (parsed.hostname.replace(/^\*\./, '').includes('*')) {
    return `Invalid entry “${entry}”: wildcard only allowed as a leading “*.”`;
  }
  return null;
}
