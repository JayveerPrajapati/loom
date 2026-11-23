import { expect, test } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';

// Viewer role: the server is the authorization boundary, and the pages
// must not offer write actions a viewer cannot perform. Read actions
// (History, Usage, Export) stay available.

test('a viewer gets read-only asset pages and no admin surfaces', async ({
  page,
  context,
  request,
}) => {
  await login(request);
  const stamp = Date.now();
  const email = `viewer-${stamp}@e2e.local`;
  const password = 'viewer-password';
  const createdUser = await request.post('/api/users', {
    data: { email, name: `Viewer ${stamp}`, role: 'viewer', password },
  });
  expect(createdUser.status(), await createdUser.text()).toBe(201);

  const createdMapping = await request.post('/api/mappings', {
    data: {
      name: `e2e-viewer-mapping-${stamp}`,
      rows: [{ target: 'email', source: '{{ $json.email }}', required: true }],
    },
  });
  expect(createdMapping.status(), await createdMapping.text()).toBe(201);
  const mappingId = (await createdMapping.json()).id;
  const shared = await request.post(`/api/mappings/${mappingId}/share`);
  expect(shared.status(), await shared.text()).toBe(200);

  try {
    await context.clearCookies();
    await page.goto('/');
    await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
    await page.locator('#auth-email').fill(email);
    await page.locator('#auth-password').fill(password);
    await page.getByRole('button', { name: 'Sign in' }).click();
    await expect(page.locator('.topbar')).toBeVisible();

    await page.goto('/mappings');
    const row = page.locator('.dashboard-run-row').filter({ hasText: `e2e-viewer-mapping-${stamp}` });
    await expect(row).toHaveCount(1);
    await expect(page.getByRole('button', { name: 'New mapping' })).toHaveCount(0);
    await expect(row.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
    await expect(row.getByRole('button', { name: 'Delete', exact: true })).toHaveCount(0);
    await expect(row.getByRole('button', { name: 'View' })).toBeVisible();

    // A viewer still gets the full read-only detail: every row of the
    // mapping, its metadata, and no editor mount at any point.
    await row.getByRole('button', { name: 'View' }).click();
    const mappingDetail = page.getByTestId('mapping-detail');
    await expect(mappingDetail).toBeVisible();
    await expect(mappingDetail.getByTestId('mapping-view-rows')).toContainText('email ←');
    await expect(mappingDetail.getByRole('button', { name: 'Edit' })).toHaveCount(0);
    await expect(page.getByTestId('asset-editor')).toHaveCount(0);
    await mappingDetail.getByRole('button', { name: 'Close detail' }).click();
    await expect(mappingDetail).toHaveCount(0);

    await page.goto('/schemas');
    await expect(page.getByRole('button', { name: 'Import JSON Schema' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Create the first contract' })).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'New contract' })).toHaveCount(0);

    await page.goto('/scripts');
    await expect(page.getByRole('button', { name: 'New script' })).toHaveCount(0);

    await page.goto('/connectors');
    await expect(page.getByRole('button', { name: 'New connector' })).toHaveCount(0);
    // A viewer still gets the read-only detail: operations, fields and
    // metadata, with no editor mount at any point.
    const connectorRow = page.getByTestId('connector-row').first();
    if (await connectorRow.count()) {
      await connectorRow.getByRole('button', { name: 'View', exact: true }).click();
      const connectorDetail = page.getByTestId('connector-detail');
      await expect(connectorDetail).toBeVisible();
      await expect(connectorDetail.getByRole('button', { name: 'Edit', exact: true })).toHaveCount(0);
      await expect(page.getByTestId('connector-editor')).toHaveCount(0);
      await connectorDetail.getByRole('button', { name: 'Close detail' }).click();
    }

    await page.goto('/hub/http.request');
    await expect(page.getByTestId('new-profile')).toHaveCount(0);

    await page.goto('/credentials');
    await expect(page.getByTestId('credentials-page')).toContainText('Credentials are admin-only.');

    await page.locator('.settings-menu summary').click();
    await expect(page.getByRole('button', { name: 'Credentials' })).toHaveCount(0);
    await expect(page.getByRole('link', { name: 'Capabilities' })).toHaveCount(0);
    await expect(page.getByRole('link', { name: 'Audit' })).toHaveCount(0);
  } finally {
    await context.request.post('/api/auth/login', {
      data: { email: e2eUser, password: e2eCode },
    });
    await context.request.delete(`/api/mappings/${mappingId}`);
  }
});
