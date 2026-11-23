# Product design contract

- Status: normative (2026-11-14)
- Applies to: every page, editor, dialog, list and node form in Loom.
- Implementation map: [`docs/page-grammar.md`](../page-grammar.md). The grammar
  names the surfaces, routes and shared components; this contract states the
  rules those surfaces must satisfy.
- Ownership vocabulary detail: [`docs/ownership.md`](../ownership.md).
- Accessibility and responsive rules are part of the acceptance bar; they are
  not a later polish pass.

## 1. Principles

1. **One rule per concept.** One origin resolver, one list row, one editor
   shell, one confirm dialog, one error component. A second implementation of
   an existing rule is a review failure.
2. **Store intent, derive display.** Ownership is stored (`profileOverrides`).
   Origin, effective value and warnings are derived by one resolver. Never
   infer ownership from value equality when explicit evidence exists.
3. **Keep power one level down.** Summary first, advanced disclosure second,
   raw JSON last. Simplification must not remove capability.
4. **A failed read is never an empty result.** Loading, empty, filtered-empty,
   error, offline and permission-denied are distinct states.
5. **Protect the differentiators.** Do not change the event hash chain, publish
   snapshot immutability, version pinning, the SSRF guard or the subprocess
   isolation model as part of a UI change.
6. **Gate by tests, not by review.** A banned pattern or required invariant
   gets a repo test.

## 2. Object-list grammar

Every object list MUST have:

- one title in user vocabulary and a short explanation of the object;
- one primary create action in the page header;
- search when the collection can grow, filters when it has meaningful kinds;
- stable rows exposing identity, description, owner/version/usage or
  visibility, tags, and one primary row action;
- secondary actions collected in one action set, never scattered across the
  row;
- all six states from §5, including filtered-empty and offline;
- counts and metadata derived from the actual response, never invented or
  estimated.

The shared row and state components are the default. A page that needs a
different shape (for example the profile node-type cards) still follows the
same primary action, first-run empty state and failure state.

## 3. Object-editor grammar

Every asset editor MUST have:

- a stable header with identity/status and access to history and usage;
- one primary content area;
- advanced controls in a disclosure, not mixed into the default path;
- a footer that stays visible with **Save** and **Cancel**;
- a visible save state: `All changes saved`, `Unsaved changes` or `Saving…`;
- validation and save errors next to their cause;
- a 409 stale-version recovery message where the asset is versioned;
- history and usage available without blocking the edit.

A profile form edits a partial overlay: omitted fields stay omitted; an empty
profile is rejected unless it sets a field, a retry policy or a bundle asset.
The raw-JSON mode is the documented escape hatch, not the default path.

## 4. Destructive-action grammar

High-impact deletion and irreversible actions MUST use the shared accessible
dialog, directly or through `confirmAction`:

- name the object and state the consequence;
- explain usage blocks and offer **View usage** when that explains the block;
- trap focus, autofocus the safe action, and restore focus to the trigger;
- close on Escape **only when no request is in flight**;
- keep a server error inside the dialog so the user can retry or cancel;
- disable confirm and cancel while the request is busy, and show the busy
  state on the confirm label;
- never use `window.confirm` or `window.prompt`.

Low-risk graph edits use visible Undo instead. Undo copy states what it
restores.

## 5. State grammar

| State | Required content | Implementation |
|---|---|---|
| Loading | Calm, preserves layout, does not imply success. | `AssetLoadingState` (`role="status"`). |
| Empty | Explains the object and offers the primary action. | `AssetEmptyState`. |
| Filtered empty | Says no results match and offers **Clear filters**. | `AssetFilteredEmptyState`. |
| Error | Names the failed operation, preserves data, offers Retry when retryable. | `AssetErrorState` (`role="alert"`). |
| Offline | Non-alarming, says nothing was changed, offers Retry. | `AssetErrorState` with notice tone and `role="status"`. |
| Permission denied | Names the required role or capability; no retry loop. | Classified 401/403, Retry hidden. |

Retry is shown for network failures, 429 and 5xx. A 401/403 is a permission
state, not a transient error. A failed primary load suppresses empty states so
the page cannot say "nothing here" when it failed to read the list. Secondary
loads that fail must surface a section-scoped error or warning; they must not
silently become empty arrays.

## 6. Origin and ownership vocabulary

Origin is two facts, not one label:

**Ownership state** (who authored the effective value):

