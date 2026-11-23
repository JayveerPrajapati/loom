import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, expectAssetHistoryDialog, openAssetMenu } from './helpers';

// Data contracts (Configuration Hub v2, Phase V1): management page CRUD,
// the recursive field editor, client-side contract validation, version
// history and the sample tester against the production validator.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('data contracts: create, edit, history, duplicate, delete', async ({ page }) => {
  await page.goto('/schemas');
  await expect(page.getByRole('heading', { name: 'Data contracts', exact: true })).toBeVisible();

  // Create a contract through the recursive field editor: two scalars,
  // one object with a nested field, one array of strings.
  await page.getByRole('button', { name: 'New contract' }).click();
  await expect(page.locator('#schema-name')).toBeVisible();
  await page.locator('#schema-name').fill('Inbound lead');
  await page.locator('#schema-description').fill('Webhook lead payload');
  await page.locator('#schema-owner').fill('growth-team');
  await page.locator('#schema-tags').fill('webhook, leads');

  await page.getByRole('button', { name: 'Add field' }).click();
  await page.getByLabel('Field 1 name').fill('email');
  await page.getByLabel('Field 1 type').selectOption('string');
  await page.getByLabel('Field 1 required').check();

  // With no object fields yet, the first Add field button is top-level.
  await page.getByRole('button', { name: 'Add field' }).click();
  await page.getByLabel('Field 2 name').fill('name');
  await page.getByLabel('Field 2 type').selectOption('string');

  await page.getByRole('button', { name: 'Add field' }).click();
  await page.getByLabel('Field 3 name').fill('address');
  await page.getByLabel('Field 3 type').selectOption('object');
  // The nested editor inside field 3 renders its own Add field first.
  await page.getByRole('button', { name: 'Add field' }).first().click();
  await page.getByLabel('Field 1 name', { exact: true }).last().fill('city');
  await page.getByLabel('Field 1 type', { exact: true }).last().selectOption('string');

  // The top-level Add field is the last one in the DOM now.
  await page.getByRole('button', { name: 'Add field' }).last().click();
  await page.getByLabel('Field 4 name').fill('tags');
  await page.getByLabel('Field 4 type').selectOption('array');
  await page.getByLabel('Field 1 name', { exact: true }).last().fill('item');

  await page.getByRole('button', { name: 'Save contract' }).click();
  await expect(page.getByText('Created Inbound lead.')).toBeVisible();

  const row = page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead' });
  await expect(row).toHaveCount(1);
  await expect(row.locator('.meta-list')).toContainText('5 fields');
  await expect(row.locator('.meta-list')).toContainText('v1');
  await expect(row).toContainText('growth-team');
  await expect(row.locator('.profile-tags em')).toHaveCount(2);

  // Edit: rename and save as v2.
  await row.getByRole('button', { name: 'Edit' }).click();
  await expect(page.getByRole('heading', { name: 'Edit Inbound lead' })).toBeVisible();
  await page.locator('#schema-name').fill('Inbound lead v2');
  await page.getByRole('button', { name: 'Save as v2' }).click();
  await expect(page.getByText('Saved Inbound lead v2 as v2.')).toBeVisible();
  await expect(
    page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead v2' }),
  ).toContainText('v2');

  // History dialog lists both versions with its actors.
  const v2row = page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead v2' });
  await openAssetMenu(v2row);
  await v2row
    .getByRole('button', { name: 'History' })
    .click();
  const history = page.getByTestId('schema-history-backdrop');
  await expect(history).toBeVisible();
  // The history dialog is a wide shell: close pinned top-right in the
  // head, body scrolls without horizontal clipping.
  await expectAssetHistoryDialog(history.locator('.dialog'));
  await expect(history.getByTestId('schema-history-body').locator('.schema-history-row')).toHaveCount(2);
  await expect(history.locator('.schema-history-row').first()).toContainText('v2');
  // The dialog closes cleanly (state asserted at each step).
  await history.getByRole('button', { name: 'Close history' }).click();
  await expect(history).toHaveCount(0);

  // Duplicate, then delete both copies.
  await openAssetMenu(v2row);
  await v2row
    .getByRole('button', { name: 'Duplicate data contract Inbound lead v2' })
    .click();
  await expect(page.getByText(/Duplicated Inbound lead v2 as .+\./)).toBeVisible();
  await expect(
    page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead v2 (copy)' }),
  ).toHaveCount(1);

  const copyRow = page
    .locator('.dashboard-run-row')
    .filter({ hasText: 'Inbound lead v2 (copy)' });
  await openAssetMenu(copyRow);
  await copyRow
    .getByRole('button', { name: 'Delete data contract Inbound lead v2 (copy)' })
    .click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(
    page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead v2 (copy)' }),
  ).toHaveCount(0);

  await openAssetMenu(v2row);
  await v2row
    .getByRole('button', { name: 'Delete data contract Inbound lead v2' })
    .click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead v2' })).toHaveCount(0);
  // Scoped by name: other specs legitimately leave in-use contracts behind,
  // so the list-level empty state only holds on a fresh server.
  await expect(page.locator('.dashboard-run-row').filter({ hasText: 'Inbound lead' })).toHaveCount(0);
});

