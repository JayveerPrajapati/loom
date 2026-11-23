# Upgrading Loom

Back up the data directory (including `keys/`, or `LOOM_SECRET_KEY_FILE`, or
`secret.key`) before upgrading. Opening an older database runs additive
migrations; no rows are rewritten.

## Unreleased — configuration ownership

Opening a data directory does **not** migrate ownership. Legacy nodes — a
`profileRef` with no `profileOverrides` map — keep the equality branch for one
release, and the operator migrates explicitly:

```sh
loom ownership migrate --data ./data --dry-run
loom backup --data ./data --out loom-backup-<timestamp>.tar.gz
loom ownership migrate --data ./data --apply --backup loom-backup-<timestamp>.tar.gz
```

The migration only touches workflow nodes that have a `profileRef` and no
`profileOverrides` map; a node that already has a map is skipped, so a second
run is a no-op. Stored config values are never changed.

`--apply` requires a backup created within 24 hours that passes the same chain
and blob verification as `loom backup verify`. Each changed node is copied to
the `ownership_migration_backup` table with its exact original and migrated
workflow docs; `loom ownership migrate --rollback` restores the latest
migration, and refuses if a draft changed after the apply. Every applied
decision writes an `ownership.migrated` audit event.

A stored key is marked **inherited** only when all four pieces of evidence
agree: the pinned profile version supplies it, the stored value equals the
pinned value, the last published snapshot contains that node/key, and the stored
value equals the published value. Everything else is preserved as an
**override**, including:

- a value that differs from the pinned profile;
- a key added after the pin or after the last publish;
- a draft edited after the last publish;
- a node with no published snapshot evidence;
- a profile or profile version that cannot be read.

Ambiguity always preserves the authored value. The migration does not guess
from value equality alone.

### Migration report

The dry-run writes:

`<data>/ownership-migration.json` (mode `0600`)

The report records the generated time, workflow/node counts, inherited/override
counts, and one change per key:

```json
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
```

Review the report if a node’s ownership looks surprising. The report is
diagnostic: it is not included in `loom backup`, it contains keys and decisions
rather than config values, and a failed apply leaves it in place so the
decisions remain inspectable. The migration is implemented in
`internal/store/ownership_migration.go`; the full semantics are in
[ownership.md](ownership.md).

After upgrading, republish workflows that should record fresh
`configSources` provenance. Published snapshots from older versions remain
compatible; a node with no ownership map is a legacy node, not a corrupt one.

## Unreleased — safety and semantics

- New `code.python` node (behind an operator opt-in):
  subprocess execution is recorded on the effect tape and verified on
  hermetic replay without re-running anything. Requires
  `--enable-subprocess-nodes` on serve (or `LOOM_ENABLE_SUBPROCESS_NODES=1`);
  `loom run` and `replay --live` accept the same flag, and `loom run` gains
  `--record-effects` for effect-tape parity with serve. Dependencies are
  pinned (`name==version`), built at publish time with uv into
  hash-verified `data/envs/<hash>/` environments; runs never install
  packages.
