# Security model

Loom is a self-hosted, single-tenant workflow engine. The operator controls the
host; authenticated users are trusted to author workflows; inbound webhook
callers are not trusted.

## Authentication and authorization

- Named accounts with argon2id password hashes (64 MiB, t=3, p=2).
- Opaque 256-bit session tokens, stored only as SHA-256 hashes, sent as
  `HttpOnly`, `SameSite=Lax` cookies; `Secure` when TLS is terminated at the
  process or `--cookie-secure` / `LOOM_COOKIE_SECURE=1` is set.
- Sessions expire after 12 hours by default (`--session-ttl`), rotate on login,
  and are revoked on password change or when an account is disabled.
- CSRF double-submit: mutations authenticated by a session require
  `X-CSRF-Token` matching the `loom_csrf` cookie. Bearer/API-token requests are
  exempt.
- Login failures are rate limited per IP+email (10 per 15 minutes) and unknown
  accounts still pay a dummy argon2 verification.
- Roles are enforced server-side on every route: `viewer` (read), `operator`
  (run/retry/cancel/replay/approve), `editor` (workflows, jobs, expressions) and
  `admin` (credentials, users, tokens, auth audit). Undeclared routes fail closed.
- Per-user API tokens are scoped to a role no higher than the owner's, hashed at
  rest, revocable, and may carry an expiry. By design **any authenticated user,
  including viewers, may mint personal tokens capped to their own role** — a
  viewer's token can only read. Changing or resetting a password revokes that
  user's tokens. Token lifetimes are bounded server-side: a token minted
  without an explicit expiry gets exactly `--max-token-lifetime`
  (`LOOM_MAX_TOKEN_LIFETIME`, default 720h = 30 days), and longer explicit
  expiries are rejected. Setting the maximum to `0` removes the cap and allows
  non-expiring tokens — an operator opt-out, never a default. Tokens created
  before the cap was introduced keep their stored expiry; tokens minted via the
  local `loom token` CLI are governed by operator trust (data-directory access),
  not the serve flag. `token_created` / `token_revoked` audit events (admin
  visible) record actor, IP and token ID.
- `LOOM_TOKEN` is an audited break-glass `service` identity with admin rights.
  Prefer named users and scoped tokens.
- No default credentials. The first account comes from `loom user create`, or
  from `LOOM_BOOTSTRAP_ADMIN_EMAIL` / `LOOM_BOOTSTRAP_ADMIN_PASSWORD` only while
  the database has no users.
- Auth and user-administration events are written to `auth_events` (best-effort
  operational audit; not part of the run chain).

Single-tenant only. SSO/OIDC, SCIM, tenant isolation, email password reset and
audit export are not implemented.

## Asset visibility and editing

- A private profile, mapping, data contract, script or connector is visible and
  usable only by its creator and admins. Other callers get 404, never 403, so
  the asset's existence is not disclosed.
- Shared assets are reusable by every editor, but only the creator or an admin
  may edit them or change their visibility. A shared dependency cannot be
  silently rewritten by another author.
- Draft save and publish reject a reference to another user's private asset with
  a named error. A creator may publish a workflow that uses their own private
  assets; publish resolves those values into the immutable workflow version, so
  the published snapshot is the disclosure boundary. The publish audit detail
  names the resolved private assets.
- Ownership metadata (`profileRef`, `profileOverrides`, `configSources`) is
  descriptive, not an authorization grant. It never makes a private profile
  readable and never bypasses the asset-visibility check at save or publish.
- Connector-authored node types appear in `/api/node-types` only when the caller
  can see the connector. Hidden connector versions, usage and mutations return
  404.

## Run audit chain

Every run event is chained with HMAC-SHA256 over the stored bytes, using a key
derived from the credential secret. Runs created since chain v2 include the
actor in the digest, so attribution cannot be edited in place either.

- Replay, diff and restart recovery verify the chain before using the log.
  In-place edits, reordering, hash edits and stripped chains are rejected.
- Every run row carries a chain-head seal: an HMAC over the run's event count
  and terminal hash. Deleting the tail of a chain leaves a gap-free prefix,
  but the seal no longer matches, so truncation is rejected. The one-time
  sealing marker (`data/head.sealed`) lives outside the database so a
  DB-only attacker cannot delete it and have the next start re-bless a
  truncated store.
