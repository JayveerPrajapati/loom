import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, expectAssetHistoryDialog, openAssetMenu } from './helpers';

// Scripts (Profile-Centric UX Phase 4): reusable JavaScript and Python
// sources for code nodes. Covers the full loop — create through the
// page, wire a code.run node through the friendly picker, publish
// inlines the pinned source + records usage, the delete guard fires,
// the usage panel shows the pin, and the version bump follows forward
// on the next publish. Python surfaces only through the API here (the
// e2e host runs sim, where python tests answer 503 and env building is
// off); the js path exercises everything end to end.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  await deleteTestScripts(request);
});

async function deleteTestScripts(request: APIRequestContext) {
  const list = await request.get('/api/scripts');
  if (!list.ok()) return;
  const body = await list.json();
  for (const script of body.scripts ?? []) {
    if (String(script.name).startsWith('e2e-')) {
      await request.delete(`/api/scripts/${script.id}`);
    }
  }
}

test('scripts: create through the page, wire code.run, publish inlines and pins', async ({
  page,
  request,
}) => {
  await page.goto('/scripts');
  await expect(page.getByRole('heading', { name: 'Scripts', exact: true })).toBeVisible();

  // The editor mounts only on create/edit, so the flow starts at the
  // header action.
  await page.getByRole('button', { name: 'New script' }).click();
  await expect(page.getByTestId('asset-editor')).toBeVisible();

  // Create a js script through the editor panel.
  await page.locator('#script-name').fill('e2e-shape');
  await page.locator('#script-source').fill('return { ok: true };');
  await page.getByRole('button', { name: 'Create script' }).click();
  const row = page.locator('.dashboard-run-row').filter({ hasText: 'e2e-shape' });
  await expect(row).toHaveCount(1);
  await expect(row).toContainText('javascript');
  await expect(row).toContainText('v1');

  const scripts = await (await request.get('/api/scripts')).json();
  const script = scripts.scripts.find((s: { name: string }) => s.name === 'e2e-shape');
  expect(script).toBeTruthy();

  // Wire a code.run node through the friendly picker. The draft starts
  // with an authored source (a code node needs one to save); picking a
  // script then clearing the source through the raw JSON editor
  // switches the node to the script binding.
  const created = await request.post('/api/workflows', {
    data: {
      id: 'e2e-script-flow',
      name: 'Script flow',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'js',
          type: 'code.run',
          typeVersion: 1,
          name: 'Run',
          position: { x: 260, y: 0 },
          config: { language: 'javascript', source: 'return 0;' },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'js', port: 'main' } }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);

  await page.goto('/workflows/e2e-script-flow/edit');
  await page.locator('.loom-node', { hasText: 'Run' }).click();
  await expect(page.getByTestId('inspector-panel-configuration')).toBeVisible();
  await expect(page.locator('#field-source')).toBeVisible();

  // Picking a script keeps the authored source editable (the escape
  // hatch) — the picker alone does not silently drop it.
  const picker = page.getByTestId('script-ref-picker');
  await picker.selectOption(script.id);
  await expect(page.locator('#field-source')).toBeVisible();

  // The raw JSON editor on Advanced drops the authored source so the
  // script owns it.
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.getByTestId('inspector-panel-advanced')).toBeVisible();
  const raw = page.locator('#node-raw-config');
  await raw.fill(JSON.stringify({ language: 'javascript', scriptRef: script.id }));
  await raw.blur();
  await page.getByTestId('inspector-tab-configuration').click();
  await expect(page.locator('#field-source')).toHaveCount(0);
  await page.getByRole('button', { name: 'Save' }).click();
  await expect(page.locator('.toast')).toContainText('saved');

  // Publish inlines the pinned source and records usage.
  const published = await request.post('/api/workflows/e2e-script-flow/publish');
  expect(published.status(), await published.text()).toBe(201);
  const snapshot = await (
    await request.get('/api/workflows/e2e-script-flow/versions/1')
  ).json();
  const node = snapshot.nodes.find((n: { id: string }) => n.id === 'js');
  expect(node.config.source).toBe('return { ok: true };');
  expect(node.config.scriptRef).toBe(script.id);
  expect(node.config.language).toBe('javascript');

  // The delete guard fires while a published workflow pins the script.
  const blocked = await request.delete(`/api/scripts/${script.id}`);
  expect(blocked.status()).toBe(409);

  // The usage panel names the pin — Usage lives in the kebab.
  await page.goto('/scripts');
  const wired = page.locator('.dashboard-run-row').filter({ hasText: 'e2e-shape' });
  await openAssetMenu(wired);
  await wired.getByRole('button', { name: 'Usage' }).click();
  await expect(wired).toContainText('Script flow');
  await expect(wired).toContainText('script v1');

  // The editor keeps the binding visible after publish: the stored
  // workflow row carries the resolved source, so the form hides the
  // inlined textarea and keeps the picker.
  await page.goto('/workflows/e2e-script-flow/edit');
  await page.locator('.loom-node', { hasText: 'Run' }).click();
  await expect(page.getByTestId('inspector-panel-configuration')).toBeVisible();
  await expect(page.getByTestId('script-ref-picker')).toHaveValue(script.id);
  await expect(page.locator('#field-source')).toHaveCount(0);
  await expect(page.getByTestId('inspector-panel-configuration')).toContainText(
    'pinned from the selected script',
  );
});

