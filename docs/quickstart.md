# Loom quickstart

This is the 15-minute path from a fresh checkout to a running, inspectable
workflow. Loom ships as one Go binary with SQLite; the React editor is embedded
at build time.

## 1. Build and validate

Requires Go 1.26+. Node is only needed if you are rebuilding the editor.

```sh
make build
./bin/loom validate examples/*.json
```

`validate` checks the workflow schema, node configs, credentials, ports, trigger
rules, reachability and cycles. It exits non-zero on errors.

## 2. Run an example offline

The CLI uses a deterministic simulated host by default. No network, Postgres or
Slack account is required.

```sh
./bin/loom test examples/daily-digest.flow.json
./bin/loom test examples/crm-sync.flow.json
```

Use `run` instead of `test` when a workflow contains an approval. `run` stops
with a run ID; resume it explicitly:

```sh
./bin/loom run examples/refund-approval.flow.json
# copy the printed run ID
./bin/loom resume --decision approved <runID>
```

`test --auto-approve` is also available for CI. Pass `--verbose` to print every
event, and `--input '{"orderId":"A-1"}'` to seed the trigger.

## 3. Run in Docker

The image is a distroless multi-stage build with the editor embedded. The
container serves the live host and stores SQLite data in `/data`.

```sh
docker build -t loom:local .
docker run --rm -p 5678:5678 -v loom-data:/data loom:local
```

Or with Compose:

```sh
LOOM_TOKEN=secret docker compose up --build
```

For a deterministic demo inside the container, append `--sim`:

```sh
docker run --rm -p 5678:5678 -v loom-data:/data loom:local --sim
```

## 4. Open the editor

```sh
./bin/loom serve --data ./data --sim
```

Open <http://localhost:5678>. `--sim` uses the deterministic host; omit it to
serve the live host (real HTTP calls). On first load, pick a bundled example or
create a blank workflow.

In the editor:

1. Select a node to edit its config in the inspector.
2. Click **Input** to set the trigger JSON for manual runs, then **Run with
   input**; node status streams into the canvas.
3. Click a completed node to inspect its last input/output and request/response.
4. Open **Runs** to inspect the event log, per-node request/response and run
   summary, download a runbook, replay a node or diff two runs.
5. If a run fails, click **Retry failed node** to re-run it and its descendants.
6. A waiting approval exposes **Approve**, **Reject** and **Copy approval link**.
7. In the inspector, **Data from upstream** lists fields recorded by the latest
   run and inserts `$node["<id>"]` expressions into the active text field. The
   preview shows the rendered value before you run again.
8. Optional credentials such as `http.request` can be left blank. Stored
   credentials appear in the inspector's credential suggestions; manage them on
   the **Credentials** page (`/credentials`) or through the API.

The approval link is a capability URL:

```sh
curl -X POST http://localhost:5678/hook/approval/<token> \
  -H 'content-type: application/json' \
  -d '{"decision":"approved"}'
```

The GET form renders a confirmation page; POST accepts a form (`decision=approved`)
or JSON. Slack interactive payloads are accepted on the same endpoint.

## 5. Import and call the API

Import an example into the data directory, then serve that directory:

```sh
./bin/loom import --data ./data examples/publish-approval.flow.json
./bin/loom serve --data ./data --sim
```

List workflows and start a run:

```sh
curl -s http://localhost:5678/api/workflows

curl -s -X POST http://localhost:5678/api/workflows/publish-approval/runs \
  -H 'content-type: application/json' \
  -d '{"input":{"title":"Hello"}}'
```

The response includes the run ID. Stream events as SSE:

```sh
curl -N http://localhost:5678/api/runs/<runID>/events
```

