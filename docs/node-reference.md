# Node reference

Every node is versioned (`typeVersion: 1`) and declares typed input/output ports.
The validator rejects an edge when the producer's output type cannot flow into
the consumer's input type. `any` is compatible with everything; a `json`
producer flowing into a narrower type is accepted with an `untyped_edge` warning.

## Profiles

Some catalog fields are marked `profileable`: a reusable configuration profile
of the same node type may supply them. Publish merges the profile under the
node's own configuration, with explicit ownership deciding whether an equal
value is inherited or a deliberate override. Secret fields are never
profileable. The tables below describe the node's own config keys; the
ownership markers (`profileRef`, `profileOverrides`, `configSources`) live on
the node document and are documented in [ownership.md](ownership.md).

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
| `secret` | string | no | shared secret required on an inbound request |
| `secretHeader` | string | no | header that carries the shared secret |
| `hmacSecret` | string | no | key for an HMAC-SHA256 signature over the raw body |
| `hmacHeader` | string | no | header that carries the HMAC signature |
| `hmacTimestamps` | boolean | no | require a timestamped signature for replay protection |
| `timestampHeader` | string | no | header that carries the RFC3339 timestamp |

The request body becomes the trigger item. JSON objects are used directly;
arrays/scalars become `{"body": ...}`; non-JSON becomes `{"body": "<text>"}`.
Bodies larger than 1 MiB are rejected with `413`. The public response is `{runId,status}`; add
`?async=1` to return `202` immediately. Concurrent webhook runs are capped.
`approval/*` is reserved for approval callbacks.

Authentication notes: prefer the configured secret header or bearer token;
`?secret=` still works for compatibility but is deprecated and logs a warning.
With `hmacTimestamps: true`, Loom requires `X-Webhook-Timestamp` (or the
configured `timestampHeader`) containing RFC3339 time, and the HMAC payload is
`"<timestamp>.<raw body>"`. Timestamps outside a ±5-minute window are
rejected. Legacy body-only HMAC remains available when `hmacTimestamps` is not
enabled.

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

### `trigger.event`

Starts the workflow when a matching event is published by `flow.emit`. Output:
`main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `event` | string | yes | event name to match |

The event payload becomes the trigger item.

## Actions

### `http.request`

Calls an HTTP endpoint. Input: `main` (`json`). Output: `main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `method` | string | yes | `GET`, `POST`, `PUT`, `PATCH`, `DELETE`, `HEAD`, `OPTIONS` |
| `url` | string | yes | templates are rendered |
| `pathParams` | object | no | replaces `{name}` or `:name` path segments |
| `query` | object | no | rendered values replace same-named query parameters |
| `headers` | object | no | values are rendered |
| `bodyType` | string | no | `none`, `json`, `raw`, `form`, `multipart`, `graphql` |
| `body` | object | no | rendered recursively; JSON or form fields |
| `rawBody` | string | no | rendered raw request body; falls back to `body` |
| `contentType` | string | no | raw body content type; default `text/plain; charset=utf-8` |
| `parts` | array | no | multipart parts (see below) |
| `graphqlQuery` | string | no | required when `bodyType` is `graphql` |
| `graphqlVariables` | object | no | GraphQL variables, rendered recursively |
| `auth` | string | no | `none`, `credential`, `bearer`, `basic`, `apiKey`, `oauth2` |
| `successCodes` | string | no | default `2xx`; comma lists, ranges and exact codes |
| `failOnError` | boolean | no | fail the node when the status is not successful; default `false` |
| `timeout` | number | no | seconds, 1–300; live default is 30s |
| `rateLimitPerMinute` | number | no | outbound requests per minute, 0–600 |
| `responseMode` | string | no | `inline` (default), `blob`, `auto` (see below) |
| `inlineThresholdBytes` | number | no | inline ceiling for `auto`, 1 KiB–10 MiB; default 1 MiB |
| `pagination` | object | no | page/offset/cursor loop (see below) |

The parsed response body is preserved. The envelope adds `statusCode` (int),
`statusText`, `durationMs`, `url` and `method`; `status`, `headers` and `ok` are
only added when the body does not already define them. Object responses are
merged into the item; array responses become `{"items": [...]}`. Live calls add
a stable `Idempotency-Key` header and refuse loopback/private/link-local
addresses.

