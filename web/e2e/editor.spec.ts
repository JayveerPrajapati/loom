import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
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

const examples = [
  { name: 'CRM contact sync', nodes: 7 },
  { name: 'Daily news digest', nodes: 4 },
  { name: 'Six-hour order report', nodes: 4 },
  { name: 'Publish with approval', nodes: 6 },
  { name: 'Refund approval', nodes: 8 },
];

const linearWorkflow = {
  id: 'e2e-linear',
  name: 'E2E linear',
  version: 1,
  nodes: [
    {
      id: 'start',
      type: 'trigger.manual',
      typeVersion: 1,
      name: 'Start',
      position: { x: 0, y: 0 },
      config: {},
    },
    {
      id: 'shape',
      type: 'data.set',
      typeVersion: 1,
      name: 'Shape the item',
      position: { x: 260, y: 0 },
      config: { values: { message: 'hello' }, keepOnlySet: true },
    },
  ],
  edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
};

const traceWorkflow = {
  id: 'e2e-trace',
  name: 'E2E trace',
  version: 1,
  nodes: [
    {
      id: 'start',
      type: 'trigger.manual',
      typeVersion: 1,
      name: 'Start',
      position: { x: 0, y: 0 },
      config: {},
    },
    {
      id: 'call',
      type: 'http.request',
      typeVersion: 1,
      name: 'Call',
      position: { x: 260, y: 0 },
      config: {
        method: 'GET',
        url: 'https://api.example.com/orders/{{ $json.id }}',
        headers: { Authorization: 'Bearer secret', Accept: 'application/json' },
      },
      credentials: { http: 'http' },
    },
    {
      id: 'done',
      type: 'data.set',
      typeVersion: 1,
      name: 'Done',
      position: { x: 520, y: 0 },
      config: { values: { seen: '{{ $json.orderId }}' }, keepOnlySet: true },
    },
  ],
  edges: [
    { from: { node: 'start', port: 'main' }, to: { node: 'call', port: 'main' } },
    { from: { node: 'call', port: 'main' }, to: { node: 'done', port: 'main' } },
  ],
};

const flakyWorkflow = {
  id: 'e2e-flaky',
  name: 'E2E retry',
  version: 1,
  nodes: [
    {
      id: 'start',
      type: 'trigger.manual',
      typeVersion: 1,
      name: 'Start',
      position: { x: 0, y: 0 },
      config: {},
    },
    {
      id: 'call',
      type: 'http.request',
      typeVersion: 1,
      name: 'Call',
      position: { x: 260, y: 0 },
      config: { method: 'GET', url: 'https://api.example.com/flaky' },
      credentials: { http: 'http' },
    },
    {
      id: 'done',
      type: 'data.set',
      typeVersion: 1,
      name: 'Done',
      position: { x: 520, y: 0 },
      config: { values: { retried: true }, keepOnlySet: true },
    },
  ],
  edges: [
    { from: { node: 'start', port: 'main' }, to: { node: 'call', port: 'main' } },
    { from: { node: 'call', port: 'main' }, to: { node: 'done', port: 'main' } },
  ],
};

const approvalWorkflow = {
  id: 'e2e-approval',
  name: 'E2E approval',
  version: 1,
  nodes: [
    {
      id: 'start',
      type: 'trigger.manual',
      typeVersion: 1,
      name: 'Start',
      position: { x: 0, y: 0 },
      config: {},
    },
    {
      id: 'prepare',
      type: 'data.set',
      typeVersion: 1,
      name: 'Prepare',
      position: { x: 240, y: 0 },
      config: { values: { title: 'Hello' }, keepOnlySet: true },
    },
    {
      id: 'approve',
      type: 'flow.approval',
      typeVersion: 1,
      name: 'Approve',
      position: { x: 480, y: 0 },
      config: { channel: '#approvals', message: 'Publish {{ $json.title }}?' },
    },
    {
      id: 'done',
      type: 'data.set',
      typeVersion: 1,
      name: 'Finish',
      position: { x: 720, y: 0 },
      config: { values: { status: 'approved' }, keepOnlySet: true },
    },
  ],
  edges: [
    { from: { node: 'start', port: 'main' }, to: { node: 'prepare', port: 'main' } },
    { from: { node: 'prepare', port: 'main' }, to: { node: 'approve', port: 'main' } },
    { from: { node: 'approve', port: 'approved' }, to: { node: 'done', port: 'main' } },
  ],
};

async function clearWorkflows(request: APIRequestContext) {
  const jobsResponse = await request.get('/api/jobs');
  expect(jobsResponse.ok()).toBeTruthy();
  const jobs = (await jobsResponse.json()) as { jobs?: { id: string }[] };
  for (const job of jobs.jobs ?? []) {
    const deleted = await request.delete(`/api/jobs/${encodeURIComponent(job.id)}`);
    expect(deleted.status()).toBe(204);
  }
  const response = await request.get('/api/workflows');
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as { workflows?: { id: string }[] };
  for (const workflow of body.workflows ?? []) {
    const deleted = await request.delete(`/api/workflows/${encodeURIComponent(workflow.id)}`);
    expect(deleted.status()).toBe(204);
  }
}

async function seed(request: APIRequestContext, workflow: object) {
  const response = await request.post('/api/workflows', { data: workflow });
  expect(response.status(), await response.text()).toBe(201);
}

