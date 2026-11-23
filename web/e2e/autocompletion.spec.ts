import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Shape-aware expression pills (V5, D15): when the upstream node declares
// an output contract, its fields become insert candidates — ahead of
// anything observed in a run.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('expression pills surface the upstream contract fields first', async ({ page, request }) => {
  const schema = await request.post('/api/data-schemas', {
    data: {
      name: 'e2e-pill-shape',
      fields: [
        { name: 'email', type: 'string', required: true },
        { name: 'customer', type: 'object', fields: [{ name: 'name', type: 'string' }] },
      ],
    },
  });
  expect(schema.status(), await schema.text()).toBe(201);
  const schemaId = (await schema.json()).id;

  await request.post('/api/workflows', {
    data: {
      id: 'e2e-pill-flow',
      name: 'Pill flow',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'source',
          type: 'data.set',
          typeVersion: 1,
          name: 'Source',
          config: { values: { email: 'a@b.c' } },
          outputSchemaRef: schemaId,
        },
        {
          id: 'sink',
          type: 'text.template',
          typeVersion: 1,
          name: 'Sink',
          config: { template: 'hello' },
        },
      ],
      edges: [
        { from: { node: 'start', port: 'main' }, to: { node: 'source', port: 'main' } },
        { from: { node: 'source', port: 'main' }, to: { node: 'sink', port: 'main' } },
      ],
    },
  });

  await page.goto('/workflows/e2e-pill-flow/edit');
  // Focusing the template string field opens the pill row.
  const valuesField = page.getByRole('textbox', { name: /Template/ });
  await page.locator('.loom-node', { hasText: 'Sink' }).click();
  await valuesField.click();
  const pills = page.getByTestId('expression-pills');
  await expect(pills).toBeVisible();
  const contractPills = pills.locator('[data-source="contract"]');
  await expect(contractPills.first()).toHaveText('$json.email');
  await expect(contractPills).toHaveCount(3); // email, customer, customer.name
  // Static hints ride along at the end.
  await expect(pills.locator('[data-source="static"]').last()).toHaveText('$env');

  // Clicking a pill inserts the wrapped expression into the focused field.
  await contractPills.first().click();
  await expect(valuesField).toHaveValue(/\{\{ \$json\.email \}\}/);
});
