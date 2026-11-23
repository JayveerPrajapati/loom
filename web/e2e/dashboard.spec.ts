import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext } from '@playwright/test';
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

function workflow(id: string) {
  return {
    id,
    name: `Dashboard workflow ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      { id: 'shape', type: 'data.set', typeVersion: 1, name: 'Shape', position: { x: 260, y: 0 }, config: { values: { message: 'hello' }, keepOnlySet: true } },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  };
}

async function seedJob(request: APIRequestContext, prefix: string) {
  const id = `e2e-${prefix}-${Date.now()}`;
  const name = `Dashboard job ${id}`;
  expect((await request.post('/api/workflows', { data: workflow(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  expect((await request.post('/api/jobs', { data: { name, templateId: id, templateVersion: 1 } })).status()).toBe(201);
  return { id, name };
}

test('jobs list More menu opens inside the viewport', async ({ page, request }) => {
  const job = await seedJob(request, 'e2e-menu');
  await page.goto('/jobs');
  const row = page.locator('.job-row', { hasText: job.name });
  await expect(row).toBeVisible();
  await row.locator('summary', { hasText: 'More' }).click();
  const menu = row.locator('.action-menu-list');
  await expect(menu).toBeVisible();
  await expect(menu).toBeInViewport();
});

test('pausing a job shows a success toast', async ({ page, request }) => {
  const job = await seedJob(request, 'e2e-toast');
  await page.goto('/jobs');
  const row = page.locator('.job-row', { hasText: job.name });
  await expect(row).toBeVisible();
  await row.locator('summary', { hasText: 'More' }).click();
  await row.getByRole('button', { name: 'Pause' }).click();
  const toast = page.locator('.toast');
  await expect(toast).toBeVisible();
  await expect(toast).toContainText('Job paused.');
  await expect(row.locator('.job-state')).toHaveText('Paused');
});

test('credentials form shows per-type hints and inserts an example', async ({ page }) => {
  await page.goto('/credentials');
  // The typed form is the default; the JSON fallback with its per-type
  // hints lives behind the explicit toggle. Postgres is typed now, so
  // the JSON hint path is exercised with slack.
  await page.getByRole('button', { name: 'Advanced JSON' }).click();
  await page.locator('#credential-type').fill('slack');
  await expect(page.locator('.field-hint')).toContainText('webhookUrl');
  await page.getByRole('button', { name: 'Insert example' }).click();
  const value = await page.locator('#credential-data').inputValue();
  expect(value).toContain('webhookUrl');

  // The postgres shapes are typed forms, not JSON hints.
  await page.getByRole('button', { name: 'Typed form' }).click();
  await page.locator('#credential-shape').selectOption('postgres:Structured');
  await expect(page.locator('#cred-field-host')).toBeVisible();
  await expect(page.locator('#cred-field-password')).toHaveAttribute('type', 'password');
});

test('home shows a first-run get-started guide on an empty workspace', async ({ page }) => {
  await page.route('**/api/stats', (route) =>
    route.fulfill({
      json: { total: 0, done: 0, failed: 0, waiting: 0, active: 0, last24h: 0 },
    }),
  );
  await page.route('**/api/runs?*', (route) => route.fulfill({ json: { runs: [], count: 0 } }));
  await page.route('**/api/workflows', (route) =>
    route.fulfill({ json: { workflows: [], count: 0 } }),
  );
  await page.goto('/');
  const guide = page.locator('.home-get-started');
  await expect(guide).toBeVisible();
  await expect(page.locator('.get-started-step')).toHaveCount(3);
  // The empty workspace shows the guide instead of all-zero metrics.
  await expect(page.locator('.dashboard-metrics')).toBeHidden();
  // The step-one anchor scrolls to the bundled examples.
  await page.getByRole('link', { name: 'See examples below' }).click();
  await expect(page.locator('#home-examples')).toBeVisible();
  await expect(page.locator('.example-card').first()).toBeVisible();
});

test('runs toolbar wraps instead of overflowing', async ({ page }) => {  await page.setViewportSize({ width: 1024, height: 900 });
  await page.goto('/runs');
  const toolbar = page.locator('.page-toolbar');
  await expect(toolbar).toBeVisible();
  const overflow = await toolbar.evaluate((element) => element.scrollWidth - element.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  await expect(page.locator('#run-search')).toBeVisible();
  await expect(page.locator('#run-limit')).toBeVisible();
});

test('dashboard pages have no serious accessibility violations', async ({ page }) => {
  for (const path of ['/', '/workflows', '/jobs', '/runs', '/credentials']) {
    await page.goto(path);
    await expect(page.locator('#main-stage')).toBeVisible();
    const report = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    const serious = report.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical');
    expect(serious.flatMap((violation) => violation.nodes.map((node) => `${violation.id} ${JSON.stringify(node.target)} ${node.html}`))).toEqual([]);
  }
});

test('home get-started steps stack at 390px without colliding @isolated', async (
  { page, request },
  testInfo,
) => {
  // @isolated: the get-started section only exists on a first-run home
  // (zero runs, zero workflows), so this runs in the isolated pass before
  // the example-running specs record any runs — and once per pass, in the
  // first project, because later projects would see those runs.
  test.skip(testInfo.project.name !== 'chromium', 'first-run home only exists once per isolated pass');

  // Sweep every workflow, not just e2e- ones: the isolated server is
  // disposable and only this pass sees it.
  const listed = await request.get('/api/workflows');
  const { workflows } = (await listed.json()) as { workflows?: { id: string }[] };
  for (const existing of workflows ?? []) {
    await request.delete(`/api/workflows/${encodeURIComponent(existing.id)}`);
  }
  const stats = (await (await request.get('/api/stats')).json()) as { total?: number };
  expect(stats.total ?? 0).toBe(0);

  await page.setViewportSize({ width: 390, height: 780 });
  await page.goto('/');
  await expect(page.locator('.home-get-started')).toBeVisible();

  const steps = await page.evaluate(() =>
    Array.from(document.querySelectorAll<HTMLElement>('.get-started-step')).map((el) => {
      const rect = el.getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    }),
  );
  expect(steps.length).toBe(3);
  // Stacked: one per row, all aligned to the same left edge, no overlap.
  for (let i = 1; i < steps.length; i += 1) {
    expect(Math.abs(steps[i].x - steps[0].x)).toBeLessThanOrEqual(2);
    expect(steps[i].y).toBeGreaterThanOrEqual(steps[i - 1].y + steps[i - 1].height);
  }
  // The "See examples below" button sits clear of surrounding text.
  const button = await page.getByRole('link', { name: 'See examples below' }).boundingBox();
  expect(button).toBeTruthy();
  const firstText = await page
    .locator('.get-started-step')
    .first()
    .getByRole('heading', { level: 2 })
    .boundingBox();
  expect(firstText).toBeTruthy();
  expect(button!.y).toBeGreaterThanOrEqual(firstText!.y + firstText!.height);
});
