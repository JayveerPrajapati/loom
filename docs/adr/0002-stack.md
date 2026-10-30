# ADR-0002: Go + SQLite + WASM + React

- **Status:** Accepted
- **Date:** 2026-10-22
- **Deciders:** Loom core
- **Related:** `go.mod`, `internal/server`, milestone M2

## Context

The product promise is "one binary, zero dependencies, workflows you can trust".
n8n requires Node, a database, and Redis for queue mode. We want a self-hosted
engine an operator can run without a platform team, while keeping a path to a
real sandbox for untrusted steps.

## Decision

- **Engine + control plane:** Go 1.23, standard library first.
- **Storage:** SQLite (WAL) + a content-addressed blob directory. Postgres is a
  later adapter, not a v1 requirement.
- **Sandbox:** `wazero` (pure Go, no cgo) for untrusted WASM connectors from
  M1 onward.
- **Code steps:** JavaScript via `goja` in v1. This is a **trusted-author**
  feature, not a security boundary; untrusted code must run as WASM.
- **Editor:** React 18 + TypeScript + Vite + React Flow, served by the Go binary.
- **Distribution:** a single `loom` binary and a distroless Docker image.

## Consequences

**Positive**

- Cross-compiles cleanly; no cgo; instant start; predictable memory.
- One deploy artifact; no npm/pip runtime on the server.
- WASM gives a language-agnostic connector ABI and real isolation.

**Negative**

- Smaller connector ecosystem than Node; connector SDK must be designed.
- `goja` is not a sandbox; documented and mitigated by WASM for untrusted use.
- The editor build still needs Node in CI, but not at runtime.

## Alternatives considered

- **Rust + wasmtime.** Better raw performance and safety, but slower to reach a
  credible M2. Revisit if the engine becomes CPU-bound.
- **TypeScript/Node (n8n-like).** Fastest ecosystem, but repeats the runtime,
  dependency and memory footprint we are trying to escape.
- **Python + FastAPI.** Great data/AI libraries, weaker packaging and sandbox.
