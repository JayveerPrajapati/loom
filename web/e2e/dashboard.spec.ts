import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext } from '@playwright/test';

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
  const id = `${prefix}-${Date.now()}`;
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

test('runs toolbar wraps instead of overflowing', async ({ page }) => {
  await page.setViewportSize({ width: 1024, height: 900 });
  await page.goto('/runs');
  const toolbar = page.locator('.page-toolbar');
  await expect(toolbar).toBeVisible();
  const overflow = await toolbar.evaluate((element) => element.scrollWidth - element.clientWidth);
  expect(overflow).toBeLessThanOrEqual(1);
  await expect(page.locator('#run-search')).toBeVisible();
  await expect(page.locator('#run-limit')).toBeVisible();
});

test('dashboard pages have no serious accessibility violations', async ({ page }) => {
  for (const path of ['/', '/templates', '/jobs', '/runs']) {
    await page.goto(path);
    await expect(page.locator('#main-stage')).toBeVisible();
    const report = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
    const serious = report.violations.filter((violation) => violation.impact === 'serious' || violation.impact === 'critical');
    expect(serious.flatMap((violation) => violation.nodes.map((node) => `${violation.id} ${JSON.stringify(node.target)} ${node.html}`))).toEqual([]);
  }
});