test('auto-arrange turns an overlapping graph into a non-overlapping grid layout', async ({ page, request }) => {
  // UX Phase 1.3: every node deliberately stacked on the same point; the
  // Auto-arrange action must move them onto a non-overlapping grid, be
  // undoable, and be reachable from the command palette too.
  const id = `e2e-arrange-${Date.now()}`;
  await seed(request, {
    id,
    name: 'E2E arrange',
    version: 1,
    settings: {},
    nodes: ['start', 'shape', 'branch-a', 'branch-b', 'merge'].map((node, index) => ({
      id: node,
      type: index === 0 ? 'trigger.manual' : index === 4 ? 'flow.merge' : 'data.set',
      typeVersion: 1,
      name: node,
      position: { x: 40, y: 40 },
      config: index === 0 || index === 4 ? {} : { values: { n: 1 } },
    })),
    edges: [
      { from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } },
      { from: { node: 'shape', port: 'main' }, to: { node: 'branch-a', port: 'main' } },
      { from: { node: 'shape', port: 'main' }, to: { node: 'branch-b', port: 'main' } },
      { from: { node: 'branch-a', port: 'main' }, to: { node: 'merge', port: 'a' } },
      { from: { node: 'branch-b', port: 'main' }, to: { node: 'merge', port: 'b' } },
    ],
  });
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  const bounds = () =>
    page.evaluate(() =>
      Array.from(document.querySelectorAll<HTMLElement>('.loom-node')).map((el) => {
        const r = el.getBoundingClientRect();
        return { x: r.x, y: r.y, w: r.width, h: r.height };
      }),
    );

  const overlaps = (boxes: { x: number; y: number; w: number; h: number }[]) => {
    for (let i = 0; i < boxes.length; i += 1) {
      for (let j = i + 1; j < boxes.length; j += 1) {
        const a = boxes[i];
        const b = boxes[j];
        if (Math.abs(a.x - b.x) < Math.min(a.w, b.w) && Math.abs(a.y - b.y) < Math.min(a.h, b.h)) return true;
      }
    }
    return false;
  };

  expect(overlaps(await bounds())).toBe(true);

  await page.getByRole('button', { name: 'Auto-arrange' }).click();
  await expect.poll(async () => overlaps(await bounds()), { timeout: 5_000 }).toBe(false);
  await expect(page.locator('.toast')).toContainText('auto-arranged');

  // Undoable: one ⌘Z returns the stacked layout.
  await page.keyboard.press('Meta+z');
  await expect.poll(async () => overlaps(await bounds()), { timeout: 5_000 }).toBe(true);

  // Command palette reaches the same action.
  await page.keyboard.press('Meta+k');
  await page.getByPlaceholder('Search actions, nodes, and workflows').fill('auto-arrange');
  await page.getByRole('button', { name: 'Auto-arrange graph' }).click();
  await expect.poll(async () => overlaps(await bounds()), { timeout: 5_000 }).toBe(false);
});

async function openOnboarding(page: Page) {
  await page.goto('/');
  await expect(page.locator('.example-card').first()).toBeVisible();
}

async function openTemplate(page: Page, id: string) {
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
}

test('the minimap appears only once the graph outgrows the viewport', async ({ page, request }) => {
  // L5: a minimap for a two-node pipeline is a blank box; it renders
  // only when the laid-out content spans more than a viewport.
  const small = `e2e-minimap-small-${Date.now()}`;
  await seed(request, {
    id: small,
    name: 'E2E minimap small',
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      { id: 'shape', type: 'data.set', typeVersion: 1, name: 'Shape', position: { x: 260, y: 0 }, config: { values: { n: 1 } } },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  });
  await openTemplate(page, small);
  await expect(page.locator('.react-flow__minimap')).toHaveCount(0);

  // Eight nodes at a 260px pitch span ~2.1kpx of content — past the
  // 1200px horizontal threshold — so the minimap (pannable, zoomable,
  // colour-coded) must be back.
  const large = `e2e-minimap-large-${Date.now()}`;
  const nodes = Array.from({ length: 8 }, (_, index) => ({
    id: `n${index}`,
    type: index === 0 ? 'trigger.manual' : 'data.set',
    typeVersion: 1,
    name: `N${index}`,
    position: { x: index * 260, y: 0 },
    config: index === 0 ? {} : { values: { i: index } },
  }));
  await seed(request, {
    id: large,
    name: 'E2E minimap large',
    version: 1,
    nodes,
    edges: nodes.slice(1).map((node) => ({
      from: { node: nodes[0].id, port: 'main' },
      to: { node: node.id, port: 'main' },
    })),
  });
  await openTemplate(page, large);
  await expect(page.locator('.react-flow__minimap')).toBeVisible();
});

