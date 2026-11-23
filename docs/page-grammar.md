# Page grammar

Loom’s asset pages share one interaction grammar. A new page should feel like a
new room in the same building: the same title, primary action, row shape,
loading/empty/error behavior, editor footer, destructive dialog, and keyboard
rules. The implementation lives in `web/src/components/assets/` and the design
contract is [`product/design-contract.md`](product/design-contract.md).

The acceptance rule:

> One obvious primary action; effective values and origins stay honest; advanced
> power stays one level below the surface.

## Surface map

| Surface | Route | Shape |
|---|---|---|
| Profiles | `/profiles` | Node-type cards with live counts, one **New profile** action, advanced-asset links. |
| Profiles for a node type | `/hub/:nodeType` | Searchable profile list, **New profile**, row actions, mapping/contract count links. |
| New / edit profile | `/hub/:nodeType/new`, `/hub/:nodeType/:id/edit` | Profile form with Identity / Configuration / Trust areas and a sticky save bar. |
| Read-only profile | `/hub/:nodeType/:id` | Basics, configuration, credentials, bundle and used-by summary. |
| Data contracts | `/schemas` | Asset list + editor, visibility, usage, version history and delete confirmation. |
| Mappings | `/mappings` | Asset list + editor, kind/type filters, usage and history. |
| Scripts | `/scripts` | Asset list + editor, script test/history/usage, language-aware source. |
| Connectors | `/connectors` | Asset list + editor, visibility and usage. |
| Credentials | `/credentials` | Admin asset list, write-only values, delete confirmation. |
| Workflows | `/workflows` (`/templates` is a legacy alias) | Workflow list; run detail adds replay/diff. |
| Operator pages | `/audit`, `/capabilities`, `/ops` | Status/table pages using the same error, permission and loading grammar. |

`/profiles/:nodeType…` legacy links redirect to the `/hub/:nodeType` family;
`/hub` redirects to `/profiles`.

## List grammar

Every object list has:

- one title in user vocabulary and a short explanation;
- one primary create action in the page header;
- search where the collection can grow; filters where the collection has
  meaningful kinds;
- stable rows exposing identity, description, owner/version/usage or
  visibility, tags, and one primary row action;
- secondary actions in a consistent action set, never scattered across the row;
- loading, empty, filtered-empty, error, offline and permission states.

**A failed primary load is never silently swallowed and never rendered as an
empty list.** The page renders the classified failure in place of the list and
offers Retry when retrying can help.

`ListSearch` owns the search input, clear button, count and busy announcement.
`AssetRow` is the shared row shell: identity and description on the left,
metadata under them, actions aligned right, and an optional detail panel below.

The profiles landing is card-based rather than row-based because node types are
a small, stable catalog; it still uses the same primary action, first-run empty
state and failure state.

## Shared kit

| Component | Responsibility |
|---|---|
| `AssetRow` | One row shell: identity, description, meta, tags, actions, optional detail. |
| `AssetState` | `AssetLoadingState`, `AssetEmptyState`, `AssetFilteredEmptyState`, `AssetErrorState`. |
| `ListSearch` | Search field, clear action, count, `aria-live` busy/count announcement. |
| `VisibilityControl` | Shared/private chip and share/unshare control; destructive or privileged changes still confirm. |
| `UsagePanel` | Published pins and used-by counts; explains why deletion is blocked. |
| `OriginBadge` | Text origin label for a field: Inherited, Override, From bundle, Resolved at publish. |
| `AssetEditorPanel` | Always-visible editor header/body/footer with status, Cancel and Save. |
| `ConfirmActionDialog` | Accessible alert dialog used directly or through `confirmAction`. |
| `useDialogFocus` | Focus entry, Tab trap, Escape close and trigger focus restore. |

The shared dialog is mounted once by `ConfirmHost` (`web/src/confirm.tsx`), so
pages and hooks call `confirmAction({...})` and await a real dialog instead of
using `window.confirm`.

## Editor grammar

Every asset editor has:

- a stable header with identity/status and history/usage access;
- one primary content area;
- advanced controls in a disclosure, not mixed into the default path;
- a footer that stays visible, with **Save** and **Cancel**;
- a visible save state: `All changes saved`, `Unsaved changes`, or `Saving…`;
- validation and save errors next to their cause, with a 409 stale-version
  recovery message where the asset is versioned;
- history and usage available without blocking the edit.

`AssetEditorPanel` implements the stable header/body/footer, the live status
line and the shared testids (`asset-editor-save`, `asset-editor-cancel`,
`asset-editor-error`). The profile form uses the same grammar with
`ProfileStickyBar` and a URL-synced tab structure.

### Profile form

The profile form is plated to its node type and edits a **partial overlay**:

- **Identity** — name, description, owner, tags, visibility.
- **Configuration** — **Defaults** (profileable catalog fields, credentials,
  retry, raw-JSON escape hatch), **Data** (HTTP parameter groups and bundle
  mappings), **Included behavior** (the profile bundle).
