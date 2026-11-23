import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function linearWorkflow(id: string) {
  return {
    id,
    name: `Replay override ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'set',
        type: 'data.set',
        typeVersion: 1,
        name: 'Set',
        position: { x: 260, y: 0 },
        config: { values: { greeting: 'hi {{ $json.name }}' } },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'set', port: 'main' } }],
  };
}

async function seedDoneRun(request: APIRequestContext, id: string): Promise<string> {
  expect((await request.post('/api/workflows', { data: linearWorkflow(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs?async=1`, {
    data: { input: { name: 'loom' } },
  });
  expect([201, 202]).toContain(started.status());
  const { runId } = await started.json();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs/${runId}`);
    if (response.ok()) {
      const run = await response.json();
      if (run.status === 'done') return runId;
      if (['failed', 'cancelled'].includes(run.status)) break;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error(`run ${runId} never reached done`);
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

test('replaying a node with an edited input re-executes with the new item', async ({ page, request }) => {
  const id = `e2e-replay-override-${Date.now()}`;
  const runId = await seedDoneRun(request, id);

  await page.goto(`/runs/${runId}`);
  await page.locator('button[aria-label="Inspect node.finished for set"]').click();

  const textarea = page.locator('.json-input');
  await expect(textarea).toBeVisible();
  await expect(textarea).toHaveValue(/loom/);

  // Edit the recorded input before replaying from this node.
  await textarea.fill('{\n  "name": "edited"\n}');

  const replayResponse = page.waitForResponse(
    (r) => r.url().includes(`/api/runs/${runId}/replay`) && r.request().method() === 'POST',
  );
  await page.getByRole('button', { name: 'Replay from here' }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  const replay = await (await replayResponse).json();
  expect(replay.runId).toBeTruthy();
  expect(replay.runId).not.toBe(runId);

  // The app navigates to the child run.
  await page.waitForURL((url) => url.pathname === `/runs/${replay.runId}`);

  // The child run re-executed the set node with the edited input.
  await page.locator('button[aria-label="Inspect node.finished for set"]').click();
  const outputBlock = page.locator('.event-detail-block').filter({ hasText: 'Output item' });
  await expect(outputBlock).toContainText('hi edited', { timeout: 15000 });
});
