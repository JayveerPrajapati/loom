# Editor

The Loom editor is a React 18 + TypeScript + Vite app. It is embedded into the
Go binary at build time and served at `/` with SPA fallback.

## Develop

```sh
cd web
npm ci
npm run dev        # http://localhost:5173, proxies /api and /hook to :5678
```

Run the Go server in another terminal:

```sh
./bin/loom serve
```

## Build

```sh
npm run build      # type-check, then emit web/dist
```

`make ui` from the repo root does this and the Go build embeds `web/dist`.
`web/dist/.keep` keeps the embed target present before the first UI build.

## Test

```sh
npm run e2e:install   # once, downloads Chromium
npm run e2e           # Playwright starts its own Go server on :5700
```

The suite opens and executes every bundled example and covers run streaming,
run history, approvals, subflows, runbook download, failed-run presentation,
offline recovery, validation/undo, the command palette, dashboard layout,
asset pages, profile ownership/upgrade behavior, and axe accessibility audits.
It runs in desktop Chromium plus a Pixel 5 mobile project; `npm run e2e` also
runs the isolated project against a fresh data directory. Set `LOOM_E2E_PORT`
to override the port; `CI=true` disables server reuse. `npm run e2e:only` runs
Playwright without rebuilding; from the repo root, `make e2e` builds the UI
first.

The exact test count is reported by Playwright and CI rather than duplicated
here.

## Layout

```text
src/
  App.tsx                 shell, workflow state, undo/redo, run streaming
  appShared.ts            routes and pure helpers shared by the shell
  loadState.ts            loading/error/offline/permission classification
  effectiveNode.ts        effective node values and field origins
  profileEffective.ts     profile-form effective preview
  profileOwnership.ts     attach/detach/ownership transitions
  paramRows.ts            publish-parity profile, row, bundle and schema merge
  formLayout.ts           inspector tabs and Basics/Advanced field groups
  components/
    assets/               shared list/state/editor/dialog kit
    nodeForms/            per-node-type form leaves
    profileForm/          profile form panels and hooks
    dashboard/            home, templates, jobs, runs, job detail/create
    ...                   editor, inspector, run detail, asset pages
  styles/                 design tokens, light/dark, layout and page styles
```

## Design

- [../docs/ownership.md](../docs/ownership.md) — profile ownership, merge
  precedence and migration.
- [../docs/page-grammar.md](../docs/page-grammar.md) — list, editor, destructive
  action, state and accessibility grammar.


## Shortcuts

- `⌘K` / `Ctrl K` — command palette
- `⌘S` / `Ctrl S` — save
- `⌘Enter` / `Ctrl Enter` — run
- `⌘Z` / `Ctrl Z` — undo (outside inputs); `Shift` redoes
- `R` — run when not editing a field
- `Backspace` / `Delete` — delete selected node or edge