test('data contracts: the editor validates before the server sees anything', async ({ page }) => {
  await page.goto('/schemas');
  await page.getByRole('button', { name: 'New contract' }).click();
  await expect(page.locator('#schema-name')).toBeVisible();
  await page.locator('#schema-name').fill('Broken');
  await page.getByRole('button', { name: 'Add field' }).click();
  await page.getByLabel('Field 1 name').fill('bad-name');
  await page.getByRole('button', { name: 'Save contract' }).click();
  await expect(page.locator('.banner.error')).toContainText(
    'field names may only use letters, digits and underscores',
  );
  await expect(page.locator('.dashboard-run-row').filter({ hasText: 'Broken' })).toHaveCount(0);

  // Object fields need nested content; duplicates are named per level.
  await page.getByLabel('Field 1 name').fill('x');
  await page.getByLabel('Field 1 type').selectOption('object');
  await page.getByRole('button', { name: 'Save contract' }).click();
  await expect(page.locator('.banner.error')).toContainText(
    'object fields need at least one nested field',
  );
});

test('data contracts: the sample tester names violations with paths', async ({ page, request }) => {
  // Seed a contract through the API (the form path is covered above).
  await request.post('/api/data-schemas', {
    data: {
      name: 'Order contract',
      fields: [
        { name: 'email', type: 'string', required: true },
        { name: 'vip', type: 'boolean' },
        {
          name: 'address',
          type: 'object',
          fields: [{ name: 'city', type: 'string', required: true }],
        },
        { name: 'tags', type: 'array', items: { name: 'item', type: 'string' } },
      ],
    },
  });
  await page.goto('/schemas');

  const row = page.locator('.dashboard-run-row').filter({ hasText: 'Order contract' });
  await row.getByRole('button', { name: 'Edit' }).click();

  // A broken sample: every violation is named with its path.
  await page.locator('#schema-sample').fill(
    JSON.stringify({ email: 7, vip: 'yes', address: { city: 9 }, tags: ['ok', 3] }),
  );
  await page.getByRole('button', { name: 'Test sample' }).click();
  const result = page.getByTestId('schema-test-result');
  await expect(result).toBeVisible();
  for (const path of ['email', 'vip', 'address.city', 'tags[1]']) {
    await expect(result.getByText(path)).toBeVisible();
  }

  // A conforming sample passes.
  await page.locator('#schema-sample').fill(
    JSON.stringify({ email: 'a@b.c', vip: true, address: { city: 'london' }, tags: ['x'] }),
  );
  await page.getByRole('button', { name: 'Test sample' }).click();
  await expect(page.getByText('The sample conforms to the contract.')).toBeVisible();
});

test('data contracts: share and make-private surface visibility', async ({ page }) => {
  await page.goto('/schemas');
  await expect(page.getByRole('heading', { name: 'Data contracts', exact: true })).toBeVisible();

  // Create a minimal private contract, then flip it to shared and back.
  await page.getByRole('button', { name: 'New contract' }).click();
  await expect(page.locator('#schema-name')).toBeVisible();
  await page.locator('#schema-name').fill('Share probe');
  await page.getByRole('button', { name: 'Add field' }).click();
  await page.getByLabel('Field 1 name').fill('id');
  await page.getByLabel('Field 1 type').selectOption('string');
  await page.getByRole('button', { name: 'Save contract' }).click();

  const row = page.locator('.dashboard-run-row').filter({ hasText: 'Share probe' });
  await expect(row).toBeVisible();
  await expect(row.locator('.chip-private')).toBeVisible();

  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Share data contract Share probe' }).click();
  await expect(row.locator('.chip-shared')).toBeVisible();

  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Make data contract Share probe private' }).click();
  await expect(row.locator('.chip-private')).toBeVisible();

  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Delete data contract Share probe' }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(row).toHaveCount(0);
});

