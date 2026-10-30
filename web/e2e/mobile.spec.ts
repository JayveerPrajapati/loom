import { expect, test } from '@playwright/test';

const pages = [
  { path: '/', name: 'home' },
  { path: '/templates', name: 'templates' },
  { path: '/jobs', name: 'jobs' },
  { path: '/runs', name: 'runs' },
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
  await page.getByRole('button', { name: 'Browse templates' }).click();
  await expect(page).toHaveURL(/\/templates$/);
  await expect(page.locator('#main-stage')).toBeVisible();
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
