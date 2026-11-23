# Loom

Self-hosted workflow automation with one differentiator: **every run is an
immutable, hash-chained event log you can replay**. Build a typed graph, run it
manually, on a schedule, as a batch, or from a webhook; the log is both the
audit trail and the debug trail.

## Quick start

Requires Go 1.26+ (Node 20+ only to rebuild the embedded editor).

```sh
make build

printf '%s' 'choose-a-strong-password' | \
  ./bin/loom user create --data ./data --email you@example.com --role admin --password-stdin

./bin/loom serve --data ./data          # http://localhost:5678
```

The first account must be created from the CLI; there is no default password.

Docker:

```sh
docker build -t loom .
docker run -p 5678:5678 -v loom-data:/data \
  -e LOOM_BOOTSTRAP_ADMIN_EMAIL=you@example.com \
  -e LOOM_BOOTSTRAP_ADMIN_PASSWORD='choose-a-strong-password' \
  loom
```

`LOOM_BOOTSTRAP_ADMIN_*` only creates an account when the database has none;
unset the password after the first start.

With compose (same bootstrap variables, passed through `compose.yaml`):

```sh
LOOM_BOOTSTRAP_ADMIN_EMAIL=you@example.com \
LOOM_BOOTSTRAP_ADMIN_PASSWORD='choose-a-strong-password' \
docker compose up -d
```

The default distroless image contains only the Loom binary and does **not**
support `code.python`. Build the Python-enabled variant when using
`--enable-subprocess-nodes`:

```sh
docker build -f Dockerfile.python -t loom:python .
```

That image includes `python3` and a pinned `uv`, and still runs as a non-root
user with `/data` as the volume.

## Why replay matters

- Runs are append-only events: HMAC-chained, gap-free `seq`, actor-bound
  (chain v2), with step inputs/outputs in content-addressed blobs.
- `loom replay --step <node> <run>` verifies a step against its recorded output
  and executes nothing; `--hermetic` replays from the recorded effect tape with
  no network; `--from <node>` re-executes as a child run.
- Signing keys rotate without breaking history (`loom key rotate`); old runs
  keep verifying, credentials are re-sealed.
- `loom doctor` re-verifies every chain, blob and ownership record;
  `loom backup` / `loom backup verify` / `loom backup restore` move a verified
  data directory.

## Platform support

Loom ships binaries for **Linux and macOS** (amd64 + arm64; see
`.goreleaser.yaml`). Windows is intentionally unsupported: the live
execution path manages Unix process groups (`SIGHUP`/`setsid` semantics) for
subprocess nodes, and the security posture of the Python sandbox is grounded
in Unix process controls. Building from source on Windows is not tested and
the server there is not supported; on Windows use the container image or
WSL.

## Features

- **Typed graph, server-validated**: cycles, ports, credentials, trigger rules.
- **Reusable configuration profiles with explicit ownership**: a profile holds
  one node type’s non-secret defaults; a node inherits, overrides or stays
  local, and a deliberate equal-value override survives a profile upgrade.
  Bundles can supply mappings, contracts and scripts. Publishing resolves and
  snapshots the effective values, so runs and replay never read the profile
  store.
- **21 built-in nodes**: triggers (manual, webhook, cron, event, subflow),
  `http.request`, `db.postgres`, `code.run` (goja), `code.python` (opt-in
  subprocess), `data.map` plus JSON/text templating, and flow control (if,
  switch, merge, loop, wait, approval, subflow, emit).
- **`http.request` full surface**: all methods, path/query/header templating,
  JSON/raw/form/multipart/GraphQL bodies, credential/bearer/basic/apiKey/OAuth2
  auth, pagination (page/offset/cursor), response-to-blob, `successCodes`,
  per-node rate limit, SSRF guard, proxy support. Retries use the engine retry
  policy (per-attempt tapes).
- **`code.python`** (opt-in `--enable-subprocess-nodes`): pinned `name==version`
  requirements built at publish time with `uv` into hash-verified
  `data/envs/<hash>/`; runs never install or touch the network. Recorded on the
  effect tape and verified on hermetic replay. Hardened per execution: OS
  sandbox where available, memory/time/output caps, package policy file. On
  Linux there is no OS-level sandbox — run Python workflows inside a
  container; the process-level caps are not a security boundary.
- **Operations**: approvals with single-use links, restart recovery, cron and
  one-time jobs, batches, subflows, retention pruning with `--dry-run`.
- **Offline by default**: CLI runs use a deterministic simulated host;
  `--live` enables real network. SQLite only; the editor is embedded in the
  binary.

## Roles

Cookie sessions, argon2id passwords, CSRF-protected mutations, per-user API
tokens. Single-tenant.

| Role | Can |
|---|---|
| `viewer` | read workflows, runs, events, blobs, diffs |
| `operator` | run, retry, cancel, replay, approve |
| `editor` | create/edit/publish workflows, manage jobs, preview expressions |
| `admin` | everything, plus credentials, users, tokens and auth audit |

`LOOM_TOKEN` remains an audited break-glass service identity. Webhooks and the
scheduler run as `system:webhook` / `system:scheduler`.

**Subprocess privilege caveat.** When `--enable-subprocess-nodes` is enabled,
an editor can publish and execute arbitrary Python. Python now receives an
explicit allowlisted environment (not the server’s full environment), but on a
host or container where that Python can read Loom’s data directory or other
service secrets, an editor is effectively an admin. Run subprocess-enabled
servers in a dedicated container or VM with the data directory isolated from
the Python process where possible.

## CLI

```sh
loom validate <file...>                 # schema + graph validation
loom run|test <file>                    # simulated; --live, --input, --auto-approve
loom replay <run> [--step N] [--execute] [--from N] [--hermetic]
loom verify --from-run <run>            # replay a workflow version against a run
loom serve [--data DIR] [--addr :5678] [--sim] [--enable-subprocess-nodes]
loom runs|workflows|runbook|prune [--dry-run]
loom profile|script|schema|mapping|hub <list|export|import>
loom backup [verify|restore]            # verified snapshots of the data dir
loom ownership migrate [--dry-run|--apply|--rollback]  # explicit profile-ownership migration
loom doctor [--resume]                  # chains, blobs, ownership and policies
loom key <rotate|list>                  # signing-key keyring
loom user|token <...>                   # accounts and scoped API tokens
```

`--allow-anonymous` is development-only. `--cookie-secure` (or
`LOOM_COOKIE_SECURE=1`) is required behind HTTPS.

## Testing

```sh
make check        # fmt, vet, race tests, coverage floors, UI build, example validation
make e2e          # Playwright, desktop + mobile Chromium plus the isolated project
make perf         # engine performance budgets
```

## Docs

- [docs/quickstart.md](docs/quickstart.md) — build, run, API and approvals.
- [docs/ownership.md](docs/ownership.md) — profiles, overrides, bundles,
  migration and effective-value precedence.
- [docs/page-grammar.md](docs/page-grammar.md) — list, editor, destructive-action,
  state and accessibility grammar.
- [docs/node-reference.md](docs/node-reference.md) — every node and config field.
- [docs/security.md](docs/security.md) — threat model, auth, audit chain, limits.
- [SECURITY.md](SECURITY.md) — reporting a vulnerability.
- [docs/schema-spec.md](docs/schema-spec.md) — workflow, run and storage schema.
- [docs/upgrade.md](docs/upgrade.md) — breaking changes and bootstrap notes.
- [docs/performance.md](docs/performance.md) — CI performance budgets.
- [docs/adr/](docs/adr/) — architecture decisions.

## License

Apache-2.0. See [LICENSE](LICENSE).
