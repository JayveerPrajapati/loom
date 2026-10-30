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
| `retry` | object | optional `{ maxAttempts, backoffMs, maxDelayMs }`; 0 values use engine defaults |
| `onError` | string | `stop` (default), `continue` (error item on main), or `branch` (error item on the `error` port) |
| `batch` | object | optional `{ itemsPath, continueOnItemError }`; runs the node per array element |

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
| `trigger` | string | `manual`, `webhook`, `cron`, `once`, `job`, `batch`, `subflow`, `replay` |
| `startedAt` | date-time | RFC3339 |
| `finishedAt` | date-time | present for terminal runs |
| `error` | string | present for failed runs |
| `templateVersion` | integer | published template version for job runs |
| `jobId` | string | job that started the run |
| `parentRunId` | string | parent run for a subflow execution |
| `parentNodeId` | string | subflow node in the parent run |
| `resumeNode` | string | node where a waiting run is suspended |
| `resumeToken` | string | capability token for resuming a waiting run |

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

Event types:

`run.started`, `node.started`, `node.finished`, `node.failed`, `node.retried`,
`run.waiting`, `run.resumed`, `run.finished`, `run.failed`, `run.cancelled`.

Payloads are content-addressed. The event stores hashes; `GET /api/blobs/{hash}`
returns the exact bytes. Replay verifies the event stream before re-executing.

## Node definition

Required: `type`, `typeVersion`, `category`, `inputs`, `outputs`, `inputType`,
`outputType`, `config`.

| Field | Type | Notes |
|---|---|---|
| `type` | string | catalog key, e.g. `http.request` |
| `typeVersion` | integer | `>= 1` |
| `category` | string | `trigger`, `action` or `logic` |
| `description` | string | human summary |
| `inputs` | array | input port names |
| `outputs` | array | output port names |
| `inputType` | port type | one of `any`, `json`, `text`, `event`, `order`, `message`, `rows` |
| `outputType` | port type | same set |
| `outputTypeKey` | string | config field that overrides `outputType` |
| `credentials` | array | credential types the node can use |
| `optionalCredentials` | array | credential types whose absence is valid |
| `config` | object | field name → field definition |

A field definition requires `type` (`string`, `number`, `boolean`, `array`,
`object`) and may set `required` and `enum`. The validator enforces required
fields, field types and enum values. Every entry in `credentials` is required
unless it also appears in `optionalCredentials`; `http.request` uses this for its
optional `http` credential. `outputTypeKey` is used by `code.run` and
`data.set` so the canvas can type an edge from the node's config.

## Compatibility and versioning

- Adding a node type or config field is backward compatible.
- Removing/renaming a field, changing a port name/type, or changing runtime
  semantics requires a new `typeVersion` and a migration.
- Workflow `version` is user-managed; `workflowVersion` in a run is the content
  hash and is not user-editable.
- The event log is append-only. Pruning removes old terminal runs and orphaned
  blobs; it never rewrites a retained run's sequence.
