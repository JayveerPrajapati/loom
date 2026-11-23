import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, expectAssetHistoryDialog, openAssetMenu } from './helpers';

// Mappings (Configuration Hub v2, D13): reusable target←source row sets
// consumed by the data.map node. Covers the full loop: create on the Hub
// page, wire a data.map node in the editor, publish resolution + usage pin,
// and the delete guard.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function createMapping(request: APIRequestContext, name: string): Promise<string> {
  const response = await request.post('/api/mappings', {
    data: {
      name,
      rows: [
        { target: 'email', source: '{{ $json.email }}', required: true },
        { target: 'city', source: '{{ $json.city }}' },
      ],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()).id;
}

test('mappings: create through the page, wire data.map, publish resolves inline', async ({ page, request }) => {
  const mappingId = await createMapping(request, 'e2e-map-orders');

  await page.goto('/mappings');
  await expect(page.getByRole('heading', { name: 'Mappings', exact: true })).toBeVisible();
  const row = page.locator('.dashboard-run-row').filter({ hasText: 'e2e-map-orders' });
  await expect(row).toHaveCount(1);
  await expect(row.locator('.meta-list')).toContainText('2 rows');
  await expect(row.locator('.meta-list')).toContainText('v1');
  // The row preview names the mapping's shape.
  await expect(row.locator('code').first()).toContainText('email ←');

  // Seed a workflow with a data.map node pointing at the mapping.
  await request.post('/api/workflows', {
    data: {
      id: 'e2e-map-flow',
      name: 'Map flow',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'map',
          type: 'data.map',
          typeVersion: 1,
          name: 'Map data',
          config: { mappingRef: mappingId },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'map', port: 'main' } }],
    },
  });

  // The inspector previews the mapping's rows on the data.map node (the
  // preview lives on the Data tab — Phase 3).
  await page.goto('/workflows/e2e-map-flow/edit');
  await page.locator('.loom-node', { hasText: 'Map data' }).click();
  await page.getByTestId('inspector-tab-data').click();
  const preview = page.getByTestId('mapping-preview');
  await expect(preview).toBeVisible();
  await expect(preview).toContainText('email');
  await expect(preview).toContainText('required');

  const publish = await request.post('/api/workflows/e2e-map-flow/publish');
  expect(publish.status(), await publish.text()).toBe(201);
  const version = await (await request.get('/api/workflows/e2e-map-flow/versions/1')).json();
  const mapNode = version.nodes.find((n: { id: string }) => n.id === 'map');
  expect(mapNode.config.mappingRows).toHaveLength(2);
  expect(mapNode.config.mappingRef).toBe(mappingId);

  // The usage pin guards the mapping.
  const usage = await (await request.get(`/api/mappings/${mappingId}/usage`)).json();
  expect(usage.usage).toHaveLength(1);
  const deleted = await request.delete(`/api/mappings/${mappingId}`);
  expect(deleted.status()).toBe(409);

  // And the pinned usage is visible on the page.
  await page.goto('/mappings');
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Usage' }).click();
  await expect(page.locator('.mapping-detail')).toContainText('Map flow');
});

test('mappings: editor flags a dangling mappingRef before the server sees it', async ({ page, request }) => {
  // The realistic stale-list scenario: the draft was saved against a
  // mapping that has since been deleted (the server rejects new saves;
  // the editor's own validator must name the problem before that).
  const mappingId = await createMapping(request, 'e2e-map-then-delete');
  const created = await request.post('/api/workflows', {
    data: {
      id: 'e2e-map-ghost',
      name: 'Map ghost',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'map',
          type: 'data.map',
          typeVersion: 1,
          name: 'Map data',
          config: { mappingRef: mappingId },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'map', port: 'main' } }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const deleted = await request.delete(`/api/mappings/${mappingId}`);
  expect(deleted.status()).toBe(204);

  await page.goto('/workflows/e2e-map-ghost/edit');
  await expect(page.getByText(/references missing mapping/i).first()).toBeVisible({ timeout: 10000 });

  // And the server refuses to re-save the dangling draft.
  const resave = await request.put('/api/workflows/e2e-map-ghost', {
    data: {
      id: 'e2e-map-ghost',
      name: 'Map ghost',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'map',
          type: 'data.map',
          typeVersion: 1,
          name: 'Map data',
          config: { mappingRef: mappingId },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'map', port: 'main' } }],
    },
  });
  expect(resave.status()).toBe(422);
});

test('mappings: the history dialog is a wide shell with a named empty state', async ({
  page,
  request,
}) => {
  const mappingId = await createMapping(request, 'e2e-hist');

  await page.goto('/mappings');
  const row = page.locator('.dashboard-run-row').filter({ hasText: 'e2e-hist' });
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();

  const dialog = page.getByTestId('mapping-history');
  await expectAssetHistoryDialog(dialog);
  await expect(dialog).toContainText('v1');
  await dialog.getByRole('button', { name: 'Close history' }).click();
  await expect(dialog).toHaveCount(0);

  // No versions (route-intercepted): a named empty state.
  await page.route(`**/api/mappings/${mappingId}/versions*`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ versions: [], count: 0 }),
    }),
  );
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();
  await expect(page.getByTestId('mapping-history')).toContainText('No mapping versions recorded yet.');
});