- Runs created before the chain (`chain_version = 0`) are gap-checked only.
- Rotating the signing key is safe: `loom key rotate` generates a new key and
  keeps the previous keys in the on-disk keyring (`data/keys/`), so runs
  recorded under an old key keep verifying and their credentials stay
  readable. Run `rotate` while `loom serve` is stopped; a pinned secret
  (`LOOM_SECRET`/`LOOM_SECRET_KEY_FILE`) cannot be rotated in place.
- `actor` values are `user:<id>`, `token:<id>`, `service:loom-token`,
  `system:scheduler`, `system:webhook`, `system:recovery`,
  `system:approval-link`, or `system:engine` for unsolicited runs.

## Data protection

- Credentials are encrypted at rest with AES-256-GCM; the key comes from
  `LOOM_SECRET`, `LOOM_SECRET_KEY_FILE`, or the on-disk keyring in the data
  directory (a pre-keyring `secret.key` is imported as its first entry). The
  list endpoint never returns secret data.
- Content-addressed blobs store the exact bytes so replay is faithful. Blob
  directories are created `0700`, but blobs are not encrypted: anyone with
  filesystem access can read recorded payloads.
- The API and editor mask sensitive keys (authorization headers; password,
  secret, token, api_key, private_key, credential) at the display boundary.
  `GET /api/blobs/{hash}`, run data-context and replay responses are masked;
  execution and replay read the raw bytes.
- Workflow code cannot read the process environment by default. Opt names in
  with `LOOM_EXPR_ENV_ALLOW`; `LOOM_SECRET*`, `LOOM_TOKEN`,
  `LOOM_SECRET_KEY_FILE`, `AWS_*`, `GCP_*`, `GOOGLE_*` and `AZURE_*` are always
  denied.

## Network and execution

- `http.request` uses a guarded dialer that rejects private, loopback and
  link-local destinations after DNS resolution; redirects are re-checked.
- `--allow-private-targets` (`LOOM_ALLOW_PRIVATE_TARGETS=1`) is a self-hosted
  opt-in that disables the SSRF private-target guard for live requests,
  redirects and a configured server-wide proxy address. It is intended for
  trusted local testing and internal services, never for a multi-tenant or
  public deployment. It does not bypass the per-workflow egress allowlist or a
  credential's `allowedHosts`; both layers still apply. `loom serve --help`
  and the startup posture name the flag.
- Webhooks can require a shared secret header or an HMAC-SHA256 signature over
  the raw body. Approval links are unguessable capability URLs.
- `code.run` scripts execute in goja without filesystem, process or Go object
  access. `$http` is routed through the guarded host, so the SSRF guard applies
  and every call is recorded on the effect tape (hermetic replay never dials).
  Scripts are interrupted after 2 seconds by default (`timeoutSeconds`, max
  300) or when the run is cancelled.
- `code.python` is disabled unless the operator starts serve (or `loom run`) with
  `--enable-subprocess-nodes` (`LOOM_ENABLE_SUBPROCESS_NODES=1`). Requirements
  must be pinned as `name==version`; they are installed at **publish** time by
  `uv` into `data/envs/<sha256>/` (the manifest must hash to the directory
  name), and runs never install or fetch anything. The subprocess is recorded on
  the effect tape, so replay verifies the step without executing it.
  Per execution: a fresh writable workdir (removed afterwards), an explicit
  allowlisted environment (isolated `HOME`/`TMPDIR`, a controlled `PATH`, locale,
  and non-secret TLS trust-store variables — never the server’s inherited
  environment), an OS sandbox on macOS (Seatbelt: writes confined to the
  workdir, exec confined to the interpreter; reads and network stay open, so
  Python bypasses the `http.request` SSRF guard by design), an `RLIMIT_AS`
  memory cap where the kernel allows
  lowering it (macOS refuses — the refusal is written to the run trace, never
  silent), 1 MiB stdout/stderr caps, a process-group kill on timeout, and a
  package policy file (`data/subprocess-policy.json` or `--subprocess-policy`)
  with an allowlist and per-environment size cap, enforced before and after
  build and reported by `loom doctor`.
