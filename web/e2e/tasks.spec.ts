import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function approvalWorkflow(id: string) {
  return {
    id,
    name: `Approval ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'approve',
        type: 'flow.approval',
        typeVersion: 1,
        name: 'Approve',
        position: { x: 260, y: 0 },
        config: { message: 'ship it?' },
      },
      {
        id: 'yes',
        type: 'data.set',
        typeVersion: 1,
        name: 'Yes',
        position: { x: 520, y: 0 },
        config: { values: { ok: true } },
      },
    ],
    edges: [
      { from: { node: 'start', port: 'main' }, to: { node: 'approve', port: 'main' } },
      { from: { node: 'approve', port: 'approved' }, to: { node: 'yes', port: 'main' } },
    ],
  };
}

async function startWaitingRun(request: APIRequestContext, id: string): Promise<string> {
  expect((await request.post('/api/workflows', { data: approvalWorkflow(id) })).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs`, { data: { input: {} } });
  expect(started.status()).toBe(200);
  const { runId } = (await started.json()) as { runId: string };
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs/${runId}`);
    if (response.ok()) {
      const run = (await response.json()) as { status: string };
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

test('the tasks inbox approves a waiting run with a note', async ({ page, request }) => {
  const id = `e2e-tasks-${Date.now()}`;
  const runId = await startWaitingRun(request, id);

  await page.goto('/tasks');
  const row = page.locator('.task-row', { hasText: `Approval ${id}` });
  await expect(row).toBeVisible();

  // the nav badge shows the waiting count
  await expect(page.locator('nav.topnav .nav-badge')).toBeVisible({ timeout: 10000 });

  await page.locator('.task-note').fill('ok from inbox');
  await row.getByRole('button', { name: 'Approve', exact: true }).click();

  // the row clears once the decision lands (other specs may park their own
  // waiting runs concurrently, so scope to this workflow's row)
  await expect(row).toHaveCount(0, { timeout: 15000 });

  // the run finished and the decision note was recorded
  let status = '';
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs/${runId}`);
    if (response.ok()) {
      status = (await response.json()).status;
      if (status === 'done') break;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  expect(status).toBe('done');

  const events = await request.get(`/api/runs/${runId}/events?format=json`);
  const body = (await events.json()) as
    | { events?: Array<{ type: string; data?: { decision?: string; note?: string } }> }
    | Array<{ type: string; data?: { decision?: string; note?: string } }>;
  const evs = Array.isArray(body) ? body : (body.events ?? []);
  const resumed = evs.find((ev) => ev.type === 'run.resumed');
  expect(resumed?.data?.decision).toBe('approved');
  expect(resumed?.data?.note).toBe('ok from inbox');
});

test('the tasks inbox approves a selection in bulk', async ({ page, request }) => {
  const base = `e2e-bulk-${Date.now()}`;
  const first = await startWaitingRun(request, `${base}-a`);
  const second = await startWaitingRun(request, `${base}-b`);

  await page.goto('/tasks');
  // scope to this test's workflows: other specs may park waiting runs too
  const mine = page.locator('.task-row', { hasText: base });
  await expect(mine).toHaveCount(2, { timeout: 15000 });

  await mine.locator('.task-select').first().check();
  await mine.locator('.task-select').nth(1).check();
  await page.getByRole('button', { name: 'Approve selected' }).click();

  await expect(mine).toHaveCount(0, { timeout: 15000 });
  for (const runId of [first, second]) {
    const response = await request.get(`/api/runs/${runId}`);
    expect((await response.json()).status).toBe('done');
  }
});

