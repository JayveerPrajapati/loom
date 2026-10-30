# Design principles: Apple HIG applied to Loom

The product bar is Apple's Human Interface Guidelines. These are not slogans;
each one maps to a review rule we can check.

## 1. Purpose

Every screen has one job and one primary action.

- Canvas: **Run**. Editor: **Save**. Run view: **Replay**.
- Secondary actions live in an overflow menu, never next to the primary.
- If a screen needs two competing primary buttons, the screen is wrong.

## 2. Simplicity

Progressive disclosure, not feature deletion.

- Maximum three panels: nodes, canvas, inspector.
- Advanced config is collapsed behind one disclosure row.
- Defaults are opinionated; an empty workflow still runs.
- v1 ships 12 nodes, not 1,500. Depth beats breadth.

## 3. Clarity

The interface explains itself without a manual.

- Ports carry a type token and a tooltip with the schema.
- Errors sit on the exact node or edge that caused them.
- "Run", "Replay", "Version" — no invented jargon.
- A generated runbook (README) is always available from the canvas.

## 4. Consistency

One system, everywhere.

- One component library, one spacing scale, one icon set, one type ramp.
- The same action has the same label and shortcut in every surface.
- CLI verbs match UI verbs: `validate`, `plan`, `run`, `replay`.

## 5. Feedback

Never silent, never surprising.

- Validation is instant and inline; no modal for a field error.
- Running nodes show progress; finished nodes show duration.
- Destructive actions require explicit confirmation; undo covers the rest.
- The run log is the source of truth for what happened.

## 6. Metaphors

Use what people already understand.

- Canvas, node, edge, port, run, version.
- The graph *is* the documentation; the run *is* the audit trail.
- A pause for approval reads as "waiting", not "blocked on state machine".

## 7. User control

The user is in charge.

- Undo/redo everywhere on the canvas.
- Cancel a run at any time; replay from any node.
- Nothing is hidden or auto-applied; changes are explicit.
- Advanced users can drop into code, but never are forced to.

## Review checklist

Before a feature ships, answer yes to all:

- [ ] Does it have exactly one primary action?
- [ ] Is the default path obvious in under 10 seconds?
- [ ] Are errors shown where they happen?
- [ ] Does it reuse existing components and tokens?
- [ ] Can the user undo or cancel it?
- [ ] Does it earn its place against the "say no" list?
