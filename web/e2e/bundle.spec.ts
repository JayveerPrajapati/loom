import { expect, test } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows, openProfileTab } from './helpers';

// Profile-Centric UX Phase 1: profile bundles compose at publish, and
// private/shared visibility gates what other users see.

test.beforeEach(async ({ request }) => {
  await login(request);
});

// The shared login helper only authenticates the API request context;
// pages sign in through the real form.
async function signInPage(page: import('@playwright/test').Page): Promise<void> {
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();
}

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  // e2e- prefixed assets are swept by name.
  const mappings = await request.get('/api/mappings');
  if (mappings.ok()) {
    const { mappings: list } = (await mappings.json()) as { mappings: { id: string; name: string }[] };
    for (const mapping of list ?? []) {
      if (mapping.name.startsWith('e2e-')) {
        await request.delete(`/api/mappings/${mapping.id}`);
      }
    }
  }
  const profiles = await request.get('/api/config-profiles');
  if (profiles.ok()) {
    const { profiles: list } = (await profiles.json()) as { profiles: { id: string; name: string }[] };
    for (const profile of list ?? []) {
      if (profile.name.startsWith('e2e-')) {
        await request.delete(`/api/config-profiles/${profile.id}`);
      }
    }
  }
});

test('the bundle section composes a profile end to end', async ({ page, request }) => {
  const stamp = Date.now();

  // Seed a query mapping and a schema to compose.
  const mappingResp = await request.post('/api/mappings', {
    data: {
      name: `e2e-bundle-query-${stamp}`,
      kind: 'query',
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(mappingResp.status()).toBe(201);
  const mapping = await mappingResp.json();
  const schemaResp = await request.post('/api/data-schemas', {
    data: {
      name: `e2e-bundle-schema-${stamp}`,
      fields: [{ name: 'limit', type: 'number' }],
    },
  });
  expect(schemaResp.status()).toBe(201);
  const schema = await schemaResp.json();

  // Build the profile through the form: included assets compose the
  // mapping and the input contract. URL is a Defaults field; both
  // included selects live under Configuration → Included behavior
  // (Phase 4 D4-5).
  await signInPage(page);
  await page.goto('/hub/http.request/new');
  await page.getByTestId('profile-name').fill(`e2e-bundle-profile-${stamp}`);
  await openProfileTab(page, 'defaults');
  await page.getByTestId('profile-field-url').fill('https://api.example.com/items');
  await openProfileTab(page, 'included');
  await page
    .getByTestId('bundle-mapping-query')
    .selectOption({ label: `e2e-bundle-query-${stamp} (v1)` });
  await page
    .getByTestId('bundle-schema-input')
    .selectOption({ label: `e2e-bundle-schema-${stamp} (v1)` });
  await page.getByTestId('profile-save').click();

  // A successful create lands on the edit page with the included assets
  // intact.
  await expect(page).toHaveURL(/\/hub\/http\.request\/profile-[^/]+\/edit$/);
  await openProfileTab(page, 'included');
  await expect(page.getByTestId('bundle-mapping-query')).toHaveValue(mapping.id);
  await expect(page.getByTestId('bundle-schema-input')).toHaveValue(schema.id);

  // Publish a workflow whose http node only references the profile —
  // the mapping and contract arrive via the bundle.
  const profileId = (page.url().match(/profile-[^/]+/) ?? [''])[0];
  const wfResp = await request.post('/api/workflows', {
    data: {
      id: `e2e-wf-bundle-${stamp}`,
      name: `e2e-bundle-workflow-${stamp}`,
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          config: { method: 'GET', url: 'https://api.example.com/items' },
          credentials: { http: 'none' },
          profileRef: profileId,
        },
      ],
      edges: [
        { from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } },
      ],
    },
  });
  expect(wfResp.status()).toBe(201);
  const publishResp = await request.post(`/api/workflows/e2e-wf-bundle-${stamp}/publish`);
  expect(publishResp.status()).toBe(201);

  // The snapshot is self-contained: the bundle mapping compiled into the
  // query object.
  const versionResp = await request.get(`/api/workflows/e2e-wf-bundle-${stamp}/versions/1`);
  const snapshot = await versionResp.json();
  expect(snapshot.nodes[1].config.query).toEqual({ limit: '20' });
  expect(snapshot.nodes[1].inputSchemaRef).toBe(schema.id);

  // The bundle pins guard the mapping: deletion is a named 409.
  const delResp = await request.delete(`/api/mappings/${mapping.id}`);
  expect(delResp.status()).toBe(409);
  expect(await delResp.text()).toContain('referenced by published workflows');

  // An unpublished bundle reference guards the mapping too, naming the
  // referencing profile.
  const secondMapping = await (
    await request.post('/api/mappings', {
      data: {
        name: `e2e-bundle-guard-${stamp}`,
        kind: 'query',
        rows: [{ target: 'offset', source: '0' }],
      },
    })
  ).json();
  const guardProfile = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-bundle-guard-profile-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET' },
        bundle: { mappings: { query: secondMapping.id } },
      },
    })
  ).json();
  const guardDel = await request.delete(`/api/mappings/${secondMapping.id}`);
  expect(guardDel.status()).toBe(409);
  expect(await guardDel.text()).toContain(`e2e-bundle-guard-profile-${stamp}`);
  // Cleaning up: remove the guard profile so the sweep can delete the
  // mapping.
  await request.delete(`/api/config-profiles/${guardProfile.id}`);
});