| State | Meaning |
|---|---|
| `inherited` | The profile or bundle supplies the value; the node does not own it. |
| `override` | The node explicitly authored the value where the profile also supplies one. The marker, not equality, makes it an override. |
| `local` | The node supplies a key the profile does not. |

**Source** (where the value came from): `profile`, `bundle`, `node`, `catalog`
or `script`. A published workflow additionally records `configSources`; a value
read from the stored publish snapshot is shown as **Resolved at publish**.

**Derived status:** `invalid` when the effective value or ownership metadata
violates the catalog, a contract or the doctor rules.

UI labels: **Inherited**, **Override**, **From bundle**, **Resolved at publish**.
`local` stays quiet. Origin is always readable as text; colour alone never
conveys it. The node Inspector shows one origin per key, and parameter-group
rows show the winning side per row, not one badge for the whole group.

`profileOverrides` is authoritative when present, including `{}`. A key mapped
to `true` is an explicit override; an omitted key is inherited when the profile
supplies it; a `false` value is invalid. A node without the map is a legacy
node and keeps equality inference only until migration or the next explicit
edit.

## 7. Version and usage vocabulary

| Term | Meaning | Display rule |
|---|---|---|
| Draft | The editable workflow/profile/asset row. | `draft` or no version chip. |
| Version | An immutable published or appended snapshot. | `vN`. |
| Pinned version | The version a workflow, job or node resolves against. | State the pin, not only the current version. |
| Usage pin | A record that a published workflow version uses an asset or profile. | Powers used-by counts and delete guards. |
| Used by | The workflows/profiles that pin the object. | A real count; no placeholder numbers. |

Deleting a pinned object is blocked. The dialog names the pin and offers the
usage view. A publish snapshot is immutable; changing the draft does not
rewrite history.

## 8. Responsive behavior policy

- The desktop graph editor keeps the full editing surface.
- Mobile supports a safe read-only graph plus simple node edits (rename,
  select fields, copy ids, approve/reject, simple non-secret config).
- Graph editing that is not supported on a narrow screen MUST show a clear
  "Open on desktop to edit" state instead of a clipped or half-hidden canvas.
- Asset rows wrap their action cluster onto its own line at narrow widths
  instead of clipping or overflowing the viewport.
- Touch targets on coarse pointers are at least 44px, except controls that are
  explicitly desktop-only and cannot be reached on a touch device.
- No horizontal overflow on any page at the supported widths.

## 9. Accessibility policy

- Every surface is keyboard reachable in a logical order.
- Every interactive control has an accessible name.
- Modals trap focus, close on Escape when allowed, and restore the trigger.
- Validation messages are programmatically associated with their field
  (`aria-describedby`, `aria-invalid` where applicable).
- Live regions announce asynchronous errors and lockout state without
  re-announcing on every tick.
- Origin and status are conveyed by text, not colour alone.
- Text and non-text contrast meet WCAG 2.1 AA.
- `prefers-reduced-motion` is honoured; motion never blocks an action.
- Every interactive element has a visible `:focus-visible` style.

## 10. Visual system constraints

These are non-negotiable for Loom UI work:

- Every colour and font comes from a `:root` token; no ad-hoc hex values or
  font stacks in components.
- No `transition: all`. Animate specific properties only.
- Do not animate `width`, `height`, `top` or `left`; use transforms and
  opacity for motion.
- `overflow-x: clip` on `html` and `body`; no page-level horizontal scroll.
- Never invent metrics, testimonials, logos, case-study counts or usage
  numbers. Every displayed number comes from a real response.

## Appendix A — Feature review checklist

A new or changed page, editor or node form states:

- [ ] the one obvious primary action;
- [ ] the effective-value and origin behavior, if config is shown;
- [ ] the explicit ownership state, if a profile is involved;
- [ ] which shared destructive dialog is used (or why the action is not
      destructive);
- [ ] which loading, empty, filtered-empty, error, offline and permission
      states changed, and their tests;
- [ ] desktop and narrow-width verification;
- [ ] keyboard and screen-reader verification;
- [ ] the test names for each acceptance claim;
- [ ] that banned patterns (`window.confirm`, `window.prompt`, silent primary
      catches) are not reintroduced;
- [ ] that the visual-system constraints in §10 hold.

## Appendix B — Temporary exceptions

A temporary exception is allowed only when it states:

1. the user impact;
2. why the normal rule cannot be met now;
3. the issue or milestone that removes the exception.

An exception without a closing issue is a defect, not an exception.
