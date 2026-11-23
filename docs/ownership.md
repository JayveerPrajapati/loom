# Configuration ownership

Loom stores **who authored a value**, not just the value. A node can carry its
own configuration and reference a reusable configuration profile; publish
resolves both into one effective document. Ownership makes that merge legible
before and after publish, and makes a deliberate override survive a profile
upgrade even when the two values happen to be equal.

The one-sentence rule:

> Store intent explicitly, show effective behavior honestly, and never infer
> ownership from value equality when explicit evidence exists.

## Vocabulary

| State | Meaning | Inspector label |
|---|---|---|
| `inherited` | The profile supplies the value; the node does not own it. | Inherited |
| `override` | The node authored the value where the profile also supplies one. | Override |
| `local` | The node supplies a key the profile does not. | No badge (quiet normal case) |
| `bundle` | A reusable asset is supplied by the profile bundle. | From bundle |
| `resolved` | A publish-time value whose source is recorded. | Resolved at publish |
| `invalid` | The effective value or ownership metadata is structurally invalid. | Validation or doctor finding |

The [design contract](product/design-contract.md) separates ownership state
from source: a bundle-supplied value is inherited from a bundle asset. The
Inspector’s literal label is **From bundle**.

## What is stored

A workflow node has three related fields:

```json
{
  "id": "fetch",
  "type": "http.request",
  "profileRef": "profile-http-prod",
  "profileOverrides": { "timeout": true },
  "config": {
    "url": "https://api.example.com/orders",
    "timeout": 45
  }
}
```

| Field | Written by | Meaning |
|---|---|---|
| `profileRef` | editor / API | The configuration profile id pinned by this node. |
| `profileOverrides` | editor / API / migration | Explicit ownership for keys the node stores while the profile also supplies them. |
| `configSources` | publish only | Per-key provenance: `node`, `profile`, `bundle`, `catalog` or `script`. Advisory metadata; execution never reads it. |

`profileOverrides` is subtly different from a normal optional map:

- **The map itself is the authority.** A present map, including `{}`, means
  ownership is explicit.
- **A key mapped to `true` is an explicit override.** It stays the node’s value
  even when it equals the profile’s value.
- **A key omitted from the map is inherited** when the profile supplies it.
- **A key mapped to `false` is invalid.** Inherited keys are omitted, not
  recorded as false.
- **The map has no `omitempty` in the workflow JSON.** Dropping an empty map
  would silently revert the node to legacy equality inference.

`configSources` is cleared on a draft save because an edit invalidates the
previous publish-time resolution. It is recomputed on the next publish. The
published version, and the workflow row that publish replaces, retain it.

## Effective-value precedence

Publish applies the layers in this order (highest precedence first):

1. **Node plain target object.** For `http.request`, a node-level `body`,
   `query`, `headers` or `pathParams` object owns the whole group; profile
   parameter rows for that group are dropped.
2. **Node parameter rows.** `bodyFields`, `queryFields`, `headerFields` and
   `pathFields` merge with profile rows **per row name**. The node’s row wins
   and keeps its position; profile rows with new names follow.
3. **Node reference slots.** `mappingRef`, `mappingRefs`, `inputSchemaRef`,
   `outputSchemaRef`, `scriptRef` and credentials that the node set itself win.
4. **Profile parameter rows and profile configuration.**
5. **Profile bundle references.** Bundle mappings fill empty mapping
   groups; a bundle script fills an empty `scriptRef`; bundle schema refs fill
   empty schema slots. Anything the node set wins. The referenced assets are
   resolved into rows/source later in the same publish.
6. **Catalog defaults.** Applied last, only where no earlier layer supplied the
   key. A default never suppresses a profile, and a profile never re-enables a
   key the node explicitly set to an empty or `none` value.

Profile credentials fill only empty slots. A node retry policy wins over the
profile’s; the profile’s retry applies only when the node has none.

The publish sequence is visible in `internal/server/workflows.go`: profile refs,
mapping refs, schema refs, script refs, connector refs, then
`ApplyCatalogDefaults`. The same profile merge is mirrored in the browser by
`web/src/paramRows.ts` and `effectiveNode.ts`.

## Editing behavior

### Attach

Attaching a profile to a **sparse node** fills the form from the profile and
marks no overrides.

Attaching to a node that already has values is deliberately conservative:

- an existing value for a profile-supplied key becomes an **override**;
- a key the profile does not supply stays local;
- re-attaching or switching profiles preserves existing explicit overrides;
- an inherited key from the previous profile follows the new profile.