- **Trust** — **Test**, **Effective preview**, **History**, **Used by**.

Fields left empty are omitted from the saved profile; an empty profile is
rejected unless it sets a field, a retry policy or a bundle asset. The raw-JSON
mode is the documented escape hatch for exotic values. The Effective preview
composes the profile against a synthetic empty node or a real usage site and
shows one origin per value.

The URL carries the selected area/section so a reload or shared link lands on
the same place; `?tab=` aliases from older builds still resolve.

### Node Inspector

The Inspector is summary-first: node identity, profile identity and picker,
effective values with origins, bundled assets, then validation/issues. Its tab
inventory is:

1. **Configuration** — always, and the default.
2. **Data** — every type except `trigger.cron` and `trigger.manual`.
3. **Runtime** — retry, failure handling, batch, webhook tester and run data.
4. **Advanced** — advanced fields, ports and the raw node-config JSON editor.

The profile is not a tab; its identity and picker stay in the stable summary
above the tabs. Tabs use `role="tablist"`, `aria-controls`, roving arrow-key
activation and `inspector-tab-<id>` / `inspector-panel-<id>` testids. Normal-mode
forms hide publish-computed fields such as `data.map.mappingRows`; the Advanced
JSON mode remains the escape hatch.

## Destructive actions

High-impact deletion uses the shared accessible dialog:

- it names the object;
- it explains usage consequences and whether deletion is blocked;
- it offers **View usage** when that explains the block;
- it traps focus, closes on Escape when no request is in flight, and returns
  focus to the trigger;
- it keeps a server error inside the dialog so the user can retry or cancel;
- it disables the confirm button while the action is busy.

Low-risk graph edits use visible Undo instead. No user-facing code uses raw
`window.confirm`.

## State grammar

`web/src/loadState.ts` classifies every primary load into `permission`,
`offline` or `error`; pages render the matching `AssetState` rather than
collapsing the failure into “no results”.

| State | Required content | Implementation |
|---|---|---|
| Loading | Calm, preserves layout, does not imply success. | `AssetLoadingState` (`role="status"`). |
| Empty | Explains the object and offers the primary action. | `AssetEmptyState`. |
| Filtered empty | Says no results match and offers **Clear filters**. | `AssetFilteredEmptyState`. |
| Error | Names the failed operation, preserves data, offers Retry when retryable. | `AssetErrorState` (`role="alert"` for errors). |
| Offline | Non-alarming, says nothing was changed, offers Retry. | `AssetErrorState` with `notice` tone and `role="status"`. |
| Permission denied | Names the required role or capability; no retry loop. | classified 401/403, Retry hidden. |

Retry is shown for network failures, 429 and 5xx. A 401/403 is a permission
state, not a transient error. A failed primary load suppresses empty states so
the page cannot say “nothing here” when it failed to read the list.

## Accessibility and responsive policy

- Every surface is keyboard reachable.
- Every interactive control has an accessible name.
- Modals trap focus, close on Escape and restore the trigger.
- Origin is readable as text and never conveyed by colour alone.
- Validation messages are programmatically associated with fields.
- Live regions announce asynchronous errors without trapping focus.
- Touch targets remain comfortable on mobile.
- The desktop graph editor retains advanced behavior; mobile supports a safe
  read-only graph plus simple node edits.
- Asset rows wrap their action cluster onto its own line at narrow widths
  instead of clipping or overflowing the viewport.

## Test contract

Pages that change one of these surfaces must keep the relevant tests green:

| Claim | Test |
|---|---|
| Shared list/editor/state grammar | `web/e2e/asset-grammar.spec.ts` |
| Destructive dialog semantics | `web/e2e/destructive-actions.spec.ts` |
| Primary-load error, offline, permission | `web/e2e/load-states.spec.ts` |
| Axe checks on product/operator pages | `web/e2e/a11y.spec.ts` |
| Keyboard tabs, shortcuts, focus | `web/e2e/keyboard.spec.ts` |
| Viewer/editor/admin visibility | `web/e2e/permissions.spec.ts` |
| Phone layout and safe editing subset | `web/e2e/mobile.spec.ts` |
| Profile ownership and origin badges | `web/e2e/profile-ownership.spec.ts` |
| Profile form, history, preview, test | `profile-editor`, `profile-history`, `profile-preview`, `profile-test`, `profile-used-by` specs |

Pure layout and merge rules are unit-tested in `web/src/__tests__/` (for
example `formLayout.test.ts`, `paramRows.test.ts`, `effectiveParity.test.ts`).
Playwright assertions are deterministic DOM/geometry checks; screenshots are
artifacts, not assertions.

## Review checklist

A page or editor change states:

- the one obvious primary action;
- effective-value and origin behavior, if config is shown;
- explicit ownership state, if a profile is involved;
- which shared destructive dialog is used, if any;
- which loading/empty/error/offline/permission states changed and their tests;
- desktop and narrow-width verification;
- keyboard and screen-reader verification;
- the test names for each acceptance claim.

Temporary exceptions state user impact and the issue that closes them.