test('scripts: version bump follows a published node forward on the next publish', async ({
  request,
}) => {
  const created = await request.post('/api/scripts', {
    data: { name: 'e2e-bump', language: 'javascript', source: 'return 1;' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const script = await created.json();

  const workflow = {
    id: 'e2e-script-bump',
    name: 'Script bump',
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
      {
        id: 'js',
        type: 'code.run',
        typeVersion: 1,
        name: 'Run',
        position: { x: 260, y: 0 },
        config: { language: 'javascript', scriptRef: script.id },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'js', port: 'main' } }],
  };
  await request.post('/api/workflows', { data: workflow });
  await request.post('/api/workflows/e2e-script-bump/publish');

  // The script moves to v2; the draft's stored source matches v1, so
  // the next publish follows forward instead of pinning v1 forever.
  const updated = await request.put(`/api/scripts/${script.id}`, {
    data: { name: 'e2e-bump', source: 'return 2;', version: script.version },
  });
  expect(updated.status(), await updated.text()).toBe(200);

  const republished = await request.post('/api/workflows/e2e-script-bump/publish');
  expect(republished.status(), await republished.text()).toBe(201);
  const snapshot = await (
    await request.get('/api/workflows/e2e-script-bump/versions/2')
  ).json();
  const node = snapshot.nodes.find((n: { id: string }) => n.id === 'js');
  expect(node.config.source).toBe('return 2;');
});

test('scripts: stale update conflicts and python tests are capability-gated', async ({
  request,
}) => {
  const created = await request.post('/api/scripts', {
    data: { name: 'e2e-stale', language: 'javascript', source: 'return 1;' },
  });
  const script = await created.json();

  const stale = await request.put(`/api/scripts/${script.id}`, {
    data: { name: 'e2e-stale', source: 'return 2;', version: script.version },
  });
  expect(stale.status()).toBe(200);
  const conflict = await request.put(`/api/scripts/${script.id}`, {
    data: { name: 'e2e-stale', source: 'return 3;', version: script.version },
  });
  expect(conflict.status()).toBe(409);

  // A python script exists; its test answers 503 on the sim host.
  const py = await request.post('/api/scripts', {
    data: { name: 'e2e-py', language: 'python', source: "return {'ok': True}" },
  });
  expect(py.status(), await py.text()).toBe(201);
  const pyScript = await py.json();
  const tested = await request.post(`/api/scripts/${pyScript.id}/test`, {
    data: { input: {} },
  });
  expect(tested.status()).toBe(503);
});

test('scripts: the editor mounts on demand and cancel returns the list to full width', async ({
  page,
}) => {
  await page.goto('/scripts');
  // Wait for render: the width check reads the live DOM.
  await expect(page.getByRole('heading', { name: 'Scripts', exact: true })).toBeVisible();
  await expect(page.getByTestId('asset-editor')).toHaveCount(0);

  // Closed editor: the list spans the whole workspace.
  const closedWidths = await page.evaluate(() => {
    const grid = document.querySelector('.dashboard-grid');
    const list = document.querySelector('.dashboard-grid > .dashboard-panel');
    if (!grid || !list) return null;
    return {
      list: list.getBoundingClientRect().width,
      grid: grid.getBoundingClientRect().width,
    };
  });
  expect(closedWidths).not.toBeNull();
  expect(closedWidths!.list).toBeGreaterThanOrEqual(closedWidths!.grid * 0.9);

  await page.getByRole('button', { name: 'New script' }).click();
  await expect(page.getByTestId('asset-editor')).toBeVisible();

  await page.getByTestId('asset-editor-cancel').click();
  await expect(page.getByTestId('asset-editor')).toHaveCount(0);

  const reopenedWidths = await page.evaluate(() => {
    const grid = document.querySelector('.dashboard-grid');
    const list = document.querySelector('.dashboard-grid > .dashboard-panel');
    if (!grid || !list) return null;
    return {
      list: list.getBoundingClientRect().width,
      grid: grid.getBoundingClientRect().width,
    };
  });
  expect(reopenedWidths).not.toBeNull();
  expect(reopenedWidths!.list).toBeGreaterThanOrEqual(reopenedWidths!.grid * 0.9);
});

test('scripts: an empty save is named inline and never reaches the server', async ({ page }) => {
  await page.goto('/scripts');
  await page.getByRole('button', { name: 'New script' }).click();

  let writes = 0;
  page.on('request', (request) => {
    if (/\/api\/scripts/.test(request.url()) && request.method() !== 'GET') writes += 1;
  });

  await page.getByTestId('asset-editor-save').click();
  await expect(page.getByTestId('asset-editor-error')).toContainText('Name is required.');

  // The source textarea is the other required field.
  await page.locator('#script-name').fill('e2e-empty-save');
  await page.getByTestId('asset-editor-save').click();
  await expect(page.getByTestId('asset-editor-error')).toContainText('Source is required.');

  expect(writes).toBe(0);
});

test('scripts: the history dialog is a wide shell with a distinct empty state', async ({
  page,
  request,
}) => {
  const created = await request.post('/api/scripts', {
    data: { name: 'e2e-hist', language: 'javascript', source: 'return 1;' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const script = await created.json();

  await page.goto('/scripts');
  const row = page.locator('.dashboard-run-row').filter({ hasText: 'e2e-hist' });
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();

  const dialog = page.getByTestId('script-history');
  await expectAssetHistoryDialog(dialog);
  await expect(dialog).toContainText('v1');
  await dialog.getByRole('button', { name: 'Close history' }).click();
  await expect(dialog).toHaveCount(0);

  // No versions (route-intercepted): a named empty state, not a blank
  // or endlessly-loading body.
  await page.route(`**/api/scripts/${script.id}/versions*`, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ versions: [], count: 0 }),
    }),
  );
  await openAssetMenu(row);
  await row.getByRole('button', { name: 'History' }).click();
  await expect(page.getByTestId('script-history')).toContainText('No script versions recorded yet.');
});

