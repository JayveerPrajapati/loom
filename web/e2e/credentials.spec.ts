import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';

// UX Phase 3.4: typed credential forms. The typed form does not choose the
// credential id (the server mints one), so every test finds what it created
// by diffing the credential list before/after and deletes exactly those ids.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

async function listIds(request: APIRequestContext): Promise<string[]> {
  const listed = await request.get('/api/credentials');
  expect(listed.ok()).toBeTruthy();
  const { credentials } = (await listed.json()) as { credentials: { id: string }[] };
  return (credentials ?? []).map((credential) => credential.id);
}

async function createdSince(request: APIRequestContext, before: string[]): Promise<string[]> {
  const now = await listIds(request);
  return now.filter((id) => !before.includes(id));
}

test('typed bearer form creates a working credential', async ({ page, request }) => {
  const before = await listIds(request);
  await page.goto('/credentials');
  await expect(page.locator('#main-stage')).toBeVisible();

  // 3B: the editor is the shared asset panel with a sticky footer Save.
  const editor = page.getByTestId('credential-editor');
  await expect(editor).toBeVisible();
  await expect(editor).toHaveClass(/asset-editor/);
  await expect(page.getByTestId('asset-editor-save')).toBeVisible();
  await expect(page.getByTestId('asset-editor-save')).toHaveText('Save credential');

  await page.locator('#credential-shape').selectOption('http:Bearer token');
  await page.locator('#cred-field-token').fill('typed-form-bearer-value');
  await page.getByTestId('asset-editor-save').click();
  await expect(page.locator('.toast')).toContainText('Credential saved.');

  const created = await createdSince(request, before);
  expect(created.length).toBe(1);

  // Secrets are masked in the typed form (password input).
  await expect(page.locator('#cred-field-token')).toHaveAttribute('type', 'password');

  // The typed form produced a server payload identical to the old JSON
  // textarea: the credential listing shows an http credential.
  const listed = await request.get('/api/credentials');
  const { credentials } = (await listed.json()) as { credentials: { id: string; type: string }[] };
  expect(credentials.find((credential) => credential.id === created[0])?.type).toBe('http');

  // 3B: the row is the shared asset row carrying the credential identity.
  const row = page.getByTestId(`credential-row-${created[0]}`);
  await expect(row).toBeVisible();
  await expect(row).toHaveClass(/asset-row/);
  await expect(page.getByTestId(`credential-usage-count-${created[0]}`)).toHaveText('Not referenced');

  await request.delete(`/api/credentials/${encodeURIComponent(created[0])}`);
});

test('missing required fields are rejected without touching the server', async ({ page, request }) => {
  const before = await listIds(request);
  await page.goto('/credentials');
  await expect(page.locator('#main-stage')).toBeVisible();

  await page.locator('#credential-shape').selectOption('http:OAuth2');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('.banner.error')).toContainText('Client id');
  // Nothing was created.
  expect(await createdSince(request, before)).toEqual([]);
});

test('postgres structured form validates and accepts a port number', async ({ page, request }) => {
  const before = await listIds(request);
  await page.goto('/credentials');
  await expect(page.locator('#main-stage')).toBeVisible();

  await page.locator('#credential-shape').selectOption('postgres:Structured');
  await page.locator('#cred-field-host').fill('localhost');
  await page.locator('#cred-field-port').fill('5432');
  await page.locator('#cred-field-user').fill('app');
  await page.locator('#cred-field-password').fill('typed-form-db-password');
  await page.locator('#cred-field-database').fill('app');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('.toast')).toContainText('Credential saved.');

  const created = await createdSince(request, before);
  expect(created.length).toBe(1);
  await request.delete(`/api/credentials/${encodeURIComponent(created[0])}`);
});

