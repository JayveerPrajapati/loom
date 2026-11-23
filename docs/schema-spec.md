# Schema specification

Loom uses three JSON Schema documents as the public contracts:

| Schema | File | Purpose |
|---|---|---|
| Workflow | [`schema/workflow.schema.json`](../schema/workflow.schema.json) | editor file, API payload, test fixture |
| Execution log | [`schema/execution.schema.json`](../schema/execution.schema.json) | run metadata + append-only events; replay is defined over it |
| Node definition | [`schema/node-definition.schema.json`](../schema/node-definition.schema.json) | connector contract consumed by the compiler |

All three use JSON Schema draft 2020-12. The Go loader is stricter than the
schemas: unknown fields are rejected (`DisallowUnknownFields`), and validation
also checks node configs, credentials, ports, reachability, trigger rules and
cycles.

## Workflow document

Required: `id`, `name`, `version`, `nodes`, `edges`.

```json
{
  "id": "publish-approval",
  "name": "Publish with approval",
  "version": 1,
  "settings": {},
  "nodes": [
    {
      "id": "start",
      "type": "trigger.manual",
      "typeVersion": 1,
      "name": "Start",
      "position": { "x": 0, "y": 0 },
      "config": {}
    }
  ],
  "edges": []
}
```

### Root fields

| Field | Type | Rules |
|---|---|---|
| `id` | string | `^[a-z0-9][a-z0-9-]*$`; stable and URL-safe |
| `name` | string | display name |
| `version` | integer | `>= 1` |
| `settings` | object | workflow settings; `variables` values are exposed as `$vars.<name>` |
| `egress` | object | optional `{ "allowlist": ["https://api.example.com", ...] }` enforced per run on top of the operator policy |
| `inputSchemaRef` | string | data-contract id resolved into `inputSchemaFields` at publish |
| `outputSchemaRef` | string | data-contract id resolved into `outputSchemaFields` at publish |
| `inputSchemaFields` | array | publish-resolved workflow input contract |
| `outputSchemaFields` | array | publish-resolved workflow output contract |
| `nodes` | array | at least one node |
| `edges` | array | endpoint references; may be empty only for a single trigger |

Workflow-scoped variables live in `settings.variables`:

```json
{ "settings": { "variables": { "region": "eu-west-1" } } }
```

They are available to expressions as `$vars.region`. Settings remain open for
deployment-specific keys such as `timezone`, `concurrency` and `retention`.

### Node

Required: `id`, `type`, `typeVersion`, `name`, `position`, `config`.

| Field | Type | Rules |
|---|---|---|
| `id` | string | unique within the workflow |
| `type` | string | must exist in the node catalog |
| `typeVersion` | integer | `>= 1`; must match the catalog version |
| `name` | string | display label |
| `position` | object | `{ "x": number, "y": number }` for the canvas |
| `config` | object | validated against the node definition |
| `credentials` | object | credential type → credential ID; never secrets |
| `profileRef` | string | configuration profile id pinned by this node |
| `profileOverrides` | object | explicit ownership for node-authored keys the profile also supplies |
| `configSources` | object | publish-time per-key provenance (`node`, `profile`, `bundle`, `catalog`, `script`) |
| `inputSchemaRef` | string | data-contract id for this node's input |
| `outputSchemaRef` | string | data-contract id checked against this node's main output |
| `inputSchemaFields` | array | publish-resolved node input contract |
| `outputSchemaFields` | array | publish-resolved node output contract |
| `retry` | object | optional `{ maxAttempts, backoffMs, maxDelayMs }`; 0 values use engine defaults |
| `onError` | string | `stop` (default), `continue` (error item on main), or `branch` (error item on the `error` port) |
| `batch` | object | optional `{ itemsPath, continueOnItemError, concurrency }`; runs the node per array element |

#### Ownership and publish provenance

`profileRef` names the profile. `profileOverrides` is authoritative when the map
is present, including `{}`:

- `true` marks an explicit override;
- an omitted key is inherited when the profile supplies it;
- `false` is invalid (inherited keys are omitted);
- a missing map means a legacy node and keeps compatibility inference.

`configSources` is written by publish and cleared on a draft save. It is
advisory metadata for the UI and doctor; the engine and replay never read it.
The full ownership model, migration and troubleshooting are in
[ownership.md](ownership.md).

