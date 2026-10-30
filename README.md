# Loom

**Workflows you can trust.** A single-binary, self-hosted automation canvas where
every connection is type-checked and every run can be replayed.

> Status: **M4 hardening complete; v1.0 tag pending**. The React editor is embedded in the binary: typed
> canvas edges, live SSE runs with reconnect, an inspector, run history,
> blob-backed step detail, replay-from-node, run diffs, subflow navigation,
> generated runbooks, retention, token-link approval callbacks, and polished
> failure/waiting/offline/validation states. Engine coverage is above 80% and
> E2E runs every bundled example. See [PLAN.md](PLAN.md).

## Why

n8n is a great product, but its graphs are untyped, replay is partial, and
self-hosting needs Node + a database (+ Redis for queue mode). Loom takes the
opposite bet:

| | n8n | Loom |
|---|---|---|
| Edge validation | runtime discovery | compile-time type check |
| Replay | retry from failed node | deterministic full/step replay |
| Install | Node + DB (+ Redis) | one binary, SQLite |
| Nodes | 1,500 uneven | 12 complete, tested |
| License | fair-code | Apache-2.0 |

## Quickstart

```sh
make ui                     # build the React editor into web/dist
make build
./bin/loom validate examples/*.json
./bin/loom plan examples/postgres-report.flow.json
./bin/loom test examples/daily-digest.flow.json   # simulated, no network
./bin/loom run examples/refund-approval.flow.json # waits for approval
./bin/loom serve            # http://localhost:5678 (live host)
./bin/loom serve --sim      # deterministic host for demos and tests
```

Open `http://localhost:5678` for the editor. For UI development, run
`cd web && npm install && npm run dev`; Vite proxies `/api` and `/hook` to the
Go server on port 5678.

No Go? Install Go 1.26+ first. Go and Node are only needed to build; the shipped
binary embeds the editor. `run` and `test` use the simulated host by default;
pass `--live` to call real endpoints.

## Develop

```sh
make check   # gofmt, vet, race tests, all-package coverage, UI build, examples
make e2e     # Playwright: desktop Chromium + Pixel 5 mobile
make hooks   # install .githooks (pre-commit format/vet, pre-push race tests)
```

`make test-race` requires a C toolchain because Go's race detector uses cgo.

`make coverage-all` enforces a 50% all-package floor; `make coverage` keeps the
engine at 80%. CI runs `test-race`, both coverage gates, `govulncheck`, the
performance budget and Playwright on every push and pull request.

Docker (distroless, editor embedded, SQLite in a volume):

```sh
docker build -t loom:local .
docker run --rm -p 5678:5678 -v loom-data:/data loom:local
# deterministic demo: append --sim
LOOM_TOKEN=secret docker compose up --build
```

## What works

- `loom validate` — schema, config, credential, port, trigger and cycle checks.
- `loom plan` — prints the DAG as parallel execution levels.
- `loom run` / `loom test` — execute the DAG, log every step, suspend on
  approvals, retry failed nodes with a stable `Idempotency-Key` on HTTP calls.
- `loom replay <run>` — verify a run, re-execute one node in place (`--step`),
  or start a new run from a node (`--from`).
- `loom runbook <workflow>` — generate a Markdown operations runbook.
- `loom prune --before 30d` — delete old terminal runs, events and orphan blobs.
- `loom export` / `loom import`, plus `workflows` and `runs` listings.
- `loom serve` — workflow CRUD, run/resume/cancel, SSE events, webhook entry,
  cron triggers, retention (`--retention-days`), optional bearer-token auth and
  a deterministic `--sim` host for demos/tests.
- React editor — node library, React Flow canvas with typed ports and inline
  validation, inspector with upstream data picker and expression preview,
  credential selection, undo/redo, command palette, light/dark themes.
- Live runs — async start, streaming node status, cancel and approval resume;
  waiting runs expose a copyable `/hook/approval/{token}` link (Slack payload
  compatible) alongside the in-app Approve/Reject actions.
- Run history — per-workflow list, event log, step input/output inspection,
  canvas view of past runs, child-run navigation and runbook download.
- Resilient states — SSE reconnect with backoff, an offline/server-reachability
  banner, explicit failure/waiting treatments and issue-to-node validation.
- SQLite event store with content-addressed blobs and encrypted credentials;
  credential create/list/delete API and inspector selection.
- HTTP configuration covers path/query params, raw/JSON/form bodies, credential
  auth, success-code matching and optional fail-on-error.
- Distroless Docker image, Compose example and GoReleaser releases for
  linux/darwin/windows on amd64/arm64.
- Five example workflows that validate and execute with zero warnings.
- `go test ./...` — graph, engine replay/resume and HTTP API tests.

## Examples

| File | Shows |
|---|---|
| `examples/refund-approval.flow.json` | webhook → HTTP → if → approval → refund/ticket |
| `examples/daily-digest.flow.json` | cron → HTTP → code → Slack |
| `examples/postgres-report.flow.json` | cron → Postgres (`rows`) → code (`message`) → Slack |
| `examples/crm-sync.flow.json` | webhook → set → HTTP → if → audit/alert |
| `examples/publish-approval.flow.json` | manual → HTTP → approval → publish/reject |

## Layout

```
cmd/loom/            single binary entrypoint
internal/graph/      workflow model, loader, validator, planner
internal/flow/       runtime contracts (Item, Output, Node, Host, Suspend)
internal/expr/       template rendering and goja evaluation
internal/host/       simulated and live effect adapters
internal/nodes/      node catalog and runtime implementations
internal/store/      SQLite store, event log, blobs, credentials
internal/engine/     DAG executor, retries, suspend/resume, replay
internal/server/     API, SSE, webhooks, cron scheduler
schema/              JSON Schemas: workflow, execution, node definition
examples/            runnable, validated workflows
docs/adr/            architecture decisions
docs/                scope, design principles, UX wireframe
web/                 React editor, embedded at build time
```

## Architecture

- **Go + SQLite + WASM + React.** Rationale in
  [ADR-0002](docs/adr/0002-stack.md).
- **Event-sourced runs.** Append-only log, content-addressed step results,
  deterministic replay. Rationale in [ADR-0001](docs/adr/0001-execution-model.md).
- **Typed graph.** Nodes declare input/output contracts; the validator rejects
  incompatible edges before a run.
- **Deterministic by default.** A simulated host runs every example offline;
  real network access is an explicit `--live` choice.

## Documents

- [PLAN.md](PLAN.md) — live tracker and milestones.
- [docs/quickstart.md](docs/quickstart.md) — 15-minute build, run, API and approval tour.
- [docs/node-reference.md](docs/node-reference.md) — every node, port and config field.
- [docs/schema-spec.md](docs/schema-spec.md) — workflow, execution and node-definition contracts.
- [docs/security.md](docs/security.md) — threat model, controls and residual risks.
- [docs/performance.md](docs/performance.md) — CI performance budgets.
- [docs/v1-scope.md](docs/v1-scope.md) — what is in and, importantly, out.
- [docs/design-principles.md](docs/design-principles.md) — Apple HIG rules.
- [docs/ux-wireframe.md](docs/ux-wireframe.md) — canvas, replay, empty state.
- [docs/adr/](docs/adr/) — decisions.

## Roadmap

| Phase | Status |
|---|---|
| M0 spec, schemas, validator, examples | done |
| M1 engine, event store, replay, CLI | done |
| M2 editor, typed edges, run history | done |
| M3 approvals, subflows, replay UI, diff, runbooks, retention, states | done |
| M4 tests, docs, Docker, releases | complete (tag pending) |

## License

Apache-2.0. See [LICENSE](LICENSE).