- **Linux requires a container for `code.python`.** Linux has no OS-level
  sandbox here (Landlock/seccomp cannot be applied between Go's fork and
  exec), so a Python subprocess on Linux runs with process-level caps only.
  Treat a container (or VM) as a hard requirement when
  `--enable-subprocess-nodes` is on, on Linux; the process-level caps are
  containment, not a security boundary. When the flag is enabled, serve
  prints this warning at startup and `GET /healthz` reports the execution
  posture (`subprocessNodes`: enabled, policy path, per-env cap, and
  `osLevel: "none"` on Linux).
- **Supported container pattern.** Build the Python-enabled image and run it
  with the data directory on a volume:

  ```sh
  docker build -f Dockerfile.python -t loom:python .
  docker run -d --name loom \
    -p 8080:5678 \
    -v loom-data:/data \
    -e LOOM_BOOTSTRAP_ADMIN_EMAIL=you@example.com \
    -e LOOM_BOOTSTRAP_ADMIN_PASSWORD='choose-a-strong-password' \
    loom:python --enable-subprocess-nodes --prod
  ```

  Both images set the entrypoint to `loom serve --addr :5678 --data /data`, so
  flags passed after the image name append to `serve`. The default distroless
  image contains only the Loom binary and does not support `code.python`;
  `code.python` requires the `Dockerfile.python` variant built above.

  The container is the isolation boundary for `code.python` on Linux. Pin an
  explicit package policy (`/data/subprocess-policy.json` inside the volume,
  or `--subprocess-policy /path/to/policy.json`) in production: the default
  allow-all policy exists for evaluation only. `loom backup` includes the
  policy file, so it survives restores.
- Request bodies are capped (1 MiB webhooks, 10 MiB HTTP responses); concurrent
  webhooks are bounded.
- Run timeouts are workflow-configured; runs are also cancelled on server
  shutdown.

## Public endpoints

`/healthz`, `/readyz`, `/api/version` and `/metrics` are intentionally
unauthenticated read-only probe surfaces.

- `/healthz` and `/readyz` report process and store liveness/readiness; they
  return status only.
- `/api/version` returns only the build identity
  (`{"name":"loom","version":"..."}`) — no workspace, user, run, configuration
  or credential data. It is public so deployment probes and the unauthenticated
  shell can identify the build. If the build version itself is sensitive,
  terminate authentication at a reverse proxy for this path too.
- `/metrics` exposes Prometheus counters (run and decision counts) for a
  sidecar scraper. On a public host, restrict it at the reverse proxy or
  firewall.

The `/hook/*` webhook and approval capability URLs are unauthenticated by
design and carry their own shared-secret/HMAC or unguessable-token checks.

## Operational

- Terminate TLS in front of Loom and set `--cookie-secure`. For proxy-aware
  login throttling and auth audit attribution, set
  `--trusted-proxy-cidr CIDR` (or `LOOM_TRUSTED_PROXY_CIDRS`) to the network
  that contains a reverse proxy known to overwrite `X-Forwarded-For`. Loom
  uses the rightmost forwarded hop only from a trusted proxy address; direct
  clients cannot choose their throttle or audit IP. Leave it empty when not
  behind such a proxy.
- `GET /metrics` is unauthenticated so a sidecar scraper works. Put it behind a
  proxy on public hosts.
- `/healthz` is liveness; `/readyz` checks the store.
- Keep the data directory on local disk. SQLite WAL on a network filesystem is
  unsupported.
- `loom backup` writes a consistent tar.gz snapshot (database via VACUUM INTO,
  blobs, keyring, Python environments under `data/envs/`, the subprocess
  package policy, and the chain/head-seal markers) and
  `loom backup verify --backup F` restores it to a
  temporary directory and re-verifies every event chain and blob hash before
  you trust it. `loom doctor --data DIR` runs the same scan plus structural
  ownership checks against a live data directory. Stop `loom serve` before
  taking a backup. The ownership migration report and `doctor.checkpoint` are
  diagnostic files outside the backup payload.
