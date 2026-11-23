import { expect, test, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows, openProfileTab } from './helpers';

// UX Phase 2 D2-3 (plan P0-5/P1-7): the profile form's Effective preview
// tab composes the live form state against a synthetic new node by
// default, and against real usage sites (node overrides win) once a
// published workflow pins the profile.

test.beforeEach(async ({ request }) => {
  await login(request);
});

// The shared login helper only authenticates the API request context;
// pages sign in through the real form.
async function signInPage(page: Page): Promise<void> {
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();
}

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  // e2e- prefixed assets are swept by name; workflows were deleted first
  // so usage pins are already pruned.
  const profiles = await request.get('/api/config-profiles');
  if (profiles.ok()) {
    const { profiles: list } = (await profiles.json()) as {
      profiles: { id: string; name: string }[];
    };
    for (const profile of list ?? []) {
      if (profile.name.startsWith('e2e-')) {
        await request.delete(`/api/config-profiles/${profile.id}`);
      }
    }
  }
  const mappings = await request.get('/api/mappings');
  if (mappings.ok()) {
    const { mappings: list } = (await mappings.json()) as {
      mappings: { id: string; name: string }[];
    };
    for (const mapping of list ?? []) {
      if (mapping.name.startsWith('e2e-')) {
        await request.delete(`/api/mappings/${mapping.id}`);
      }
    }
  }
  const schemas = await request.get('/api/data-schemas');
  if (schemas.ok()) {
    const { schemas: list } = (await schemas.json()) as { schemas: { id: string; name: string }[] };
    for (const schema of list ?? []) {
      if (schema.name.startsWith('e2e-')) {
        await request.delete(`/api/data-schemas/${schema.id}`);
      }
    }
  }
});

function rowByKey(page: Page, key: string) {
  return page.locator('[data-testid="profile-effective-preview"] .profile-preview-row', {
    has: page.locator('code', { hasText: key }),
  });
}