Add `?format=json` for newline-delimited JSON. Useful endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/workflows` | list workflows |
| `POST` | `/api/workflows` | create a workflow |
| `GET` | `/api/workflows/{id}` | read one workflow |
| `PUT` | `/api/workflows/{id}` | replace a workflow |
| `DELETE` | `/api/workflows/{id}` | delete a workflow |
| `POST` | `/api/workflows/{id}/runs` | start a run (`?async=1` returns 202) |
| `POST` | `/api/schedules/preview` | validate `{triggerType,triggerConfig,count}` and return the next run times |
| `GET` | `/api/runs` | list runs (`?workflow=`, `?parent=`, `?limit=` up to 500) |
| `GET` | `/api/stats` | workspace run totals for the dashboard |
| `GET` | `/api/runs/{id}` | run metadata |
| `GET` | `/api/runs/{id}/events` | SSE event stream |
| `GET` | `/api/runs/{id}/data-context?nodeId=` | recorded upstream outputs and status |
| `POST` | `/api/workflows/{id}/preview` | render an expression against a recorded run |
| `GET` | `/api/credentials` | list credential IDs and types |
| `POST` | `/api/credentials` | create `{type,data,id?}` (encrypted at rest) |
| `DELETE` | `/api/credentials/{id}` | delete a credential |
| `POST` | `/api/runs/{id}/cancel` | cancel a queued/running/waiting run |
| `POST` | `/api/runs/{id}/resume` | resume with `{"decision":"approved"}` |
| `POST` | `/api/runs/{id}/replay` | replay `{"mode":"full"}`, `{"mode":"step","nodeId":...}` or `{"mode":"from","nodeId":...}` |
| `GET` | `/api/runs/{id}/diff?against=<runID>` | compare two runs |
| `GET` | `/api/workflows/{id}/runbook` | Markdown operations runbook |
| `GET` | `/api/blobs/{hash}` | content-addressed step payload |
| `GET` | `/api/node-types` | catalog and config contracts |
| `GET` | `/api/examples` | bundled examples |

Enable bearer-token auth with `--token` or `LOOM_TOKEN`:

```sh
LOOM_TOKEN=secret ./bin/loom serve --data ./data
curl -H 'Authorization: Bearer secret' http://localhost:5678/api/workflows
```

`/healthz`, `/readyz` and approval/webhook capability URLs are intentionally unauthenticated.

## 6. Webhooks and cron

A `trigger.webhook` node with `path: "orders"` listens at `POST /hook/orders`.
The request body becomes the trigger item. JSON objects are used directly; JSON
arrays/scalars are wrapped as `{"body": ...}`. A configured `method` is enforced.
The default response is a small `{runId,status}` receipt; add `?async=1` to
return immediately while the run continues in the background. Concurrent
webhook executions are capped so one slow flow cannot exhaust the process.

A `trigger.cron` node uses a five-field cron expression, for example
`0 8 * * *`; an optional `timezone` prefix such as `CRON_TZ=Europe/Berlin` is
honored. The scheduler reloads when workflows change. On startup, legacy
workflow cron triggers are migrated into cron jobs; a paused cron job suppresses
the workflow-level fallback so pausing actually stops the schedule. The jobs UI
previews the next three run times through `/api/schedules/preview`. Cron runs
start from the job's input defaults (or an empty item) and the trigger node adds
an RFC3339 `now` field.

## 7. Replay and inspect

```sh
./bin/loom runs --data ./data
./bin/loom replay --data ./data <runID> --step summarize
./bin/loom replay --data ./data <runID> --from notify
./bin/loom runbook --data ./data publish-approval
```

Replay verifies the recorded event stream first. `--step` re-executes one node
against its recorded input (side effects repeat) without appending to the
original log; `--from` starts a new child run seeded with the original upstream
outputs, then executes that node and its descendants. The original run is never
mutated.

## 8. Build the editor from source

```sh
make ui        # npm install + vite build into web/dist
make build     # embeds web/dist in the Go binary
make check     # fmt, vet, tests, engine coverage, UI build, validation
make e2e       # Playwright, uses serve --sim
```

For UI development, run `cd web && npm run dev`; Vite proxies `/api` and
`/hook` to `127.0.0.1:5678`.

## Current limits

- Live `http.request` is implemented with an SSRF guard; live Postgres and Slack
  adapters are not wired yet. Use `--sim` for the bundled examples.
- Workflow `credentials` map credential types to IDs and never contain secrets.
  Credentials can be created through the API and selected in the inspector;
  secret data is encrypted at rest and never returned by the list endpoint.
- Deployment secret stores can supply credentials without the database:
  `LOOM_SECRET_<ID>` (JSON envelope or bare data object),
  `LOOM_SECRETS_FILE` (JSON map of ID → credential), or
  `LOOM_SECRETS_DIR/<id>.json`. Database credentials win; external values are
  the fallback, so a sidecar or cloud secret manager can write either form.
- Cron `timezone` is honored by the scheduler via `CRON_TZ` (descriptors such as
  `@daily` included). Unset timezones use the server's local time. Missed cron
  windows are not backfilled while the server is down; a one-time job whose
  timestamp has passed fires immediately on the next reload.
- In-flight runs survive a restart: on startup Loom replays each run's
  `node.finished` events, resumes only the unfinished nodes and appends a
  `run.resumed` marker. Completed steps are not executed twice.
- Crash-resume, cron scheduling and retention pruning are guarded by a
  renewable store lease. A second server pointed at the same data directory
  serves the API but does not schedule jobs or resume runs while the first
  instance holds the lease.
- `/healthz` is liveness; `/readyz` pings the store and returns `503` when the
  database is unavailable, so a load balancer can drain a broken replica.
  SQLite runs in WAL mode with `synchronous=NORMAL`; keep the data directory on
  local disk (not a network filesystem) when running more than one process.
- SQLite is the only store. Postgres store/queue mode is post-v1.