- `--prod` rejects short `LOOM_TOKEN` values and expects named users.

## Hardening guarantees
- **Workflow secrets are write-only for readers.** `GET /api/workflows/{id}`,
  `/versions/{v}` and `/runs/{id}/workflow` return `secret`/`hmacSecret` as
  `[redacted]` with `secretSet: true` for roles below `editor`. Editors and
  admins keep the plaintext so the editor still works.
- **Webhook credentials are not persisted.** The configured secret header, the
  HMAC signature header, `Authorization`, and `query.secret` are removed before
  the run input is stored. Other headers and query parameters are unchanged.
- **Query-string webhook secrets are deprecated.** Header and bearer secret
  authentication are preferred. If `?secret=` is used, Loom logs a warning
  because URLs can leak through proxy and access logs. Once callers have
  migrated, start the server with `--reject-webhook-query-secrets`
  (`LOOM_REJECT_WEBHOOK_QUERY_SECRETS=1`) to refuse query-string secrets
  outright; header and bearer authentication are unaffected.
- **Timestamped HMAC is opt-in.** With `hmacTimestamps: true`, Loom requires an
  RFC3339 timestamp header, signs `"<timestamp>.<body>"`, and rejects requests
  outside a ±5-minute window. Legacy body-only HMAC remains available only
  when timestamp mode is disabled.
- **SQL is single-statement.** `db.postgres` rejects a second statement, DDL/DML
  under `operation:"query"` (including `SELECT … INTO`, data-modifying CTEs and
  `MERGE`), and comments hiding a statement. A missing `operation` behaves as a
  read-only query. `host.Live.Query` repeats the check before dialing.
- PostgreSQL DSNs built from host fields default to `sslmode=require`.
  `sslmode=disable` is accepted only when explicitly configured; use it only on
  an isolated local network. A complete connection URL or DSN is passed through
  unchanged so operators retain full lib/pq control.
- `db.postgres` maintains a bounded connection cache keyed by the resolved
  DSN. At most 8 pools are retained; each pool has at most 4 open, 2 idle
  connections, 5-minute idle lifetimes, and 30-minute connection lifetimes. The
  guarded dialer still applies, and server shutdown closes every pool.
- **Hermetic replay fails closed.** A `from`/`from-hermetic` replay that would
  have to wait for a run slot is cancelled and answers `409`; it never executes
  a parked state or touches the live host.
- **Run capacity covers roots.** `maxParallelRuns` counts parentless runs and
  `trigger:"event"` roots; batch/subflow/CI children inherit their parent's
  slot. Queued event roots are dequeued when a slot frees.
- **Effect tapes are retained.** Prune keeps blobs referenced by `input_hash`,
  `output_hash` or a `node.effects` tape; full replay fails if a tape is
  missing instead of reporting a false match.
- **Streams re-authenticate.** The run event stream re-checks the session/token
  at least once per second and closes after logout, disable or revocation.
- **Browser cookies.** `--prod` forces `Secure` on session cookies. Login
  rejects a cross-origin `Origin`; behind a proxy set `--trust-forwarded-host`
  (or `LOOM_TRUST_FORWARDED_HOST=1`) to compare against `X-Forwarded-Host`.
- **Resource bounds.** Batch concurrency is capped at 32 and 10 000 items; a
  stored workflow above the cap is clamped. `flow.wait` is capped at 30 days.
  `flow.switch` supports three cases and errors on a fourth matching case.
  A single event tree may create at most 100 runs.
- **Approval links expire into cancellation.** A waiting run whose link TTL has
  passed is cancelled by the reminder sweep (`approval link expired`) instead of
  waiting forever.

## CI and supply chain

Continuous integration (`.github/workflows/`) pins every third-party action by
full commit SHA with a comment naming the version it resolves to, and pins the
staticcheck version explicitly instead of `latest`. Dependabot
(`.github/dependabot.yml`) opens weekly update PRs for GitHub Actions, Go
modules, and the editor's npm dependencies.

Upgrading a pinned tool is deliberate, not automatic:

1. Let Dependabot's PR (or a manual bump) propose the new SHA; verify the
   comment's version matches the tag the SHA was resolved from.
