# ADR-0001: Event-sourced execution

- **Status:** Accepted
- **Date:** 2026-10-22
- **Deciders:** Loom core
- **Related:** `schema/execution.schema.json`, milestone M1

## Context

Loom's wedge is trust: type-checked graphs and deterministic replay. n8n keeps
mutable run rows and supports "retry from failed node", but it cannot reproduce
a past run exactly, and there is no first-class test fixture for a workflow.

We need:

1. Deterministic replay (full run, from a node, single step).
2. Time-travel inspection of the exact data each node saw.
3. Crash recovery without cross-store transactions.
4. An audit trail that is the same artifact as the debug trail.
5. SQLite as the only required dependency in v1.

## Decision

Every run is an **append-only event stream**.

- `events` table: one row per event, `seq` monotonic per run, gap-free.
- Step inputs and outputs are content-addressed blobs. Events carry hashes,
  never the bulk payload.
- Run state machine: `queued → running → waiting → done | failed | cancelled`.
- All side effects go through a `Host` interface. In **record** mode the host
  captures responses; in **replay** mode it serves recorded responses.
- Idempotency key: `runID:nodeID`, plus `:item-N` for batch items. Stable
  across retries (no attempt component) so a lost response is deduped.
- SQLite in WAL mode with a single writer. On boot, a reconciliation pass finds
  runs marked `running` without a heartbeat and marks them `failed` (or
  resumes a `waiting` run from its resume token).
- Event schema is versioned. Migrations are additive; committed generations are
  never rewritten in place.

### Execution and replay modes

| Mode | Behaviour |
|---|---|
| `full` | Re-run the whole graph from recorded inputs. |
| `from <node>` | Reuse recorded upstream outputs, execute downstream live or recorded. |
| `step <node>` | Re-execute one node against its recorded input. |
| `test` | Simulated run with approvals auto-approved. Hermetic replay of recorded effects is `replay --step <node> --hermetic` or `replay --from <node> --hermetic`. |

## Consequences

**Positive**

- Deterministic replay, time-travel debugging and audit for free.
- Recorded effects replay hermetically: `loom replay --from <node> --hermetic <runID>`.
  `loom test` runs a workflow offline with approvals auto-approved.
- Crash recovery is a log reconciliation, not a distributed transaction.

**Negative**

- Write amplification: every step appends events plus blobs.
- Retention and compaction are required from M3.
- Event schema becomes a compatibility surface; it must be versioned carefully.
- Secrets must be redacted before an event is written.

## Alternatives considered

- **Mutable run rows** (n8n-style). Rejected: no exact replay, weak audit.
- **Temporal / durable execution cluster.** Rejected: violates "one binary";
  too much operational surface for v1.
- **Snapshot per step only.** Rejected: storage-hungry and misses intra-step
  events (retries, waits, failures).

## Follow-ups

- M1: implement the event store, content-addressed blobs, `loom replay`.
- M3: retention policy and compaction job.
- M3: redaction rules for credentials and PII in event payloads.

## Amendment (2026-11-14)

The original decision recorded `runID/nodeID/attempt` and described `test` as
hermetic replay. Neither matched the implementation:

- The implemented key is `runID:nodeID`, with `:item-N` appended for batch
  items (`internal/flow/flow.go:85-95`). It is deliberately stable across
  retries; `TestInputIdempotencyKey` and `TestRetryUsesStableIdempotencyKey`
  pin that contract. Including the attempt would make every retry a new
  logical effect and defeat downstream deduplication.
- `loom test` runs the workflow as a simulated run with approvals
  auto-approved (`cmd/loom/main.go:136`, `cmd/loom/run.go:20-24,81`). Hermetic
  replay of recorded effects is `loom replay --step <node> --hermetic` or
  `loom replay --from <node> --hermetic` (`cmd/loom/run.go:103-122`).

The decision text and the execution-and-replay table above are corrected in
place; this note preserves the original claim and the reason it changed.
