import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Configuration Hub v2 visual regression (V6): deterministic DOM-geometry
// gates for the new hub surfaces — data contracts page, mappings page and
// the shape-aware inspector. Screenshots are artifacts only.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function createSchema(request: APIRequestContext, name: string) {
  const response = await request.post('/api/data-schemas', {
    data: {
      name,
      fields: [
        { name: 'email', type: 'string', required: true },
        { name: 'count', type: 'number' },
        { name: 'tags', type: 'array', items: { name: 'tag', type: 'string' } },
      ],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()).id;
}

async function createMapping(request: APIRequestContext, name: string) {
  const response = await request.post('/api/mappings', {
    data: {
      name,
      rows: [
        { target: 'email', source: '{{ $json.email }}', required: true },
        { target: 'label', source: '{{ $json.name }} {{ $json.count }}' },
      ],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()).id;
}

test('schemas page: two-column grid, panels never overlap', async ({ page, request }) => {
  await createSchema(request, 'e2e-visual-schema');
  await page.goto('/schemas');
  const panels = page.locator(
    '.dashboard-grid > .dashboard-panel, .dashboard-grid > .asset-editor',
  );
  // The read-only rail is on demand: the list starts full width, and
  // selecting a row opens the detail rail beside it.
  await expect(panels.nth(0)).toBeVisible();
  await expect(panels).toHaveCount(1);
  await page.getByRole('button', { name: 'View', exact: true }).first().click();
  await expect(panels.nth(1)).toBeVisible();
  const left = await panels.nth(0).boundingBox();
  const right = await panels.nth(1).boundingBox();
  expect(left).toBeTruthy();
  expect(right).toBeTruthy();
  expect(right!.x).toBeGreaterThan(left!.x + left!.width - 1);
  expect(Math.abs(left!.y - right!.y)).toBeLessThanOrEqual(1);
  const pageWidth = await page.evaluate(() => document.documentElement.clientWidth);
  expect(left!.x).toBeGreaterThanOrEqual(0);
  expect(right!.x + right!.width).toBeLessThanOrEqual(pageWidth);
  await page.screenshot({ path: 'e2e-artifacts/visual-schemas-page.png' });
});

test('mappings page: rows stay inside the page shell', async ({ page, request }) => {
  await createMapping(request, 'e2e-visual-mapping');
  await page.goto('/mappings');
  const shell = page.locator('.page-shell');
  await expect(shell).toBeVisible();
  const shellBox = await shell.boundingBox();
  const rows = page.locator('.dashboard-run-row');
  await expect(rows.first()).toBeVisible();
  const count = await rows.count();
  for (let i = 0; i < count; i++) {
    const box = await rows.nth(i).boundingBox();
    expect(box).toBeTruthy();
    expect(box!.x).toBeGreaterThanOrEqual(shellBox!.x);
    expect(box!.x + box!.width).toBeLessThanOrEqual(shellBox!.x + shellBox!.width);
  }
  await page.screenshot({ path: 'e2e-artifacts/visual-mappings-page.png' });
});

test('inspector: contract preview and pills stay inside the inspector column', async ({ page, request }) => {
  const schemaId = await createSchema(request, 'e2e-visual-shape');
  await request.post('/api/workflows', {
    data: {
      id: 'e2e-visual-flow',
      name: 'Visual v2 flow',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
        {
          id: 'source',
          type: 'data.set',
          typeVersion: 1,
          name: 'Source',
          position: { x: 260, y: 0 },
          config: { values: { email: 'a@b.c' } },
          outputSchemaRef: schemaId,
        },
        {
          id: 'sink',
          type: 'text.template',
          typeVersion: 1,
          name: 'Sink',
          position: { x: 520, y: 0 },
          config: { template: 'hello' },
        },
      ],
      edges: [
        { from: { node: 'start', port: 'main' }, to: { node: 'source', port: 'main' } },
        { from: { node: 'source', port: 'main' }, to: { node: 'sink', port: 'main' } },
      ],
    },
  });

  await page.goto('/workflows/e2e-visual-flow/edit');
  await page.locator('.loom-node', { hasText: 'Sink' }).click();
  const template = page.getByRole('textbox', { name: /Template/ });
  await template.click();

  const pills = page.getByTestId('expression-pills');
  await expect(pills).toBeVisible();
  const inspector = page.locator('.inspector, aside').last();
  const inspectorBox = await inspector.boundingBox();
  const pillBox = await pills.boundingBox();
  expect(inspectorBox).toBeTruthy();
  expect(pillBox).toBeTruthy();
  expect(pillBox!.x).toBeGreaterThanOrEqual(inspectorBox!.x - 1);
  expect(pillBox!.x + pillBox!.width).toBeLessThanOrEqual(inspectorBox!.x + inspectorBox!.width + 1);

  // The source node's contract preview also stays in the column (the
  // preview lives on the Data tab — Phase 3).
  await page.locator('.loom-node', { hasText: 'Source' }).click();
  await page.getByTestId('inspector-tab-data').click();
  const preview = page.getByTestId('schema-preview');
  await expect(preview).toBeVisible();
  const previewBox = await preview.boundingBox();
  expect(previewBox!.x).toBeGreaterThanOrEqual(inspectorBox!.x - 1);
  expect(previewBox!.x + previewBox!.width).toBeLessThanOrEqual(inspectorBox!.x + inspectorBox!.width + 1);

  // Canvas never overlaps the inspector.
  const canvas = page.locator('.react-flow').first();
  const canvasBox = await canvas.boundingBox();
  expect(canvasBox!.x + canvasBox!.width).toBeLessThanOrEqual(inspectorBox!.x + 1);
  await page.screenshot({ path: 'e2e-artifacts/visual-v2-inspector.png' });
});