The optional `http` credential supplies `headers`, `token`, `apiKey` (plus
`apiKeyHeader`, default `X-API-Key`), `username`/`password` for Basic auth, or a
raw bearer token. The `auth` mode selects which credential shape is required;
explicit `headers` win over credential-generated headers. An `http` credential
may also carry `proxyUrl` to route this node's requests through a proxy.

**OAuth2 client credentials.** `auth: "oauth2"` requires a separate `oauth2`
credential (`tokenUrl`, `clientId`, `clientSecret`, `scopes`). The host fetches
a bearer token via client credentials, caches it in memory with its expiry
(never at rest), and injects the `Authorization` header. The token fetch shares
the SSRF guard and the shared connection pool. By construction the effect tape
only ever records the final request/response pair — tokens never enter the
tape, and hermetic replay never fetches tokens. Workflows declaring
`auth: "oauth2"` without the credential fail validation at save time.

**Proxy.** Route all live HTTP through one proxy with serve's
`--http-proxy` (or `LOOM_HTTP_PROXY`), or per node via the `http` credential's
`proxyUrl`. Proxies are trusted configuration: the SSRF target guard applies
to the proxy address (a private proxy needs `--allow-private-targets`), and
the proxy — not Loom — connects to the final target.

**GraphQL.** `bodyType: "graphql"` POSTs `{query, variables}` as JSON
(`graphqlQuery` required, `graphqlVariables` rendered recursively). GraphQL
responses can carry errors with a 200 status: the `errors` array stays in the
item for downstream handling, and `failOnError` fails the node on it.

The run event records the rendered `method`, `url`, request headers/body and
response `status`/headers for inspection. Sensitive request headers
(`authorization`, `cookie`, `set-cookie`, `proxy-authorization`, and
`x-api-key`) are replaced with `[redacted]`; request/response bodies are not
automatically redacted.

Retries with backoff are the node-level retry policy (`retry.maxAttempts`,
`retry.backoffMs`, `retry.maxDelayMs` on the node, not the config): pair
`failOnError` with a retry policy and each attempt gets its own effect tape,
so replays verify every retry.

**Multipart bodies.** `bodyType: multipart` builds `multipart/form-data` from
a `parts` array; each part is `{name, type, value, contentType?, filename?}`.
`type` is `text` (default), `json` (rendered then encoded), or `blob` (`value`
is a blob hash from a prior `responseMode: blob` response; serve with a store).
The boundary is derived from the part content, so replays rebuild a
byte-identical body.

**Response modes.** `responseMode` controls where the body lands:
`inline` (default) keeps today's behavior; `blob` always spills the raw body
into the content-addressed blob store and replaces it with `blob`, `blobSize`
and `contentType` (readable via `GET /api/blobs/{hash}`); `auto` inlines up to
`inlineThresholdBytes` and spills beyond. Paginated runs collect per-page
references in `pageBlobs`.

**Pagination.** `pagination` walks pages with one recorded HTTP effect per
page, so a replay verifies every request:

```
{"mode": "page",   "pageParam": "page", "startPage": 1, "limitParam": "limit", "limit": 100,
 "itemsPath": "data.results", "until": "$json.hasMore === false", "maxPages": 100, "maxItems": 10000}
{"mode": "offset", "offsetParam": "offset", "limit": 100}
{"mode": "cursor", "cursorParam": "after", "cursorPath": "nextCursor"}
```

`mode` is required. `itemsPath` extracts each page's records (default: the
`items` array the host decodes, else the whole body as one record). `page` and
`offset` modes stop on an empty page; `cursor` stops when `cursorPath` yields
an empty value; `until` (a boolean expression over the page body) stops
early. Output is a single item: `{"items": [...all records...], "itemsCount",
"pages", "statusCode", ...}` — feed `items` straight into `flow.loop`. Caps:
`maxPages` ≤ 1000 (default 100), `maxItems` ≤ 100 000 (default 10 000).
Hitting a cap fails the node rather than silently truncating; a failed page
mid-loop fails the node too. `offset` mode requires a positive `limit`.

### `code.run`

Runs JavaScript against the incoming item. Input: `main` (`json`). Output:
`main` (`json` by default).

