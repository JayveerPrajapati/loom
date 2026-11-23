import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// code.python is subprocess execution — the largest security surface in
// Loom, gated behind --enable-subprocess-nodes. The e2e server runs -sim
// (no executor), so the honest end-to-end proof here is the GATE: a python
// workflow must fail with a clear, actionable error, and the editor must
// present the node with its effects summary. The positive execution path
// is proven by the engine test (TestPythonRecordsAndReplaysHermetically,
// which runs real Python) and the live CLI proof.

function pythonWorkflow(id: string) {
  return {
    id,
    name: `Python ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'py',
        type: 'code.python',
        typeVersion: 1,
        name: 'Compute',
        position: { x: 260, y: 0 },
        config: { source: 'return {"total": item["qty"] * 2}' } as Record<string, unknown>,
      },
      {
        id: 'done',
        type: 'data.set',
        typeVersion: 1,
        name: 'Done',
        position: { x: 520, y: 0 },
        config: { values: { seen: '{{ $json.total }}' } },
      },
    ],
    edges: [
      { from: { node: 'start', port: 'main' }, to: { node: 'py', port: 'main' } },
      { from: { node: 'py', port: 'main' }, to: { node: 'done', port: 'main' } },
    ],
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

async function seed(request: APIRequestContext, id: string, source = 'return {"total": item["qty"] * 2}') {
  const workflow = pythonWorkflow(id);
  workflow.nodes[1].config = { source };
  const response = await request.post('/api/workflows', { data: workflow });
  expect(response.status(), await response.text()).toBe(201);
}

// The suite normally runs -sim (gate closed); a LOOM_E2E_SUBPROCESS=1 run
// flips it. Each test probes the live server so the right side runs.
async function subprocessEnabled(request: APIRequestContext): Promise<boolean> {
  const response = await request.get('/healthz');
  const state = (await response.json()) as { subprocessNodes?: { enabled?: boolean } };
  return state.subprocessNodes?.enabled === true;
}

test('code.python surfaces the subprocess gate clearly', async ({ page, request }) => {
  test.skip(await subprocessEnabled(request), 'needs the -sim gate (subprocess disabled)');
  const id = `e2e-python-${Date.now()}`;
  await seed(request, id);
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  // The preflight banner appears before any run: the gate is discovered in
  // the editor, not in a failure message afterwards.
  const gate = page.getByTestId('python-gate-notice');
  await expect(gate).toBeVisible();
  await expect(gate).toContainText('Python nodes are disabled');
  await expect(gate).toContainText('--enable-subprocess-nodes');

  // The editor presents the node with its effects summary — the honesty
  // contract for effectful nodes.
  await page.locator('.loom-node').nth(1).click();
  await expect(page.getByTestId('node-effects')).toBeVisible();
  await expect(page.getByTestId('node-effects')).toContainText('subprocess');

  // Running under -sim first asks for confirmation (accept here), then
  // fails with the actionable opt-in error, not a silent or cryptic one.
  await page.getByRole('button', { name: 'Run workflow' }).click();
  const gateDialog = page.getByTestId('confirm-dialog');
  await expect(gateDialog).toBeVisible();
  await expect(gateDialog).toContainText('--enable-subprocess-nodes');
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Failed');
  await expect(page.locator('.runbar-line')).toContainText('--enable-subprocess-nodes');
});

test('pre-run warning can cancel a python run before it starts', async ({ page, request }) => {
  test.skip(await subprocessEnabled(request), 'needs the -sim gate (subprocess disabled)');
  const id = `e2e-python-gate-cancel-${Date.now()}`;
  await seed(request, id);
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.getByTestId('python-gate-notice')).toBeVisible();

  // Dismissing the pre-run warning must not start a run at all.
  await page.getByRole('button', { name: 'Run workflow' }).click();
  const gateDialog = page.getByTestId('confirm-dialog');
  await expect(gateDialog).toBeVisible();
  await expect(gateDialog).toContainText('--enable-subprocess-nodes');
  await page.getByTestId('confirm-dialog-cancel').click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Ready');
});

test('python preflight stays silent when subprocess nodes are enabled', async ({ page, request }) => {
  test.skip(!(await subprocessEnabled(request)), 'needs LOOM_E2E_SUBPROCESS=1');

  const id = `e2e-python-live-${Date.now()}`;
  await seed(request, id, 'return {"total": 1}');
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  // No banner and no confirmation: the enabled server is quiet.
  await expect(page.getByTestId('python-gate-notice')).toHaveCount(0);
  let dialogSeen = false;
  page.on('dialog', () => {
    dialogSeen = true;
  });
  await page.getByRole('button', { name: 'Run workflow' }).click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Complete');
  expect(dialogSeen).toBe(false);
});

test('code.python rejects unpinned requirements at save time', async ({ request }) => {
  const id = `e2e-python-unpinned-${Date.now()}`;
  await seed(request, id);

  // Unpinned requirements are rejected by the save contract (422).
  const bad = pythonWorkflow(id);
  bad.nodes[1].config = { source: 'return 1', requirements: ['httpx'] };
  const rejected = await request.put(`/api/workflows/${id}`, { data: bad });
  expect(rejected.status()).toBe(422);
  expect(await rejected.text()).toContain('invalid_python_requirement');

  // Pinned requirements save cleanly.
  const good = pythonWorkflow(id);
  good.nodes[1].config = { source: 'return 1', requirements: ['httpx==0.27.0'] };
  const accepted = await request.put(`/api/workflows/${id}`, { data: good });
  expect([200, 204]).toContain(accepted.status());
});
