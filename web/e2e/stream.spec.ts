import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function approvalWorkflow(id: string) {
  return {
    id,
    name: `Waiting run ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'approve',
        type: 'flow.approval',
        typeVersion: 1,
        name: 'Approve publish',
        position: { x: 260, y: 0 },
        config: { channel: '#content', message: 'Publish?' },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'approve', port: 'main' } }],
  };
}

async function seedWaitingRun(request: APIRequestContext, id: string): Promise<string> {
  expect((await request.post('/api/workflows', { data: approvalWorkflow(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs?async=1`, { data: { input: {} } });
  expect([201, 202]).toContain(started.status());
  const { runId } = await started.json();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs/${runId}`);
    if (response.ok()) {
      const run = await response.json();
      if (run.status === 'waiting') return runId;
      if (['done', 'failed', 'cancelled'].includes(run.status)) break;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`run ${runId} never reached waiting`);
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

test('a waiting run does not reconnect the event stream', async ({ page, request }) => {
  const id = `e2e-waiting-${Date.now()}`;
  const runId = await seedWaitingRun(request, id);

  const counts = new Map<string, number>();
  let streamRequests = 0;
  page.on('request', (req) => {
    const url = new URL(req.url());
    if (!url.pathname.startsWith('/api/')) return;
    counts.set(url.pathname, (counts.get(url.pathname) ?? 0) + 1);
    if (url.pathname.endsWith('/events')) streamRequests += 1;
  });

  await page.goto(`/runs/${runId}`);
  await expect(page.getByRole('button', { name: 'Approve', exact: true })).toBeVisible();
  await page.waitForTimeout(3000);

  const total = [...counts.values()].reduce((sum, value) => sum + value, 0);
  expect(streamRequests, `event stream requests in 3s: ${streamRequests}`).toBeLessThanOrEqual(3);
  expect(total, `api requests in 3s: ${total} ${JSON.stringify(Object.fromEntries(counts))}`).toBeLessThanOrEqual(12);
  await expect(page.getByRole('button', { name: 'Approve', exact: true })).toBeVisible();

  await page.getByRole('button', { name: 'Approve', exact: true }).click();
  await expect(page.locator('.status-text')).toContainText(/done|failed|running/i, { timeout: 20_000 });
});