test('legacy /templates URLs redirect to /workflows', async ({ page, request }) => {
  // UX Phase 3.1 terminology: /workflows is the canonical path family;
  // legacy /templates links must land on it, not dead-end.
  const id = `e2e-redirect-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E redirect' });

  await page.goto('/templates');
  await expect(page.locator('#main-stage')).toBeVisible();
  await expect(page).toHaveURL(/\/workflows$/);

  await page.goto(`/templates/${encodeURIComponent(id)}`);
  await expect(page.locator('#main-stage')).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/workflows/${id}$`));

  await page.goto(`/templates/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  await expect(page).toHaveURL(new RegExp(`/workflows/${id}/edit$`));
});

// No file-wide wipe: tests create their own `e2e-` resources and the
// afterEach sweep removes exactly those. The two @isolated tests need a
// genuinely empty workspace and instead run on a dedicated server with a
// fresh data directory (see playwright.config.ts).

test('lists examples, exposes a skip link, and creates a blank workflow @isolated', async ({ page, request }) => {
  // @isolated: this test asserts the empty-workspace onboarding view, so it
  // runs in the isolated pass on its own server and data directory.
  await clearWorkflows(request);
  await openOnboarding(page);
  await expect(page.locator('.example-card')).toHaveCount(5);

  await page.keyboard.press('Tab');
  await expect(page.locator('.skip-link')).toBeFocused();

  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await expect(page.locator('.workspace')).toBeVisible();
  await expect(page.locator('.loom-node')).toHaveCount(1);
  await expect(page.locator('.validation-chip')).toHaveText('Valid');

  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.locator('.toast')).toContainText('Workflow saved.');
});

test('opens and runs every bundled example @isolated', async ({ page, request }) => {
  // @isolated: asserts a workspace containing exactly one example workflow.
  await clearWorkflows(request);
  for (const example of examples) {
    await clearWorkflows(request);
    await page.goto('/');
    await page.getByRole('button', { name: new RegExp(example.name) }).click();
    await expect(page.locator('.workspace')).toBeVisible();
    await expect(page.locator('.loom-node')).toHaveCount(example.nodes);
    await expect(page.locator('.validation-chip')).toHaveText('Valid');

    const response = await request.get('/api/workflows');
    const body = (await response.json()) as { count: number; workflows: { name: string }[] };
    expect(body.count).toBe(1);
    expect(body.workflows[0].name).toBe(example.name);

    await page.getByRole('button', { name: 'Run workflow' }).click();
    const state = page.locator('.runbar-state strong');
    await expect(state).toHaveText(/Complete|Waiting/, { timeout: 15_000 });
    if ((await state.textContent()) === 'Waiting') {
      await page.locator('.runbar').getByRole('button', { name: 'Approve' }).click();
      await expect(state).toHaveText('Complete');
    }
  }
});

test('six-node example opens at zoom <= 1 with legible canvas text @isolated', async ({ page, request }) => {
  // UX Phase 1.2 contract: fitView caps the initial zoom at 1 so a small
  // graph never opens blown-up. L6 honesty fix: the rendered size is the
  // CSS fontSize times the viewport scale — with the 0.85 min-zoom floor
  // the smallest canvas type (--text-xs 11px ports) is guaranteed >=
  // 9.35px effective, so that is the threshold asserted here.
  await clearWorkflows(request);
  const example = examples.find((e) => e.nodes === 6);
  expect(example).toBeTruthy();
  await page.goto('/');
  await page.getByRole('button', { name: new RegExp(example.name) }).click();
  await expect(page.locator('.workspace')).toBeVisible();
  await expect(page.locator('.loom-node')).toHaveCount(example.nodes);

  const viewport = await page.evaluate(() => {
    const pane = document.querySelector('.react-flow__viewport');
    if (!pane) throw new Error('no react-flow viewport');
    const m = new DOMMatrixReadOnly(getComputedStyle(pane).transform);
    return m.a;
  });
  expect(viewport).toBeLessThanOrEqual(1);

  const tooSmall = await page.evaluate(() => {
    const pane = document.querySelector('.react-flow__viewport');
    if (!pane) throw new Error('no react-flow viewport');
    const scale = new DOMMatrixReadOnly(getComputedStyle(pane).transform).a;
    const selectors = ['.loom-node-name', '.loom-node-kind', '.loom-node-state', '.loom-node-type', '.port-row', '.port-name', '.port-type'];
    const offenders: string[] = [];
    for (const sel of selectors) {
      for (const el of document.querySelectorAll<HTMLElement>(sel)) {
        const effective = parseFloat(getComputedStyle(el).fontSize) * scale;
        if (effective < 9) offenders.push(`${sel}: ${effective}px at scale ${scale}`);
      }
    }
    return offenders;
  });
  expect(tooSmall).toEqual([]);
});

test('navigates home, workflows, and global runs', async ({ page, request }) => {
  const id = `e2e-nav-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E nav' });
  const started = await request.post(`/api/workflows/${id}/runs`, { data: {} });
  expect(started.ok()).toBeTruthy();

  await page.goto('/');
  await expect(page.locator('.home-hero')).toBeVisible();
  await expect(page.locator('.dashboard-metric').first()).toContainText('1');

  await page.getByRole('link', { name: 'Workflows' }).click();
  await expect(page).toHaveURL(/\/workflows$/);
  await expect(page.locator('.template-card')).toContainText('E2E nav');

  await page.getByRole('link', { name: 'Runs' }).click();
  await expect(page).toHaveURL(/\/runs$/);
  await expect(page.locator('.runs-row').first()).toContainText('E2E nav');

  await page.locator('.runs-row').first().click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toBeVisible();
  await expect(page.locator('.run-event-list')).toBeVisible();
  const log = page.locator('.run-event-list');
  const collapsedHeight = await log.evaluate((element) => getComputedStyle(element).maxHeight);
  expect(collapsedHeight).toBe('520px');
  await expect(page.getByTestId('run-event-expand')).toHaveText('Expand');
  await page.getByTestId('run-event-expand').click();
  await expect(page.getByTestId('run-event-expand')).toHaveText('Collapse');
  await expect(page.getByTestId('run-event-expand')).toHaveAttribute('aria-expanded', 'true');
  await expect(log).toHaveClass(/expanded/);
  const expandedHeight = await log.evaluate((element) => getComputedStyle(element).maxHeight);
  expect(expandedHeight).toBe('none');

  await page.getByRole('button', { name: 'Open workflow' }).click();
  await expect(page).toHaveURL(new RegExp(`/workflows/${id}$`));
  await expect(page.locator('.template-view-page')).toBeVisible();
  await page.getByRole('button', { name: 'Edit', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/workflows/${id}/edit$`));
  await expect(page.locator('.workspace')).toBeVisible();

  await page.getByRole('link', { name: 'Loom workflow studio' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator('.home-hero')).toBeVisible();
});

test('runs a workflow, streams node status, and inspects step data', async ({ page, request }) => {
  const id = `e2e-run-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E run' });
  await openTemplate(page, id);

  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Complete');
  await expect(page.locator('.loom-node.status-done')).toHaveCount(2);

  await page.locator('.runbar .action-menu summary').click();
  await page.getByRole('button', { name: 'Open run log' }).click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toBeVisible();
  await expect(page.locator('.event-row').filter({ hasText: 'node.finished' }).first()).toBeVisible();

  await page
    .locator('.event-row')
    .filter({ hasText: 'node.finished' })
    .last()
    .locator('.event-inspect')
    .click();
  await expect(page.locator('.event-detail')).toBeVisible();
  await expect(page.locator('.event-detail')).toContainText('hello');
  await expect(page.locator('.event-detail')).toContainText('Configuration');

  await page.getByRole('button', { name: 'Verify step' }).click();
  await expect(page.locator('.replay-result')).toContainText('matched');

  await page.getByRole('button', { name: 'Re-run live' }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.replay-result')).toContainText('Live step re-run');

  await page.getByRole('button', { name: 'Replay from here' }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toBeVisible();
});

test('runs with JSON input and shows request, response, and downstream data', async ({ page, request }) => {
  const id = `e2e-trace-${Date.now()}`;
  await seed(request, { ...traceWorkflow, id, name: 'E2E trace' });
  await openTemplate(page, id);

  await page.locator('.runbar .action-menu summary').click();
  await page
    .locator('.runbar .action-menu-list')
    .getByRole('button', { name: /Run with input/ })
    .click();
  await expect(page.getByRole('dialog', { name: 'Run input' })).toBeVisible();
  await page.locator('#run-input').fill(JSON.stringify({ id: 'A-42' }));
  await page
    .getByRole('dialog', { name: 'Run input' })
    .getByRole('button', { name: 'Run with input' })
    .click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Complete');

  await page.locator('.runbar .action-menu summary').click();
  await page.getByRole('button', { name: 'Open run log' }).click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toBeVisible();

  await page
    .locator('.event-row')
    .filter({ hasText: 'node.finished' })
    .filter({ hasText: 'call' })
    .locator('.event-inspect')
    .click();
  const detail = page.locator('.event-detail');
  await expect(detail).toContainText('Request');
  await expect(detail).toContainText('https://api.example.com/orders/A-42');
  await expect(detail).toContainText('[redacted]');
  await expect(detail).toContainText('Response');
  await expect(detail).toContainText('200');
  await expect(detail).toContainText('A-42');
  await page.getByRole('button', { name: 'Close step data' }).click();

  await page
    .locator('.event-row')
    .filter({ hasText: 'node.finished' })
    .filter({ hasText: 'done' })
    .locator('.event-inspect')
    .click();
  await expect(page.locator('.event-detail')).toContainText('A-1042');
});

test('retries from the failed node', async ({ page, request }) => {
  const id = `e2e-flaky-${Date.now()}`;
  await seed(request, { ...flakyWorkflow, id, name: 'E2E retry' });
  await openTemplate(page, id);
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Failed');
  await expect(page.locator('.runbar').getByRole('button', { name: 'Retry failed node' })).toBeVisible();

  await page.locator('.runbar').getByRole('button', { name: 'Retry failed node' }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Complete');
  await expect(page.locator('.loom-node.status-done')).toHaveCount(3);
});

test('diffs two runs of the same workflow', async ({ page, request }) => {
  const workflowID = `e2e-diff-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id: workflowID, name: 'E2E diff' });
  const first = await request.post(`/api/workflows/${workflowID}/runs`, { data: { input: {} } });
  expect(first.status()).toBe(200);
  const second = await request.post(`/api/workflows/${workflowID}/runs`, { data: { input: {} } });
  expect(second.status()).toBe(200);
  const secondID = ((await second.json()) as { runId: string }).runId;
  expect(secondID).toBeTruthy();

  await openTemplate(page, workflowID);
  await page.locator('.runbar .action-menu summary').click();
  await page.getByRole('button', { name: 'Run history' }).click();
  await expect(page.locator('.history-row')).toHaveCount(2);
  await page.locator('.diff-select').selectOption({ index: 1 });
  await expect(page.locator('.run-diff')).toBeVisible();
  await expect(page.locator('.run-diff-head')).toContainText('No differences');
  await expect(page.locator('.diff-row')).toHaveCount(2);
  await page.locator('.diff-row summary').first().click();
  await expect(page.locator('.diff-row').first()).toContainText('Base output');
});

test('opens a child run from the parent run history', async ({ page, request }) => {
  const stamp = Date.now();
  const child = {
    ...linearWorkflow,
    id: `e2e-z-child-${stamp}`,
    name: 'E2E child',
  };
  const parent = {
    id: `e2e-a-parent-${stamp}`,
    name: 'E2E parent',
    version: 1,
    nodes: [
      {
        id: 'start',
        type: 'trigger.manual',
        typeVersion: 1,
        name: 'Start',
        position: { x: 0, y: 0 },
        config: {},
      },
      {
        id: 'sub',
        type: 'flow.subflow',
        typeVersion: 1,
        name: 'Child',
        position: { x: 260, y: 0 },
        config: { workflowId: child.id, wait: true },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'sub', port: 'main' } }],
  };
  await seed(request, child);
  await seed(request, parent);
  const started = await request.post(`/api/workflows/${encodeURIComponent(parent.id)}/runs`, {
    data: {},
  });
  expect(started.ok()).toBeTruthy();

  await openTemplate(page, parent.id);
  await expect(page.locator('#workflow-name')).toHaveValue('E2E parent');
  await page.locator('.runbar .action-menu summary').click();
  await page.getByRole('button', { name: 'Run history' }).click();
  const children = page.getByRole('region', { name: 'Child runs' });
  await expect(children).toBeVisible();
  await children.getByRole('button', { name: /Open child run/ }).click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toContainText('E2E child');
});

test('downloads a generated runbook', async ({ page, request }) => {
  const id = `e2e-runbook-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E runbook' });
  await openTemplate(page, id);
  await expect(page.locator('#workflow-name')).toHaveValue('E2E runbook');
  const downloadPromise = page.waitForEvent('download');
  await page.getByRole('button', { name: 'Download runbook' }).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe(`${id}-runbook.md`);
});

test('suspends for approval and resumes on approval', async ({ page, request }) => {
  const id = `e2e-approval-${Date.now()}`;
  await seed(request, { ...approvalWorkflow, id, name: 'E2E approval' });
  await openTemplate(page, id);

  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Waiting');
  await expect(page.locator('.loom-node.status-waiting')).toHaveCount(1);
  await expect(page.locator('.runbar-line')).toHaveText('Publish Hello?');
  await expect(page.getByRole('button', { name: 'Copy approval link' })).toBeVisible();

  await page.locator('.runbar').getByRole('button', { name: 'Approve' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Complete');
  expect(await page.locator('.loom-node.status-done').count()).toBeGreaterThanOrEqual(3);
});

test('validates required credentials and can undo an edit', async ({ page }) => {
  await openOnboarding(page);
  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await page.getByLabel('Search nodes').fill('Postgres');
  await page.locator('.node-item').filter({ hasText: 'Postgres' }).click();
  await expect(page.locator('.loom-node')).toHaveCount(2);
  await expect(page.locator('.validation-chip.bad')).toBeVisible();

  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.locator('.toast-error')).toContainText('Fix validation errors');

  await page.keyboard.press('Control+z');
  await expect(page.locator('.loom-node')).toHaveCount(1);
  await expect(page.locator('.validation-chip')).toHaveText('Valid');
});

test('undoing a node delete restores its edges in one step', async ({ page, request }) => {
  const id = `e2e-undo-edge-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E undo edge' });
  await openTemplate(page, id);

  await page.locator('.loom-node').nth(1).click();
  await page.keyboard.press('Backspace');
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.loom-node')).toHaveCount(1);
  await expect(page.locator('.react-flow__edge')).toHaveCount(0);

  // One Backspace arrives as two commits (edge removal, then node removal);
  // a single undo must restore both the node and its edge.
  await page.keyboard.press('Control+z');
  await expect(page.locator('.loom-node')).toHaveCount(2);
  await expect(page.locator('.react-flow__edge')).toHaveCount(1);
});

test('adds a node from the command palette', async ({ page }) => {
  await openOnboarding(page);
  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await page.keyboard.press('Control+k');
  await expect(page.locator('.palette')).toBeVisible();
  await page.locator('.palette-input').fill('Set data');
  await page.locator('.palette-row').filter({ hasText: 'Set data' }).click();
  await expect(page.locator('.palette')).toBeHidden();
  await expect(page.locator('.loom-node')).toHaveCount(2);
});

test('a node added from the palette pans into view', async ({ page }) => {
  await openOnboarding(page);
  await page.getByRole('button', { name: 'Blank workflow' }).click();
  // Secondary categories start collapsed; searching a node's name opens
  // the groups it matched.
  await page.getByLabel('Search nodes').fill('Set data');
  await page.locator('.node-item').filter({ hasText: 'Set data' }).click();
  await expect(page.locator('.react-flow__node')).toHaveCount(2);

  // The pan runs on requestAnimationFrame after the node paints, then
  // animates for 200ms; poll until the new node settles inside the pane
  // instead of assuming a fixed delay.
  await expect
    .poll(
      async () => {
        const pane = (await page.locator('.react-flow__pane').boundingBox())!;
        const node = (await page.locator('.react-flow__node').nth(1).boundingBox())!;
        return node.x + node.width - (pane.x + pane.width);
      },
      { timeout: 4000, message: 'the new node must fit inside the canvas' },
    )
    .toBeLessThanOrEqual(1);
  const pane = (await page.locator('.react-flow__pane').boundingBox())!;
  const node = (await page.locator('.react-flow__node').nth(1).boundingBox())!;
  expect(node.x, 'the new node must not hang off the left edge').toBeGreaterThanOrEqual(pane.x - 1);
});

test('shows a failed run with its error', async ({ page, request }) => {
  const id = `e2e-failure-${Date.now()}`;
  await seed(request, {
    id,
    name: 'E2E failure',
    version: 1,
    nodes: [
      {
        id: 'start',
        type: 'trigger.manual',
        typeVersion: 1,
        name: 'Start',
        position: { x: 0, y: 0 },
        config: {},
      },
      {
        id: 'boom',
        type: 'code.run',
        typeVersion: 1,
        name: 'Boom',
        position: { x: 260, y: 0 },
        config: { language: 'javascript', source: "throw new Error('boom');" },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'boom', port: 'main' } }],
  });
  await openTemplate(page, id);
  await expect(page.locator('#workflow-name')).toHaveValue('E2E failure');
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Failed');
  await expect(page.locator('.runbar-line')).toContainText('boom');
  await expect(page.locator('.runbar')).toHaveClass(/is-failed/);
  await expect(page.locator('.loom-node.status-failed')).toHaveCount(1);
});

test('shows an offline banner and recovers when the server returns', async ({ page, context }) => {
  await openOnboarding(page);
  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await expect(page.locator('.workspace')).toBeVisible();

  await context.setOffline(true);
  await page.evaluate(() => window.dispatchEvent(new Event('offline')));
  const banner = page.locator('.connection-banner');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('Loom server unreachable');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await expect(page.locator('.runbar').getByRole('button', { name: 'Run workflow' })).toBeDisabled();

  await context.setOffline(false);
  await page.evaluate(() => window.dispatchEvent(new Event('online')));
  await expect(banner).toBeHidden({ timeout: 10_000 });
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
});

test('passes an automated accessibility audit on onboarding and canvas', async ({ page }) => {
  await openOnboarding(page);
  let report = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  let serious = report.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  expect(serious.flatMap((v) => v.nodes.map((n) => `${v.id} ${JSON.stringify(n.target)} ${n.html}`))).toEqual([]);

  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await expect(page.locator('.workspace')).toBeVisible();
  report = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  serious = report.violations.filter((v) => v.impact === 'serious' || v.impact === 'critical');
  expect(serious.flatMap((v) => v.nodes.map((n) => `${v.id} ${JSON.stringify(n.target)} ${n.html}`))).toEqual([]);
});

test('publishes a version and shows draft state with version history', async ({ page, request }) => {
  const id = `e2e-publish-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E publish' });
  await openTemplate(page, id);

  await page.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(page.locator('.toast')).toContainText('Published v1.');
  await expect(page.locator('.version-chip')).toHaveText('v1');

  const draft = { ...linearWorkflow, id, name: 'E2E publish draft' };
  const saved = await request.put(`/api/workflows/${id}`, { data: draft });
  expect(saved.ok()).toBeTruthy();

  await page.reload();
  await expect(page.locator('.version-chip')).toContainText('v1 · draft');
  await page.getByTestId('topbar-more').locator('summary').click();
  await page.locator('.version-chip').click();
  await expect(page.locator('.version-dialog')).toBeVisible();
  await expect(page.locator('.version-row').first()).toContainText('v1');
  await expect(page.locator('.version-detail')).toContainText('Template name');

  // Escape closes the version-history dialog.
  await page.keyboard.press('Escape');
  await expect(page.locator('.version-dialog')).toBeHidden();
});

test('creates a job from a published version and runs it', async ({ page, request }) => {
  const id = `e2e-job-${Date.now()}`;
  const name = `E2E job ${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E job template' });
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status()).toBe(201);

  await page.goto('/jobs');
  await page.getByRole('button', { name: 'Create job' }).first().click();
  await expect(page).toHaveURL(/\/jobs\/new$/);
  await page.locator('#job-template').selectOption(id);
  await page.locator('#job-version').selectOption('1');
  await page.locator('#job-name').fill(name);
  await page.locator('#job-input').fill('{"name":"loom"}');
  await page.getByRole('button', { name: 'Create job', exact: true }).click();

  await expect(page).toHaveURL(/\/jobs\/job-/);
  await expect(page.locator('.job-detail-head h1')).toHaveText(name);
  await expect(page.locator('.job-detail-head')).toContainText(/pinned to v1/i);
  await page.getByRole('button', { name: 'Run now' }).click();

  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toBeVisible();
});

test('runs a batch and shows child progress', async ({ page, request }) => {
  const id = `e2e-batch-${Date.now()}`;
  const name = `E2E batch job ${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E batch template' });
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status()).toBe(201);
  const created = await request.post('/api/jobs', {
    data: { name, templateId: id, templateVersion: 1 },
  });
  expect(created.status()).toBe(201);
  const job = (await created.json()) as { id: string };

  await page.goto(`/jobs/${job.id}`);
  await page.getByRole('button', { name: 'Run batch' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();

  // Escape closes the batch modal without starting anything.
  await page.keyboard.press('Escape');
  await expect(page.getByRole('dialog')).toBeHidden();

  // Backdrop click closes it too.
  await page.getByRole('button', { name: 'Run batch' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.locator('.batch-modal-backdrop').click({ position: { x: 4, y: 4 } });
  await expect(page.getByRole('dialog')).toBeHidden();

  await page.getByRole('button', { name: 'Run batch' }).click();
  await expect(page.getByRole('dialog')).toBeVisible();
  await page.locator('#batch-text').fill(
    JSON.stringify([{ name: 'Ada' }, { name: 'Grace' }, { name: 'Lin' }]),
  );
  await page.locator('#batch-concurrency').fill('2');
  await page.getByRole('button', { name: 'Start batch' }).click();

  const batch = page.locator('.batch-run').first();
  await expect(batch).toBeVisible();
  await expect(batch).toContainText('3 items');
  await expect(batch).toContainText('3 done');
  await expect(page.locator('.batch-child')).toHaveCount(3);
});

test('retries failed batch items', async ({ page, request }) => {
  const stamp = Date.now();
  const id = `e2e-batch-retry-${stamp}`;
  const name = `E2E batch retry job ${stamp}`;
  const workflow = {
    ...flakyWorkflow,
    id,
    name: 'E2E batch retry template',
    nodes: flakyWorkflow.nodes.map((node) =>
      node.id === 'call'
        ? {
            ...node,
            config: { ...node.config, url: `https://api.example.com/flaky-batch-${stamp}` },
          }
        : node,
    ),
  };
  await seed(request, workflow);
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status()).toBe(201);
  const created = await request.post('/api/jobs', {
    data: { name, templateId: id, templateVersion: 1 },
  });
  expect(created.status()).toBe(201);
  const job = (await created.json()) as { id: string };

  await page.goto(`/jobs/${job.id}`);
  await page.getByRole('button', { name: 'Run batch' }).click();
  await page.locator('#batch-text').fill(
    JSON.stringify([{ name: 'one' }, { name: 'two' }, { name: 'three' }, { name: 'four' }]),
  );
  await page.locator('#batch-concurrency').fill('1');
  await page.getByRole('button', { name: 'Start batch' }).click();

  const first = page.locator('.batch-run').first();
  await expect(first).toContainText('1 failed', { timeout: 20_000 });
  await first.getByRole('button', { name: /Retry 1 failed item/ }).click();

  await expect(page.locator('.batch-run')).toHaveCount(2, { timeout: 20_000 });
  const retried = page.locator('.batch-run').first();
  await expect(retried).toContainText('Batch run', { timeout: 20_000 });
  await expect(retried.locator('.meta-list')).toContainText('done', { timeout: 20_000 });
});

test('creates a cron job with a timezone and shows its next run', async ({ page, request }) => {
  const id = `e2e-cron-${Date.now()}`;
  const name = `E2E cron job ${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E cron template' });
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status()).toBe(201);

  await page.goto('/jobs/new');
  await page.locator('#job-template').selectOption(id);
  await page.locator('#job-version').selectOption('1');
  await page.locator('#job-name').fill(name);
  await page.locator('#job-trigger').selectOption('cron');
  await page.locator('#job-schedule').fill('0 9 * * *');
  await page.locator('#job-timezone').fill('America/New_York');
  await page.getByRole('button', { name: 'Create job', exact: true }).click();

  await expect(page).toHaveURL(/\/jobs\/job-/);
  const jobId = page.url().split('/').pop() ?? '';
  await expect(page.locator('.job-detail-head')).toContainText('Schedule trigger');
  await expect(page.locator('.job-facts')).not.toContainText('Next run Manual');

  const response = await request.get(`/api/jobs/${jobId}`);
  expect(response.ok()).toBeTruthy();
  const job = (await response.json()) as { triggerType: string; nextRunAt?: string };
  expect(job.triggerType).toBe('cron');
  expect(job.nextRunAt).toBeTruthy();
});

test('runs a one-time job once and pauses it', async ({ page, request }) => {
  const id = `e2e-once-${Date.now()}`;
  const name = `E2E one-time job ${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E one-time template' });
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status()).toBe(201);

  const at = new Date(Date.now() + 4000);
  const local = new Date(at.getTime() - at.getTimezoneOffset() * 60_000).toISOString().slice(0, 16);

  await page.goto('/jobs/new');
  await page.locator('#job-template').selectOption(id);
  await page.locator('#job-version').selectOption('1');
  await page.locator('#job-name').fill(name);
  await page.locator('#job-trigger').selectOption('once');
  await page.locator('#job-run-at').fill(local);
  await page.getByRole('button', { name: 'Create job', exact: true }).click();

  await expect(page).toHaveURL(/\/jobs\/job-/);
  const jobId = page.url().split('/').pop() ?? '';

  await expect
    .poll(async () => {
      const response = await request.get(`/api/jobs/${jobId}`);
      if (!response.ok()) return 'missing';
      const job = (await response.json()) as { enabled: boolean };
      return job.enabled;
    }, { timeout: 20_000 })
    .toBe(false);

  await expect
    .poll(async () => {
      const runs = await request.get(`/api/runs?job=${jobId}`);
      if (!runs.ok()) return -1;
      const body = (await runs.json()) as { count: number };
      return body.count;
    }, { timeout: 20_000 })
    .toBe(1);

  await page.reload();
  await expect(page.locator('.job-state')).toHaveText('Paused');
});

test('manages a job, duplicates it, and filters runs by job', async ({ page, request }) => {
  const id = `e2e-manage-${Date.now()}`;
  const name = `E2E managed job ${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E managed template' });
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status()).toBe(201);
  const created = await request.post('/api/jobs', {
    data: { name, templateId: id, templateVersion: 1, inputDefaults: { name: 'loom' } },
  });
  expect(created.status()).toBe(201);
  const job = (await created.json()) as { id: string };

  await page.goto(`/jobs/${job.id}`);
  await expect(page.locator('.job-detail-head h1')).toHaveText(name);
  await expect(page.locator('.job-facts')).toContainText('v1');

  // Pause/Resume and Edit settings live in the More kebab now. The
  // native details element stays open after an action, so each entry
  // only opens it when closed.
  const more = page.locator('.job-actions .action-menu');
  const openMore = async () => {
    const isOpen = await more.evaluate((el) => (el as HTMLDetailsElement).open);
    if (!isOpen) await more.locator('summary').click();
    await expect(more.locator('.action-menu-list')).toBeVisible();
  };

  await openMore();
  await more.getByRole('button', { name: 'Pause', exact: true }).click();
  await expect(page.locator('.job-state')).toHaveText('Paused');
  await openMore();
  await more.getByRole('button', { name: 'Resume', exact: true }).click();
  await expect(page.locator('.job-state')).toHaveText('Active');

  await openMore();
  await more.getByRole('button', { name: 'Edit settings', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${job.id}/edit$`));
  await page.locator('#detail-job-name').fill(`${name} renamed`);
  await page.getByRole('button', { name: 'Save settings' }).click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${job.id}$`));
  await expect(page.locator('#detail-job-name')).toHaveValue(`${name} renamed`);

  await openMore();
  await more.getByRole('button', { name: 'Edit settings', exact: true }).click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${job.id}/edit$`));
  await page.locator('#detail-job-name').fill('discarded edit');
  await page.getByRole('button', { name: 'Cancel' }).click();
  await expect(page).toHaveURL(new RegExp(`/jobs/${job.id}$`));
  await expect(page.locator('#detail-job-name')).toHaveValue(`${name} renamed`);

  await page.getByRole('button', { name: 'Run now' }).click();
  await expect(page).toHaveURL(/\/runs\//);
  await expect(page.locator('.run-detail-page')).toBeVisible();

  await page.goto(`/jobs/${job.id}`);
  await page.locator('.job-actions .action-menu summary').click();
  await page.getByRole('button', { name: 'Duplicate' }).click();
  await expect(page).toHaveURL(/\/jobs\/job-/);
  await expect(page.locator('.job-detail-head h1')).toHaveText(`${name} renamed copy`);
  await expect(page.locator('.job-state')).toHaveText('Paused');

  await page.goto('/runs');
  await page.locator('#run-job').selectOption(job.id);
  await expect(page.locator('.runs-row')).toHaveCount(1);
  await expect(page.locator('.runs-row').first()).toContainText(`${name} renamed`);
});

test('the inspector shows the EffectPolicy for effectful nodes only', async ({ page, request }) => {
  const id = `e2e-effects-${Date.now()}`;
  await seed(request, {
    id, name: 'E2E effects', version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      { id: 'http', type: 'http.request', typeVersion: 1, name: 'Fetch', position: { x: 260, y: 0 }, config: { method: 'GET', url: 'https://example.test/api' } },
      { id: 'shape', type: 'data.set', typeVersion: 1, name: 'Shape', position: { x: 520, y: 0 }, config: { values: { keep: true } } },
    ],
    edges: [
      { from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } },
      { from: { node: 'http', port: 'main' }, to: { node: 'shape', port: 'main' } },
    ],
  });
  await openTemplate(page, id);

  // http.request carries external effects: the summary is visible and
  // tells the truth about the effect tape.
  await page.locator('.loom-node').nth(1).click();
  await expect(page.getByTestId('node-effects')).toBeVisible();
  await expect(page.getByTestId('node-effects')).toContainText('effect tape');

  // A pure node must not show an effects block at all.
  await page.locator('.loom-node').nth(2).click();
  await expect(page.getByTestId('node-effects')).toHaveCount(0);
});

test('the inspector renders grouped fields with labels, placeholders, and an Advanced disclosure', async ({
  page,
  request,
}) => {
  // UX Phase 3.3: catalog metadata drives the inspector — curated labels
  // and placeholders, descriptions as hints, secrets masked, and advanced
  // fields behind a disclosure. Covers the four test-target node types.
  const id = `e2e-inspector-${Date.now()}`;
  await seed(request, {
    id, name: 'E2E inspector', version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      { id: 'http', type: 'http.request', typeVersion: 1, name: 'Fetch', position: { x: 260, y: 0 }, config: { method: 'GET', url: 'https://example.test/api' } },
      { id: 'py', type: 'code.python', typeVersion: 1, name: 'Py', position: { x: 520, y: 0 }, config: { source: 'return {"ok": True}' } },
      { id: 'db', type: 'db.postgres', typeVersion: 1, name: 'Db', position: { x: 780, y: 0 }, config: { operation: 'query', query: 'select 1' }, credentials: { postgres: 'e2e-inspector-cred' } },
      { id: 'approve', type: 'flow.approval', typeVersion: 1, name: 'Gate', position: { x: 1040, y: 0 }, config: { message: 'ship it?' } },
    ],
    edges: [
      { from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } },
      { from: { node: 'http', port: 'main' }, to: { node: 'py', port: 'main' } },
      { from: { node: 'py', port: 'main' }, to: { node: 'db', port: 'main' } },
      { from: { node: 'db', port: 'main' }, to: { node: 'approve', port: 'main' } },
    ],
  });
  await openTemplate(page, id);

  // http.request: curated label and placeholder on Basics; advanced
  // fields live on the Advanced tab (Phase 3) and are hidden until then.
  await page.locator('.loom-node').nth(1).click();
  await expect(page.locator('.field-label', { hasText: 'URL' })).toBeVisible();
  await expect(page.locator('#field-url')).toHaveAttribute('placeholder', 'https://api.example.com/posts/{postId}');
  await expect(page.locator('.field-hint', { hasText: 'path placeholders' })).toBeVisible();
  await expect(page.locator('#field-timeout')).toBeHidden();
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.locator('#field-timeout')).toBeVisible();
  await page.getByTestId('inspector-tab-configuration').click();
  await expect(page.locator('#field-timeout')).toBeHidden();

  // code.python: description hint under the requirements field.
  await page.locator('.loom-node').nth(2).click();
  await expect(page.locator('.field-label', { hasText: 'Python source' })).toBeVisible();
  await expect(page.locator('.field-hint', { hasText: 'time and memory cap' })).toBeVisible();

  // db.postgres: SQL label + parameterized-statement guidance.
  await page.locator('.loom-node').nth(3).click();
  await expect(page.locator('.field-label', { hasText: 'SQL' })).toBeVisible();
  await expect(page.locator('.field-hint', { hasText: 'never interpolate' })).toBeVisible();

  // flow.approval: message field on Basics, notify URL on Advanced.
  await page.locator('.loom-node').nth(4).click();
  await expect(page.locator('#field-message')).toBeVisible();
  await expect(page.locator('#field-notifyUrl')).toBeHidden();
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.locator('#field-notifyUrl')).toBeVisible();
});

