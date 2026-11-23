# ADR-0003: Advanced node capabilities

- **Status:** Accepted (2026-11-06)
- **Deciders:** Loom core + operator
- **Related:** `internal/nodes/catalog.go`, `docs/node-reference.md`,
  `docs/upgrade.md`, `internal/pyenv`, `internal/host`

## Context

v1 shipped thin nodes: `http.request` covered plain requests, `code.run` was a
sandboxed expression evaluator, and there was no way to run Python or install a
dependency. Real workflows need paginated APIs, multipart uploads, OAuth2, and
data-science scripts. Four `ai.*` implementations existed but were absent from
the catalog and therefore unreachable (graph validation resolves types against
the catalog).

## Decision

1. **HTTP is the primary integration surface.** Support every method,
   path/query/header templating, JSON/raw/form/multipart/GraphQL bodies,
   credential/bearer/basic/apiKey/OAuth2 auth, `successCodes`, pagination
   (page/offset/cursor), response-to-blob (`inline`/`blob`/`auto`+threshold),
   per-node rate limiting, proxy support and the SSRF guard. Retries are the
   engine's node-level retry policy with per-attempt tapes, not a second HTTP
   config. Server-sent events are deferred to v2.
2. **JavaScript stays in goja, with `$http` routed through the guarded host.**
   Script HTTP therefore keeps the SSRF guard and is recorded on the effect tape.
   Add `console`, `$crypto`, `$base64`, `btoa`/`atob`; raise the timeout cap to
   300 s. Defer async/await and `$pg` (credential IDs are not in the script
   scope).
3. **Python ships behind an explicit opt-in.** `code.python` executes a
   subprocess only with `--enable-subprocess-nodes`
   (`LOOM_ENABLE_SUBPROCESS_NODES=1`). Requirements must be pinned
   `name==version`; they are installed **at publish time only** by `uv` into
   `data/envs/<sha256>/` (the manifest must hash to the directory name). Runs
   never install or reach the network for dependencies.
4. **Every new external effect is taped.** `node.effects` records one tape per
   attempt; hermetic replay verifies the step without executing it and fails
   closed when a tape is missing.
5. **Harden the subprocess, don't trust it.** Fresh writable workdir per
   execution, Seatbelt profile on macOS (write-confined, interpreter-exec only),
   `RLIMIT_AS` where the kernel allows it (macOS refuses; the refusal is written
   to the run trace), output/timeout caps, process-group kill, and a package
   policy file with an allowlist and per-environment size cap. Linux
   Landlock/seccomp remains open (needs a child-side helper).
6. **Delete the unreachable AI nodes.** `ai.chat`, `ai.classify`, `ai.extract`
   and `ai.embed` were removed rather than registered; reintroduction is
   post-v1 and requires reimplementation.

## Consequences

- The v1 catalog is frozen at **20** node types. `code.python` is the single
  sanctioned addition; `TestCatalogIsFrozen` enforces this mechanically.
  **Amendment (2026-11-11).** The catalog now has 21 types: `data.map` was
  added deliberately with the Configuration Hub v2 data-flow work, and
  `internal/nodes/catalog_freeze_test.go` records both sanctioned additions.
  The "frozen at 20" decision above remains the historical record; the current
  count is 21.
- `code.python` is the largest security surface in Loom — the opt-in flag, the
  policy file and the OS sandbox are the compensating controls, and the docs
  state that Python bypasses the `http.request` SSRF guard by design.
- Replay guarantees extend to new effects: a workflow that cannot be verified
  from its tape is not shippable.
- Deferred: SSE/streaming (v2), async JS and `$pg`, Linux sandboxing, AI nodes.

## Alternatives considered

- **A separate runner process per language** (gRPC sidecar): stronger isolation,
  but a second deploy artifact and a new protocol to version. Revisit if Python
  becomes a first-class surface.
- **WASM sandbox for all code**: consistent isolation, but immature Python
  packaging and slow for data workloads.
- **HTTP-level retry config**: duplicates the engine retry policy and would
  double-tape attempts. Rejected.
- **Registering the AI nodes**: freezes four untested types into v1. Rejected.