test('rejecting requires a reason and refreshes the badge immediately', async ({ page, request }) => {
  const id = `e2e-reject-${Date.now()}`;
  const runId = await startWaitingRun(request, id);

  await page.goto('/tasks');
  const row = page.locator('.task-row', { hasText: `Approval ${id}` });
  await expect(row).toBeVisible();

  // Other specs may leave their own waiting runs, so compare against the
  // server count instead of assuming this run is the only one.
  const apiWaiting = async () => {
    const res = await request.get('/api/runs?status=waiting&limit=1');
    return ((await res.json()) as { count: number }).count;
  };
  const shownBadge = async () => {
    const badge = page.locator('nav.topnav .nav-badge');
    if ((await badge.count()) === 0) return 0;
    return Number((await badge.first().textContent())?.trim() || '0');
  };
  const before = await apiWaiting();
  expect(before).toBeGreaterThan(0);

  await row.getByRole('button', { name: 'Reject', exact: true }).click();
  await expect(page.locator('.task-note-error')).toBeVisible();
  const still = await request.get(`/api/runs/${runId}`);
  expect((await still.json()).status).toBe('waiting');

  await page.locator('.task-note').fill('out of budget');
  await expect(page.locator('.task-note-error')).toBeHidden();
  await row.getByRole('button', { name: 'Reject', exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 15000 });

  // The run leaves the waiting set, and the header badge tracks the server
  // without waiting for the 30s poll.
  await expect.poll(apiWaiting, { timeout: 10000 }).toBeLessThan(before);
  await expect
    .poll(async () => (await shownBadge()) === (await apiWaiting()), { timeout: 6000 })
    .toBe(true);

  const events = await request.get(`/api/runs/${runId}/events?format=json`);
  const body = (await events.json()) as {
    events?: Array<{ type: string; data?: { decision?: string; note?: string } }>;
  };
  const resumed = (body.events ?? []).find((ev) => ev.type === 'run.resumed');
  expect(resumed?.data?.decision).toBe('rejected');
  expect(resumed?.data?.note).toBe('out of budget');
});

test('a decision and its note are visible on the run', async ({ page, request }) => {
  const id = `e2e-decision-${Date.now()}`;
  const runId = await startWaitingRun(request, id);

  await page.goto('/tasks');
  const row = page.locator('.task-row', { hasText: `Approval ${id}` });
  await expect(row).toBeVisible();
  await page.locator('.task-note').fill('looks good');
  await row.getByRole('button', { name: 'Approve', exact: true }).click();
  await expect(row).toHaveCount(0, { timeout: 15000 });

  await page.goto(`/runs/${runId}`);
  await expect(page.locator('.run-decision-summary')).toContainText('approved');
  await expect(page.locator('.run-decision-summary')).toContainText('looks good');
  // The actor must resolve to the signed-in user, never the old "unknown".
  await expect(page.locator('.run-decision-summary')).toContainText('@');
  await expect(page.locator('.run-decision-summary')).not.toContainText('unknown');
  await page.getByRole('tab', { name: 'Timeline', exact: true }).click();
  await expect(page.locator('.decision-row').first()).toBeVisible();
});

test('the run page requires a reason before rejecting', async ({ page, request }) => {
  const id = `e2e-runreject-${Date.now()}`;
  const runId = await startWaitingRun(request, id);

  await page.goto(`/runs/${runId}`);
  await page.getByRole('button', { name: 'Reject', exact: true }).click();
  await expect(page.locator('.decision-popover')).toBeVisible();

  await page.getByRole('button', { name: 'Confirm rejection', exact: true }).click();
  await expect(page.locator('.decision-popover [role="alert"]')).toBeVisible();
  // A one-word answer is not a reason; the UI must refuse it too.
  await page.getByLabel('Rejection reason').fill('ok');
  await page.getByRole('button', { name: 'Confirm rejection', exact: true }).click();
  await expect(page.locator('.decision-popover [role="alert"]')).toContainText('at least 3 characters');
  const still = await request.get(`/api/runs/${runId}`);
  expect((await still.json()).status).toBe('waiting');

  await page.getByLabel('Rejection reason').fill('no budget this quarter');
  await page.getByRole('button', { name: 'Confirm rejection', exact: true }).click();
  await expect
    .poll(async () => {
      const res = await request.get(`/api/runs/${runId}`);
      return ((await res.json()) as { status: string }).status;
    }, { timeout: 15000 })
    .toBe('done');

  await expect(page.locator('.run-decision-summary')).toContainText('rejected');
  await expect(page.locator('.run-decision-summary')).toContainText('no budget this quarter');
});