| Config | Type | Required | Notes |
|---|---|---|---|
| `language` | string | yes | `javascript` |
| `source` | string | yes | function body; must return a value |
| `outputType` | string | no | overrides the output port type |
| `timeoutSeconds` | number | no | 1–300; default 2 |

`$json` is the incoming item. Execution is interrupted after 2 seconds by
default, after `timeoutSeconds` (max 300), or when the run is cancelled. The sandbox caps
each string result at 1 MiB and each array at 100 000 elements. Return values
become:

- object → the item
- array → `{"items": [...]}`
- scalar → `{"value": ...}`
- `null`/`undefined` → `{}`

**Script capabilities** (code steps only; `{{ }}` templates stay
side-effect free):

- `$http({method, url, headers?, json?, body?, contentType?, timeout?})` —
  performs an HTTP call routed through the guarded host, so the SSRF
  private-target guard applies and every call is recorded on the effect
  tape: replays verify script HTTP like any other effect. Returns
  `{statusCode, statusText, headers, body}`; a transport error throws.
- `console.log/info/warn/error(...)` — output is captured into the run's
  node trace (one `console` entry with all log lines).
- `$crypto.sha256(s)`, `$crypto.hmacSha256(s, key)` — hex digests.
- `$base64.encode(s)` / `$base64.decode(s)`, `btoa(s)` / `atob(s)`.

Not available: `fs`, `os`, `process`, `require`, `import`, timers beyond the
deadline. Python runs in a separate `code.python` node, documented below.

Example:

```js
return { total: $json.rows.length, label: `Rows: ${$json.rows.length}` };
```

### `code.python`

Runs Python against the incoming item in a subprocess. Input: `main`
(`json`). Output: `main` (`json`). **Opt-in**: the node is disabled unless
the server (or `loom run`) starts with `--enable-subprocess-nodes` — it is
the largest security surface in Loom, so it ships off by default.

| Config | Type | Required | Notes |
|---|---|---|---|
| `source` | string | yes | function body receiving `item`; must return a value |
| `requirements` | array | no | pinned packages, `["httpx==0.27.0"]` |
| `timeoutSeconds` | number | no | 1–900; default 60 |
| `maxMemoryMB` | number | no | 16–8192; default 512 |

The protocol: the item arrives as JSON on stdin; the source is the body of a
function `main(item)`, `print` output is captured into the node trace, and
the return value follows the same shaping convention as `code.run` (object →
item, array → `{"items": [...]}`, scalar → `{"value": ...}`, `None` → `{}`).
The subprocess runs with `python -I` (isolated), its own process group (a
timeout kills user-spawned children too), stdout/stderr caps of 1 MiB, a
private writable workdir that is removed afterwards, and an `RLIMIT_AS`
memory cap of `maxMemoryMB` on kernels that support lowering it — macOS
reserves a huge fixed address space and refuses, in which case the refusal
is printed to the run's trace and the time/output caps are what stand.
The system interpreter is used unless `requirements` name pinned packages.

**Sandboxing.** On macOS each execution is wrapped in a Seatbelt profile
(`sandbox-exec`): file writes are allowed only inside the per-execution
workdir, and only the interpreter itself may be exec'd. Reads and network
remain allowed — the Python process can still make network calls directly,
bypassing the SSRF guard that `http.request` applies. If `sandbox-exec` is
missing or cannot apply (nested sandboxes, future macOS), execution
continues with the process-level caps and one loud log line. On Linux,
Landlock/seccomp is not yet applied — the memory cap, timeout kill and
output caps are the whole story there.

