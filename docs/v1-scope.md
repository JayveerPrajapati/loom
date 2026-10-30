# Loom v1 scope

> One sentence: a single-binary, self-hosted automation canvas where every
> connection is type-checked and every run can be replayed.

## In scope

### Triggers (4)

| Type | Purpose | Output |
|---|---|---|
| `trigger.manual` | Run by hand with seed data | `json` |
| `trigger.webhook` | Inbound HTTP request | `event` |
| `trigger.cron` | Schedule | `event` |
| `trigger.subflow` | Entry point for a child workflow | `json` |

### Nodes (12 + 4 triggers)

| Type | Category | In → Out |
|---|---|---|
| `http.request` | action | `json` → `json` |
| `code.run` | action | `json` → declared (`json`/`message`/…) |
| `data.set` | action | `json` → declared |
| `db.postgres` | action | `json` → `rows` |
| `chat.slack` | action | `message` → `json` |
| `flow.if` | logic | `json` → `json` (ports `true`, `false`) |
| `flow.switch` | logic | `json` → `json` (3 cases + default) |
| `flow.merge` | logic | `json` ×2 → `json` |
| `flow.loop` | logic | `json` → `json` (subflow per item) |
| `flow.wait` | logic | `json` → `json` |
| `flow.approval` | logic | `json` → `json` (ports `approved`, `rejected`) |
| `flow.subflow` | logic | `json` → `json` |

### Editor

- Create, edit, delete nodes and edges.
- Typed ports; incompatibility is highlighted on the offending node/edge.
- Inspect a node's config, recorded input/output and expression preview.
- Run history, replay from a node, run diff, runbook download.
- Credentials page for create/list/delete (secrets never returned).
- One primary action per screen: **Run** or **Save**.

### Runtime

- Single binary, SQLite, one admin token.
- Append-only execution log; deterministic replay.
- Retry per node (exponential backoff, max attempts).
- Wait and human approval suspend and resume.
- Graceful shutdown; restart reconciliation.

### CLI

```
loom validate <file...>
loom plan <file>
loom run <file>
loom test <file>
loom replay <run> [--step node] [--from node]
loom resume --decision approved|rejected <run>
loom runbook <workflow>
loom prune --before 30d
loom export <workflow> <file>
loom import <file>
loom workflows
loom runs
loom serve [--addr :5678]
loom version
```

## Out of scope (contract)

No marketplace. No 1,500 connectors. No multi-tenant cloud. No SSO/RBAC in v1.
No mobile app. No node-builder GUI. No custom theming. No AI copilot.
No Redis requirement.

Adding anything here requires an ADR and an explicit scope change.

## Acceptance criteria (definition of done)

- `loom serve` starts cold in **< 1 s** with no external services and opens UI.
- All five examples validate with **zero errors and zero warnings**.
- An invalid edge (e.g. `rows → message`) is blocked **before** a run with a
  message on the offending edge.
- Any completed run can be replayed deterministically; step replay **< 1 s**.
- `kill -9` mid-run leaves no corruption; the run reconciles on restart; a
  waiting run resumes from its token.
- 100 concurrent webhook runs complete with zero dropped events.
- Binary **< 30 MB**; idle RSS **< 80 MB**.
- Engine unit coverage **≥ 80%**; all five examples covered by E2E.
- A non-author can read a workflow and explain it (usability test, 3 users).

## Milestones

| Phase | Deliverable | Status |
|---|---|---|
| M0 | Spec, schemas, ADRs, 5 examples, validator, placeholder UI | **done** |
| M1 | Engine: triggers, nodes, event store, replay, CLI | **done** |
| M2 | Editor: canvas, typed edges, run history, live stream | **done** |
| M3 | Trust UX: approvals, subflow, retries, replay UI, diff | **done** |
| M4 | Harden: tests, docs, Docker, releases | **complete (v1.0 tag pending)** |

See `PLAN.md` for the live task tracker.