### Edge and endpoint

An edge is `{ "from": endpoint, "to": endpoint }`; both endpoints require
`{ "node": "<nodeID>", "port": "<portName>" }`.

Validation rules:

- Both node IDs must exist.
- The source port must be one of the source node's `outputs`.
- The destination port must be one of the destination node's `inputs`.
- A trigger node cannot have incoming edges.
- A destination input port can have only one incoming edge; use `flow.merge`
  for multiple branches.
- The source output type must be compatible with the destination input type:
  equal types pass; `any` passes; `json → narrower` passes with a warning;
  `narrower → json` passes; other mismatches fail.
- Exactly one trigger is required.
- The graph must be acyclic.
- Every non-trigger node must be reachable from the trigger.

## Execution log

Required: `run`, `events`.

```json
{
  "run": {
    "id": "run-...",
    "workflowId": "publish-approval",
    "workflowVersion": "sha256:...",
    "status": "done",
    "trigger": "manual",
    "startedAt": "2026-10-22T10:00:00Z",
    "finishedAt": "2026-10-22T10:00:01Z"
  },
  "events": [
    { "seq": 1, "type": "run.started", "at": "2026-10-22T10:00:00Z" }
  ]
}
```

### Run

| Field | Type | Notes |
|---|---|---|
| `id` | string | unique run ID |
| `workflowId` | string | workflow ID |
| `workflowVersion` | string | content hash of the exact workflow document |
| `status` | string | `queued`, `running`, `waiting`, `done`, `failed`, `cancelled` |
| `trigger` | string | `manual`, `webhook`, `cron`, `once`, `event`, `job`, `batch`, `ci`, `subflow`, `replay` |
| `startedAt` | date-time | RFC3339 |
| `finishedAt` | date-time | present for terminal runs |
| `error` | string | present for failed runs |
| `templateVersion` | integer | published template version for job runs |
| `jobId` | string | job that started the run |
| `parentRunId` | string | parent run for a subflow execution |
| `parentNodeId` | string | subflow node in the parent run |
| `rootRunId` | string | first run in an event chain; budgets event fan-out |
| `resumeNode` | string | node where a waiting run is suspended |
| `resumeToken` | string | capability token for resuming a waiting run |
| `resumeTokenExpiresAt` | date-time | expiry of the resume token |
| `actor` | string | creating principal (`user:<id>`, `token:<id>`, `service:*`, `system:*`) |
| `chainVersion` | integer | `0` gap-check only, `1` HMAC, `2` HMAC + actor |
| `chainTag` | string | HMAC over run-level fields, including the effects mode |
| `keyId` | string | signing-key id; empty means the bootstrap (legacy) key |
| `effects` | string | effect-tape mode the run was executed with (`record`, replay modes, empty) |
| `cassetteRunId` | string | run whose tapes a replay consumes (empty for recorded runs) |

`workflowVersion` pins replay to the document that actually ran, not the current
workflow. A terminal run has exactly one of `run.finished`, `run.failed` or
`run.cancelled` as its last lifecycle event.

### Event

Required: `seq`, `type`, `at`.

| Field | Type | Notes |
|---|---|---|
| `seq` | integer | monotonic per run, gap-free, starts at 1 |
| `type` | string | see event types below |
| `at` | date-time | RFC3339 |
| `nodeId` | string | node events |
| `inputHash` | string | content hash of the exact node input |
| `outputHash` | string | content hash of the exact node output |
| `attempt` | integer | `>= 1`; retry attempt |
| `durationMs` | integer | `>= 0` |
| `error` | string | failure detail |
| `data` | object | small metadata; sensitive headers redacted, bodies not automatically redacted; large payloads live in blobs |
| `prevHash` | string | `hash` of the previous event (empty for `seq` 1) |
| `hash` | string | HMAC over the digest fields below |
| `actor` | string | principal; part of the digest when `chain_version >= 2` |

Event types:

`run.started`, `node.started`, `item.started`, `item.finished`, `item.failed`,
`node.finished`, `node.failed`, `node.retried`, `run.waiting`, `run.resumed`,
`approval.notified`, `approval.reminded`, `run.finished`, `run.failed`,
`run.cancelled`, `node.effects` (one tape per node attempt) and
`node.effects.drift` (a replay found a tape mismatch).

