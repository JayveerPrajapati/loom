import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Configuration Hub v2 (data flow): contract pickers in the inspector,
// publish resolution + usage pins, webhook annotate/strict behavior and the
// run-detail contract verdict — all through the real UI and API.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function createSchema(request: any, name: string, fields: unknown[]) {
  const response = await request.post('/api/data-schemas', {
    data: { name, fields },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string; name: string; version: number };
}

test('inspector attaches an input contract to a webhook and publishes it inline', async ({
  page,
  request,
}) => {
  const schema = await createSchema(request, 'e2e-order-contract', [
    { name: 'email', type: 'string', required: true },
    { name: 'total', type: 'number' },
  ]);
  const id = `e2e-contract-wf-${Date.now()}`;
  const seed = await request.post('/api/workflows', {
    data: {
      id,
      name: 'Contract webhook',
      version: 1,
      nodes: [
        {
          id: 'hook',
          type: 'trigger.webhook',
          typeVersion: 1,
          name: 'Webhook',
          position: { x: 0, y: 0 },
          config: { path: `contract-${id}`, method: 'POST' },
        },
        {
          id: 'echo',
          type: 'data.set',
          typeVersion: 1,
          name: 'Echo',
          position: { x: 220, y: 0 },
          config: {
            values: { valid: '{{ $json.schemaValid }}', violation: '{{ $json.schemaViolations[0].path }}' },
          },
        },
      ],
      edges: [
        { from: { node: 'hook', port: 'main' }, to: { node: 'echo', port: 'main' } },
      ],
    },
  });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(0).click();

  // The webhook node offers an input contract; the data.set node offers an
  // output contract. Non-trigger, non-subflow manual triggers show none
  // only for cron/manual — every other node gets the output picker.
  // The contract picker lives on the inspector's Data tab (Phase 3).
  await page.getByTestId('inspector-tab-data').click();
  const section = page.getByTestId('schema-section');
  await expect(section).toBeVisible();
  await expect(section).toContainText('Input contract');
  await page.locator('#node-schema-ref').selectOption(schema.id);
  const preview = page.getByTestId('schema-preview');
  await expect(preview).toContainText('email');
  await expect(preview).toContainText('required');

  // Saving persists the ref on the node.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const doc = await request.get(`/api/workflows/${id}`);
  const wf = (await doc.json()) as { nodes: { inputSchemaRef?: string }[] };
  expect(wf.nodes[0].inputSchemaRef).toBe(schema.id);

  // The data.set node gets an output contract too.
  await page.locator('.loom-node').nth(1).click();
  await page.getByTestId('inspector-tab-data').click();
  await expect(page.getByTestId('schema-section')).toContainText('Output contract');
  await page.locator('#node-schema-ref').selectOption(schema.id);
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();

  // Publish: the snapshot resolves the contracts inline and pins usage.
  const publish = await request.post(`/api/workflows/${id}/publish`);
  expect(publish.status(), await publish.text()).toBe(201);
  const version = await request.get(`/api/workflows/${id}/versions/1`);
  const snap = (await version.json()) as {
    nodes: { inputSchemaFields?: unknown[]; outputSchemaFields?: unknown[] }[];
  };
  expect(snap.nodes[0].inputSchemaFields).toHaveLength(2);
  expect(snap.nodes[1].outputSchemaFields).toHaveLength(2);

  // In-use contracts are guarded.
  const del = await request.delete(`/api/data-schemas/${schema.id}`);
  expect(del.status()).toBe(409);

  // Annotate mode (D11 default): a non-conforming payload still runs and
  // the verdict rides the trigger item.
  const fire = await request.post(`/hook/contract-${id}`, {
    data: { email: 7 },
    headers: { Authorization: '' },
  });
  expect(fire.status(), await fire.text()).toBe(200);
  const fired = (await fire.json()) as { runId: string };
  await page.goto(`/runs/${fired.runId}`);
  await expect(page.locator('.run-detail-page')).toBeVisible();

  // The echo node's finished event surfaces its output-contract verdict —
  // violations named with paths, and the run was never gated.
  await page.getByRole('tab', { name: 'Raw events' }).click();
  const finished = page
    .locator('.event-row', { hasText: 'node.finished' })
    .filter({ hasText: 'echo' })
    .first();
  await finished.locator('.event-row-main').click();
  const verdict = page.getByTestId('contract-verdict');
  await expect(verdict).toBeVisible({ timeout: 15000 });
  // The webhook payload {email: 7} flows through data.set's output, so the
  // contract flags email as a non-string.
  await expect(verdict).toContainText('email');
  await expect(verdict).toContainText('expected a string');
});

test('strict webhook contracts reject non-conforming requests', async ({ request }) => {
  const schema = await createSchema(request, 'e2e-strict-contract', [
    { name: 'email', type: 'string', required: true },
  ]);
  const id = `e2e-strict-wf-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id,
      name: 'Strict webhook',
      version: 1,
      nodes: [
        {
          id: 'hook',
          type: 'trigger.webhook',
          typeVersion: 1,
          name: 'Webhook',
          position: { x: 0, y: 0 },
          config: { path: `strict-${id}`, method: 'POST', strictSchema: true },
          inputSchemaRef: schema.id,
        },
      ],
      edges: [],
    },
  });
  const publish = await request.post(`/api/workflows/${id}/publish`);
  expect(publish.status(), await publish.text()).toBe(201);

  // Non-conforming: rejected at the door with the violation named.
  const bad = await request.post(`/hook/strict-${id}`, { data: { email: 7 } });
  expect(bad.status()).toBe(422);
  const badBody = (await bad.json()) as { violations: { path: string }[] };
  expect(badBody.violations[0].path).toBe('email');

  // Conforming: accepted.
  const good = await request.post(`/hook/strict-${id}`, { data: { email: 'a@b.c' } });
  expect(good.status(), await good.text()).toBe(200);
});

test('subflow shows the callee input contract in the inspector', async ({ page, request }) => {
  const schema = await createSchema(request, 'e2e-subflow-contract', [
    { name: 'orderId', type: 'string', required: true },
  ]);
  const callee = `e2e-callee-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id: callee,
      name: 'Callee',
      version: 1,
      inputSchemaRef: schema.id,
      nodes: [
        {
          id: 'start',
          type: 'trigger.manual',
          typeVersion: 1,
          name: 'Start',
          position: { x: 0, y: 0 },
          config: {},
        },
      ],
      edges: [],
    },
  });

  const caller = `e2e-caller-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id: caller,
      name: 'Caller',
      version: 1,
      nodes: [
        {
          id: 'start',
          type: 'trigger.manual',
          typeVersion: 1,
          name: 'Start',
          position: { x: 0, y: 0 },
          config: {},
        },
        {
          id: 'sub',
          type: 'flow.subflow',
          typeVersion: 1,
          name: 'Subflow',
          position: { x: 220, y: 0 },
          config: { workflowId: callee, wait: true },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'sub', port: 'main' } }],
    },
  });

  await page.goto(`/workflows/${encodeURIComponent(caller)}/edit`);
  await page.locator('.loom-node').nth(1).click();
  await page.getByTestId('inspector-tab-data').click();
  const calleeBox = page.getByTestId('callee-contract');
  await expect(calleeBox).toBeVisible();
  await expect(calleeBox).toContainText('orderId');
  await expect(calleeBox).toContainText('required');
});
