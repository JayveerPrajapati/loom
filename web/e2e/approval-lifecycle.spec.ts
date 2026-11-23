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

test('the inbox shows wait age and orders the longest wait first', async ({ page, request }) => {
  const base = `e2e-age-${Date.now()}`;
  await startWaitingRun(request, `${base}-old`);
  // A visible gap so the two ages cannot land on the same tick.
  await new Promise((resolve) => setTimeout(resolve, 1500));
  await startWaitingRun(request, `${base}-new`);

  await page.goto('/tasks');
  const oldRow = page.locator('.task-row', { hasText: `${base}-old` });
  const newRow = page.locator('.task-row', { hasText: `${base}-new` });
  await expect(oldRow).toBeVisible({ timeout: 15000 });
  await expect(newRow).toBeVisible();

  // age appears, stated as elapsed time only
  await expect(oldRow.locator('.task-wait-age')).toHaveText(/waiting \d+(s|m|h|d)/);
  await expect(newRow.locator('.task-wait-age')).toHaveText(/waiting \d+(s|m|h|d)/);

  // oldest first: the earlier request sits above the later one
  const texts = await page.locator('.task-row').allInnerTexts();
  const oldIndex = texts.findIndex((text) => text.includes(`${base}-old`));
  const newIndex = texts.findIndex((text) => text.includes(`${base}-new`));
  expect(oldIndex).toBeGreaterThanOrEqual(0);
  expect(newIndex).toBeGreaterThan(oldIndex);
});

test('revoking the approval link hides the action and kills the link', async ({ page, request }) => {
  const id = `e2e-revoke-${Date.now()}`;
  expect((await request.post('/api/workflows', { data: approvalWorkflow(id) })).status()).toBe(201);

  // the runbar only tracks a run started from the editor
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Waiting');

  // capture the live token before revoking it
  const listed = (await (
    await request.get(`/api/runs?workflow=${encodeURIComponent(id)}`)
  ).json()) as {
    runs?: Array<{ id: string; status: string; resumeToken?: string }>;
  };
  const mine = (listed.runs ?? []).find((run) => run.status === 'waiting' && run.resumeToken);
  expect(mine?.resumeToken).toBeTruthy();

  // the link state shows next to the waiting actions
  await expect(page.locator('.runbar-link-state')).toHaveText(/Link valid for \d+d/);

  await page.getByRole('button', { name: 'Revoke link' }).click();
  // inline confirmation, not a browser dialog
  await expect(page.getByRole('button', { name: 'Confirm revoke' })).toBeVisible();
  await page.getByRole('button', { name: 'Confirm revoke' }).click();

  await expect(page.getByRole('button', { name: 'Revoke link' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Copy approval link' })).toHaveCount(0);
  await expect(page.locator('.runbar-link-state')).toHaveText('Link revoked');

  // the link itself is dead, and the run still waits for a decision
  const hook = await request.get(`/hook/approval/${mine!.resumeToken}`);
  expect(hook.status()).toBe(404);
  const after = (await (await request.get(`/api/runs/${mine!.id}`)).json()) as { status: string };
  expect(after.status).toBe('waiting');
});

test('a decision made on the approval link is labelled by source', async ({ page, request }) => {
  const id = `e2e-linksrc-${Date.now()}`;
  const runId = await startWaitingRun(request, id);
  const run = (await (await request.get(`/api/runs/${runId}`)).json()) as {
    resumeToken?: string;
  };
  expect(run.resumeToken).toBeTruthy();

  await page.goto(`/runs/${runId}`);
  // the waiting call to action carries the link expiry
  await expect(page.locator('.link-expiry')).toHaveText(/Link valid for \d+d/);

  // decide through the approval link itself, the way a recipient would
  const decided = await request.post(`/hook/approval/${run.resumeToken}`, {
    form: { decision: 'approved' },
  });
  expect(decided.status()).toBe(200);

  await page.reload();
  await expect(page.locator('.run-decision-summary')).toContainText('via approval link');
  await page.getByRole('tab', { name: 'Timeline', exact: true }).click();
  await expect(page.locator('.decision-row').first()).toContainText('via approval link');
});

test('bulk decisions keep failures itemised and retry only the failed ones', async ({
  page,
  request,
}) => {
  const base = `e2e-retry-${Date.now()}`;
  const good = await startWaitingRun(request, `${base}-good`);
  const bad = await startWaitingRun(request, `${base}-bad`);

  let failResume = true;
  await page.route(`**/api/runs/${encodeURIComponent(bad)}/resume`, async (route) => {
    if (failResume) {
      await route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'engine hiccup' }),
      });
    } else {
      await route.continue();
    }
  });

  await page.goto('/tasks');
  const goodRow = page.locator('.task-row', { hasText: `${base}-good` });
  const badRow = page.locator('.task-row', { hasText: `${base}-bad` });
  await expect(goodRow).toBeVisible({ timeout: 15000 });
  await expect(badRow).toBeVisible();

  await goodRow.locator('.task-select').check();
  await badRow.locator('.task-select').check();
  await page.getByRole('button', { name: 'Approve selected' }).click();

  // the failure is itemised with its id and message, not just counted
  await expect(page.locator('.task-failures')).toBeVisible();
  await expect(page.locator('.task-failures')).toContainText('engine hiccup');
  await expect(page.locator('.task-failures')).toContainText(bad.split('_').pop()!);

  // the good one landed; the bad one is untouched
  await expect(goodRow).toHaveCount(0, { timeout: 15000 });
  const badStatus = (await (await request.get(`/api/runs/${bad}`)).json()) as { status: string };
  expect(badStatus.status).toBe('waiting');

  // retry re-runs the decision for exactly the failed ids
  failResume = false;
  await page.getByRole('button', { name: 'Retry failed' }).click();
  await expect(page.locator('.task-failures')).toHaveCount(0, { timeout: 15000 });
  await expect(badRow).toHaveCount(0, { timeout: 15000 });
  const badAfter = (await (await request.get(`/api/runs/${bad}`)).json()) as { status: string };
  expect(badAfter.status).toBe('done');
});
