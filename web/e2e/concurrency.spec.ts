import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function waitingWorkflow(id: string) {
  return {
    id,
    name: `Concurrent ${id}`,
    version: 1,
    settings: { maxParallelRuns: 1 },
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'hold',
        type: 'flow.wait',
        typeVersion: 1,
        name: 'Hold',
        position: { x: 260, y: 0 },
        config: { seconds: 8 },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'hold', port: 'main' } }],
  };
}

async function runStatus(request: APIRequestContext, runId: string): Promise<string> {
  const response = await request.get(`/api/runs/${runId}`);
  expect(response.ok()).toBeTruthy();
  return ((await response.json()) as { status: string }).status;
}

async function waitForStatus(
  request: APIRequestContext,
  runId: string,
  want: string,
  timeoutMs: number,
): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    last = await runStatus(request, runId);
    if (last === want) return;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`run ${runId} stuck at ${last}, want ${want}`);
}

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

// This file holds the wakeup-latency regression guard; timing assertions
// must never pass on a retry, so retries are disabled even on CI.
test.describe.configure({ retries: 0 });

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

test('a capped workflow still completes every run through the server', async ({ request }) => {
  test.setTimeout(60_000);
  const id = `e2e-conc-${Date.now()}`;
  const created = await request.post('/api/workflows', { data: waitingWorkflow(id) });
  expect(created.status()).toBe(201);

  const ids: string[] = [];
  for (let i = 0; i < 3; i += 1) {
    const started = await request.post(`/api/workflows/${id}/runs`, { data: { input: {} } });
    expect(started.status()).toBe(200);
    ids.push(((await started.json()) as { runId: string }).runId);
  }
  // Server mode enables durable waits even with -sim: runs park at the wait
  // node (freeing the cap slot) and the exact-deadline scheduler resumes
  // them at their persisted wake time; the periodic sweep is only a
  // fallback. The 8-second wait must therefore finish well inside the wait
  // plus a tolerance — if wakeups ever regress to sweep-phase latency
  // (15s+), this timeout fails. What only the real server can show is still
  // verified here: the settings survive the API round-trip into the engine,
  // and the cap admits every run — none parks forever, deadlocks, or fails.
  for (const runId of ids) {
    await waitForStatus(request, runId, 'done', 15000);
  }
});

test('the inspector saves max parallel runs @isolated', async ({ page, request }) => {
  // @isolated: asserts the empty-workspace onboarding view; runs in the
  // isolated pass on its own server and data directory.
  await clearWorkflows(request);

  await page.goto('/');
  await expect(page.locator('.example-card').first()).toBeVisible();
  await page.getByRole('button', { name: 'Blank workflow' }).click();
  await expect(page.locator('.workspace')).toBeVisible();

  await page.locator('#workflow-max-runs').fill('2');
  await page.getByRole('button', { name: 'Save' }).click();

  const id = ((await page.locator('.meta-grid code').first().textContent()) ?? '').trim();
  expect(id).not.toBe('');

  const deadline = Date.now() + 10000;
  for (;;) {
    const response = await request.get(`/api/workflows/${encodeURIComponent(id)}`);
    expect(response.ok()).toBeTruthy();
    const workflow = (await response.json()) as { settings?: { maxParallelRuns?: number } };
    if (workflow.settings?.maxParallelRuns === 2) break;
    if (Date.now() > deadline) {
      throw new Error(`settings never persisted: ${JSON.stringify(workflow.settings)}`);
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
});
