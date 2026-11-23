import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Profile ownership: a UI-created node is sparse, attaching a profile
// inherits its values, and a deliberate override survives save, publish,
// reload and a profile upgrade even when it equals the new profile value.
// Bundle contracts and mappings are visible in the node's Data tab.

test.beforeEach(async ({ request }) => {
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  await deleteAssets(request, '/api/config-profiles', 'profiles');
  await deleteAssets(request, '/api/data-schemas', 'schemas');
  await deleteAssets(request, '/api/mappings', 'mappings');
});

async function deleteAssets(request: APIRequestContext, path: string, key: string): Promise<void> {
  const response = await request.get(path);
  if (!response.ok()) return;
  const body = (await response.json()) as Record<string, { id: string; name: string }[]>;
  for (const asset of body[key] ?? []) {
    if (asset.name.startsWith('e2e-own-')) {
      await request.delete(`${path}/${asset.id}`).catch(() => undefined);
    }
  }
}

async function signInPage(page: Page): Promise<void> {
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();
}

async function seedTriggerOnly(request: APIRequestContext, id: string, name: string): Promise<void> {
  const response = await request.post('/api/workflows', {
    data: {
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
      ],
      edges: [],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
}

test('explicit ownership survives save, publish, reload, and an equal-value upgrade', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const profileResponse = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-own-profile-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'POST',
        url: 'https://api.example.com/v1',
        timeout: 45,
      },
    },
  });
  expect(profileResponse.status(), await profileResponse.text()).toBe(201);
  const profile = (await profileResponse.json()) as { id: string; version: number };

  const wfId = `e2e-own-${stamp}`;
  await seedTriggerOnly(request, wfId, `e2e own ${stamp}`);

  await signInPage(page);
  await page.goto(`/workflows/${encodeURIComponent(wfId)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  // Create the HTTP node through the editor: it must start sparse so the
  // profile can supply every value.
  await page.keyboard.press('Control+k');
  await expect(page.locator('.palette')).toBeVisible();
  await page.locator('.palette-input').fill('HTTP request');
  await page.locator('.palette-row').filter({ hasText: 'HTTP request' }).first().click();
  await expect(page.locator('.loom-node')).toHaveCount(2);
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.locator('#node-raw-config')).toHaveValue('{}');

  // Attach the profile: every supplied value renders as inherited.
  await page.locator('#node-profile-ref').selectOption(profile.id);
  await page.getByTestId('inspector-tab-configuration').click();
  await expect(page.locator('#field-method')).toHaveValue('POST');
  await expect(page.locator('#field-url')).toHaveValue('https://api.example.com/v1');
  await expect(page.getByTestId('origin-badge-method')).toHaveText('Inherited');
  await expect(page.getByTestId('origin-badge-url')).toHaveText('Inherited');
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.locator('#field-timeout')).toHaveValue('45');
  await expect(page.getByTestId('origin-badge-timeout')).toHaveText('Inherited');

  // Deliberately type the URL the next profile version will use: a typed
  // edit is an explicit override even when the value later equals the
  // profile's.
  await page.getByTestId('inspector-tab-configuration').click();
  await page.locator('#field-url').fill('https://api.example.com/v2');
  await expect(page.getByTestId('origin-badge-url')).toHaveText('Override');
  await expect(page.getByTestId('reset-override-url')).toBeVisible();

  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();

  const publish = await request.post(`/api/workflows/${encodeURIComponent(wfId)}/publish`);
  expect(publish.status(), await publish.text()).toBe(201);
  const stored = (await (await request.get(`/api/workflows/${encodeURIComponent(wfId)}`)).json()) as {
    nodes: { id: string; config: Record<string, unknown>; profileOverrides?: Record<string, boolean> }[];
  };
  const publishedHttp = stored.nodes.find((node) => node.id !== 'start');
  expect(publishedHttp?.config['method']).toBe('POST');
  expect(publishedHttp?.config['url']).toBe('https://api.example.com/v2');
  expect(publishedHttp?.config['timeout']).toBe(45);
  expect(publishedHttp?.profileOverrides).toEqual({ url: true });

  // Reload: origins come from stored intent, not value equality.
  await page.reload();
  await page.locator('.loom-node').nth(1).click();
  await page.getByTestId('inspector-tab-configuration').click();
  await expect(page.locator('#field-method')).toHaveValue('POST');
  await expect(page.getByTestId('origin-badge-method')).toHaveText('Inherited');
  await expect(page.locator('#field-url')).toHaveValue('https://api.example.com/v2');
  await expect(page.getByTestId('origin-badge-url')).toHaveText('Override');
  await expect(page.getByTestId('reset-override-url')).toBeVisible();

  // The profile moves to v2 with the override's exact URL.
  const update = await request.put(`/api/config-profiles/${profile.id}`, {
    data: {
      name: `e2e-own-profile-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'PUT',
        url: 'https://api.example.com/v2',
        timeout: 60,
      },
      version: profile.version,
    },
  });
  expect(update.status(), await update.text()).toBe(200);

  await page.reload();
  await page.getByTestId('topbar-more').locator('summary').click();
  const chip = page.getByTestId('upgrade-available-chip');
  await expect(chip).toBeVisible();
  await chip.click();
  const dialog = page.getByTestId('upgrade-preview-dialog');
  await expect(dialog).toContainText('config.method');
  await expect(dialog).toContainText('config.timeout');
  await expect(dialog).not.toContainText('config.url');
  await page.getByTestId('upgrade-apply').click();
  await expect(page.getByTestId('upgrade-preview-backdrop')).toHaveCount(0);

  const version = (await (
    await request.get(`/api/workflows/${encodeURIComponent(wfId)}/versions/2`)
  ).json()) as {
    nodes: { id: string; config: Record<string, unknown>; profileOverrides?: Record<string, boolean> }[];
  };
  const upgradedHttp = version.nodes.find((node) => node.id !== 'start');
  expect(upgradedHttp?.config['method']).toBe('PUT');
  expect(upgradedHttp?.config['url']).toBe('https://api.example.com/v2');
  expect(upgradedHttp?.config['timeout']).toBe(60);
  expect(upgradedHttp?.profileOverrides).toEqual({ url: true });

  // The override is still a node value in the UI though it now equals
  // the profile's URL; the inherited fields followed the upgrade.
  await page.reload();
  await page.locator('.loom-node').nth(1).click();
  await page.getByTestId('inspector-tab-configuration').click();
  await expect(page.locator('#field-method')).toHaveValue('PUT');
  await expect(page.getByTestId('origin-badge-method')).toHaveText('Inherited');
  await expect(page.locator('#field-url')).toHaveValue('https://api.example.com/v2');
  await expect(page.getByTestId('origin-badge-url')).toHaveText('Override');
  await expect(page.getByTestId('reset-override-url')).toBeVisible();
});