`approval.waiting` is the `event` value sent to an approval webhook, not a
stored run-event type.

Payloads are content-addressed. The event stores hashes; `GET /api/blobs/{hash}`
returns the stored bytes with sensitive keys masked at the display boundary.
Replay reads the raw bytes so it reproduces the original run.

`blobs`:
`hash` (primary key; SHA-256 of the exact bytes), `size`, `created_at`. Bytes
live at `<data>/blobs/<first two hex chars>/<hash>`. Prune keeps any blob
referenced by an `input_hash`, `output_hash` or a `node.effects` tape.

## Identity and audit storage

Authentication is stored alongside runs in the same SQLite database.

`users`:
`id`, `email` (unique, case-insensitive), `name`, `password_hash` (argon2id),
`role` (`viewer|operator|editor|admin`), `disabled`, `created_at`, `updated_at`,
`password_changed_at`.

`sessions`:
`token_hash` (primary key; SHA-256 of the cookie value), `user_id`, `created_at`,
`last_seen_at`, `expires_at`, `ip`, `user_agent`. The raw token is never stored.

`api_tokens`:
`id`, `user_id`, `name`, `token_hash` (unique), `role`, `created_at`,
`last_used_at`, `expires_at`, `revoked_at`.

`auth_events`:
`id`, `at`, `type`, `user_id`, `actor`, `ip`, `detail`. These are operational
audit rows and are **not** part of the run HMAC chain.

Run attribution:
`runs.actor` is the creating principal (`user:<id>`, `token:<id>`,
`service:loom-token`, `system:*`). `events.actor` carries the same value and is
included in the HMAC digest when the run's `chain_version >= 2`. Runs with
`chain_version = 1` verify without the actor; `0` is gap-checked only.

Chain digest fields, length-prefixed in order: run id, seq, type, timestamp,
node id, attempt, input hash, output hash, duration, error, actor (v2+), stored
data bytes, previous hash.

## Node definition

Required: `type`, `typeVersion`, `category`, `inputs`, `outputs`, `inputType`,
`outputType`, `config`.

| Field | Type | Notes |
|---|---|---|
| `type` | string | catalog key, e.g. `http.request` |
| `typeVersion` | integer | `>= 1` |
| `category` | string | `trigger`, `action` or `logic`; `ai` is reserved by the JSON schema but no built-in node uses it |
| `displayName` | string | human-facing name in the node library |
| `description` | string | human summary |
| `inputs` | array | input port names |
| `outputs` | array | output port names |
| `inputType` | port type | one of `any`, `json`, `text`, `event`, `order`, `message`, `rows` |
| `outputType` | port type | same set |
| `outputTypeKey` | string | config field that overrides `outputType` |
| `mappingKinds` | array | hub mapping kinds consumed: `item`, `body`, `query`, `headers`, `pathParams` |
| `credentials` | array | credential types the node can use |
| `effects` | string | human-readable side-effect contract ("pure" or what it touches) |
| `optionalCredentials` | array | credential types whose absence is valid |
| `config` | object | field name → field definition |

A field definition requires `type` (`string`, `number`, `boolean`, `array`,
`object` or `any`) and may set `required`, `enum`, `default`, `pattern`, `min`,
`max`, `label`, `description`, `placeholder`, `advanced`, `secret` and
`profileable`. The validator enforces required fields, field types, enum values,
patterns and numeric ranges. A `secret` field is never `profileable`; the
catalog freeze test enforces that pairing. Every entry in `credentials` is
required unless it also appears in `optionalCredentials`; `http.request` uses
this for its optional `http` credential. `outputTypeKey` is used by `code.run`
and `data.set` so the canvas can type an edge from the node's config.

## Compatibility and versioning

- Adding a node type or config field is backward compatible.
- Removing/renaming a field, changing a port name/type, or changing runtime
  semantics requires a new `typeVersion` and a migration.
- Workflow `version` is user-managed; `workflowVersion` in a run is the content
  hash and is not user-editable.
- The event log is append-only. Pruning removes old terminal runs and orphaned
  blobs; it never rewrites a retained run's sequence.
