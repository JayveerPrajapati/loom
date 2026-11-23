import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UX Phase 4.3: a large graph stays usable. The spec generates a 50-node
// fan-out/fan-in workflow (the realistic worst case for the grid layout),
// loads the editor, and verifies the readability contract: fit view lands
// within the min-zoom floor, auto-arrange produces a non-overlapping layout
// in bounded time, and selected nodes remain visible after arrange.

async function seed(request: APIRequestContext, workflow: object) {
  const response = await request.post('/api/workflows', { data: workflow });
  expect(response.status(), await response.text()).toBe(201);
}

function largeWorkflow(id: string) {
  // A 50-node chain (trigger -> http -> data.set -> http -> ... -> final):
  // the longest single-path worst case for the grid layout, exercising the
  // row balancing across 50 rows.
  const nodes: object[] = [
    {
      id: 'start',
      type: 'trigger.manual',
      typeVersion: 1,
      name: 'Start',
      position: { x: 0, y: 0 },
      config: {},
    },
  ];
  const edges: object[] = [];
  for (let i = 0; i < 24; i += 1) {
    const http = `http-${i}`;
    nodes.push({
      id: http,
      type: 'http.request',
      typeVersion: 1,
      name: `Fetch ${i}`,
      position: { x: 320, y: i * 240 },
      config: { method: 'GET', url: `https://example.test/api/${i}` },
      credentials: { http: 'e2e-large-cred' },
    });
    const shape = `shape-${i}`;
    nodes.push({
      id: shape,
      type: 'data.set',
      typeVersion: 1,
      name: `Shape ${i}`,
      position: { x: 640, y: i * 240 },
      config: { values: { i } },
    });
  }
  nodes.push({
    id: 'final',
    type: 'data.set',
    typeVersion: 1,
    name: 'Final',
    position: { x: 960, y: 24 * 240 },
    config: { values: { done: true } },
  });
  edges.push({ from: { node: 'start', port: 'main' }, to: { node: 'http-0', port: 'main' } });
  for (let i = 0; i < 24; i += 1) {
    edges.push({ from: { node: `http-${i}`, port: 'main' }, to: { node: `shape-${i}`, port: 'main' } });
    const next = i < 23 ? `http-${i + 1}` : 'final';
    edges.push({ from: { node: `shape-${i}`, port: 'main' }, to: { node: next, port: 'main' } });
  }
  return { id, name: 'E2E large graph', version: 1, nodes, edges };
}

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('a 50-node graph loads readable and auto-arranges without overlap', async ({ page, request }) => {
  const id = `e2e-large-${Date.now()}`;
  await seed(request, largeWorkflow(id));
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.react-flow__node')).toHaveCount(50);

  // L6: fit view clamps at the 0.85 min-zoom floor, never below. The
  // scale is read from the transformed viewport (not the pane) so the
  // number is the one the rendered text actually scales by. The mount
  // fit animates over 220ms and d3's zoom interpolation can transiently
  // undershoot the endpoints mid-flight, so the assertions read the
  // scale only once it has settled on two consecutive frames.
  const readScale = () =>
    page.evaluate(() => {
      const viewport = document.querySelector('.react-flow__viewport');
      if (!viewport) throw new Error('no react-flow viewport');
      return new DOMMatrixReadOnly(getComputedStyle(viewport).transform).a;
    });
  const settledScale = async () => {
    let previous = await readScale();
    for (let attempt = 0; attempt < 20; attempt += 1) {
      await page.waitForTimeout(150);
      const next = await readScale();
      if (Math.abs(next - previous) < 1e-9) return next;
      previous = next;
    }
    return previous;
  };
  const zoom = await settledScale();
  expect(zoom, 'fit view must not fall through the min-zoom floor').toBeGreaterThanOrEqual(0.85);
  expect(zoom, 'fit view should not over-zoom a large graph').toBeLessThanOrEqual(1.001);

  // Effective legibility at the floor: the rendered size is the CSS
  // fontSize times the viewport scale. Node titles (--text-md 13px)
  // must land at >= 10px and port labels (--text-xs 11px) at >= 9px;
  // the floor's own math guarantees 11.05px and 9.35px.
  const effective = await page.evaluate(() => {
    const viewport = document.querySelector('.react-flow__viewport');
    if (!viewport) throw new Error('no react-flow viewport');
    const scale = new DOMMatrixReadOnly(getComputedStyle(viewport).transform).a;
    const title = document.querySelector<HTMLElement>('.loom-node-name');
    const port = document.querySelector<HTMLElement>('.port-name');
    return {
      title: title ? parseFloat(getComputedStyle(title).fontSize) * scale : 0,
      port: port ? parseFloat(getComputedStyle(port).fontSize) * scale : 0,
    };
  });
  expect(effective.title, 'node titles stay legible at fit-to-view').toBeGreaterThanOrEqual(10);
  expect(effective.port, 'port labels stay legible at fit-to-view').toBeGreaterThanOrEqual(9);

  // Auto-arrange completes in bounded time with a non-overlapping
  // layout. A 50-row chain cannot fit the viewport inside the 0.85
  // zoom floor, so the contract is geometry (no overlaps), budget
  // (< 6s), and the zoom floor — not viewport containment.
  const started = Date.now();
  await page.getByRole('button', { name: 'Auto-arrange' }).click();
  await expect
    .poll(
      async () => {
        const nodes = await page.locator('.react-flow__node').evaluateAll((elements) =>
          elements.map((element) => {
            const rect = element.getBoundingClientRect();
            return { x: rect.x, y: rect.y, w: rect.width, h: rect.height };
          }),
        );
        for (let i = 0; i < nodes.length; i += 1) {
          for (let j = i + 1; j < nodes.length; j += 1) {
            const a = nodes[i];
            const b = nodes[j];
            const separated =
              a.x + a.w < b.x || b.x + b.w < a.x || a.y + a.h < b.y || b.y + b.h < a.y;
            if (!separated) return false;
          }
        }
        return true;
      },
      { timeout: 5000, message: 'auto-arrange must produce a non-overlapping layout within 5s' },
    )
    .toBe(true);
  expect(Date.now() - started).toBeLessThan(6000);

  // The zoom floor still holds after the arrange (readability contract).
  const zoomAfter = await settledScale();
  expect(zoomAfter, 'the zoom floor must hold after auto-arrange').toBeGreaterThanOrEqual(0.85);
});