test('a sparse draft runs before publish with its profile values', async ({ request }) => {
  const stamp = Date.now();
  const profileResponse = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-own-run-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/run' },
    },
  });
  expect(profileResponse.status(), await profileResponse.text()).toBe(201);
  const profile = (await profileResponse.json()) as { id: string };

  const wfId = `e2e-own-run-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `e2e own run ${stamp}`,
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
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          position: { x: 300, y: 0 },
          config: {},
          profileRef: profile.id,
          profileOverrides: {},
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
    },
  });
  expect(seed.status(), await seed.text()).toBe(201);

  // The node stores no config of its own; the run boundary must resolve the
  // profile before validation, exactly like the editor's Data tab does.
  const run = await request.post(`/api/workflows/${encodeURIComponent(wfId)}/runs`, {
    data: { input: {} },
  });
  expect(run.status(), await run.text()).toBe(200);
  const result = (await run.json()) as { runId: string; status: string };
  expect(result.status).toBe('done');

  const mappingResponse = await request.post('/api/mappings', {
    data: {
      name: `e2e-own-run-map-${stamp}`,
      kind: 'item',
      rows: [{ target: 'label', source: '$json.name' }],
    },
  });
  expect(mappingResponse.status(), await mappingResponse.text()).toBe(201);
  const mapping = (await mappingResponse.json()) as { id: string };

  const mapId = `e2e-own-run-map-${stamp}`;
  const mapSeed = await request.post('/api/workflows', {
    data: {
      id: mapId,
      name: `e2e own map run ${stamp}`,
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
          id: 'map',
          type: 'data.map',
          typeVersion: 1,
          name: 'Map',
          position: { x: 300, y: 0 },
          config: { mappingRef: mapping.id },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'map', port: 'main' } }],
    },
  });
  expect(mapSeed.status(), await mapSeed.text()).toBe(201);

  const mapRun = await request.post(`/api/workflows/${encodeURIComponent(mapId)}/runs`, {
    data: { input: { name: 'loom' } },
  });
  expect(mapRun.status(), await mapRun.text()).toBe(200);
  const mapResult = (await mapRun.json()) as { status: string };
  expect(mapResult.status).toBe('done');
});

test('bundle contracts and mappings are visible in the node Data tab', async ({ page, request }) => {
  const stamp = Date.now();
  const schemaResponse = await request.post('/api/data-schemas', {
    data: {
      name: `e2e-own-schema-${stamp}`,
      fields: [{ name: 'id', type: 'string' }],
    },
  });
  expect(schemaResponse.status(), await schemaResponse.text()).toBe(201);
  const schema = (await schemaResponse.json()) as { id: string };
  const mappingResponse = await request.post('/api/mappings', {
    data: {
      name: `e2e-own-mapping-${stamp}`,
      kind: 'query',
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(mappingResponse.status(), await mappingResponse.text()).toBe(201);
  const mapping = (await mappingResponse.json()) as { id: string };

  const profileResponse = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-own-bundle-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'GET',
        url: 'https://api.example.com/bundle',
      },
      bundle: {
        schemas: { output: schema.id },
        mappings: { query: mapping.id },
      },
    },
  });
  expect(profileResponse.status(), await profileResponse.text()).toBe(201);
  const profile = (await profileResponse.json()) as { id: string };

  const wfId = `e2e-own-bundle-wf-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `e2e own bundle ${stamp}`,
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
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          position: { x: 300, y: 0 },
          config: {},
          profileRef: profile.id,
          profileOverrides: {},
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
    },
  });
  expect(seed.status(), await seed.text()).toBe(201);

  await signInPage(page);
  await page.goto(`/workflows/${encodeURIComponent(wfId)}/edit`);
  await page.locator('.loom-node').nth(1).click();
  await page.getByTestId('inspector-tab-data').click();

  const section = page.getByTestId('schema-section');
  await expect(page.locator('#node-schema-ref')).toHaveValue(schema.id);
  await expect(section).toContainText('From bundle');
  await expect(page.getByTestId('schema-preview')).toContainText(`e2e-own-schema-${stamp}`);

  const queryGroup = page.getByTestId('param-group-query');
  await expect(queryGroup.locator('#mapping-ref-query')).toHaveValue(mapping.id);
  await expect(queryGroup).toContainText('From bundle');
  await expect(page.getByTestId('mapping-preview-query')).toContainText('limit');
});