test('private assets are invisible to other editors until shared', async ({
  page,
  request,
  browser,
}) => {
  const stamp = Date.now();

  await signInPage(page);

  // The main e2e user is admin; create a second editor through the admin
  // user API.
  const editorEmail = `e2e-editor-${stamp}@example.com`;
  const editorPassword = `e2e-editor-pass-${stamp}`;
  const createResp = await request.post('/api/users', {
    data: { email: editorEmail, role: 'editor', password: editorPassword },
  });
  expect(createResp.status()).toBe(201);

  // The admin creates a private profile (the create default).
  const profileResp = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-private-profile-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/private' },
    },
  });
  expect(profileResp.status()).toBe(201);
  const profile = await profileResp.json();
  expect(profile.visibility).toBe('private');

  // The editor signs in through the real form (the shared login helper
  // only authenticates the API context).
  const editorContext = await browser.newContext();
  const editorPage = await editorContext.newPage();
  await editorPage.goto('/');
  await editorPage.locator('#auth-email').fill(editorEmail);
  await editorPage.locator('#auth-password').fill(editorPassword);
  await editorPage.getByRole('button', { name: 'Sign in' }).click();
  await expect(editorPage.locator('.topbar')).toBeVisible();

  // The private profile is not in the editor's hub page or list.
  await editorPage.goto('/hub/http.request');
  await expect(editorPage.getByText(`e2e-private-profile-${stamp}`)).toHaveCount(0);
  const foreignGet = await editorPage.request.get(`/api/config-profiles/${profile.id}`);
  expect(foreignGet.status()).toBe(404);

  // Sharing makes it visible without a reload of the world.
  const shareResp = await request.post(`/api/config-profiles/${profile.id}/share`);
  expect(shareResp.status()).toBe(200);
  await editorPage.reload();
  await expect(
    editorPage.getByText(`e2e-private-profile-${stamp}`, { exact: true }),
  ).toBeVisible();

  // The editor cannot govern the shared asset.
  const foreignUnshare = await editorPage.request.post(
    `/api/config-profiles/${profile.id}/unshare`,
  );
  expect(foreignUnshare.status()).toBe(403);

  // The Share button on the edit page round-trips visibility (admin
  // owns the asset here, so the promote action succeeds).
  await page.goto(`/hub/http.request/${profile.id}/edit`);
  await page.getByTestId('profile-unshare').click();
  await expect(page.getByTestId('profile-share')).toBeVisible();
  await editorPage.reload();
  await expect(editorPage.getByText(`e2e-private-profile-${stamp}`)).toHaveCount(0);

  await editorContext.close();
});
