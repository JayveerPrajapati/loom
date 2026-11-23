# Quickstart

From a fresh checkout to a running, inspectable workflow. Loom is one Go binary
with SQLite; the React editor is embedded at build time.

Requires Go 1.26+. Node 20+ is only needed to rebuild the editor.

## 1. Build and validate

```sh
make build
./bin/loom validate examples/*.json
```

`validate` checks the schema, node configs, credentials, ports, trigger rules,
reachability and cycles.

## 2. Run a workflow offline

The CLI uses a deterministic simulated host by default: no HTTP or Postgres
calls leave the process.

```sh
./bin/loom test examples/daily-digest.flow.json
./bin/loom test examples/crm-sync.flow.json
```

Use `run` instead of `test` for workflows with an approval:

```sh
./bin/loom run examples/refund-approval.flow.json
./bin/loom resume --decision approved <runID>
```

`loom test` auto-approves approval nodes, so it is the CI-safe run mode. Add
`--verbose` for every event and `--input '{"orderId":"A-1"}'` to seed the
trigger. Use `loom run --auto-approve` when you want a normal run with
approvals auto-approved.

## 3. Create the first account

```sh
printf '%s' 'choose-a-strong-password' | \
  ./bin/loom user create --data ./data --email you@example.com --role admin --password-stdin
```

The server refuses to start with no users and no `LOOM_TOKEN` unless you pass
`--allow-anonymous` (development only). Add teammates with
`loom user create --role viewer|operator|editor|admin`.

## 4. Serve

```sh
./bin/loom serve --data ./data --sim
```

Open <http://localhost:5678> and sign in. `--sim` uses the deterministic host;
omit it for real HTTP calls. Behind HTTPS, also pass `--cookie-secure` or set
`LOOM_COOKIE_SECURE=1`.

Docker:

```sh
docker build -t loom:local .
docker run --rm -p 5678:5678 -v loom-data:/data \
  -e LOOM_BOOTSTRAP_ADMIN_EMAIL=you@example.com \
  -e LOOM_BOOTSTRAP_ADMIN_PASSWORD='choose-a-strong-password' \
  loom:local
```

The bootstrap variables only create an account when the database has none.
Unset the password after the first start.

## 5. Editor basics

1. Select a node to edit its config in the inspector.
2. Use **Input** to set the trigger JSON, then **Run with input**.
3. Click a completed node to see input/output, request/response, configuration
   and credential names.
4. **Runs** shows the event log, diff, runbook export and replay.
5. A failing run offers **Retry failed node**.
6. A waiting approval offers **Approve**, **Reject**, **Copy approval link** and
   **Revoke link**; the bar shows how long the link stays valid. The Tasks inbox
   lists approvals oldest-first with their wait age, and itemizes any bulk
   failures so the failed runs can be retried.
   `GET /api/decisions` and `GET /api/metrics/decisions` expose the audit trail
   (who decided, why, through which surface, how long it waited).
7. **Data from upstream** inserts `$node["<id>"]` expressions for fields recorded
   by the latest run.
8. Manage credentials on **Credentials** (`/credentials`). Credential values are
   encrypted at rest and never returned by the list endpoint.
9. Reusable configuration lives in **Profiles** (`/profiles`).
   Pick a node type, create a profile, then attach it from the Inspector. A
   sparse node inherits the profile; values you author on the node win and are
   badged **Override**. Profiles apply at publish; the node’s own fields win.
   See [ownership.md](ownership.md).
10. **Data contracts**, **Mappings**, **Scripts** and **Connectors** are
    advanced assets under the Profiles landing. Every asset page shares the same
    list, editor, destructive-dialog and error/retry grammar; see
    [page-grammar.md](page-grammar.md).

## 6. Authentication and API

Cookie sessions, argon2id passwords, CSRF-protected mutations and per-user API
tokens. Roles: `viewer` < `operator` < `editor` < `admin`.

Create a token for scripts:

```sh
./bin/loom token create --data ./data --email you@example.com --name ci
```

Use it as a bearer token (no CSRF needed):

```sh
TOKEN=... # printed once by the command above
curl -H "Authorization: Bearer $TOKEN" http://localhost:5678/api/workflows
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'content-type: application/json' \
  -d '{"input":{"title":"Hello"}}' \
  http://localhost:5678/api/workflows/publish-approval/runs
curl -N -H "Authorization: Bearer $TOKEN" \
  http://localhost:5678/api/runs/<runID>/events
```

