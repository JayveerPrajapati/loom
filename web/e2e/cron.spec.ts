import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function cronWorkflow(id: string) {
  return {
    id,
    name: `Cron ${id}`,
    version: 1,
    nodes: [
      {
        id: 'start',
        type: 'trigger.cron',
        typeVersion: 1,
        name: 'Schedule',
        position: { x: 0, y: 0 },
        config: { schedule: '0 9 * * *', timezone: 'UTC' },
      },
      {
        id: 'set',
        type: 'data.set',
        typeVersion: 1,
        name: 'Shape',
        position: { x: 260, y: 0 },
        config: { values: { ok: true } },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'set', port: 'main' } }],
  };
}

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

test('the cron trigger offers the same schedule controls as jobs', async ({ page, request }) => {
  const id = `e2e-cron-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: cronWorkflow(id) })).status()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.workspace').waitFor({ state: 'visible' });
  await page.locator('.react-flow__node').filter({ hasText: 'Schedule' }).click();

  await expect(page.locator('#cron-preset')).toBeVisible();
  await expect(page.locator('#cron-schedule')).toHaveValue('0 9 * * *');
  await expect(page.locator('#cron-timezone')).toHaveValue('UTC');
  expect(await page.locator('#cron-preset option').count()).toBeGreaterThan(3);

  await page.locator('#cron-preset').selectOption('hourly');
  await expect(page.locator('#cron-schedule')).toHaveValue('0 * * * *');

  await page.locator('#cron-schedule').fill('*/15 * * * *');
  await expect(page.locator('#cron-preset')).toHaveValue('every-15');
  await page.locator('#cron-schedule').fill('7 3 * * *');
  await expect(page.locator('#cron-preset')).toHaveValue('custom');

  await expect(page.locator('.schedule-preview')).toBeVisible({ timeout: 10000 });

  // The timezone control is the same datalist the job editor uses.
  await page.locator('#cron-timezone').fill('Asia/Kolkata');
  await expect(page.locator('#cron-timezone-options option')).toHaveCount(13);
});
