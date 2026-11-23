import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Phase 6 D6-4: publishing a data.map node whose mapping and output
// contract disagree records named, non-blocking warnings in the publish
// response — the publish stands and the editor surfaces them.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function seed(request: APIRequestContext, workflow: object) {
  const response = await request.post('/api/workflows', { data: workflow });
  expect(response.status(), await response.text()).toBe(201);
}

async function openWorkflow(page: Page, id: string) {
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
}

test('publish records mapping-contract warnings without blocking', async ({ page, request }) => {
  const stamp = Date.now();

  // The mapping produces one target ("shaped") that the output contract
  // does not know, and produces nothing for the contract's required
  // "vip" field.
  const mapping = await request.post('/api/mappings', {
    data: {
      name: `e2e-mw-map-${stamp}`,
      rows: [{ target: 'shaped', source: 'leadEmail' }],
    },
  });
  expect(mapping.status(), await mapping.text()).toBe(201);
  const mappingId = (await mapping.json()) as { id: string };

  const schema = await request.post('/api/data-schemas', {
    data: {
      name: `e2e-mw-out-${stamp}`,
      fields: [
        { name: 'vip', type: 'boolean', required: true },
        { name: 'note', type: 'string' },
      ],
    },
  });
  expect(schema.status(), await schema.text()).toBe(201);
  const schemaId = (await schema.json()) as { id: string };

  const id = `e2e-mw-${stamp}`;
  await seed(request, {
    id,
    name: 'E2E mapping warnings',
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
        id: 'shape',
        type: 'data.map',
        typeVersion: 1,
        name: 'Shape',
        position: { x: 220, y: 0 },
        config: { mappingRef: mappingId.id },
        outputSchemaRef: schemaId.id,
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  });

  await openWorkflow(page, id);
  await page.getByRole('button', { name: 'Publish', exact: true }).click();

  // The publish stands: v1 is out.
  await expect(page.locator('.version-chip')).toHaveText('v1');

  // Both disagreements are named in the warning banner.
  const banner = page.getByTestId('publish-mapping-warnings');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('Shape');
  await expect(banner).toContainText('mapping target "shaped" is not a field of the output contract');
  await expect(banner).toContainText('required output contract field "vip" is not produced');

  // The toast reports the count alongside the normal publish notice.
  await expect(page.locator('.toast')).toContainText('mapping warnings');

  // Dismiss removes the banner; a fresh publish with the same drift
  // brings it back (the toast alone never carries the detail).
  await banner.getByRole('button', { name: 'Dismiss' }).click();
  await expect(banner).toHaveCount(0);
  await page.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(page.locator('.version-chip')).toHaveText('v2');
  await expect(banner).toBeVisible();
});