test('the effective preview composes the live form and tracks edits', async ({ page, request }) => {
  const stamp = Date.now();

  // Seed a query mapping and an input contract to bundle.
  const mappingResp = await request.post('/api/mappings', {
    data: {
      name: `e2e-preview-query-${stamp}`,
      kind: 'query',
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(mappingResp.status()).toBe(201);
  const mapping = await mappingResp.json();
  const schemaResp = await request.post('/api/data-schemas', {
    data: {
      name: `e2e-preview-schema-${stamp}`,
      fields: [{ name: 'limit', type: 'number' }],
    },
  });
  expect(schemaResp.status()).toBe(201);
  const schema = await schemaResp.json();

  // Sign the page in through the real form, then build the profile.
  await signInPage(page);
  await page.goto('/hub/http.request/new');
  await page.getByTestId('profile-name').fill(`e2e-preview-profile-${stamp}`);

  // A bare form previews nothing yet.
  await openProfileTab(page, 'preview');
  await expect(page.getByTestId('profile-effective-preview')).toContainText(
    'Nothing set yet — this profile would change nothing.',
  );

  // Node defaults feed the preview with profile-origin rows.
  await openProfileTab(page, 'defaults');
  await page.getByTestId('profile-field-method').selectOption('GET');
  await page.getByTestId('profile-field-url').fill('https://api.example.com/preview');
  await openProfileTab(page, 'preview');
  await expect(rowByKey(page, 'method')).toContainText('GET');
  await expect(rowByKey(page, 'method')).toContainText('profile');
  await expect(rowByKey(page, 'url')).toContainText('https://api.example.com/preview');

  // Included assets feed the preview as bundle-origin ref rows, resolved
  // to names (Phase 4 D4-5 groups them under Included behavior).
  await openProfileTab(page, 'included');
  await page
    .getByTestId('bundle-mapping-query')
    .selectOption({ label: `e2e-preview-query-${stamp} (v1)` });
  await page
    .getByTestId('bundle-schema-input')
    .selectOption({ label: `e2e-preview-schema-${stamp} (v1)` });
  await openProfileTab(page, 'preview');
  const mappingRow = rowByKey(page, 'mappingRefs.query');
  await expect(mappingRow).toContainText(`e2e-preview-query-${stamp} (v1)`);
  await expect(mappingRow).toContainText('bundle');
  const schemaRow = rowByKey(page, 'inputSchemaRef');
  await expect(schemaRow).toContainText(`e2e-preview-schema-${stamp} (v1)`);
  await expect(schemaRow).toContainText('bundle');

  // The preview tracks live edits: a changed method updates the row.
  await openProfileTab(page, 'defaults');
  await page.getByTestId('profile-field-method').selectOption('POST');
  await openProfileTab(page, 'preview');
  await expect(rowByKey(page, 'method')).toContainText('POST');

  // Saving keeps the composition on the edit page.
  await page.getByTestId('profile-save').click();
  await expect(page).toHaveURL(/\/hub\/http\.request\/profile-[^/]+\/edit$/);
  await openProfileTab(page, 'preview');
  await expect(rowByKey(page, 'method')).toContainText('POST');
  await expect(rowByKey(page, 'mappingRefs.query')).toContainText(`e2e-preview-query-${stamp} (v1)`);
});

test('the preview composes real usage sites with node overrides winning', async ({ page, request }) => {
  const stamp = Date.now();

  // Create the profile through the API.
  const profileResp = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-site-profile-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/site' },
    },
  });
  expect(profileResp.status()).toBe(201);
  const profile = await profileResp.json();

  // A published workflow pins it; its http node overrides the method.
  // Publish resolves the profile into the site's stored config (plan D4),
  // so the site carries the v1 values as its own.
  const wfId = `e2e-site-wf-${stamp}`;
  const wfResp = await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `e2e-site-workflow-${stamp}`,
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          config: { method: 'POST' },
          profileRef: profile.id,
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
    },
  });
  expect(wfResp.status()).toBe(201);
  const publishResp = await request.post(`/api/workflows/${wfId}/publish`);
  expect(publishResp.status()).toBe(201);

  // v2 of the profile adds a field no site has resolved yet.
  const updateResp = await request.put(`/api/config-profiles/${profile.id}`, {
    data: {
      name: `e2e-site-profile-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/site', timeout: 30 },
      version: 1,
    },
  });
  expect(updateResp.status()).toBe(200);
  const updated = await updateResp.json();
  expect(updated.version).toBe(2);

  // The edit page previews against a new node by default.
  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit`);
  await openProfileTab(page, 'preview');
  await expect(rowByKey(page, 'method')).toContainText('GET');
  await expect(rowByKey(page, 'method')).toContainText('profile');
  await expect(rowByKey(page, 'timeout')).toContainText('profile');

  // The usage site appears in the picker; selecting it applies the
  // node's override per key. The site resolved the profile's v1 at
  // publish, so its stored method (POST) wins, and the new v2 timeout
  // arrives as a profile fill.
  const picker = page.getByTestId('profile-preview-site');
  await expect(picker).toBeVisible();
  await picker.selectOption({ label: `e2e-site-workflow-${stamp} · Fetch` });
  const methodRow = rowByKey(page, 'method');
  await expect(methodRow).toContainText('POST');
  await expect(methodRow).toContainText('node');
  await expect(rowByKey(page, 'url')).toContainText('https://api.example.com/site');
  await expect(rowByKey(page, 'timeout')).toContainText('30');
  await expect(rowByKey(page, 'timeout')).toContainText('profile');

  // Back to the synthetic node: the profile's values again.
  await picker.selectOption({ label: 'A new node (no overrides)' });
  await expect(rowByKey(page, 'method')).toContainText('GET');
  await expect(rowByKey(page, 'method')).toContainText('profile');
});