test('the empty canvas offers three ways to start', async ({ page, request }) => {
  await openOnboarding(page);
  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await expect(page.locator('.react-flow__node')).toHaveCount(1);

  // A blank workflow seeds one trigger; deleting it reaches the truly
  // empty canvas where the overlay appears.
  await page.locator('.react-flow__node').first().click();
  await page.keyboard.press('Backspace');
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.react-flow__node')).toHaveCount(0);
  await expect(page.getByTestId('canvas-empty')).toBeVisible();

  // "Add first node" seeds a manual trigger directly.
  await page.getByRole('button', { name: 'Add first node' }).click();
  await expect(page.locator('.react-flow__node')).toHaveCount(1);
  await expect(page.getByTestId('canvas-empty')).toHaveCount(0);
  await page.keyboard.press('Control+z');
  await expect(page.locator('.react-flow__node')).toHaveCount(0);
  await expect(page.getByTestId('canvas-empty')).toBeVisible();

  // "Import JSON" applies pasted nodes to the still-empty editor. The API
  // refuses to create a nodeless workflow, so the empty state is reached
  // through the editor itself (as above), not by seeding.
  await page.getByRole('button', { name: 'Import JSON' }).click();
  await page
    .locator('.import-dialog textarea')
    .fill(JSON.stringify({ nodes: linearWorkflow.nodes, edges: linearWorkflow.edges }));
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(page.locator('.react-flow__node')).toHaveCount(2);
  await expect(page.locator('.react-flow__edge')).toHaveCount(1);

  // Invalid JSON is rejected inline, leaving the dialog open.
  await page.keyboard.press('Control+z');
  await expect(page.locator('.react-flow__node')).toHaveCount(0);
  await page.getByRole('button', { name: 'Import JSON' }).click();
  await page.locator('.import-dialog textarea').fill('{not json');
  await page.getByRole('button', { name: 'Import', exact: true }).click();
  await expect(page.locator('.import-dialog .field-error')).toContainText('not valid JSON');
  await page.getByRole('button', { name: 'Cancel' }).click();

  // "Start from template" navigates to the workflows list. The editor is
  // dirty (the deleted trigger) so the navigation asks for confirmation.
  await page.getByRole('button', { name: 'Start from template' }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page).toHaveURL(/\/workflows$/);
});

test('the run detail page copies the full run id', async ({ page, request, context }) => {
  // Headless Chromium denies clipboard writes without an explicit grant;
  // grant both directions so the copy path runs for real.
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const id = `e2e-copy-id-${Date.now()}`;
  await seed(request, { ...linearWorkflow, id, name: 'E2E copy id' });
  const started = await request.post(`/api/workflows/${id}/runs`, { data: {} });
  expect(started.ok()).toBeTruthy();
  const { runId } = await started.json();

  await page.goto(`/runs/${runId}`);
  await expect(page.locator('.run-detail-page')).toBeVisible();
  await page.getByRole('button', { name: 'Copy full run id' }).click();
  await expect(page.locator('.toast')).toContainText('Full run id copied');
  // The write is async — poll the clipboard until it lands.
  await expect
    .poll(async () => await page.evaluate(() => navigator.clipboard.readText()))
    .toBe(runId);
});
