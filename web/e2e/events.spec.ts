import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function emitWorkflow(id: string) {
  return {
    id,
    name: `Emit ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'emit',
        type: 'flow.emit',
        typeVersion: 1,
        name: 'Emit',
        position: { x: 260, y: 0 },
        config: { event: 'e2e-ping', payload: { hello: 'world' } },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'emit', port: 'main' } }],
  };
}

function subscriberWorkflow(id: string) {
  return {
    id,
    name: `Subscribe ${id}`,
    version: 1,
    nodes: [
      {
        id: 'start',
        type: 'trigger.event',
        typeVersion: 1,
        name: 'On ping',
        position: { x: 0, y: 0 },
        config: { event: 'e2e-ping' },
      },
      {
        id: 'mark',
        type: 'data.set',
        typeVersion: 1,
        name: 'Mark',
        position: { x: 260, y: 0 },
        config: { values: { got: 'hi {{ $json.hello }}' } },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'mark', port: 'main' } }],
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

test('an emitted event starts the subscribing workflow', async ({ page, request }) => {
  const suffix = Date.now();
  const emitterId = `e2e-emit-${suffix}`;
  const subscriberId = `e2e-sub-${suffix}`;
  expect((await request.post('/api/workflows', { data: emitWorkflow(emitterId) })).status()).toBe(201);
  expect((await request.post('/api/workflows', { data: subscriberWorkflow(subscriberId) })).status()).toBe(201);

  const started = await request.post(`/api/workflows/${emitterId}/runs?async=1`, { data: { input: {} } });
  expect([201, 202]).toContain(started.status());
  const { runId: emitterRun } = await started.json();

  let subRunId = '';
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs?workflow=${subscriberId}&limit=10`);
    if (response.ok()) {
      const { runs } = (await response.json()) as { runs: Array<{ id: string; parentRunId?: string; status: string }> };
      const hit = runs.find((r) => r.parentRunId === emitterRun && r.status === 'done');
      if (hit) {
        subRunId = hit.id;
        break;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  expect(subRunId, 'subscriber run never completed').not.toBe('');

  await page.goto(`/runs/${subRunId}`);
  await expect(page.locator('h1')).toContainText(`Subscribe ${subscriberId}`);
});

test('the editor palette offers the event nodes', async ({ page, request }) => {
  const id = `e2e-palette-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: subscriberWorkflow(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  // Secondary categories start collapsed; a search opens the groups it
  // matched, so the event nodes are offered without manual expanding.
  await page.getByLabel('Search nodes').fill('event');
  await expect(page.locator('.node-item-desc', { hasText: 'matching event' })).toBeVisible();
  await expect(page.locator('.node-item-desc', { hasText: 'other workflows can subscribe' })).toBeVisible();
});
