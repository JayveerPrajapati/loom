# Security review

Scope: the v1 single-binary, single-tenant deployment. The operator is trusted;
workflow authors and inbound webhook callers are not assumed to be trusted.

## Controls

### Authentication and capabilities

- API routes require `Authorization: Bearer <token>` when `--token` or
  `LOOM_TOKEN` is set. Comparison uses `crypto/subtle.ConstantTimeCompare`.
- `/healthz`, `/hook/{path...}` and `/hook/approval/{token}` are intentionally
  public. Webhooks are entry points; approval links are bearer capabilities.
- Approval tokens are 128-bit random hex values, stored on the waiting run and
  accepted only while the run is `waiting`.
- The approval GET page never decides; POST accepts an explicit
  `approved`/`rejected` decision. HTML is escaped.
- Run, replay, diff, runbook, blob, workflow, credential, data-context, stats
  and preview routes are all behind API auth.

### SSRF and outbound HTTP

- Only `http` and `https` URLs are accepted.
- Literal loopback, private, link-local, multicast and unspecified addresses are
  rejected before the request.
- `safeDialer` resolves the host once, rejects **any** private result, and dials
  the validated IP directly, so a second DNS lookup cannot substitute a private
  address (DNS rebinding).
- Redirects are limited to 10 and re-checked for scheme and literal private
  hosts; the custom transport re-checks resolved addresses on every connection.
- Environment proxies are not used by the live transport, avoiding a proxy
  bypass of the address checks.
- Response bodies are capped at 10 MiB; per-request timeouts are preserved
  without dropping the guarded transport.
- Postgres and Slack live adapters are not wired in v1, so they cannot be used
  as SSRF/egress paths.

### Secrets and redaction

- Credential blobs use AES-256-GCM with a random nonce. The key comes from
  `LOOM_SECRET`, an isolated external key path via `LOOM_SECRET_KEY_FILE`, or a generated
  `secret.key` written with mode `0600`. In production deployments, mount keys
  independently of the data directory.
- Cloud secret managers (AWS Secrets Manager, GCP Secret Manager, HashiCorp Vault)
  integrate via sidecars writing either a directory of per-credential files
  (`LOOM_SECRETS_DIR`) or a JSON secrets file (`LOOM_SECRETS_FILE`).
- Workflow `credentials` contains IDs, not plaintext secrets. The credential API
  accepts secret data for storage, encrypts it, and returns only ID/type/created
  metadata; secret values are never returned by the list endpoint.
- All stored event payloads and content-addressed SQLite blobs undergo automatic
  recursive PII and secret redaction (`internal/store/redact.go`), masking
  authorization headers and keys matching `*password*`, `*secret*`, `*token*`,
  `*api_key*`, and `*credential*`.
- Webhooks support shared secrets (`secret`, `secretHeader`) and HMAC-SHA256 signature
  verification (`hmacSecret`, `hmacHeader`).

### Execution boundaries

- JavaScript runs in goja with `$json`, `$node`, `$run`, `$job`, `$vars` and
  `$index` bound as plain JSON values. There is no filesystem, process, network
  or Go object access from scripts.
- Scripts are interrupted after 2 seconds and when the run is cancelled.
  Array constructors are capped to prevent single-statement memory bombs.
- Workflows support hard wall-clock execution deadlines via `settings.timeoutSeconds`.
- The graph is acyclic; each input port has at most one writer; triggers cannot
  receive edges.
- Store SQL uses driver parameters; workflow IDs and blob hashes are validated
  before filesystem/SQL use. Blob reads reject non-hex hashes.
- Database access via `db.postgres` uses live connection pools with parameterized
  queries and context timeouts.
- Slack messaging supports incoming webhooks and bot tokens with SSRF protection.

### Input limits

- API JSON bodies: 5 MiB with unknown fields rejected.
- Webhook bodies: 1 MiB; non-object JSON is wrapped, non-JSON is stored as text.
- Live HTTP response bodies: 10 MiB.
- UI rendering uses React escaping; the approval page escapes all interpolated
  values.

## Findings fixed in M4

1. **Redirect/custom-timeout SSRF bypass.** A per-request timeout replaced the
   guarded `http.Client` with a bare client, dropping the transport and redirect
   checks. The client is now copied and only its timeout is changed.
2. **Hostname SSRF.** The original guard only rejected literal private IPs and
   `localhost`. The dialer now resolves and validates DNS results and dials the
   validated IP directly.
3. **Redirect SSRF.** Redirect targets are now scheme-checked, length-limited
   and re-validated by the guarded transport.
4. **Resume token in event data.** Removed from `run.waiting`; an engine test
   asserts it cannot reappear.
5. **Approval GET could decide.** The capability GET page is now read-only and
   only POST with an explicit decision resumes the run.
6. **Webhook response leaked run data.** Public webhook responses are limited to
   `{runId,status}`, and concurrent webhook executions are capped.

## Residual risks

- No API rate limiting or webhook throttling. Put Loom behind a reverse proxy
  with request limits if exposed to the internet.
- Approval POSTs rely on the capability token rather than a separate CSRF token.
  An attacker who obtains the link can decide the approval; treat it as a secret.
- JavaScript execution has a wall-clock limit but no explicit memory cap.
- HTTP request/response trace metadata redacts sensitive headers
  (`authorization`, `cookie`, `set-cookie`, `proxy-authorization`, `x-api-key`),
  but request/response bodies and item data are user-controlled and are not
  automatically redacted from blobs/errors. Do not place long-lived secrets in
  workflow JSON or request bodies.
- Error strings may contain URLs (including query strings). Review run errors
  before exporting logs.
- No RBAC/SSO, tenant isolation, audit export or encrypted-at-rest database in
  v1. Single-tenant deployment is assumed.
- DNS results are validated at dial time; an attacker controlling DNS can only
  select among public IPs. HTTPS still relies on normal certificate validation.
