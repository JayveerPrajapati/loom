import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, openAssetMenu } from './helpers';

// Phase 2 destructive grammar: every object delete goes through the one
// shared alertdialog — it names the object, Escape cancels and restores
// focus, and a server usage guard keeps the dialog open with the message.

test.beforeEach(async ({ context, request }) => {
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
      rows: [{ target: 'email', source: '{{ $json.email }}', required: true }],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()).id;
}

test('mapping delete uses the shared dialog; Escape cancels and restores focus', async ({
  page,
  request,
}) => {
  const name = `e2e-destructive-map-${Date.now()}`;
  const id = await createMapping(request, name);
  try {
    await page.goto('/mappings');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toHaveCount(1);
    // Delete lives in the row kebab; the trigger is the focus anchor
    // the dialog restores to.
    const trigger = row.getByTestId('asset-actions').locator('summary');

    await openAssetMenu(row);
    await row.getByTestId('asset-action-delete').click();
    const dialog = page.getByTestId('confirm-dialog');
    await expect(dialog).toBeVisible();
    await expect(dialog).toContainText(`Delete the mapping “${name}”?`);

    // The dialog autofocuses Cancel, traps Tab, and returns focus on close.
    await expect(page.getByTestId('confirm-dialog-cancel')).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(page.getByTestId('confirm-dialog-confirm')).toBeFocused();
    await page.keyboard.press('Shift+Tab');
    await expect(page.getByTestId('confirm-dialog-cancel')).toBeFocused();

    await page.keyboard.press('Escape');
    await expect(dialog).toBeHidden();
    await expect(trigger).toBeFocused();
    await expect(row).toHaveCount(1);

    await openAssetMenu(row);
    await row.getByTestId('asset-action-delete').click();
    await page.getByTestId('confirm-dialog-confirm').click();
    await expect(row).toHaveCount(0);
  } finally {
    await request.delete(`/api/mappings/${id}`);
  }
});

test('Escape is ignored while a destructive request is in flight', async ({ page, request }) => {
  const name = `e2e-destructive-busy-${Date.now()}`;
  const id = await createMapping(request, name);
  let release: () => void = () => undefined;
  const held = new Promise<void>((resolve) => {
    release = resolve;
  });
  const route = `**/api/mappings/${id}`;
  await page.route(route, async (intercepted) => {
    if (intercepted.request().method() !== 'DELETE') {
      await intercepted.continue();
      return;
    }
    await held;
    await intercepted.continue();
  });
  try {
    await page.goto('/mappings');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toHaveCount(1);

    await openAssetMenu(row);
    await row.getByTestId('asset-action-delete').click();
    const dialog = page.getByTestId('confirm-dialog');
    await expect(dialog).toBeVisible();
    await page.getByTestId('confirm-dialog-confirm').click();

    // The request is held open: the dialog is busy and cannot be dismissed.
    await expect(page.getByTestId('confirm-dialog-cancel')).toBeDisabled();
    await expect(page.getByTestId('confirm-dialog-confirm')).toBeDisabled();
    await page.keyboard.press('Escape');
    await expect(dialog).toBeVisible();

    release();
    await expect(dialog).toBeHidden();
    await expect(row).toHaveCount(0);
  } finally {
    release();
    await page.unroute(route);
    await request.delete(`/api/mappings/${id}`);
  }
});

test('a pinned mapping keeps the dialog open and names the usage guard', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const name = `e2e-destructive-pinned-${stamp}`;
  const mappingId = await createMapping(request, name);
  const workflowId = `e2e-destructive-pin-${stamp}`;
  const workflow = await request.post('/api/workflows', {
    data: {
      id: workflowId,
      name: 'E2E destructive pin',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'map',
          type: 'data.map',
          typeVersion: 1,
          name: 'Map',
          config: { mappingRef: mappingId },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'map', port: 'main' } }],
    },
  });
  expect(workflow.status(), await workflow.text()).toBe(201);
  const publish = await request.post(`/api/workflows/${workflowId}/publish`);
  expect(publish.status(), await publish.text()).toBe(201);

  try {
    await page.goto('/mappings');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toHaveCount(1);
    await openAssetMenu(row);
    await row.getByRole('button', { name: 'Delete', exact: true }).click();
    const dialog = page.getByTestId('confirm-dialog');
    await expect(dialog).toContainText('pin this mapping');

    await page.getByTestId('confirm-dialog-confirm').click();
    await expect(page.getByTestId('confirm-dialog-error')).toBeVisible();
    await expect(dialog).toBeVisible();
    await expect(row).toHaveCount(1);

    await page.getByTestId('confirm-dialog-cancel').click();
    await expect(dialog).toBeHidden();
  } finally {
    await request.delete(`/api/workflows/${workflowId}`);
    await request.delete(`/api/mappings/${mappingId}`);
  }
});

test('a bundle-only mapping names the profile that blocks deletion', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const name = `e2e-bundle-map-${stamp}`;
  const mappingResponse = await request.post('/api/mappings', {
    data: {
      name,
      kind: 'body',
      rows: [{ target: 'q', source: '{{ $json.q }}', required: false }],
    },
  });
  expect(mappingResponse.status(), await mappingResponse.text()).toBe(201);
  const mappingId = (await mappingResponse.json()).id;
  const profileName = `e2e-bundle-profile-${stamp}`;
  const profileResponse = await request.post('/api/config-profiles', {
    data: {
      name: profileName,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com' },
      bundle: { mappings: { body: mappingId } },
    },
  });
  expect(profileResponse.status(), await profileResponse.text()).toBe(201);
  const profileId = (await profileResponse.json()).id;

  try {
    await page.goto('/mappings');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toHaveCount(1);
    await openAssetMenu(row);
    await row.getByRole('button', { name: 'Delete', exact: true }).click();
    const dialog = page.getByTestId('confirm-dialog');
    await expect(dialog).toContainText('profile bundle');
    await expect(dialog).toContainText(profileName);

    await page.getByTestId('confirm-dialog-confirm').click();
    await expect(page.getByTestId('confirm-dialog-error')).toBeVisible();
    await expect(row).toHaveCount(1);
    await page.getByTestId('confirm-dialog-cancel').click();
  } finally {
    await request.delete(`/api/config-profiles/${profileId}`);
    await request.delete(`/api/mappings/${mappingId}`);
  }
});

test('script delete requires the shared dialog', async ({ page, request }) => {
  const name = `e2e-destructive-script-${Date.now()}`;
  const created = await request.post('/api/scripts', {
    data: { name, language: 'javascript', source: 'return 1;' },
  });
  expect(created.status(), await created.text()).toBe(201);
  const script = await created.json();
  try {
    await page.goto('/scripts');
    const row = page.locator('.dashboard-run-row').filter({ hasText: name });
    await expect(row).toHaveCount(1);

    await openAssetMenu(row);
    await row.getByRole('button', { name: 'Delete', exact: true }).click();
    await expect(page.getByTestId('confirm-dialog')).toBeVisible();
    await page.getByTestId('confirm-dialog-cancel').click();
    await expect(row).toHaveCount(1);

    await openAssetMenu(row);
    await row.getByRole('button', { name: 'Delete', exact: true }).click();
    await page.getByTestId('confirm-dialog-confirm').click();
    await expect(row).toHaveCount(0);
  } finally {
    const list = await request.get('/api/scripts');
    if (list.ok()) {
      const body = await list.json();
      for (const item of body.scripts ?? []) {
        if (item.id === script.id) await request.delete(`/api/scripts/${item.id}`);
      }
    }
  }
});