The rule lives in `web/src/profileOwnership.ts` (`profileAttachPatch`). A new
node has no stored values, so a profile applies cleanly without suppressing
catalog defaults.

### Override and reset

Editing a profileable field that the profile supplies marks that key `true` in
`profileOverrides`. The badge reads **Override**. Reset is offered only for an
explicit override: it removes the node’s value and the marker, so the profile
value becomes effective again.

A deliberate override may equal the profile value. The marker, not equality,
keeps it an override. This is the case that survives a profile upgrade.

### Detach

Detaching materializes the effective configuration into the node before
clearing `profileRef` and `profileOverrides`: inherited config, bundle-filled
references and profile retry become node-owned values. Nothing is silently
lost. Removing `profileRef` by hand in raw JSON is not detach and does not
materialize bundle values.

### Inspector origins

The node Inspector shows one origin per key. Normal local values stay quiet;
profile and bundle values carry a text badge. Parameter-group rows show the
winning side per row, not one badge for the whole group. Published snapshots
can show **Resolved at publish** where the UI is reading the stored snapshot
rather than the draft.

The origin helpers are `web/src/effectiveNode.ts` and
`web/src/components/assets/OriginBadge.tsx`.

## Publish and upgrade

Publish resolves the profile into the workflow document and records
`configSources` per key. The immutable `workflow_versions` row is the run and
replay input; it is also the disclosure boundary for private assets. The current
`workflows` row is replaced with the same resolved snapshot, so after a publish
the editor’s “current workflow” already carries resolved values plus
`profileRef`.

That resolved draft is why an equal-value override needs explicit ownership:
without a marker, a stored value equal to the profile value is indistinguishable
from a value that came from the profile.

## Draft runs

A run started before publish — manual, webhook, event or workflow-level cron —
resolves the same chain on a copy of the stored row before validation: profile
values and bundle refs, contracts, mappings, scripts, connector templates and
catalog defaults. The stored draft stays sparse and unchanged; the run snapshot
carries the resolved copy, exactly like a published version. This is why the
editor's effective preview and a draft run agree even when the node has no
config of its own.

Draft runs resolve against the profile's current version, the same rule save
validation uses; publish still pins a version. Running a row that was already
resolved at publish re-resolves to the same values, so it is a no-op given
explicit ownership. If a referenced profile or asset was deleted, the run is
rejected with the same named issue codes save and publish report (`missing_profile`,
`missing_schema`, `missing_mapping`, `missing_script`, `missing_connector`)
instead of a node-level `missing_config`.

The upgrade preview (`UpgradeWorkflowProfiles`, exposed as
`POST /api/workflows/{id}/upgrade-preview`) re-resolves against the profile’s
current version:

- explicit overrides always stay, including equal values;
- an explicitly inherited key follows the new profile;
- a key the new profile no longer supplies becomes local rather than being
  deleted;
- a never-published sparse draft is filled from the latest profile;
- a legacy node with no ownership map keeps equality inference until migration
  or the next explicit edit;
- `configSources` is cleared because the next publish recomputes it.

## Legacy migration

Legacy nodes — a `profileRef` with **no** `profileOverrides` map — keep the
equality branch below so un-migrated data stays usable for one release. The
store never stamps them at open time; migration is an explicit operator
command with a backup gate and a rollback table.

Run it as a sequence:

```sh
# 1. Plan: writes the report, changes nothing.
loom ownership migrate --data ./data --dry-run

# 2. Back up and verify the snapshot.
loom backup --data ./data --out loom-backup-$(date +%Y%m%d-%H%M%S).tar.gz
loom backup verify --backup loom-backup-....tar.gz

# 3. Apply: requires a backup created within 24h and verified by the command.
loom ownership migrate --data ./data --apply --backup loom-backup-....tar.gz

# 4. If needed, restore the exact pre-migration drafts.
loom ownership migrate --data ./data --rollback
```

`--apply` refuses when the archive is older than 24 hours, fails verification,
or a draft changed since the dry-run. Each changed node is written to the
`ownership_migration_backup` table with its exact original and migrated
workflow docs; `--rollback` restores the latest migration and refuses when a
draft changed after the apply, so it never discards later edits. Every applied
decision also writes an `ownership.migrated` audit event.

The classifier only touches nodes with a `profileRef` and **no**
`profileOverrides` map; a node that already has a map is skipped, which makes a
second run a no-op. Stored config values are never changed.

For each stored key, the migration marks the key inherited only when:

1. the pinned profile version supplies the key;
2. the stored value equals the pinned version’s value;
3. the last published snapshot contains that node and key; and
4. the stored value equals the published value.