`LOOM_TOKEN` remains as an audited break-glass admin identity; prefer named users
and scoped tokens. `/healthz`, `/readyz`, `/api/version`, `/metrics` and the
`/hook/*` capability endpoints are unauthenticated by design. See
[security.md](security.md#public-endpoints) for what each surface exposes and
how to restrict it.

Selected endpoints:

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/auth/me` | current user and CSRF token |
| `GET` | `/api/users` | list users (admin) |
| `POST` | `/api/tokens` | create a scoped token |
| `GET` | `/api/workflows` | list workflows |
| `POST` | `/api/workflows/{id}/runs` | start a run (`?async=1` returns 202) |
| `GET` | `/api/runs/{id}/events` | SSE event stream (`?format=json` for NDJSON) |
| `GET` | `/api/runs/{id}/data-context?nodeId=` | recorded upstream outputs and config |
| `POST` | `/api/runs/{id}/replay` | `full`, `step`, `step` + `"execute":true`, or `from` |
| `GET` | `/api/runs/{id}/diff?against=` | compare two runs |
| `GET` | `/api/credentials` | list credential IDs (admin) |

## 7. Webhooks, cron and replay

A `trigger.webhook` node with `path: "orders"` listens at `POST /hook/orders`.
The JSON body becomes the trigger item; arrays and scalars are wrapped as
`{"body": ...}`. Add `?async=1` to return immediately.

A `trigger.cron` node takes a five-field expression and optional
`CRON_TZ=Europe/Berlin`. Missed windows are not backfilled. Legacy workflow cron
triggers are migrated into jobs on startup.

```sh
./bin/loom replay --data ./data --step summarize <runID>            # verify only
./bin/loom replay --data ./data --step summarize --execute <runID>  # re-run live
./bin/loom replay --data ./data --from notify <runID>               # child run
```

Replay verifies the keyed event chain and every referenced blob first. `--step`
verifies one node without executing it; `--execute` re-runs it live. `--from`
starts a child run and repeats side effects from that node onward.

## 8. Development

```sh
make ui        # npm ci + vite build into web/dist
make build     # embeds web/dist in the Go binary
make check     # fmt, vet, race tests, coverage floors, UI build, example validation
make e2e       # Playwright against a simulated server
```

For UI development, run `cd web && npm run dev`; Vite proxies `/api` and `/hook`
to `127.0.0.1:5678`.

## 9. Operations

```sh
./bin/loom doctor --data ./data                 # chains, blobs and ownership
./bin/loom doctor --data ./data --resume        # continue an interrupted scan
./bin/loom backup --data ./data --out snap.tgz  # consistent snapshot
./bin/loom backup verify --backup snap.tgz      # restore to a temp dir and verify
./bin/loom key rotate --data ./data             # rotate, old runs keep verifying
./bin/loom prune --data ./data --dry-run        # preview retention, no deletes
```

Stop `loom serve` before a backup or key rotation. On an upgraded data
directory, legacy profile ownership stays on the equality branch until you run
`loom ownership migrate --data ./data --dry-run`, review
`data/ownership-migration.json`, and apply with a fresh verified backup
(`loom ownership migrate --apply --backup <archive>`). Then run `loom doctor`;
the full migration and doctor semantics are in [upgrade.md](upgrade.md) and
[ownership.md](ownership.md).

## Where to go next

- [ownership.md](ownership.md) — profiles, overrides, bundles and publish
  resolution.
- [page-grammar.md](page-grammar.md) — the shared list/editor/state grammar.
- [node-reference.md](node-reference.md) — every node and config field.
- [security.md](security.md) — threat model and operational limits.

## Limitations

- Live `http.request` and `db.postgres` are wired; they need reachable services
  and outbound egress. `http.request` has an SSRF guard that refuses private,
  loopback and link-local addresses. Bundled examples use `--sim`.
- Restart recovery re-executes the node that was in flight. HTTP-style nodes send
  a stable idempotency key; SQL writes and services that ignore it are
  at-least-once.
- SQLite is the only store. Multi-writer HA is not supported; a second process
  serves reads but does not schedule or recover runs.
- `code.python` requires `--enable-subprocess-nodes` and `uv` on the host at
publish time. Installed dependencies are pinned to exact versions; runs never
install anything. Python has network access inside its sandbox by design (it
does not share the `http.request` SSRF guard).
- No SSO, tenant isolation or encrypted-at-rest database yet.
