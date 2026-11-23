import { expect, test, type Locator } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

// Tests only delete resources they created: every test resource carries an
// `e2e-` id, and this sweep removes exactly those (jobs first, then the
// workflows themselves) so state never accumulates across the suite.
test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

const pages = [
  { path: '/', name: 'home' },
  { path: '/workflows', name: 'workflows' },
  { path: '/jobs', name: 'jobs' },
  { path: '/runs', name: 'runs' },
  // Configuration Hub surfaces (Profile-Centric UX Phase 0 item 2: the
  // phone-fit audit now covers the hub routes too).
  { path: '/profiles', name: 'profiles landing' },
  { path: '/hub/http.request', name: 'hub node type' },
  { path: '/hub/http.request/new', name: 'profile form' },
  { path: '/schemas', name: 'schemas' },
  { path: '/mappings', name: 'mappings' },
];

function mobileWorkflow(id: string) {
  return {
    id,
    name: `Mobile workflow ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      { id: 'shape', type: 'data.set', typeVersion: 1, name: 'Shape', position: { x: 260, y: 0 }, config: { values: { message: 'hello' }, keepOnlySet: true } },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  };
}

for (const page of pages) {
  test(`${page.name} fits a phone viewport`, async ({ page: app }) => {
    await app.setViewportSize({ width: 360, height: 780 });
    await app.goto(page.path);
    await expect(app.locator('#main-stage')).toBeVisible();
    await app.waitForLoadState('networkidle');
    const overflow = await app.evaluate(() => {
      const shell = document.querySelector<HTMLElement>('.page-shell');
      return {
        document: document.documentElement.scrollWidth - window.innerWidth,
        shell: shell ? shell.scrollWidth - shell.clientWidth : 0,
      };
    });
    expect(overflow.document).toBeLessThanOrEqual(1);
    expect(overflow.shell).toBeLessThanOrEqual(1);
  });
}

test('home actions navigate on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 780 });
  await page.goto('/');
  await page.getByRole('button', { name: 'Browse workflows' }).click();
  await expect(page).toHaveURL(/\/workflows$/);
  await expect(page.locator('#main-stage')).toBeVisible();
});

test('primary navigation is reachable on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 360, height: 780 });
  await page.goto('/');
  await page.locator('details.mobile-nav summary').click();
  await expect(page.locator('.mobile-nav-list')).toBeVisible();
  await page.locator('.mobile-nav-list').getByRole('button', { name: 'Workflows' }).click();
  await expect(page).toHaveURL(/\/workflows$/);
});

test('touch targets meet 44x44 under a coarse pointer', async ({ page, request }) => {
  const id = `e2e-mobile-touch-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: mobileWorkflow(id) })).status()).toBe(201);

  await page.setViewportSize({ width: 390, height: 780 });
  await page.goto('/workflows');
  await expect(page.locator('#main-stage')).toBeVisible();

  const fits = async (control: Locator, name: string) => {
    const box = await control.boundingBox();
    expect(box, `${name} is rendered`).toBeTruthy();
    expect(box!.width, `${name} width`).toBeGreaterThanOrEqual(44);
    expect(box!.height, `${name} height`).toBeGreaterThanOrEqual(44);
  };

  // Top navigation: the menu trigger and the links inside it.
  const navTrigger = page.locator('details.mobile-nav > summary');
  await fits(navTrigger, 'mobile nav trigger');
  await navTrigger.click();
  await fits(page.locator('.mobile-nav-list button').first(), 'mobile nav link');
  await navTrigger.click();

  // A row action on a workflow card.
  const card = page.locator('.template-card', { hasText: `Mobile workflow ${id}` });
  await expect(card).toBeVisible();
  await fits(card.locator('.text-button').first(), 'row action');

  // Canvas zoom controls reach 44px and stay clear of the minimap. The
  // canvas renders from tablet width up (L2 removed it at phone
  // width); the device's touch emulation keeps pointer: coarse. L5
  // hides the minimap for small graphs, so this segment seeds a graph
  // wide enough (8 nodes, 260px pitch) to guarantee one.
  const wideId = `e2e-mobile-wide-${Date.now()}`;
  const wideNodes = Array.from({ length: 8 }, (_, index) => ({
    id: `n${index}`,
    type: index === 0 ? 'trigger.manual' : 'data.set',
    typeVersion: 1,
    name: `N${index}`,
    position: { x: index * 260, y: 0 },
    config: index === 0 ? {} : { values: { i: index } },
  }));
  expect(
    (
      await request.post('/api/workflows', {
        data: {
          id: wideId,
          name: `Mobile wide workflow ${wideId}`,
          version: 1,
          nodes: wideNodes,
          edges: wideNodes.slice(1).map((node) => ({
            from: { node: wideNodes[0].id, port: 'main' },
            to: { node: node.id, port: 'main' },
          })),
        },
      })
    ).status(),
  ).toBe(201);
  await page.setViewportSize({ width: 768, height: 1024 });
  await page.goto(`/workflows/${wideId}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  await fits(page.locator('.react-flow__controls-button').first(), 'canvas zoom control');
  const controls = await page.locator('.react-flow__controls').boundingBox();
  const minimap = await page.locator('.react-flow__minimap').boundingBox();
  expect(controls, 'zoom controls are rendered').toBeTruthy();
  expect(minimap, 'minimap is rendered').toBeTruthy();
  const overlapX =
    Math.min(controls!.x + controls!.width, minimap!.x + minimap!.width) -
    Math.max(controls!.x, minimap!.x);
  const overlapY =
    Math.min(controls!.y + controls!.height, minimap!.y + minimap!.height) -
    Math.max(controls!.y, minimap!.y);
  expect(
    overlapX <= 0 || overlapY <= 0,
    'the larger zoom controls must not overlap the minimap',
  ).toBeTruthy();
});

test('settings menu stays inside a phone viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await page.locator('details.settings-menu summary').click();
  const menu = page.locator('.settings-menu-list');
  await expect(menu).toBeVisible();
  const box = await menu.boundingBox();
  expect(box).toBeTruthy();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
});

test('home examples use a bounded horizontal carousel on a phone', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  const carousel = page.locator('.home-examples .example-grid');
  await expect(carousel).toBeVisible();
  const geometry = await carousel.evaluate((el) => ({
    scrollWidth: el.scrollWidth,
    clientWidth: el.clientWidth,
    viewport: window.innerWidth,
    cards: [...el.children].map((card) => {
      const rect = card.getBoundingClientRect();
      return { width: rect.width, right: rect.right };
    }),
  }));
  expect(geometry.scrollWidth).toBeGreaterThanOrEqual(geometry.clientWidth);
  expect(geometry.cards.length).toBeGreaterThan(1);
  for (const card of geometry.cards) {
    expect(card.width).toBeLessThanOrEqual(geometry.viewport);
    expect(card.right).toBeLessThanOrEqual(geometry.viewport + geometry.scrollWidth - geometry.clientWidth + 1);
  }
});

test('editor shows a desktop-only screen and offers simple node edits on a phone', async ({ page, request }) => {
  const id = `e2e-mobile-ro-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: mobileWorkflow(id) })).status()).toBe(201);

  await page.setViewportSize({ width: 390, height: 780 });
  await page.goto(`/workflows/${id}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  // L2: the dedicated desktop-only panel owns the message…
  const panel = page.getByTestId('mobile-desktop-only');
  await expect(panel).toBeVisible();
  await expect(panel).toContainText('Open on desktop to edit');
  const panelFont = await page.evaluate((el) => parseFloat(getComputedStyle(el).fontSize), await panel.elementHandle());
  expect(panelFont).toBeGreaterThanOrEqual(12);

  // …and the canvas is genuinely absent: no half-hidden strip, no zoom
  // controls over the first node, no minimap over the rest.
  await expect(page.locator('.canvas-shell')).toHaveCount(0);
  await expect(page.locator('.react-flow__controls')).toHaveCount(0);
  await expect(page.locator('.react-flow__minimap')).toHaveCount(0);

  // The mobile editing subset: pick a node, rename it, change a simple
  // value, copy the id. Destructive actions stay desktop-only.
  const mobile = page.getByTestId('mobile-inspector');
  await expect(mobile).toBeVisible();
  await page.getByTestId('mobile-node-select').selectOption('shape');
  await expect(page.locator('#mobile-node-name')).toHaveValue('Shape');
  await page.locator('#mobile-node-name').fill('Shape renamed');
  await page.locator('#field-outputType').selectOption('text');
  await expect(page.getByTestId('mobile-copy-node-id')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Delete node' })).toHaveCount(0);

  // The edit persists through the same save path as desktop.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const doc = await request.get(`/api/workflows/${id}`);
  const wf = (await doc.json()) as {
    nodes: { id: string; name: string; config: Record<string, unknown> }[];
  };
  const shape = wf.nodes.find((node) => node.id === 'shape');
  expect(shape?.name).toBe('Shape renamed');
  expect(shape?.config.outputType).toBe('text');
});

test('approvals stay reachable on a phone', async ({ page, request }) => {
  const id = `e2e-mobile-approval-${Date.now()}`;
  const approvalWorkflow = {
    id,
    name: `Mobile approval ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'approve',
        type: 'flow.approval',
        typeVersion: 1,
        name: 'Approve',
        position: { x: 360, y: 0 },
        config: { message: 'ship it?' },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'approve', port: 'main' } }],
  };
  expect((await request.post('/api/workflows', { data: approvalWorkflow })).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs`, { data: { input: {} } });
  expect(started.status()).toBe(200);
  const { runId } = (await started.json()) as { runId: string };
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const run = (await (await request.get(`/api/runs/${runId}`)).json()) as { status: string };
    if (run.status === 'waiting') break;
    if (['done', 'failed', 'cancelled'].includes(run.status)) {
      throw new Error(`run reached ${run.status} before waiting for approval`);
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }

  await page.setViewportSize({ width: 390, height: 780 });
  await page.goto(`/runs/${runId}`);
  await expect(page.locator('#main-stage')).toBeVisible();
  const approve = page.getByRole('button', { name: 'Approve', exact: true });
  await expect(approve).toBeVisible();
  const box = await approve.boundingBox();
  expect(box).toBeTruthy();
  expect(box!.width).toBeGreaterThanOrEqual(44);
  expect(box!.height).toBeGreaterThanOrEqual(44);
  await approve.click();
  // The decision took effect: the page leaves waiting (Approve button
  // disappears) and the run reaches a terminal status.
  await expect(approve).toBeHidden({ timeout: 15_000 });
  await expect
    .poll(async () => {
      const body = (await request.get(`/api/runs/${runId}`)) as unknown as { json(): Promise<{ status: string }> };
      return (await body.json()).status;
    })
    .toBe('done');
});

test('editor topbar wraps instead of clipping on a phone', async ({ page, request }) => {
  const id = `e2e-mobile-editor-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: mobileWorkflow(id) })).status()).toBe(201);

  await page.setViewportSize({ width: 360, height: 780 });
  await page.goto(`/workflows/${id}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  // Every topbar control stays reachable: the bar wraps instead of
  // overflowing the viewport.
  const overflow = await page.evaluate(() => {
    const bar = document.querySelector<HTMLElement>('.topbar');
    return {
      document: document.documentElement.scrollWidth - window.innerWidth,
      bar: bar ? bar.scrollWidth - bar.clientWidth : 0,
    };
  });
  expect(overflow.document).toBeLessThanOrEqual(1);
  expect(overflow.bar).toBeLessThanOrEqual(1);
  await expect(page.getByRole('button', { name: 'Run', exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Undo' })).toBeVisible();
});

test('run detail fits a phone viewport and is titled by the workflow', async ({ page, request }) => {
  const id = `e2e-mobile-run-${Date.now()}`;
  const name = `Mobile run ${id}`;
  expect((await request.post('/api/workflows', { data: { ...mobileWorkflow(id), name } })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs?async=1`, { data: { input: {} } });
  expect([201, 202]).toContain(started.status());
  const { runId } = await started.json();

  await page.setViewportSize({ width: 360, height: 780 });
  await page.goto(`/runs/${runId}`);
  await expect(page.locator('#main-stage')).toBeVisible();
  await expect(page.locator('h1')).toHaveText(name);

  // L3: the graph is replaced by a legible step list — no canvas, zoom
  // controls or minimap at phone width.
  const steps = page.getByTestId('run-step-list');
  await expect(steps).toBeVisible();
  await expect(steps.locator('.timeline-row')).not.toHaveCount(0);
  await expect(page.locator('.canvas-shell')).toHaveCount(0);
  await expect(page.locator('.react-flow__controls')).toHaveCount(0);
  await expect(page.locator('.react-flow__minimap')).toHaveCount(0);

  const overflow = await page.evaluate(() => {
    const shell = document.querySelector<HTMLElement>('.page-shell');
    return {
      document: document.documentElement.scrollWidth - window.innerWidth,
      shell: shell ? shell.scrollWidth - shell.clientWidth : 0,
    };
  });
  expect(overflow.document).toBeLessThanOrEqual(1);
  expect(overflow.shell).toBeLessThanOrEqual(1);
});

test('job table stays inside the phone viewport', async ({ page, request }) => {
  const id = `e2e-mobile-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: mobileWorkflow(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  expect((await request.post('/api/jobs', { data: { name: `Mobile job ${id}`, templateId: id, templateVersion: 1 } })).status()).toBe(201);

  await page.setViewportSize({ width: 360, height: 780 });
  await page.goto('/jobs');
  await expect(page.locator('.job-table')).toBeVisible();
  const overflow = await page.evaluate(() => {
    const shell = document.querySelector<HTMLElement>('.page-shell');
    const table = document.querySelector<HTMLElement>('.job-table');
    return {
      document: document.documentElement.scrollWidth - window.innerWidth,
      shell: shell ? shell.scrollWidth - shell.clientWidth : 0,
      table: table ? table.scrollWidth - table.clientWidth : 0,
    };
  });
  expect(overflow.document).toBeLessThanOrEqual(1);
  expect(overflow.shell).toBeLessThanOrEqual(1);
  expect(overflow.table).toBeLessThanOrEqual(1);
});
