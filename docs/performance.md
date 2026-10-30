# Performance budgets

CI enforces a small, deterministic budget on the engine so regressions in event
writes, blob storage or replay are caught before release.

## Budgets

| Scenario | Budget | Where |
|---|---|---|
| 100-action-node linear run (plus trigger) | 5s | `TestPerformanceBudget` |
| Full replay of that run | 5s | `TestPerformanceBudget` |

The budget is deliberately generous for shared CI runners. The test logs actual
timings; the current local baseline is roughly 30–40ms for the run and 2–3ms for
replay.

Run it locally:

```sh
make perf
```

The test is also part of `go test ./...`, so a normal test run exercises it.
The 5s ceiling is a regression guard, not a target for production sizing.

## What the budget covers

- DAG scheduling and node execution
- SQLite event appends and content-addressed input/output blobs
- Engine replay verification and re-execution

It does not cover live network latency, Postgres, Slack, browser rendering or
SSE fan-out. Those depend on external systems and are measured separately when
those adapters land.