Everything else is preserved as an override. The exact reasons recorded in the
report are:

| Reason | Decision |
|---|---|
| matches the pinned profile and the last published snapshot | inherited |
| value differs from the pinned profile | override |
| no published snapshot evidence; ambiguous ownership | override |
| key was added after the last publish | override |
| draft value was edited after the last publish | override |
| profile added the key after the pin; preserving the stored value | override |
| no pinned profile version; preserving the stored value | override |
| profile config unavailable; preserving the stored value | override |

The report is written by the dry-run (and re-written by `--apply` before the
transaction):

`<data>/ownership-migration.json` (mode `0600`)

```json
{
  "generatedAt": "2026-11-14T00:00:00Z",
  "workflows": 1,
  "nodes": 2,
  "inherited": 3,
  "overrides": 1,
  "changes": [
    {
      "workflowId": "orders",
      "workflowName": "Orders",
      "nodeId": "fetch",
      "nodeType": "http.request",
      "key": "timeout",
      "decision": "override",
      "reason": "draft value was edited after the last publish",
      "pinnedProfileVersion": 4
    }
  ]
}
```

The report is always written by the command, including the zero-change case. It
is a diagnostic file: it is not part of `loom backup`, it contains keys and
decisions rather than config values, and a failed apply still leaves the
decisions inspectable. The implementation is
`internal/store/ownership_migration.go`; the classifier is in
`internal/store/ownership.go`.

## Doctor

`loom doctor` reads every draft and published workflow version and reports
structural ownership findings:

- `profileOverrides` present without `profileRef`;
- a false override value;
- an override key the node does not store;
- an unknown `configSources` origin;
- a `configSources` key the node does not store.

A node with **no** ownership map is compatible, not invalid: that is a legacy
node. An empty map is valid. Any finding makes the doctor verdict non-zero.

```sh
loom doctor --data ./data
loom doctor --data ./data --resume   # continue after the last clean prefix
```

The doctor is read-only and never executes workflow code. It also verifies event
chains, blob hashes, missing/orphaned blobs, Python environment manifests,
subprocess policy, egress policy, and the bootstrap-password warning. See
[upgrade.md](upgrade.md) for the operator procedure and checkpoint behavior.

## Troubleshooting

| Symptom | Likely cause | Action |
|---|---|---|
| A profile change does not reach a node. | The node stores the key as a local value or override. | Inspect the origin badge; reset an explicit override or remove the local value. |
| Two equal values show different origins. | One is an explicit override; the other is inherited. | That is intentional. The override marker preserves intent. |
| A profile value is ignored while the key is absent from the node. | The key may be supplied by a bundle or a mapping; or the profile version pinned by the workflow is older than the profile page. | Check the pinned version and the effective preview, then republish. |
| Doctor reports an ownership finding. | A hand-edited workflow has a false marker, a ghost key, an unknown source, or markers without a `profileRef`. | Fix the document using the named node/key in the finding; rerun doctor. |
| A migration report exists after an upgrade. | Legacy nodes were stamped. | Review the decisions; stored values were not rewritten. Keep or remove the diagnostic file. |
| An override disappeared after a profile upgrade. | The node had no explicit map, and legacy equality inference followed the profile. | Mark the key as an override before upgrading, or use the upgrade preview to review the change. |

## Source and test index

| Concern | Source | Tests |
|---|---|---|
| Stored fields and source constants | `internal/graph/model.go` | graph round-trip tests |
| Publish merge, bundle injection, catalog fallback, upgrade | `internal/store/profiles.go` | `internal/store/profiles_test.go`, `profiles_bundle_test.go`, `profiles_bundle_resolution_test.go` |
| Legacy migration and structural scan | `internal/store/ownership.go` | `internal/store/ownership_test.go`, `ownership_fuzz_test.go` |
| Doctor surfacing | `cmd/loom/doctor.go`, `internal/store/health.go` | `cmd/loom/cli_lifecycle_test.go` |
| Browser merge and origins | `web/src/paramRows.ts`, `effectiveNode.ts`, `profileEffective.ts`, `profileOwnership.ts` | `web/src/__tests__/effectiveParity.test.ts`, `effectiveNode.test.ts`, `profileOwnership.test.ts`, `catalogOwnership.test.ts` |
| Inspector badges and reset | `web/src/components/Inspector.tsx`, `InspectorProfileSection.tsx`, `assets/OriginBadge.tsx` | `web/e2e/profile-ownership.spec.ts` |
