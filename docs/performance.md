# Performance budgets

CI enforces a small, deterministic budget on the engine so regressions in event
writes, blob storage or replay are caught before release.

## Baseline environment

Local reference numbers in this document come from an Apple M1 Pro (8 cores).
The run/replay figures were refreshed on 2026-11-14 with macOS 26.7.2, Go
1.27.1, and commit `1dc1884`; the store and scale tables are earlier snapshots
from the same machine class. Treat all local numbers as reference points, not
production sizing promises: CI enforces the budget, and shared runners vary.

## Budgets

| Scenario | Budget | Local baseline (2026-11-14) | Where |
|---|---|---|---|
| 100-action-node linear run (plus trigger) | 5s | 164–185 ms (median 169 ms) | `TestPerformanceBudget` |
| Full replay of that run | 5s | 35–55 ms (median 41 ms) | `TestPerformanceBudget` |

The budget is deliberately generous for shared CI runners. The local baseline
is four samples on 2026-11-14: three consecutive
`go test ./internal/engine -run '^TestPerformanceBudget$' -count=3 -v` runs
plus one `make perf` run. A single sample varies with load; the budget is the
gate, and the local numbers are context.

Run it locally:

```sh
make perf
```

The test is also part of `go test ./...`, so a normal test run exercises it.
The 5s ceiling is a regression guard, not a target for production sizing.

## What the budget covers

- DAG scheduling and node execution
- SQLite event appends and content-addressed input/output blobs
- Engine replay verification (and opt-in live step re-execution)

It does not cover live network latency, Postgres, browser rendering or SSE
fan-out. Those depend on external systems and are measured separately.

## Event append batching (P2-2)

`Store.AppendEvents(runID, events)` appends multiple events for one run in a
single transaction — the chain metadata (max seq, previous hash, chain
version, signing key) is read once and the head seal is written once, and one
failed event rolls back the whole batch. `AppendEvent` is now a one-event
wrapper over it.

`BenchmarkAppendEvents` (internal/store/appendevents_test.go) measures 100
event appends per run — the shape of a 50-node run's log (two events per
node) — one transaction per event versus two batches of 50:

| Mode | Local baseline (Apple M1 Pro) |
|---|---|
| 100 × AppendEvent (one tx each) | ~10.8 ms per 100 events |
| 2 × AppendEvents(50) | ~2.0 ms per 100 events (~5.4× faster) |

Engine call sites today append one event at a time by design: node.started
and node.finished bracket a node's live execution, and terminal events
(run.finished, run.failed) follow engine logic rather than each other, so
there is no natural multi-event append to batch yet. The primitive is the
adopted path forward — any future site that appends adjacent events (batch
item summaries, audit summaries) should use it rather than a loop of
single appends.

## Prune throughput (P2-7)

`Store.Prune` deletes candidate runs in chunks of 500 (one `DELETE ... WHERE
run_id IN (...)` pair per chunk instead of two statements per run), and the
referenced-blob keep set is built from three targeted queries — distinct
input hashes, distinct output hashes, and the data column of `node.effects`
rows only, which uses `idx_events_type` (guarded by
`TestPruneKeepSetQueryPlans`) so the input/output scans never materialize the
event data column. Notifications for deleted runs fire after the prune
transaction commits.

`BenchmarkPrune` (internal/store/prune_chunk_test.go) measures a full prune
of 5000 terminal runs (10 chunks) including a same-size dry-run estimate:

| Scenario | Local baseline (Apple M1 Pro) |
|---|---|
| Prune 5000 runs (10 chunks) + estimate | ~52 ms |

## Scale regression guards (P2-10)

Behavioral scale tests pin the properties the plan requires; local baselines
below were recorded on the same machine as the other budgets.

| Area | Guard | Local baseline |
|---|---|---|
| Durable wait (100ms) | `TestWaitSchedulerResumesShortWaitNearDeadline` — terminal within 600ms, wait honored ≥80ms | <200 ms |
| Queue admission (100 queued) | `TestQueueAdmissionScalesHundredRuns` — full FIFO drain, one slot at a time | ~50 ms |
| History pagination (1000 runs) | `TestListRunsPageDeepHistoryStaysBounded` — 10 full cursor pages, strictly older, total constant | ~130 ms |
| Cursor page @ depth 900 | `BenchmarkListRunsPageDeepVsOffset` cursor-deep | ~1.0 ms/page |
| OFFSET page @ depth 900 | `BenchmarkListRunsPageDeepVsOffset` offset-deep-900 | ~1.5 ms/page (grows with depth; cursor stays flat) |
| Event triggers (200 unrelated) | `TestOnEventDispatchIndependentOfUnrelatedWorkflows` — dispatch starts exactly the 1 matching run | index lookup, no scan |
| SSE fan-out (10 streams) | `TestSSEStreamTenStreamsFanOutViaNotifications` — all deliver <1s, far inside the 5s fallback | ~80 ms |
| Doctor (500 runs × 5 events) | `BenchmarkCheckHealthLargeStore` — streaming chain verify + blob scans | ~36 ms |

Query plans behind the queue, history, prune keep-set, and waiting-resume
paths are asserted directly by `TestQueueAndHistoryQueryPlans` and
`TestPruneKeepSetQueryPlans` (SEARCH, never SCAN).

## Publish and picker budgets (Phase 5)

Publish resolution has a deterministic query-count gate instead of a
wall-clock assertion. The store fixture is 1,000 nodes and 1,000 asset
refs: 500 refs to one profile, 300 refs across ten 100-field contracts,
100 refs to one 200-row mapping, and 100 refs to one script. The HTTP
fixture uses the catalog's 1,000-node cap (999 refs plus the trigger) and
measures the whole publish request, including the workflow read and the
publish writes.

| Scenario | Gate | Where |
|---|---|---|
| Large publish resolution | ≤20 read queries (measured 13; one per distinct asset) | `TestPublishLargeWorkflowStaysWithinReadBudget` |
| Large publish through HTTP | ≤30 read queries (measured 17; 1,000-node cap) | `TestPublishLargeWorkflowThroughAPI` |
| Repeated references | 1 node and 12 nodes issue the same reads | `TestLookupCacheDeduplicatesResolverReads` |
| Resolver benchmark | local baseline ~6.3 ms/op, fixture construction included, no DB writes or validation | `BenchmarkResolveLargePublishWorkflow` |
| Inspector node switch | zero list requests after the first load | `web/e2e/inspector-cache.spec.ts` |
| Asset cache freshness | one request per kind per 30 s; in-flight dedup | `web/src/__tests__/assetCache.test.ts` |
| Blob cache | 20 content-addressed entries; in-flight dedup | `web/src/__tests__/assetCache.test.ts` |

The publish path creates one request-scoped `store.LookupCache` in
`publishWorkflowDoc` and shares it between validation and all five
resolvers. Duplicate refs never add reads; a missing asset's error is
memoized too, so a broken reference repeated 100 times is still one
lookup.

Run the gates:

```sh
go test ./internal/store -run 'TestPublishLargeWorkflowStaysWithinReadBudget|TestLookupCacheDeduplicatesResolverReads'
go test ./internal/store -run '^$' -bench '^BenchmarkResolveLargePublishWorkflow$' -benchtime 3x
cd web && npx vitest run src/__tests__/assetCache.test.ts
cd web && npx playwright test e2e/inspector-cache.spec.ts
```

The publish budget is part of `go test ./...`. The Inspector numbers are
behavioral (request counts), not render timings.
