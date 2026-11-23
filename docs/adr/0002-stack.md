# ADR-0002: Go + SQLite + React (WASM sandbox deferred)

- **Status:** Accepted (amended 2026-11-06: Go 1.26, WASM sandbox deferred, Postgres shipped in v1)
- **Date:** 2026-10-22
- **Deciders:** Loom core
- **Related:** `go.mod`, `internal/server`, milestone M2

## Context

The product promise is "one binary, zero dependencies, workflows you can trust".
n8n requires Node, a database, and Redis for queue mode. We want a self-hosted
engine an operator can run without a platform team, while keeping a path to a
real sandbox for untrusted steps.

## Decision

- **Engine + control plane:** Go 1.26, standard library first.
- **Storage:** SQLite (WAL) + a content-addressed blob directory. Postgres
  (`lib/pq`, the `db.postgres` node) is supported in v1.
- **Sandbox:** deferred. `goja` JavaScript is the only code runtime in v1 and is
  a **trusted-author** feature, not a security boundary; the `wazero` WASM
  sandbox for untrusted code is a future option, not a v1 deliverable.
- **Code steps:** JavaScript via `goja` in v1. Untrusted code must not run on
  Loom until a real sandbox exists.
- **Editor:** React 18 + TypeScript + Vite + React Flow, served by the Go binary.
- **Distribution:** a single `loom` binary and a distroless Docker image.

## Consequences

**Positive**

- Cross-compiles cleanly; no cgo; instant start; predictable memory.
- One deploy artifact; no npm/pip runtime on the server.
- A future WASM sandbox would give a language-agnostic connector ABI and real isolation.

**Negative**

- Smaller connector ecosystem than Node; connector SDK must be designed.
- `goja` is not a sandbox; documented, and untrusted code is out of scope until a real sandbox exists.
- The editor build still needs Node in CI, but not at runtime.

## Alternatives considered

- **Rust + wasmtime.** Better raw performance and safety, but slower to reach a
  credible M2. Revisit if the engine becomes CPU-bound.
- **TypeScript/Node (n8n-like).** Fastest ecosystem, but repeats the runtime,
  dependency and memory footprint we are trying to escape.
- **Python + FastAPI.** Great data/AI libraries, weaker packaging and sandbox.
