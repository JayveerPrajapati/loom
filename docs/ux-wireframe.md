# UX wireframe (M0)

Text wireframes for the three surfaces that matter. The M0 server ships a
placeholder page; these become the M2 editor.

## 1. Canvas (the only screen that matters)

```
┌────────────────────────────────────────────────────────────────────────────┐
│ Loom   Refund approval · v3        ● All checks pass   [Run]   [Save]  ⌘K  │
├──────────────┬───────────────────────────────────────────┬─────────────────┤
│ NODES        │  CANVAS                                   │ INSPECTOR       │
│              │                                           │                 │
│ Triggers     │   ┌──────────┐    ┌──────────┐            │ If              │
│  Webhook     │   │ Refund   │───▶│ Lookup   │            │ Amount over 100 │
│  Cron        │   │ requested│    │ order    │            │                 │
│  Manual      │   └──────────┘    └────┬─────┘            │ condition       │
│              │                        │                  │ ┌─────────────┐ │
│ Logic        │                   ┌────▼─────┐            │ │$json.amount │ │
│  If          │                   │ Amount   │            │ │ > 100       │ │
│  Merge       │                   │ over 100?│            │ └─────────────┘ │
│  Approval    │                   └─┬─────┬──┘            │                 │
│              │              true   │     │ false         │ Outputs         │
│ Actions      │            ┌────────▼┐  ┌─▼────────┐      │  ● true  json   │
│  HTTP        │            │ Approve │  │ Ticket   │      │  ● false json   │
│  Code        │            └────┬────┘  └──────────┘      │                 │
│  Postgres    │                 │                          │ Advanced ▸      │
│  Slack       │            ┌────▼────┐                     │                 │
│              │            │ Refund  │                     │                 │
│ + Add node   │            └─────────┘                     │                 │
├──────────────┴───────────────────────────────────────────┴─────────────────┤
│ Run #1842  ● running  node 3/7  [Replay] [Cancel]                          │
└────────────────────────────────────────────────────────────────────────────┘
```

Rules:

- One primary button (`Run`), one secondary (`Save`), command palette for the rest.
- Red badge sits on the exact edge with the type error, not in a toast.
- Bottom bar appears only when a run exists; it is the single status surface.

## 2. Run history + replay

```
┌────────────────────────────────────────────────────────────────────────────┐
│ ← Refund approval    Run #1842   done in 4.2s    [Replay from…] [Diff]     │
├───────────────────────────────┬────────────────────────────────────────────┤
│ STEPS                         │ STEP: Lookup order                         │
│ ✓ Refund requested   12ms     │ started  10:42:01.120                      │
│ ✓ Lookup order      210ms     │ input    { orderId: "A-1042" }             │
│ ✓ Amount over 100?    1ms     │ output   { id, amount: 249, status: … }    │
│ ✓ Ask for approval  1.9s      │ attempt  1/3                               │
│ ✓ Process refund    340ms     │ [Replay this step] [Copy input] [Copy JSON]│
│ ✓ Record refund     120ms     │                                            │
│                               │ "Every past run is reproducible."          │
└───────────────────────────────┴────────────────────────────────────────────┘
```

Rules:

- Steps are the timeline; selecting one shows exact input/output hashes.
- Replay is always available on a completed run.
- Diff compares two runs of the same workflow version.

## 3. Empty state

```
┌────────────────────────────────────────────────────────────────────────────┐
│                                                                            │
│                         Build your first workflow                          │
│                                                                            │
│      Start with a trigger. Everything else can change later.               │
│                                                                            │
│        [ Webhook ]     [ Schedule ]     [ Run manually ]                   │
│                                                                            │
│      Or open an example:  Refund approval · Daily digest · CRM sync        │
│                                                                            │
└────────────────────────────────────────────────────────────────────────────┘
```

Rules:

- Three choices, not a gallery.
- Examples are one click, not a marketplace.
- No signup, no wizard, no tour.

## Keyboard

| Key | Action |
|---|---|
| `⌘K` | Command palette |
| `R` | Run |
| `S` | Save |
| `⌘Z` / `⇧⌘Z` | Undo / redo |
| `Esc` | Cancel run or close inspector |
| `Tab` | Move between ports |

## States to design for

- **Empty** — no workflows.
- **Invalid** — type error on an edge; Run disabled with reason.
- **Running** — per-node progress, cancel available.
- **Waiting** — approval card with Approve / Reject.
- **Failed** — error on the node, retry from here.
- **Offline** — read-only banner, replay still available.
