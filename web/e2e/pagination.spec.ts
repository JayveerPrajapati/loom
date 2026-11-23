import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Phase 5 browse paging: one search control drives the server query, the
// count is the filtered total, load-more appends the next keyset page, and
// a no-match search keeps the filtered-empty state instead of the
// first-run empty state.

const STAMP = Date.now().toString(36);
const PREFIX = `e2e-page-${STAMP}`;

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

async function createMapping(request: APIRequestContext, name: string): Promise<void> {
  const response = await request.post('/api/mappings', {
    data: {
      name,
      rows: [{ target: 'email', source: '{{ $json.email }}' }],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
}

async function createProfile(request: APIRequestContext, name: string): Promise<void> {
  const response = await request.post('/api/config-profiles', {
    data: {
      name,
      nodeType: 'http.request',
      config: { method: 'GET', url: `https://api.example.com/${name}` },
    },
  });
  expect(response.status(), await response.text()).toBe(201);
}

async function cleanupMappings(request: APIRequestContext): Promise<void> {
  const response = await request.get('/api/mappings');
  if (!response.ok()) return;
  const body = (await response.json()) as { mappings?: { id: string; name: string }[] };
  for (const mapping of body.mappings ?? []) {
    if (!mapping.name.startsWith(PREFIX)) continue;
    const deleted = await request.delete(`/api/mappings/${encodeURIComponent(mapping.id)}`);
    if (deleted.status() !== 204) console.warn(`cleanup mapping ${mapping.id} -> ${deleted.status()}`);
  }
}

async function cleanupProfiles(request: APIRequestContext): Promise<void> {
  const response = await request.get('/api/config-profiles');
  if (!response.ok()) return;
  const body = (await response.json()) as { profiles?: { id: string; name: string }[] };
  for (const profile of body.profiles ?? []) {
    if (!profile.name.startsWith(PREFIX)) continue;
    const deleted = await request.delete(`/api/config-profiles/${encodeURIComponent(profile.id)}`);
    if (deleted.status() !== 204) console.warn(`cleanup profile ${profile.id} -> ${deleted.status()}`);
  }
}

async function cleanupCredentials(request: APIRequestContext): Promise<void> {
  const response = await request.get('/api/credentials');
  if (!response.ok()) return;
  const body = (await response.json()) as { credentials?: { id: string }[] };
  for (const credential of body.credentials ?? []) {
    if (!credential.id.startsWith(PREFIX)) continue;
    const deleted = await request.delete(`/api/credentials/${encodeURIComponent(credential.id)}`);
    if (deleted.status() !== 204) {
      console.warn(`cleanup credential ${credential.id} -> ${deleted.status()}`);
    }
  }
}

function workflowBody(id: string, name: string) {
  return {
    id,
    name,
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
        type: 'data.set',
        typeVersion: 1,
        name: 'Shape',
        position: { x: 260, y: 0 },
        config: { values: { message: 'hello' }, keepOnlySet: true },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  };
}

async function createWorkflow(
  request: APIRequestContext,
  id: string,
  name: string,
): Promise<void> {
  const response = await request.post('/api/workflows', { data: workflowBody(id, name) });
  expect(response.status(), await response.text()).toBe(201);
}

async function createCredential(request: APIRequestContext, id: string): Promise<void> {
  const response = await request.post('/api/credentials', {
    data: { id, type: 'http', data: { token: 'x' } },
  });
  expect(response.status(), await response.text()).toBe(201);
}

async function seedRun(request: APIRequestContext, id: string, name: string): Promise<void> {
  await createWorkflow(request, id, name);
  const published = await request.post(`/api/workflows/${encodeURIComponent(id)}/publish`);
  expect(published.status(), await published.text()).toBe(201);
  const started = await request.post(`/api/workflows/${encodeURIComponent(id)}/runs?async=1`, {
    data: { input: {} },
  });
  expect([201, 202]).toContain(started.status());
  const { runId } = (await started.json()) as { runId: string };
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs/${encodeURIComponent(runId)}`);
    if (response.ok()) {
      const run = (await response.json()) as { status: string };
      if (['done', 'failed', 'cancelled', 'waiting'].includes(run.status)) return;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
}

test.afterEach(async ({ request }) => {
  await cleanupMappings(request);
  await cleanupProfiles(request);
  await cleanupCredentials(request);
  await deleteTestWorkflows(request);
});

test('mappings: search narrows the total, load-more appends, no-match clears', async ({ page, request }) => {
  for (let index = 0; index < 51; index += 1) {
    await createMapping(request, `${PREFIX}-map-${String(index).padStart(3, '0')}`);
  }

  await page.goto('/mappings');
  await expect(page.getByRole('heading', { name: 'Mappings', exact: true })).toBeVisible();
  const search = page.getByTestId('mappings-search');
  const rows = page.locator('.dashboard-run-row');

  await search.fill(`${PREFIX}-map-`);
  await expect(page.getByTestId('mappings-search-count')).toHaveText('51 results');
  await expect(rows).toHaveCount(50);

  const loadMore = page.getByTestId('mappings-load-more');
  await expect(loadMore).toBeVisible();
  await loadMore.click();
  await expect(rows).toHaveCount(51);
  await expect(loadMore).toBeHidden();

  await search.fill(`${PREFIX}-map-007`);
  await expect(page.getByTestId('mappings-search-count')).toHaveText('1 result');
  await expect(rows).toHaveCount(1);
  await expect(rows.first()).toContainText(`${PREFIX}-map-007`);

  await search.fill(`${PREFIX}-missing`);
  await expect(page.getByTestId('asset-filtered-empty')).toBeVisible();
  await expect(rows).toHaveCount(0);

  await page.getByRole('button', { name: 'Clear filters' }).click();
  await expect(page.getByTestId('asset-filtered-empty')).toBeHidden();
  await expect(search).toHaveValue('');
});

test('hub profiles: search narrows the paged list', async ({ page, request }) => {
  for (const suffix of ['a', 'b', 'c']) {
    await createProfile(request, `${PREFIX}-profile-${suffix}`);
  }

  await page.goto('/hub/http.request');
  const search = page.getByTestId('hub-profiles-search');
  await expect(search).toBeVisible();
  await expect(page.getByTestId(`view-profile-${PREFIX}-profile-a`)).toBeVisible();

  await search.fill(`${PREFIX}-profile-b`);
  await expect(page.getByTestId('hub-profiles-search-count')).toHaveText('1 result');
  await expect(page.getByTestId(`view-profile-${PREFIX}-profile-b`)).toBeVisible();
  await expect(page.getByTestId(`view-profile-${PREFIX}-profile-a`)).toHaveCount(0);

  await search.fill(`${PREFIX}-missing`);
  await expect(page.getByTestId('asset-filtered-empty')).toBeVisible();
});

test('workflows: search narrows the paged list', async ({ page, request }) => {
  await createWorkflow(request, `${PREFIX}-wf-a`, `${PREFIX} flow A`);
  await createWorkflow(request, `${PREFIX}-wf-b`, `${PREFIX} flow B`);

  await page.goto('/workflows');
  const search = page.locator('#workflow-search');
  await expect(search).toBeVisible();

  await search.fill(`${PREFIX} flow A`);
  await expect(page.getByText(`${PREFIX} flow A`)).toBeVisible();
  await expect(page.getByText(`${PREFIX} flow B`)).toHaveCount(0);
  await expect(page.locator('.page-toolbar')).toContainText('1 workflow');

  await search.fill(`${PREFIX} missing`);
  await expect(page.getByTestId('asset-filtered-empty')).toBeVisible();
});

test('credentials: search narrows the paged list', async ({ page, request }) => {
  await createCredential(request, `${PREFIX}-cred-a`);
  await createCredential(request, `${PREFIX}-cred-b`);

  await page.goto('/credentials');
  const search = page.getByTestId('credentials-search');
  await expect(search).toBeVisible();

  await search.fill(`${PREFIX}-cred-a`);
  await expect(page.getByTestId('credentials-search-count')).toHaveText('1 result');
  await expect(page.getByText(`${PREFIX}-cred-a`)).toBeVisible();
  await expect(page.getByText(`${PREFIX}-cred-b`)).toHaveCount(0);

  await search.fill(`${PREFIX}-missing`);
  await expect(page.getByTestId('asset-filtered-empty')).toBeVisible();
});

test('runs: search matches the workflow name', async ({ page, request }) => {
  const name = `${PREFIX} run workflow`;
  await seedRun(request, `${PREFIX}-run-wf`, name);

  await page.goto('/runs');
  const search = page.locator('#run-search');
  await expect(search).toBeVisible();

  await search.fill(name);
  await expect(page.locator('.runs-row')).toHaveCount(1);
  await expect(page.locator('.runs-row').first()).toContainText(name);
  await expect(page.locator('.page-toolbar-status')).toContainText('1 run');
});