test('a stale data contract save names the conflict', async ({ page, request }) => {
  const stamp = Date.now();
  const name = `e2e-stale-schema-${stamp}`;
  const created = await request.post('/api/data-schemas', {
    data: {
      name,
      fields: [{ name: 'id', type: 'string', required: true }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const schema = (await created.json()) as {
    id: string;
    version: number;
    fields: { name: string; type: string; required?: boolean }[];
  };
  try {
    await page.goto('/schemas');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'Edit' }).click();
    await expect(page.locator('#schema-name')).toHaveValue(name);

    // Move the server version after the editor loaded; the UI is still
    // based on v1 and must surface the 409 in the editor.
    const bumped = await request.put(`/api/data-schemas/${schema.id}`, {
      data: { name, fields: schema.fields, version: schema.version },
    });
    expect(bumped.status(), await bumped.text()).toBe(200);

    await page.getByRole('button', { name: 'Save as v2' }).click();
    const error = page.getByTestId('asset-editor-error');
    await expect(error).toBeVisible();
    await expect(error).toContainText('modified concurrently');
  } finally {
    await request.delete(`/api/data-schemas/${schema.id}`);
  }
});

test('data contract rows render de-dotted meta values', async ({ page }) => {
  await page.goto('/schemas');
  const first = page.locator('.dashboard-run-row .meta-list').first();
  await expect(first).toBeVisible();
  const children = await first.locator(':scope > *').count();
  expect(children).toBeGreaterThanOrEqual(2);
  await expect(first).not.toContainText('·');
});

test('data contracts: the detail rail serves every reader and the writer continues to the editor', async ({
  page,
  request,
}) => {
  const name = `e2e-detail-${Date.now()}`;
  await request.post('/api/data-schemas', {
    data: {
      name,
      owner: 'growth-team',
      fields: [
        { name: 'email', type: 'string', required: true, description: 'Lead email' },
        { name: 'address', type: 'object', fields: [{ name: 'city', type: 'string' }] },
      ],
    },
  });
  await page.goto('/schemas');
  const row = page.locator('.dashboard-run-row').filter({ hasText: name });
  await expect(row).toBeVisible();

  // View opens the read-only rail with the full field tree.
  await row.getByRole('button', { name: 'View' }).click();
  const detail = page.getByTestId('schema-detail');
  await expect(detail).toBeVisible();
  const viewFields = detail.getByTestId('schema-view-fields').first();
  await expect(viewFields).toContainText('email');
  await expect(viewFields).toContainText('address');
  await expect(viewFields).toContainText('city');
  await expect(detail.locator('.schema-view-header')).toContainText('Name');
  await expect(detail.locator('.schema-view-header')).toContainText('Type');
  await expect(detail).toContainText('No published workflow or profile bundle references this contract yet.');

  // The rail keeps its own width band and does not overflow.
  const rail = await detail.evaluate((element) => ({
    width: element.getBoundingClientRect().width,
    overflow: element.scrollWidth - element.clientWidth,
  }));
  expect(rail.width).toBeGreaterThanOrEqual(420);
  expect(rail.width).toBeLessThanOrEqual(480);
  expect(rail.overflow).toBeLessThanOrEqual(1);

  // The editor's field rows stay inside the rail without a horizontal
  // scrollbar, and carry visible column labels.
  await detail.getByRole('button', { name: 'Edit' }).click();
  await expect(page.locator('#schema-name')).toHaveValue(name);
  const fieldRow = page.locator('.schema-field-row').first();
  await expect(fieldRow).toBeVisible();
  const fieldGeometry = await fieldRow.evaluate((element) => ({
    overflow: element.scrollWidth - element.clientWidth,
  }));
  expect(fieldGeometry.overflow).toBeLessThanOrEqual(1);
  const header = page.locator('.schema-field-header');
  await expect(header).toContainText('Name');
  await expect(header).toContainText('Type');
  await expect(header).toContainText('Required');

  // The writer can save from the detail-opened editor.
  await page.locator('#schema-name').fill(`${name} v2`);
  await page.getByRole('button', { name: 'Save as v2' }).click();
  await expect(page.getByText(`Saved ${name} v2 as v2.`)).toBeVisible();
  await expect(page.locator('.dashboard-run-row').filter({ hasText: `${name} v2` })).toBeVisible();
});

// U6: a failed usage lookup is a named error with Retry — the detail rail
// must never read "no usage" when the read failed.
test('data contracts: a failed usage lookup is an error with Retry, not "no usage"', async ({
  page,
  request,
}) => {
  const name = `e2e-schema-usage-${Date.now()}`;
  const created = await request.post('/api/data-schemas', {
    data: {
      name,
      fields: [{ name: 'email', type: 'string', required: true }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const schemaId = ((await created.json()) as { id: string }).id;

  const route = `**/api/data-schemas/${schemaId}/usage`;
  await page.route(route, (intercepted) =>
    intercepted.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'usage boom' }),
    }),
  );
  try {
    await page.goto('/schemas');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'View' }).click();
    const detail = page.getByTestId('schema-detail');
    const failure = detail.getByTestId('schema-usage-error');
    await expect(failure).toBeVisible();
    await expect(failure).toContainText('Could not load');
    await expect(
      detail.getByText('No published workflow or profile bundle references this contract yet.'),
    ).toHaveCount(0);

    // A retry that succeeds replaces the error with the real empty state.
    await page.unroute(route);
    await detail.getByTestId('schema-usage-error-retry').click();
    await expect(failure).toHaveCount(0);
    await expect(
      detail.getByText('No published workflow or profile bundle references this contract yet.'),
    ).toBeVisible();
  } finally {
    await page.unroute(route);
  }
});