2. Read the tool's release notes for behavioral changes (staticcheck adds new
   checks; actions change inputs and defaults).
3. Merge only after the full CI gate passes on the PR.

## Residual risks

- Restart recovery re-executes the in-flight node. HTTP-style nodes send a stable
  `Idempotency-Key` (`<runID>:<nodeID>`, plus `:item-N` for batch items); SQL
  writes and services that ignore the header can repeat an effect after a
  crash. Parented runs left in flight are failed as `orphaned by restart` and
  the parent recreates them; event roots are resumed normally.
- `code.run` caps `repeat`/`padStart`/`padEnd`, the `Array` constructor and
  `Array.from`/`Array.of`. Other builtins (regex, repeated concatenation) can
  still allocate; full isolation needs a subprocess memory limit.
- `code.python` runs arbitrary Python as the service user and is the largest
  security surface in Loom. It is disabled unless the server starts with
  `--enable-subprocess-nodes`. Layers of containment: isolated interpreter,
  timeout with process-group kill, 1 MiB output caps, a private per-execution
  workdir that is removed afterwards, an `RLIMIT_AS` memory cap and an
  `RLIMIT_CPU` cap derived from the timeout (on kernels that allow lowering
  them — macOS generally refuses the address-space limit — the refusal is
  recorded in the run's trace, never silent), and a macOS Seatbelt profile
  that confines writes to the workdir and exec to the interpreter (applied
  when `sandbox-exec` is available; on Linux no OS-level filesystem sandbox
  is applied yet — run subprocess-capable instances in a container).
  The Python process can reach the network by default — the HTTP SSRF guard
  does not apply inside it. `--subprocess-network deny` cuts python's network
  at the OS level instead: an empty network namespace on Linux (unprivileged
  user namespaces), or `(deny network*)` in the Seatbelt profile on macOS.
  Deny fails closed — if the mechanism is unavailable on the host, code.python
  execution returns an error instead of running with network access.
  Dependencies are built at publish time with uv
  into hash-verified environments under a package allowlist and size-cap
  policy (`data/subprocess-policy.json`, or `--subprocess-policy`); runs
  never install packages.
- A compromised admin or editor can author workflows that exfiltrate data through
  allowed HTTP destinations. RBAC controls who may act, not what a workflow may
  do; egress policy is separate.
- If subprocess nodes are enabled in a deployment where Python can still read
  the Loom data directory or other service secrets, treat an editor as an admin:
  editors can publish and execute arbitrary Python. The explicit subprocess
  environment prevents `LOOM_TOKEN` inheritance, but it is not a substitute for
  filesystem isolation.
- **Subprocess + service token gate.** `loom serve` refuses to start when
  `--enable-subprocess-nodes` is combined with a service token
  (`--token`/`LOOM_TOKEN`) unless the operator sets
  `LOOM_SUBPROCESS_UNSAFE_WITH_TOKEN=1`: the token is an admin service
  identity, and Python running as the service user can read the data directory
  and keyring on hosts without an OS sandbox, which would make an editor
  effectively an admin. Isolate the subprocess-capable instance in a container
  without the token instead of acknowledging the merged boundary.
- File-level access to the data directory exposes raw blobs and the credential
  key unless the key is mounted separately.
- `/metrics`, webhooks and approval links are reachable without a session by
  design; protect them at the network edge.
- Large deployments need a different store; multi-writer HA is not supported.
- `maxParallelRuns` bounds new admissions of roots. Resuming a root that parked
  on an approval re-acquires its slot without re-checking the cap, so a burst of
  approvals can briefly exceed it. Batch, subflow and CI children inherit their
  parent's slot by design.
- The expression environment is default-deny. Names in `LOOM_EXPR_ENV_ALLOW`
  are still blocked when they match the built-in secret denylist (provider
  keys, database URLs); the denylist wins and the variable is dropped.
- Event rows written by older versions may still contain raw header values. The
  events endpoint masks on read, but rotate any credential that was readable by
  a viewer.

## Reporting

Open a private security advisory in the repository. Include the version, a
reproduction, and whether the issue needs database or filesystem access.