test('advanced JSON toggle still creates and rejects invalid JSON', async ({ page, request }) => {
  const before = await listIds(request);
  await page.goto('/credentials');
  await expect(page.locator('#main-stage')).toBeVisible();

  await page.getByRole('button', { name: 'Advanced JSON' }).click();
  await expect(page.locator('#credential-data')).toBeVisible();

  // Invalid JSON is rejected inline.
  await page.locator('#credential-type').fill('slack');
  await page.locator('#credential-data').fill('{not json');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('.banner.error')).toContainText('valid JSON object');
  expect(await createdSince(request, before)).toEqual([]);

  // Valid JSON creates the credential.
  await page
    .locator('#credential-data')
    .fill('{\n  "webhookUrl": "https://hooks.slack.com/services/e2e"\n}');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('.toast')).toContainText('Credential saved.');

  const created = await createdSince(request, before);
  expect(created.length).toBe(1);
  await request.delete(`/api/credentials/${encodeURIComponent(created[0])}`);
});

test('rotation is explicit and where-used names every reference', async ({ page, request }) => {
  const before = await listIds(request);
  await page.goto('/credentials');
  await expect(page.locator('#main-stage')).toBeVisible();
  await expect(page.locator('.lede')).toContainText('redacted server-side');
  await expect(page.locator('.lede')).toContainText('Only admins can create or rotate credentials');

  await page.locator('#credential-shape').selectOption('http:Bearer token');
  await page.locator('#cred-field-token').fill('rotation-original');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('.toast')).toContainText('Credential saved.');
  const created = await createdSince(request, before);
  expect(created.length).toBe(1);
  const row = page.getByTestId(`credential-row-${created[0]}`);
  await expect(row).toContainText('Not referenced');

  await row.getByRole('button', { name: /Rotate/ }).click();
  await expect(page.getByTestId('confirm-dialog')).toContainText('Replaces the stored value');
  await page
    .getByTestId('credential-rotate-data')
    .fill('{\n  "token": "rotation-replacement"\n}');
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(page.locator('.toast')).toContainText('Credential rotated');
  await expect(page.getByTestId('confirm-dialog')).toHaveCount(0);

  await row.locator('summary').click();
  await expect(row).toContainText('No profiles or workflow versions reference this credential.');

  await row.getByRole('button', { name: /Delete/ }).click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect.poll(async () => (await listIds(request)).length).toBe(before.length);
});

test('credentials can be deleted from the list', async ({ page, request }) => {
  const before = await listIds(request);
  await page.goto('/credentials');
  await expect(page.locator('#main-stage')).toBeVisible();

  await page.locator('#credential-shape').selectOption('http:Bearer token');
  await page.locator('#cred-field-token').fill('typed-form-delete-me');
  await page.getByRole('button', { name: 'Save credential' }).click();
  await expect(page.locator('.toast')).toContainText('Credential saved.');
  const created = await createdSince(request, before);
  expect(created.length).toBe(1);

  await page
    .locator('.dashboard-run-row')
    .filter({ hasText: created[0] })
    .getByRole('button', { name: /Delete/ })
    .click();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect.poll(async () => (await listIds(request)).length).toBe(before.length);
});

test('the empty list explains credentials and focuses the typed form', async ({ page, request }) => {
  // L8: with no credentials recorded the page carries the same
  // explanation + primary-CTA model as the profiles landing — not a
  // grey one-liner. The list is intercepted so the test is
  // deterministic even when other tests left credentials behind.
  const before = await listIds(request);
  await page.route('**/api/credentials*', async (route) => {
    await route.fulfill({ json: { credentials: [], count: 0 } });
  });
  await page.goto('/credentials');
  const empty = page.getByTestId('credentials-empty');
  await expect(empty).toBeVisible();
  await expect(empty).toContainText('No credentials yet');
  await expect(empty).toContainText('encrypted at rest');
  await expect(empty).toContainText('never returned by the API');

  // The CTA focuses the always-present typed form — it creates
  // nothing on its own.
  const cta = page.getByTestId('credentials-empty-create');
  await expect(cta).toBeVisible();
  await expect(cta).toHaveText('Create a credential');
  await cta.click();
  await expect(page.locator('#credential-shape')).toBeFocused();
  await expect(page.locator('#credential-shape')).toBeInViewport();

  // No search filter: the filtered variant must not appear.
  await expect(page.getByTestId('asset-filtered-empty')).toHaveCount(0);
  expect(await createdSince(request, before)).toEqual([]);
});
