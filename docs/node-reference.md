# Node reference

Every node is versioned (`typeVersion: 1`) and declares typed input/output ports.
The validator rejects an edge when the producer's output type cannot flow into
the consumer's input type. `any` is compatible with everything; a `json`
producer flowing into a narrower type is accepted with an `untyped_edge` warning.

## Port types

| Type | Meaning |
|---|---|
| `json` | untyped JSON object/item |
| `text` | plain text |
| `event` | trigger event payload |
| `order` | reserved order payload type |
| `message` | chat/message payload |
| `rows` | tabular result (`{"rows": [...]}`) |
| `any` | unconstrained |

## Triggers

Triggers have no input port and must be the only trigger in a workflow.

### `trigger.manual`

Starts a run from the editor/CLI/API. Output: `main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `fields` | array | no | seed-field metadata used by the editor |

The run input is the trigger item. The API accepts it as `{"input": {...}}`.

### `trigger.webhook`

Starts a run from `/hook/{path}` for the configured HTTP method. Output: `main` (`event`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `method` | string | yes | `GET`, `POST`, `PUT`, `PATCH`, `DELETE` |
| `path` | string | yes | matched after `/hook/`, leading/trailing slashes ignored |

The request body becomes the trigger item. JSON objects are used directly;
arrays/scalars become `{"body": ...}`; non-JSON becomes `{"body": "<text>"}`.
Bodies larger than 1 MiB are rejected with `413`. The public response is `{runId,status}`; add
`?async=1` to return `202` immediately. Concurrent webhook runs are capped.
`approval/*` is reserved for approval callbacks.

### `trigger.cron`

Starts a run on a five-field cron schedule. Output: `main` (`event`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `schedule` | string | yes | e.g. `0 8 * * *` |
| `timezone` | string | no | IANA zone or a `CRON_TZ=...` prefix; defaults to server local time |

The input item is the job's input defaults (or an empty object for a
workflow-level cron trigger). The trigger adds `now` as an RFC3339 UTC timestamp
if not already present.

### `trigger.subflow`

Entry point for a workflow invoked by `flow.subflow` or `flow.loop`. Output:
`main` (`json`). No config. Child runs carry `parentRunId`/`parentNodeId`.

## Actions

### `http.request`

Calls an HTTP endpoint. Input: `main` (`json`). Output: `main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `method` | string | yes | `GET`, `POST`, `PUT`, `PATCH`, `DELETE` |
| `url` | string | yes | templates are rendered |
| `pathParams` | object | no | replaces `{name}` or `:name` path segments |
| `query` | object | no | rendered values replace same-named query parameters |
| `headers` | object | no | values are rendered |
| `bodyType` | string | no | `none`, `json`, `raw`, `form` |
| `body` | object | no | rendered recursively; JSON or form fields |
| `rawBody` | string | no | rendered raw request body; falls back to `body` |
| `contentType` | string | no | raw body content type; default `text/plain; charset=utf-8` |
| `auth` | string | no | `none`, `credential`, `bearer`, `basic`, `apiKey` |
| `successCodes` | string | no | default `2xx`; comma lists, ranges and exact codes |
| `failOnError` | boolean | no | fail the node when the status is not successful; default `false` |
| `timeout` | number | no | seconds; live default is 30s |

The output is the decoded JSON response (or `{"body": "<text>"}`), merged with
`status`, `statusText`, `headers`, `url`, `method`, `durationMs` and `ok`.
Object responses are merged into the item; array responses become
`{"items": [...]}`. Live calls add a stable `Idempotency-Key` header and refuse
loopback/private/link-local addresses.

The optional `http` credential supplies `headers`, `token`, `apiKey` (plus
`apiKeyHeader`, default `X-API-Key`), `username`/`password` for Basic auth, or a
raw bearer token. The `auth` mode selects which credential shape is required;
explicit `headers` win over credential-generated headers.

The run event records the rendered `method`, `url`, request headers/body and
response `status`/headers for inspection. Sensitive request headers
(`authorization`, `cookie`, `set-cookie`, `proxy-authorization`, and
`x-api-key`) are replaced with `[redacted]`; request/response bodies are not
automatically redacted.

### `code.run`

Runs JavaScript against the incoming item. Input: `main` (`json`). Output:
`main` (`json` by default).

| Config | Type | Required | Notes |
|---|---|---|---|
| `language` | string | yes | `javascript` |
| `source` | string | yes | function body; must return a value |
| `outputType` | string | no | overrides the output port type |

`$json` is the incoming item. Execution is interrupted after 2 seconds or when
the run is cancelled. Return values become:

- object → the item
- array → `{"items": [...]}`
- scalar → `{"value": ...}`
- `null`/`undefined` → `{}`

Example:

```js
return { total: $json.rows.length, label: `Rows: ${$json.rows.length}` };
```

### `data.set`

Builds an item from explicit fields. Input: `main` (`json`). Output: `main`
(`json` by default).

| Config | Type | Required | Notes |
|---|---|---|---|
| `values` | object | yes | values are rendered recursively |
| `keepOnlySet` | boolean | no | start from `{}` instead of the input item |
| `outputType` | string | no | `json`, `text`, `event`, `order`, `message`, `rows` |

Use `outputType: "message"` before `chat.slack`, or `"rows"` before a consumer
that expects tabular data.

### `db.postgres`

Runs SQL and returns rows. Input: `main` (`json`). Output: `main` (`rows`) as
`{"rows": [...]}`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `operation` | string | yes | `query`, `insert`, `update` |
| `query` | string | yes | SQL text |

Declares a `postgres` credential. The simulated host returns deterministic rows;
the live Postgres adapter is not wired yet.

### `chat.slack`

Sends a chat message. Input: `main` (`message`). Output: `main` (`json`) as
`{"ok": true, "channel": "..."}`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `channel` | string | yes | rendered |
| `text` | string | yes | rendered |

Declares a `slack` credential. The simulated host records the send; the live
Slack adapter is not wired yet.

## Logic and flow control

### `flow.if`

Splits on a JavaScript boolean. Input: `main`. Outputs: `true`, `false`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `condition` | string | yes | JS expression, optionally wrapped in `{{ }}` |

The item is passed through unchanged on the selected port. Missing paths evaluate
falsy rather than failing the run.

### `flow.switch`

Routes to one of three cases or a default. Input: `main`. Outputs: `case0`,
`case1`, `case2`, `default`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `cases` | array | yes | ordered boolean expressions |

The first matching case wins. Cases beyond index 2 route to `default`.

### `flow.merge`

Joins two branches. Inputs: `a`, `b`. Output: `main`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `mode` | string | no | `append` or `byIndex` |

The engine merges incoming branch items by shallow key merge (later values win).
`mode` is accepted for forward compatibility.

### `flow.loop`

Runs a sub-workflow once per item. Input: `main`. Output: `done` as
`{"items": [childResult, ...]}`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `workflowId` | string | yes | child workflow with `trigger.subflow` |
| `itemsPath` | string | no | dot path to an array; defaults to `items` or the item itself |
| `maxIterations` | number | no | caps the loop; `<= 0` means all items |

Each array element becomes the child input. Objects are passed directly; other
values become `{"value": ...}`. Child failures fail the loop iteration.

### `flow.wait`

Pauses for a fixed duration. Input/output: `main`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `seconds` | number | yes | negative values clamp to zero |

Sleeps through the host adapter, so the run remains cancellable.

### `flow.approval`

Suspends until a human decides. Input: `main`. Outputs: `approved`, `rejected`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `message` | string | yes | rendered; shown in the UI/link |
| `channel` | string | no | notification metadata |
| `timeoutSeconds` | number | no | accepted; no automatic timeout is enforced yet |

The run enters `waiting`. Resume with `approved` or `rejected`; the selected port
receives the input item plus a boolean (`approved: true/false`). `test` and
`--auto-approve` take the `approved` branch automatically.

### `flow.subflow`

Invokes another workflow. Input/output: `main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `workflowId` | string | yes | target workflow |
| `wait` | boolean | no | accepted; execution currently waits for the child |

The child receives the current item and its final item is returned. Child runs
are linked to the parent for navigation.

## Retry policy

Every node can override the engine's retry defaults:

```json
"retry": { "maxAttempts": 4, "backoffMs": 250, "maxDelayMs": 5000 }
```

- `maxAttempts` — 0 uses the engine default (3), otherwise 1–20.
- `backoffMs` — base delay; each retry doubles it.
- `maxDelayMs` — cap for the doubled delay; 0 means no cap.

Retries happen inside the same node attempt, so `node.failed` / `node.retried`
events and the stable idempotency key are preserved. Suspensions (approvals,
waits) are never retried. Without a policy the engine keeps its historical
quadratic backoff.

## Failure handling

Set `onError` on any node to control what happens after its final attempt:

| Value | Behavior |
|---|---|
| `stop` (default) | The run fails; later nodes do not execute. |
| `continue` | The node emits `{ error, nodeId, attempts }` on `main` and downstream nodes run. |
| `branch` | The node emits the same item on the `error` port; `main` stays empty, so main-output nodes are skipped. |

`branch` adds an `error` output handle to the node in the editor. Edges from
that port are only valid when `onError` is `branch`. Handled failures still
append a `node.finished` event carrying the error text, so the run finishes as
`done` while the node remains visibly failed. On restart, the saved error item
is restored like any other output.

## Batch items

Set `batch` to run a node once per object in an input array:

```json
"batch": { "itemsPath": "items", "continueOnItemError": true }
```

- `itemsPath` — dotted path to the array; defaults to `items`.
- `continueOnItemError` — keep running after a failed item. Without it the
  first failure fails the node.
- Output is one item: `{ items: [...], count, failed }`. Successful entries are
  the node's `main` output; failed entries are `{ index, error }`.
- Every item emits `item.started` / `item.finished` or `item.failed`; failures
  also emit the normal `node.failed` attempt event with `itemIndex`.
- Each item gets its own idempotency key (`run:node:item-N`) and `$index`.

## Concurrency limits

Three limits compose; the smallest wins and the floor is 1:

- **Engine default** — the server's `Parallelism` (usually 4).
- **Workflow** — `settings.concurrency` in the workflow JSON.
- **Run/job** — `Options.MaxConcurrency`; jobs read
  `triggerConfig.maxConcurrency`, editable in the job create/detail forms.

The limit bounds how many nodes run at once inside a single run. It is not a
queue for concurrent runs; use the job overlap policy for that.

## Connector pack

These nodes use the credential store and the governed HTTP host, so they get
the same retry, trace and cancellation behavior as `http.request`.

| Node | Credential | Purpose |
|---|---|---|
| `sheets.append` | `sheets` (`token` or `accessToken`) | Append rows to a Google Sheet range. |
| `chat.discord` | `discord` (`webhookUrl`) | Post to a Discord webhook. |
| `chat.teams` | `teams` (`webhookUrl`) | Post to a Teams webhook. |
| `email.resend` | `resend` (`apiKey` or `token`) | Send an email through Resend. |
| `crm.hubspot` | `hubspot` (`apiKey` or `token`) | Create a HubSpot contact. |
| `ticket.jira` | `jira` (`email`, `token`) | Create a Jira issue. |

## Utility nodes

| Node | Output | Notes |
|---|---|---|
| `data.json.parse` | object, `{items}`, or `{value}` | Parses a rendered JSON string. |
| `data.csv.parse` | `{rows}` | `headers` defaults to true; `delimiter` defaults to `,`. |
| `text.template` | `{text}` | Renders `{{ }}` placeholders. |
| `crypto.hmac` | `{signature}` | SHA-256 by default; `sha1`/`sha512` supported. |
| `random.uuid` | `{uuid}` | UUID v4. |
| `date.format` | `{date}` | Parses RFC3339/date strings; defaults to now. |

## AI nodes

AI nodes are thin, inspectable calls to an OpenAI-compatible API. They use the
`openai` credential (`apiKey` or `token`) and accept an optional `baseUrl`
(default `https://api.openai.com/v1`), so a local gateway works unchanged.

| Node | Output | Notes |
|---|---|---|
| `ai.chat` | `{text, raw, json?}` | `jsonMode` asks for a JSON object and parses it when valid. |
| `ai.classify` | `{label, text, raw}` | Returns one of the configured `labels`. |
| `ai.extract` | `{json, text, raw}` | Requires JSON with the configured `fields`. |
| `ai.embed` | `{embedding, raw}` | Uses the embeddings endpoint. |

Prompts, system messages and text are rendered with the normal expression
engine first. The raw provider response is kept for debugging; no hidden
agent loop or retry policy is added on top of the engine.

## Expression language

- `{{ ... }}` placeholders render strings in config fields. `{{ $json.email }}`
  is a path lookup; other expressions run as JavaScript.
- Conditions and code steps evaluate JavaScript with `$json` bound to the item.
- Upstream node data is available as `$node["<nodeId>"].json.<path>` for the
  `main` port, or `$node["<nodeId>"].ports["<port>"].<path>` for another port.
- Run metadata is available as `$run.id`, `$run.trigger` and `$run.status`;
  `$job.id` identifies a job-started run.
- Workflow settings `variables` are available as `$vars.<name>`.
- Item-fan-out adapters can expose `$index` through the expression context.
- The editor inserts stable node IDs for `$node` references and only offers
  upstream nodes, so a reference cannot point forward or at the current node.
- Rendering is recursive through objects and arrays in `data.set`,
  `http.request` path/query/headers/body, and message fields.
- A missing template path renders as an empty string; a failing expression in a
  condition is falsy. A code step that throws fails the node.
- Scripts are interrupted after 2 seconds and on run cancellation.