test('scripts: the header holds two blocks and the toolbar owns the filter', async ({
  page,
  request,
}) => {
  const created = await request.post('/api/scripts', {
    data: { name: 'e2e-tb-lang', language: 'javascript', source: 'return 1;' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const script = await created.json();
  try {
    await page.goto('/scripts');
    await expect(page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-lang' })).toBeVisible();

    const header = page.locator('.page-head');
    expect(await header.locator(':scope > *').count()).toBe(2);
    const toolbar = page.locator('.page-toolbar');
    await expect(toolbar.getByTestId('scripts-search')).toBeVisible();
    await expect(toolbar.getByTestId('script-language-filter')).toBeVisible();
    expect(await header.getByTestId('script-language-filter').count()).toBe(0);

    // The filter still changes the list.
    await toolbar.getByTestId('script-language-filter').selectOption('python');
    await expect(page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-lang' })).toHaveCount(0);
    await toolbar.getByTestId('script-language-filter').selectOption('');
    await expect(page.locator('.dashboard-run-row').filter({ hasText: 'e2e-tb-lang' })).toBeVisible();
  } finally {
    await request.delete(`/api/scripts/${script.id}`);
  }
});

test('scripts: an untouched edit is clean and Save stays locked until something changes', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const created = await request.post('/api/scripts', {
    data: { name: `e2e-dirty-${stamp}`, language: 'javascript', source: 'return 1;' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const script = await created.json();

  await page.goto('/scripts');
  const row = page.locator('.dashboard-run-row').filter({ hasText: `e2e-dirty-${stamp}` });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: 'Edit', exact: true }).click();

  // Untouched edit: clean status, Save disabled.
  await expect(page.getByTestId('asset-editor-status')).toHaveText('All changes saved');
  await expect(page.getByTestId('asset-editor-save')).toBeDisabled();

  // Changing the source marks the editor dirty and unlocks Save.
  await page.locator('#script-source').fill('return 2;');
  await expect(page.getByTestId('asset-editor-status')).toHaveText('Unsaved changes');
  await expect(page.getByTestId('asset-editor-save')).toBeEnabled();

  // Cancel closes the editor; reopening the same script is clean again.
  await page.getByTestId('asset-editor-cancel').click();
  await expect(page.getByTestId('asset-editor')).toHaveCount(0);
  await row.getByRole('button', { name: 'Edit', exact: true }).click();
  await expect(page.getByTestId('asset-editor-status')).toHaveText('All changes saved');
  await expect(page.getByTestId('asset-editor-save')).toBeDisabled();
});

test('scripts: the editor is sectioned and the tester is a wide two-pane dialog', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const created = await request.post('/api/scripts', {
    data: { name: `e2e-sections-${stamp}`, language: 'javascript', source: 'return 1;' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const script = await created.json();

  await page.goto('/scripts');
  const row = page.locator('.dashboard-run-row').filter({ hasText: `e2e-sections-${stamp}` });
  await expect(row).toBeVisible();
  await row.getByRole('button', { name: 'Edit', exact: true }).click();

  // The form is grouped into named sections.
  for (const section of ['Basics', 'Source', 'Runtime']) {
    await expect(page.getByRole('group', { name: section })).toBeVisible();
  }
  await page.getByTestId('asset-editor-cancel').click();

  // The tester dialog is wide, with input and output in separate panes
  // and no horizontal overflow in either.
  await row.getByRole('button', { name: 'Test' }).click();
  const tester = page.getByTestId('script-tester');
  await expect(tester).toBeVisible();
  const width = await tester.evaluate((element) => element.getBoundingClientRect().width);
  expect(width).toBeGreaterThanOrEqual(720);

  await page.getByRole('button', { name: 'Run test' }).click();
  const result = page.getByTestId('script-test-result');
  await expect(result).toBeVisible();
  const overflow = await page.getByTestId('script-test-output').evaluate(
    (element) => element.scrollWidth - element.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);
  await page.getByRole('button', { name: 'Close' }).click();
  await expect(tester).toHaveCount(0);
});
