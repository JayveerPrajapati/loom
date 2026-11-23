import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, expectAssetHistoryDialog, openAssetMenu } from './helpers';

// Operational-trust Campaign 4 (connector SDK): black-box verification
// of the connector authoring surface and the node-types merge. The sim
// host never dials out; template rendering and the delegation into the
// trusted HTTP pipeline are pinned by the Go suite
// (internal/nodes/connector_test.go, internal/server/connectors_test.go).

test.beforeEach(async ({ request, context }) => {
  await login(request);
  await login(context.request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request, 'connector-ui');
  // The connector itself must go too: its node types otherwise leak
  // into the node-types listing the field-visibility freeze spec walks.
  const types = (await (
    await request.get('/api/node-types')
  ).json()) as { nodeTypes: Array<{ type: string; category: string }> };
  for (const entry of types.nodeTypes) {
    if (entry.category === 'connector') {
      const connectorId = entry.type.split('.')[1];
      await request.delete(`/api/connectors/${connectorId}`).catch(() => undefined);
    }
  }
});

test('a connector is authored, versioned and listed with its node types', async ({ page, request }) => {
  await page.goto('/connectors');
  await expect(page.getByTestId('connectors-empty')).toBeVisible();

  await page.getByRole('button', { name: 'New connector' }).click();
  await expect(page.getByTestId('connector-editor')).toBeVisible();
  // The shared editor shell exposes the same save/cancel/status slots as
  // every asset editor; the status line is honest — clean on open, dirty
  // after the first edit.
  await expect(page.getByTestId('asset-editor-save')).toBeVisible();
  await expect(page.getByTestId('asset-editor-cancel')).toBeVisible();
  await expect(page.getByTestId('asset-editor-status')).toHaveText('All changes saved');
  await page.locator('#connector-name').fill('Example API');
  await expect(page.getByTestId('asset-editor-status')).toHaveText('Unsaved changes');
  // Advanced templates stay in the JSON escape hatch.
  await page.getByRole('button', { name: 'JSON', exact: true }).click();
  const editor = page.getByTestId('connector-operations-json');
  await editor.fill(
    JSON.stringify(
      [
        {
          op: 'list',
          label: 'List things',
          method: 'GET',
          url: '{{cred.baseUrl}}/things',
          successCodes: '200,2xx',
          fields: { limit: { type: 'number', required: false } },
        },
      ],
      null,
      2,
    ),
  );
  await page.getByRole('button', { name: 'Save connector' }).click();
  await expect(page.getByTestId('connector-row')).toHaveCount(1);
  const row = page.getByTestId('connector-row').first();
  await expect(row).toContainText('Example API');

  // The node-types listing carries the connector type with its category.
  const types = (await (
    await request.get('/api/node-types')
  ).json()) as { nodeTypes: Array<{ type: string; category: string }> };
  const entry = types.nodeTypes.find((t) => t.category === 'connector');
  expect(entry).toBeDefined();
  expect(entry?.type).toMatch(/^connector\..+\.list$/);

  // Deleting is guarded while a workflow pins it (via publish usage).
  const id = `e2e-connector-ui-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id,
      name: `Connector UI ${id}`,
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1 },
        {
          id: 'n',
          type: entry?.type,
          typeVersion: 1,
          config: { limit: 5 },
        },
      ],
      edges: [
        { from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } },
      ],
    },
  });
  const published = await request.post(`/api/workflows/${id}/publish`, { data: {} });
  expect(published.ok()).toBeTruthy();
  const body = (await published.json()) as {
    workflow: { nodes: Array<{ config: Record<string, unknown> }> };
  };
  const connectorNode = body.workflow.nodes.find((n) => n.config?.request);
  // Publish inlines the request template so replays are self-contained.
  expect(connectorNode?.config.request).toMatchObject({
    method: 'GET',
    url: '{{cred.baseUrl}}/things',
  });

  // The usage pin guards the connector: delete must now fail.
  const connectorId = entry?.type.split('.')[1] ?? '';
  const del = await request.delete(`/api/connectors/${connectorId}`);
  expect(del.ok()).toBeFalsy();
});

test('a connector can be duplicated as a private copy', async ({ page, request }) => {
  const id = `connector-dup-${Date.now()}`;
  const created = await request.post('/api/connectors', {
    data: {
      id,
      name: 'Duplicate source',
      description: 'Copy me',
      operations: [{ op: 'list', method: 'GET', url: '{{cred.baseUrl}}/things' }],
      tags: ['copy-source'],
    },
  });
  expect(created.status(), await created.text()).toBe(200);

  await page.goto('/connectors');
  const row = page.getByTestId('connector-row').filter({ hasText: 'Duplicate source' });
  await expect(row).toHaveCount(1);
  await openAssetMenu(row);
  await page.getByTestId('asset-action-duplicate').click();

  const copyRow = page
    .getByTestId('connector-row')
    .filter({ hasText: 'Duplicate source (copy)' });
  await expect(copyRow).toHaveCount(1);
  await expect(copyRow).toContainText('1 operation');
  await expect(copyRow).toContainText('Private');

  const list = (await (await request.get('/api/connectors')).json()) as {
    connectors: Array<{ id: string; name: string; visibility: string }>;
  };
  const copy = list.connectors.find((item) => item.name === 'Duplicate source (copy)');
  expect(copy).toBeDefined();
  expect(copy?.id).not.toBe(id);
  expect(copy?.visibility).toBe('private');
});

test('invalid operations are rejected inline in the editor', async ({ page }) => {
  await page.goto('/connectors');
  await page.getByRole('button', { name: 'New connector' }).click();
  await page.locator('#connector-name').fill('Broken');
  await page.getByRole('button', { name: 'JSON', exact: true }).click();
  await page.getByTestId('connector-operations-json').fill('{"not":"an array"}');
  await page.getByRole('button', { name: 'Save connector' }).click();
  // Saving from JSON mode parses first: the invalid document is named
  // inline in the editor's error slot and the connector is not created.
  await expect(page.getByTestId('asset-editor-error')).toContainText('Operations JSON');
});

test('the structured card editor authors operations and share/history surface', async ({
  page,
  request,
}) => {
  await page.goto('/connectors');
  await expect(page.getByTestId('connectors-empty')).toBeVisible();

  await page.getByRole('button', { name: 'New connector' }).click();
  await page.locator('#connector-name').fill('Cards API');
  const card = page.getByTestId('connector-op-0');
  await card.locator('#op-0-op').fill('fetch');
  await card.locator('#op-0-label').fill('Fetch things');
  await card.locator('#op-0-url').fill('https://api.example.com/things');
  await card.getByRole('button', { name: '+ Add field' }).click();
  await page.locator('[data-testid="connector-field-row-field1"]').locator('input').first().fill('limit');
  await page.getByRole('button', { name: 'Save connector' }).click();
  await expect(page.getByTestId('connector-row')).toHaveCount(1);

  // The row shows the operation chip; new connectors start private.
  const row = page.getByTestId('connector-row').first();
  await expect(row.locator('.chip', { hasText: 'fetch' })).toBeVisible();
  await expect(row.locator('.chip-private')).toBeVisible();

  // Share flips to shared and back — both live in the kebab.
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Share', exact: true }).click();
  await expect(row.locator('.chip-shared')).toBeVisible();
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Make private' }).click();
  await expect(row.locator('.chip-private')).toBeVisible();
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'Share', exact: true }).click();
  await expect(row.locator('.chip-shared')).toBeVisible();

  // History lists every version — the create plus the share/unshare
  // governance flips. Version rows are ordered ascending; v1 is first.
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();
  const history = page.getByTestId('connector-history-backdrop');
  await expect(history).toBeVisible();
  // The history dialog is a wide shell: close pinned top-right in the
  // head, body scrolls without horizontal clipping.
  await expectAssetHistoryDialog(history.locator('.dialog'));
  const versionRows = history.locator('tbody tr');
  await expect(versionRows.first()).toBeVisible();
  await expect(versionRows.first()).toContainText('v1');
  await expect(versionRows).not.toHaveCount(0);
  const headers = history.getByRole('columnheader');
  await expect(headers).toHaveCount(4);
  for (const header of await headers.all()) {
    await expect(header).toHaveAttribute('scope', 'col');
  }
  await history.getByRole('button', { name: 'Close history' }).click();
  await expect(history).toBeHidden();

  // Usage lives in the read-only detail: every operation plus the
  // pinned-workflow list.
  await row.getByRole('button', { name: 'View', exact: true }).click();
  const detail = page.getByTestId('connector-detail');
  await expect(detail).toBeVisible();
  await expect(detail.getByTestId('connector-view-ops')).toContainText('fetch');
  await expect(detail).toContainText('No published workflows pin this connector.');
  await detail.getByRole('button', { name: 'Close detail' }).click();
  await expect(detail).toHaveCount(0);

  // Cleanup: the connector was authored through the UI.
  const types = (await (
    await request.get('/api/node-types')
  ).json()) as { nodeTypes: Array<{ type: string }> };
  const entry = types.nodeTypes.find((t) => t.type.startsWith('connector.'));
  if (entry) {
    await request.delete(`/api/connectors/${entry.type.split('.')[1]}`).catch(() => undefined);
  }
});

test('the list spans the workspace while the editor is closed, and opens on demand', async ({
  page,
}) => {
  await page.goto('/connectors');
  // Wait for render: the width check reads the live DOM.
  await expect(page.getByTestId('connectors-empty')).toBeVisible();

  // Closed editor: the list panel carries the full workspace width
  // instead of the old permanently reserved right column.
  const widths = await page.evaluate(() => {
    const grid = document.querySelector('.dashboard-grid');
    const list = document.querySelector('.dashboard-grid > .dashboard-panel');
    if (!grid || !list) return null;
    return {
      list: list.getBoundingClientRect().width,
      grid: grid.getBoundingClientRect().width,
    };
  });
  expect(widths).not.toBeNull();
  expect(widths!.list).toBeGreaterThanOrEqual(widths!.grid * 0.9);

  // The empty-state CTA opens the editor (it used to focus an input
  // that did not exist while the editor was closed).
  await page.getByRole('button', { name: 'Create the first connector' }).click();
  await expect(page.getByTestId('connector-editor')).toBeVisible();
});

test('operation config fields carry a visible column header', async ({ page }) => {
  await page.goto('/connectors');
  await page.getByRole('button', { name: 'New connector' }).click();
  await expect(page.getByTestId('connector-editor')).toBeVisible();

  const card = page.getByTestId('connector-op-0');
  // No header before any field exists.
  await expect(page.getByTestId('connector-field-header')).toHaveCount(0);

  await card.getByRole('button', { name: '+ Add field' }).click();
  const header = page.getByTestId('connector-field-header');
  await expect(header).toBeVisible();
  await expect(header).toContainText('Name');
  await expect(header).toContainText('Type');
  await expect(header).toContainText('Required');

  // The header tracks the same columns as the field rows.
  const aligned = await page.evaluate(() => {
    const header = document.querySelector('[data-testid="connector-field-header"]');
    const row = document.querySelector('.connector-field-row');
    if (!header || !row) return null;
    return {
      header: header.getBoundingClientRect().left,
      row: row.getBoundingClientRect().left,
    };
  });
  expect(aligned).not.toBeNull();
  expect(aligned!.header).toBeCloseTo(aligned!.row, 0);
});

test('an invalid save is named in the editor error slot and issues no create', async ({ page }) => {
  await page.goto('/connectors');
  await page.getByRole('button', { name: 'New connector' }).click();
  await page.locator('#connector-name').fill('NoOps');
  // JSON mode reaches the shared validator without the browser's own
  // required checks on the card fields pre-empting it.
  await page.getByRole('button', { name: 'JSON', exact: true }).click();
  await page.getByTestId('connector-operations-json').fill(
    JSON.stringify([
      { op: '', label: 'Broken', method: 'GET', url: 'https://api.example.com', fields: {} },
    ]),
  );

  let writes = 0;
  page.on('request', (request) => {
    if (/\/api\/connectors/.test(request.url()) && request.method() !== 'GET') writes += 1;
  });

  await page.getByRole('button', { name: 'Save connector' }).click();
  await expect(page.getByTestId('asset-editor-error')).toContainText('operation needs');
  expect(writes).toBe(0);
});

test('the history dialog names its empty state instead of loading forever', async ({ page, request }) => {
  const created = await request.post('/api/connectors', {
    data: {
      name: 'e2e-hist-empty',
      operations: [
        { op: 'list', label: 'List', method: 'GET', url: 'https://api.example.com', fields: {} },
      ],
      visibility: 'private',
    },
  });
  expect(created.ok()).toBeTruthy();
  const { connector } = (await created.json()) as { connector: { id: string } };

  // No versions (route-intercepted): the named empty state, not the
  // permanent loading line the old error path showed.
  await page.route(`**/api/connectors/${connector.id}/versions*`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ versions: [] }),
    }),
  );
  await page.goto('/connectors');
  const row = page.getByTestId('connector-row').filter({ hasText: 'e2e-hist-empty' });
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();
  const history = page.getByTestId('connector-history-backdrop');
  await expect(history).toBeVisible();
  await expect(history.locator('.dialog-body')).toContainText('No connector versions recorded yet.');
  await history.getByRole('button', { name: 'Close history' }).click();
  await expect(history).toBeHidden();

  // A failed load is named, and the retry recovers once the route is
  // released — the connector's real v1 loads.
  await page.route(`**/api/connectors/${connector.id}/versions*`, (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'versions unavailable' }),
    }),
  );
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();
  await expect(history.locator('.dialog-body')).toContainText('versions unavailable');
  await page.unroute(`**/api/connectors/${connector.id}/versions*`);
  await history.getByRole('button', { name: 'Try again' }).click();
  await expect(history.locator('tbody tr')).toHaveCount(1);
});

test('the detail rail serves every reader and the writer returns to it after an edit', async ({
  page,
  request,
}) => {
  const created = await request.post('/api/connectors', {
    data: {
      name: 'e2e-detail-conn',
      operations: [
        { op: 'list', label: 'List', method: 'GET', url: 'https://api.example.com', fields: {} },
      ],
      visibility: 'private',
    },
  });
  expect(created.ok()).toBeTruthy();
  const { connector } = (await created.json()) as { connector: { id: string } };

  await page.goto('/connectors');
  const row = page.getByTestId('connector-row').filter({ hasText: 'e2e-detail-conn' });
  await expect(row).toBeVisible();

  // View opens the read-only rail with the operation and its fields.
  await row.getByRole('button', { name: 'View', exact: true }).click();
  const detail = page.getByTestId('connector-detail');
  await expect(detail).toBeVisible();
  await expect(detail.getByTestId('connector-view-ops')).toContainText('list');
  await expect(detail.getByTestId('connector-view-ops')).toContainText('GET');
  await expect(detail.getByTestId('connector-view-ops')).toContainText('https://api.example.com');

  // A writer edits from the detail, saves, and lands back on the
  // refreshed detail (new version).
  await detail.getByRole('button', { name: 'Edit', exact: true }).click();
  const card = page.getByTestId('connector-op-0');
  await card.locator('#op-0-url').fill('https://api.example.com/v2');
  await page.getByRole('button', { name: 'Save connector' }).click();
  await expect(detail).toBeVisible();
  await expect(detail.getByTestId('connector-view-ops')).toContainText('https://api.example.com/v2');
  await expect(detail).toContainText('v2');

  // Cancel returns to the detail too, without saving.
  await detail.getByRole('button', { name: 'Edit', exact: true }).click();
  await card.locator('#op-0-url').fill('https://cancelled.example.com');
  await page.getByTestId('asset-editor-cancel').click();
  await expect(detail).toBeVisible();
  await expect(detail.getByTestId('connector-view-ops')).toContainText('https://api.example.com/v2');

  await detail.getByRole('button', { name: 'Close detail' }).click();
});

test('invalid JSON is validated live with a line and column, and blocks the save', async ({
  page,
}) => {
  await page.goto('/connectors');
  await page.getByRole('button', { name: 'New connector' }).click();
  await page.locator('#connector-name').fill('Broken live');
  await page.getByRole('button', { name: 'JSON', exact: true }).click();

  let writes = 0;
  page.on('request', (request) => {
    if (/\/api\/connectors/.test(request.url()) && request.method() !== 'GET') writes += 1;
  });

  // A broken document is named as soon as it is typed, with the parse
  // position translated into a line and column; the textarea keeps
  // the author's content.
  const editor = page.getByTestId('connector-operations-json');
  await editor.fill('[\n  {"op" "list"}\n]');
  await expect(page.getByTestId('asset-editor-error')).toContainText('Operations JSON');
  await expect(page.getByTestId('asset-editor-error')).toContainText('line');
  await expect(editor).toHaveValue('[\n  {"op" "list"}\n]');

  await page.getByRole('button', { name: 'Save connector' }).click();
  expect(writes).toBe(0);
  await expect(page.getByTestId('asset-editor-error')).toContainText('Operations JSON');
});

test('valid JSON compiles back into the cards', async ({ page }) => {
  await page.goto('/connectors');
  await page.getByRole('button', { name: 'New connector' }).click();
  await page.locator('#connector-name').fill('Compile back');
  await page.getByRole('button', { name: 'JSON', exact: true }).click();
  await page.getByTestId('connector-operations-json').fill(
    JSON.stringify(
      [
        {
          op: 'get',
          label: 'Get one',
          method: 'GET',
          url: 'https://api.example.com/x',
          fields: { id: { type: 'string', required: true } },
        },
      ],
      null,
      2,
    ),
  );

  // Switching back to Cards compiles the valid document.
  await page.getByRole('button', { name: 'Cards', exact: true }).click();
  const card = page.getByTestId('connector-op-0');
  await expect(card).toBeVisible();
  await expect(card.locator('#op-0-op')).toHaveValue('get');
  await expect(page.getByTestId('connector-field-row-id')).toBeVisible();

  // The editor names the operation count once in the section head.
  await expect(page.getByTestId('connector-editor')).toContainText('1 operation');
});

// U6: a failed usage lookup is a named error with Retry — the detail rail
// must never read "no usage" when the read failed.
test('a failed connector usage lookup is an error with Retry, not "no usage"', async ({
  page,
  request,
}) => {
  const id = `e2e-conn-usage-${Date.now()}`;
  const created = await request.post('/api/connectors', {
    data: {
      id,
      name: `Connector usage ${id}`,
      operations: [
        {
          op: 'list',
          label: 'List things',
          method: 'GET',
          url: '{{cred.baseUrl}}/things',
          successCodes: '200,2xx',
        },
      ],
    },
  });
  expect([200, 201], await created.text()).toContain(created.status());

  const route = `**/api/connectors/${id}/usage`;
  await page.route(route, (intercepted) =>
    intercepted.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'usage boom' }),
    }),
  );
  try {
    await page.goto('/connectors');
    const row = page.getByTestId('connector-row').filter({ hasText: id });
    await expect(row).toBeVisible();
    await row.getByRole('button', { name: 'View', exact: true }).click();
    const detail = page.getByTestId('connector-detail');
    const failure = detail.getByTestId('connector-usage-error');
    await expect(failure).toBeVisible();
    await expect(failure).toContainText('Could not load');
    await expect(detail.getByText('No published workflows pin this connector.')).toHaveCount(0);

    // A retry that succeeds replaces the error with the real empty state.
    await page.unroute(route);
    await detail.getByTestId('connector-usage-error-retry').click();
    await expect(failure).toHaveCount(0);
    await expect(detail.getByText('No published workflows pin this connector.')).toBeVisible();
  } finally {
    await page.unroute(route);
  }
});
