# Editor

The Loom editor is a React 18 + TypeScript + Vite app. It is embedded into the
Go binary at build time and served at `/` with SPA fallback.

## Develop

```sh
cd web
npm install
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

The suite opens and executes all five examples, covers run streaming, run history with
step input/output, approval resume and messages, subflow child navigation,
runbook download, failed-run presentation, offline recovery, validation/undo,
the command palette, dashboard layout/overflow, and axe accessibility audits
(onboarding, canvas, and product pages). It runs in desktop Chromium plus a
Pixel 5 mobile project (`e2e/mobile.spec.ts`). Set `LOOM_E2E_PORT` to override
the port; `CI=true` disables server reuse. `npm run e2e:only` runs Playwright
without rebuilding; from the repo root, `make e2e` builds the UI first.

## Layout

```
src/
  App.tsx                 shell, workflow state, undo/redo, run streaming
  api.ts                  bearer-token API client + SSE reader
  catalog.ts              node defaults, port types, compatibility
  validate.ts             client-side mirror of graph.Validate
  components/
    NodePanel.tsx         searchable node library
    Canvas.tsx            React Flow canvas, typed handles, drag/drop
    Inspector.tsx         config, credentials, ports, inline issues
    RunBar.tsx            live status, progress, cancel, approve/reject
    RunDetailPage.tsx     run detail, events, replay and inputs
    TemplateViewPage.tsx  published template run view
    dashboard/            home, templates, jobs, runs, job detail/create
    CommandPalette.tsx    ⌘K actions and node search
  styles.css              design tokens, light/dark, layout
  ui-pages.css            dashboard and product page styles
```

## Shortcuts

- `⌘K` / `Ctrl K` — command palette
- `⌘S` / `Ctrl S` — save
- `⌘Enter` / `Ctrl Enter` — run
- `⌘Z` / `Ctrl Z` — undo (outside inputs); `Shift` redoes
- `R` — run when not editing a field
- `Backspace` / `Delete` — delete selected node or edge