- Subprocess hardening: a per-execution writable workdir (removed after the
  run), an OS sandbox on macOS (Seatbelt: writes only to the workdir, exec
  only the interpreter; applied when `sandbox-exec` is available), an
  `RLIMIT_AS` memory cap where the kernel supports it (macOS refuses to
  lower it — the refusal is printed to the run's trace, never silent), and
  a package policy file `data/subprocess-policy.json` (or
  `--subprocess-policy <path>`) with an allowlist and a per-environment
  size cap; `loom doctor` reports the policy and environment sizes.
- `loom key rotate` rotates the current signing key while historical chains
  keep verifying; old keys are retained under `data/keys/`.
- `loom backup`, `loom backup verify` and `loom backup restore` snapshot,
  verify and unpack the data directory; restore rejects archive traversal and
  unknown entries.
- `loom doctor` runs the full integrity scan: event-chain verification, blob
  hashing, missing/orphaned blobs and structural ownership findings; see
  [Doctor](#doctor) below.
- `loom prune --dry-run` reports what would be pruned with a storage
  estimate, without deleting anything.
- `db.postgres` requires a single SQL statement; a missing `operation` behaves
  as a read-only query.
- Batch fan-out is capped at 32 concurrent items and 10 000 items.
- `flow.wait` is capped at 30 days; `flow.switch` supports at most three cases.
- `flow.subflow` honours `wait`; `wait: false` starts the child asynchronously
  and returns the input item.
- `flow.merge` supports `shallow` (default), `append` and `byIndex`. A node
  with no explicit `mode` shallow-merges; set `mode: "append"` explicitly to
  keep the previous append default.
- `http.request` response body keys win over the envelope; `statusCode` and
  `statusText` are always present.
- Reader roles receive `[redacted]` workflow secrets.
- Webhook secrets are not persisted in the stored run input.
- Login rejects a cross-origin `Origin`; behind a proxy use
  `--trust-forwarded-host`.
- `--prod` forces `Secure` on session cookies.
- Run event streams re-authenticate and close after logout, disable or
  revocation.
- Expired approval links cancel the waiting run.
- `maxParallelRuns` counts root runs; children inherit the parent's slot.
- A `from` replay that cannot get a run slot returns `409`.

## 0.32 — accounts, sessions and attribution

### Users are required

A single shared bearer token is no longer the default. Create the first account
before starting the server:

```sh
printf '%s' 'strong-password' | \
  loom user create --data ./data --email you@example.com --role admin --password-stdin
```

`loom serve` refuses to start with no users and no token. For containers, set
`LOOM_BOOTSTRAP_ADMIN_EMAIL` and `LOOM_BOOTSTRAP_ADMIN_PASSWORD` once; Loom only
uses them when the database has no users. Unset the password afterwards.

`--allow-anonymous` (or `LOOM_ALLOW_ANONYMOUS=1`) restores unauthenticated admin
access for local development only.

### Login, sessions and CSRF

The editor signs in with email/password and keeps an `HttpOnly` session cookie.
Mutations require a CSRF header; the UI handles this. CLI and CI should use
per-user API tokens:

```sh
loom token create --data ./data --email you@example.com --name ci
```

`LOOM_TOKEN` still works as an audited break-glass admin identity.

Behind HTTPS, pass `--cookie-secure` or set `LOOM_COOKIE_SECURE=1`.

### Actor attribution and chain v2

New runs store `actor` and sign it into the event chain (`chain_version = 2`).
Replay, diff and restart recovery verify it. Runs created earlier keep verifying
under `chain_version = 1`; pre-chain runs are gap-checked only.

### Other changes

- New CLI groups: `loom user` and `loom token`.
- New tables: `users`, `sessions`, `api_tokens`, `auth_events`.
- `runs.actor` and `events.actor` are added automatically.
- Roles are enforced server-side; the editor hides actions it cannot perform.

## 0.31 — replay, environment and blobs

### `$env` is deny-by-default

Workflow code cannot read the process environment unless names are opted in:

```sh
LOOM_EXPR_ENV_ALLOW=REGION,FEATURE_* loom serve
```

`LOOM_SECRET*`, `LOOM_TOKEN`, `LOOM_SECRET_KEY_FILE`, `AWS_*`, `GCP_*`,
`GOOGLE_*` and `AZURE_*` are always denied.

### `--step` verifies instead of re-executing

`loom replay <run> --step <node>` verifies the recorded blobs without calling the
host. Add `--execute` (API: `{"mode":"step","nodeId":"...","execute":true}`) to
re-run the node live. `--from` still executes the node and descendants as a new
child run.

### Blobs are stored verbatim

Content-addressed blobs keep the exact bytes so replay is faithful. The API and
editor mask sensitive fields at the display boundary; execution and replay use
the raw bytes. Blob directories are `0700`.

### Event chain

Runs carry an HMAC-SHA256 `prev_hash`/`hash` chain. Replay, diff and restart
recovery verify it; edits, reordering, hash edits and stripped chains are
rejected.

### Scheduler

The scheduler provides active/standby restart recovery on one SQLite file with
per-process job dedupe. It is not multi-writer HA and does not guarantee
zero-duplicate execution across processes.

### Keyring and rotation

Signing keys live in `data/keys/` and are versioned per run (`runs.key_id`).
`loom key rotate` makes a new key current, re-seals stored credentials, and
retains old keys — historical runs keep verifying after rotation. A secret
pinned via `LOOM_SECRET_KEY_FILE` stays single-key and refuses rotation.

## Doctor

`loom doctor --data DIR` is a read-only integrity scan. It never executes
workflow code. It verifies:

- every run’s event chain and chain-head seal;
- every indexed blob’s hash, plus missing and orphaned blob files;
- structural ownership metadata in drafts and published versions;
- Python environment manifests, subprocess policy and per-environment size;
- the egress policy and the bootstrap-password warning.

Ownership findings are printed as `OWNERSHIP` lines and make the verdict
non-zero. The checks are structural:

- `profileOverrides` present without `profileRef`;
- a false override value (inherited keys are omitted);
- an override key the node does not store;
- a `configSources` origin outside `node|profile|bundle|catalog|script`;
- a `configSources` key the node does not store.

A node with no `profileOverrides` map is compatible; an empty map is valid.

### Resumable scans

`loom doctor --resume` continues after the last run in the longest verified
prefix. Progress is checkpointed at `<data>/doctor.checkpoint` (at most one
write every two seconds). On success the checkpoint is removed; on failure it
is kept together with the failing runs so `--resume` re-checks rather than
skips them. If the checkpoint is missing or names an unknown run, the scan
starts from the beginning.

The checkpoint and the ownership migration report are diagnostic files, not
backup payload. `loom backup` includes the database, blobs, keyring, Python
environments, `secret.key`, `chain.migrated`, `head.sealed` and
`subprocess-policy.json`.

## Verification

```sh
go build ./...
go vet ./...
go test ./... -count=1
```

`go test ./...` runs with no skips. The Postgres integration test runs in CI
against a service container; locally set `LOOM_TEST_POSTGRES_DSN` to enable it.
Opening a pre-keyring, pre-chain data directory is covered by
`TestOpenUpgradesLegacyDataDir` and `TestUpgradeIsIdempotent` in
`internal/store/upgrade_test.go`: the old rows survive, the legacy signing key
is stamped, previously unsigned events become a verifying chain, and the same
health check `loom doctor` runs passes.