**Dependencies.** Requirements must be pinned (`name==version` — unpinned
entries are rejected at save time). Environments are built once at **publish
time** with [uv](https://docs.astral.sh/uv/) into `data/envs/<hash>/`, keyed
by the hash of the sorted pinned set, and verified by `loom doctor` (the
manifest must hash to its directory name). **Runs never install packages or
touch the network for dependencies** — a run whose environment is missing
fails with "environment not built; publish the workflow to build it". Which
packages may be installed, and how large an environment may be, is decided
by the policy file `data/subprocess-policy.json` (or serve's
`--subprocess-policy <path>`):

```json
{"allowedPackages": ["httpx", "scikit-learn"], "maxEnvMB": 512}
```

An absent policy file allows every pinned package with a 1 GiB per-env
cap; a policy file that is malformed, or explicitly configured and
missing, is an error rather than a silent fail-open. `loom doctor` reports
the active policy, each environment's size, and any built environment that
no longer passes the policy (a note, not a failure — the operator may have
tightened the policy after environments were built).

**Replay.** Every subprocess execution is recorded on the effect tape
(request identity = source + item + environment; response = exit status,
output, parsed result), so hermetic replay verifies a Python step without
re-running anything. A live re-run (`replay --live --step`) re-executes the
script and may repeat its side effects.

**Sandboxing honesty.** A subprocess running arbitrary Python can do most
of what the operating-system user can do — including reaching the network
directly from Python. Protections, by layer: isolated interpreter flags,
timeouts with process-group kills, 1 MiB output caps, a private removed-
afterwards workdir, an RLIMIT_AS memory cap where the kernel supports it,
and a Seatbelt profile on macOS (writes confined to the workdir, exec
confined to the interpreter). On macOS the memory cap is typically
unenforceable (the kernel refuses to lower RLIMIT_AS below the
interpreter's reserved address space) and the refusal is recorded in the
run's trace; on Linux there is no OS-level sandbox yet. Treat
`--enable-subprocess-nodes` as "run user code as the service user, with
the layers above".

### `data.set`

Builds an item from explicit fields. Input: `main` (`json`). Output: `main`
(`json` by default).

| Config | Type | Required | Notes |
|---|---|---|---|
| `values` | object | yes | values are rendered recursively |
| `keepOnlySet` | boolean | no | start from `{}` instead of the input item |
| `outputType` | string | no | `json`, `text`, `event`, `order`, `message`, `rows` |

Use `outputType: "rows"` before a consumer that expects tabular data.

### `db.postgres`

Runs SQL and returns rows. Input: `main` (`json`). Output: `main` (`rows`) as
`{"rows": [...]}`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `operation` | string | no | `query`, `insert`, `update`; missing behaves as a read-only `query` |
| `query` | string | yes | SQL text; must be a single statement |

Declares a `postgres` credential and runs against the live Postgres adapter. The
statement must be a single SQL statement. Under `operation: "query"` the
statement must be read-only: DDL/DML, `SELECT … INTO` and data-modifying CTEs
are rejected. `insert` and `update` accept the matching statement keyword,
optionally prefixed by a CTE. The simulated host returns deterministic rows.

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

The first matching case wins. The node has exactly the `case0`, `case1`,
`case2` and `default` outputs. Configuring more than three cases is a validation
error, and a fourth case that matches fails the node.

### `flow.merge`

Joins two branches. Inputs: `a`, `b`. Output: `main`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `mode` | string | no | `shallow` (default), `append`, or `byIndex` |

With no `mode`, the node shallow-merges. `shallow` merges incoming items by key
(later values win). `append` collects every item into `{"items": [...]}`.
`byIndex` merges arrays under `items` elementwise, combining object elements by
key; non-array inputs fall back to the shallow merge.

### `flow.loop`

Runs a sub-workflow once per item. Input: `main`. Output: `done` as
`{"items": [childResult, ...]}`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `workflowId` | string | yes | child workflow with `trigger.subflow` |
| `itemsPath` | string | no | dot path to an array; defaults to `items` or the item itself |
| `maxIterations` | number | no | caps the loop; `<= 0` means all items |
| `concurrency` | number | no | parallel child runs; `<= 0` means 1 |

Each array element becomes the child input. Objects are passed directly; other
values become `{"value": ...}`. Child failures fail the loop iteration.

### `flow.wait`

Pauses for a fixed duration. Input/output: `main`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `seconds` | number | yes | 1–2 592 000 (30 days); negative values clamp to zero |

Values above the cap are rejected at run time. Sleeps through the host adapter,
so the run remains cancellable.

### `flow.approval`

Suspends until a human decides. Input: `main`. Outputs: `approved`, `rejected`.

| Config | Type | Required | Notes |
|---|---|---|---|
| `message` | string | yes | rendered; shown in the UI/link |
| `channel` | string | no | destination channel for the waiting notification |
| `credential` | string | no | notification credential id (or set the node's notification credential) |
| `notifyUrl` | string | no | POSTed `{event: "approval.waiting", runId, nodeId, message, channel}` |
| `timeoutSeconds` | number | no | auto-decides after this long (see `onTimeout`) |
| `onTimeout` | string | no | `rejected` (default) or `approved` |
| `linkExpiresMinutes` | number | no | link lifetime; default 7 days |
| `reminderMinutes` | number | no | re-notify once after this long without a decision |

The run enters `waiting`. Resume with `approved` or `rejected`; a rejection
must carry a reason of 3–500 characters, or the engine refuses it with
`a rejection reason is required` / `a rejection reason must be at least 3
characters` (HTTP 422). Notes are trimmed; an oversized note is refused rather
than silently truncated. `actor` is the deciding principal's email when one is
available (API token owner or signed-in user), so decision history reads
"Rejected by ada@example.com"; approval-link resumes use
`system:approval-link`; notification callbacks record their own source. The
selected port receives
the input item plus:

```json
{
  "approved": false,
  "approval": {
    "decision": "rejected",
    "note": "amount over budget",
    "actor": "user@example.com",
    "decidedAt": "2026-11-01T16:48:55Z",
    "nodeId": "approve"
  }
}
```

**Lifecycle guarantees.** Parking an approval stores `requestedAt`, a link
expiry (`resumeTokenExpiresAt`, default 7 days unless `linkExpiresMinutes`) and,
when configured, a timeout wake and reminder time. An expired or revoked token
no longer resolves; revoking (`POST /api/runs/{id}/revoke-approval`) leaves the
run waiting so the inbox can still decide it. A timeout auto-decides with
`onTimeout` (default rejection), records the source `timeout`, actor
`system:timeout`, and the reason "Timed out after <duration> with no decision".
Notifications (`channel`+`credential`, or `notifyUrl`) are sent once on parking
and once more after `reminderMinutes`; delivery results are recorded as
`approval.notified` / `approval.reminded` events and never fail the run.

Every decision is also written to the `decisions` table in the same transaction
that resumes the run, so `GET /api/decisions` (filters: `workflowId`, `source`,
`decision`, `limit`, `offset`) and `GET /api/metrics/decisions` (volume, wait
p50/p95, current backlog) answer audit questions without replaying events.
`source` identifies the deciding surface (`api`, `cli`, `link`, `timeout`, or a
notification callback).

Downstream steps can read `{{ $json.approval.note }}` (and `decision`, `actor`,
`decidedAt`), so the reason travels with the item into subflows and follow-up
steps. Every decision is also recorded on the run as a `run.resumed` event with
`{decision, note, actor}`, which is what the run page's decision history renders.
`test` and `--auto-approve` take the `approved` branch automatically and record
the note `auto-approved`.

### `flow.subflow`

Invokes another workflow. Input/output: `main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `workflowId` | string | yes | target workflow |
| `wait` | boolean | no | default `true`; `false` starts the child asynchronously and returns the input item |

With `wait: true` the child receives the current item and its final item is
returned. With `wait: false` the child starts asynchronously and the node passes
the input item through. Child runs are linked to the parent for navigation.

### `flow.emit`

Publishes an event so workflows with a matching `trigger.event` start.
Input/output: `main` (`json`).

| Config | Type | Required | Notes |
|---|---|---|---|
| `event` | string | yes | event name |
| `payload` | any | no | rendered; defaults to the input item |

The payload becomes each subscriber's trigger item. A single event tree may
create at most 100 runs; further event triggers are not started.

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
- `batch.concurrency` — at most 32 parallel items; stored workflows above the
  cap are clamped at run time.
- `continueOnItemError` — keep running after a failed item. Without it the
  first failure fails the node.
- At most 10 000 items per batch; a larger array fails the node.
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

## Utility nodes

| Node | Output | Notes |
|---|---|---|
| `data.map` | transformed item | Applies a reusable mapping; `mappingRef` is required, publish fills `mappingRows`, and per-target `overrides` win. |
| `data.json.parse` | object, `{items}`, or `{value}` | Parses a rendered JSON string. |
| `text.template` | `{text}` | Renders `{{ }}` placeholders. |

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
- Scripts are interrupted after 2 seconds by default
  (`timeoutSeconds`, max 300) and on run cancellation.
