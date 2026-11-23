/**
 * Known moderate axe violations, keyed by surface.
 *
 * Surface keys are `<theme> <route name>` (for example `light home`) or
 * `account dialog`. Values map an axe rule id to the allowed node count.
 *
 * Serious and critical findings are never baselined. When a new moderate
 * finding appears, either fix it or add the exact rule id and count here with
 * a short reason; never widen the gate silently.
 *
 * The baseline is intentionally empty as of 2026-11-14: the audited routes
 * have no moderate findings, so any new one fails the gate.
 */
export const MODERATE_BASELINE: Record<string, Record<string, number>> = {};