test('mappings: the header holds two blocks and the toolbar owns the filter', async ({
  page,
  request,
}) => {
  const item = await createMapping(request, 'e2e-tb-item');
  const headersResponse = await request.post('/api/mappings', {
    data: {
      name: 'e2e-tb-headers',
      kind: 'headers',
      rows: [{ target: 'X-Trace', source: '{{ $json.trace }}' }],
    },
  });
  expect(headersResponse.status(), await headersResponse.text()).toBe(201);
  const headers = (await headersResponse.json()).id;
  try {
    await page.goto('/mappings');
    await expect(
      page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-item' }),
    ).toBeVisible();
    await expect(
      page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-headers' }),
    ).toBeVisible();

    // Header: title block + New mapping. The select lives in the toolbar.
    const header = page.locator('.page-head');
    expect(await header.locator(':scope > *').count()).toBe(2);
    const toolbar = page.locator('.page-toolbar');
    await expect(toolbar.getByTestId('mappings-search')).toBeVisible();
    await expect(toolbar.getByTestId('mapping-kind-filter')).toBeVisible();
    expect(await header.getByTestId('mapping-kind-filter').count()).toBe(0);

    // The filter still changes the list and syncs the URL.
    await toolbar.getByTestId('mapping-kind-filter').selectOption('headers');
    await expect(page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-headers' })).toBeVisible();
    await expect(page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-item' })).toHaveCount(0);
    expect(page.url()).toContain('kind=headers');
  } finally {
    await request.delete(`/api/mappings/${item}`);
    await request.delete(`/api/mappings/${headers}`);
  }
});

test('mapping rows render de-dotted meta values', async ({ page }) => {
  await page.goto('/mappings');
  const first = page.locator('.dashboard-run-row .meta-list').first();
  await expect(first).toBeVisible();
  const children = await first.locator(':scope > *').count();
  expect(children).toBeGreaterThanOrEqual(2);
  await expect(first).not.toContainText('·');
});

test('mappings: the editor validates rows inline and the detail rail serves every reader', async ({
  page,
  request,
}) => {
  // The kind filter still drives the list.
  await page.goto('/mappings');
  await page.getByTestId('mapping-kind-filter').selectOption('body');
  await expect(page.getByRole('heading', { name: 'Mappings', exact: true })).toBeVisible();

  // Open the editor and try to save a half-filled row.
  await page.getByRole('button', { name: 'New mapping' }).click();
  await expect(page.locator('#mapping-name')).toBeVisible();
  await page.locator('#mapping-name').fill('e2e-invalid-map');
  await page.getByLabel('Row 1 target').fill('id');
  const created = page.waitForRequest(
    (request_) => request_.method() === 'POST' && request_.url().includes('/api/mappings'),
    { timeout: 2000 },
  ).catch(() => null);
  await page.getByTestId('asset-editor-save').click();
  await expect(page.getByTestId('asset-editor-error')).toContainText(
    'Row 1 needs both target and source.',
  );
  expect(await created).toBeNull();

  // The form is padded like every editor rail.
  const padded = await page.locator('#mapping-form').evaluate((element) => {
    const style = window.getComputedStyle(element);
    return Number.parseFloat(style.paddingTop) + Number.parseFloat(style.paddingBottom);
  });
  expect(padded).toBeGreaterThan(0);

  // Cancel closes the editor; the row keeps its read-only detail.
  await page.getByTestId('asset-editor-cancel').click();
  await expect(page.getByTestId('asset-editor')).toHaveCount(0);

  const name = `e2e-map-detail-${Date.now()}`;
  await request.post('/api/mappings', {
    data: {
      name,
      rows: [
        { target: 'email', source: '{{ $json.email }}', required: true },
        { target: 'name', source: '{{ $json.name }}' },
      ],
    },
  });
  await page.goto('/mappings');
  const row = page.locator('.dashboard-run-row').filter({ hasText: name });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: 'View' }).click();
  const detail = page.getByTestId('mapping-detail');
  await expect(detail).toBeVisible();
  // Every row is present, not just the four-row preview.
  await expect(detail.getByTestId('mapping-view-rows')).toContainText('email ←');
  await expect(detail.getByTestId('mapping-view-rows')).toContainText('name ←');
  await expect(detail).toContainText('v1');
});

// U6: a failed usage lookup is a named error with Retry — the detail rail
// must never read "no usage" when the read failed.
test('mappings: a failed usage lookup is an error with Retry, not "no usage"', async ({
  page,
  request,
}) => {
  const name = `e2e-map-usage-${Date.now()}`;
  const mappingId = await createMapping(request, name);

  const route = `**/api/mappings/${mappingId}/usage`;
  await page.route(route, (intercepted) =>
    intercepted.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'usage boom' }),
    }),
  );
  try {
    await page.goto('/mappings');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'View' }).click();
    const detail = page.getByTestId('mapping-detail');
    const failure = detail.getByTestId('mapping-usage-error');
    await expect(failure).toBeVisible();
    await expect(failure).toContainText('Could not load');
    await expect(
      detail.getByText('No published workflow or profile bundle references this mapping yet.'),
    ).toHaveCount(0);

    // A retry that succeeds replaces the error with the real empty state.
    await page.unroute(route);
    await detail.getByTestId('mapping-usage-error-retry').click();
    await expect(failure).toHaveCount(0);
    await expect(
      detail.getByText('No published workflow or profile bundle references this mapping yet.'),
    ).toBeVisible();
  } finally {
    await page.unroute(route);
  }
});
