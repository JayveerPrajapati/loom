import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UI-audit campaign B1/B2/B3: the profile's values are visible in the
// node's own form fields (with origin badges), editing a field writes a
// node-level override, reset restores the inherited value, and a
// published site (whose stored node config already carries resolved
// values) still badges inherited fields via the ownership-inference rule
// — the same rule upgrade-preview applies. login(context.request)
// already signs the page in (the API context shares cookies with the
// browser context).

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  const profiles = await request.get('/api/config-profiles');
  if (profiles.ok()) {
    const { profiles: list } = (await profiles.json()) as { profiles: { id: string }[] };
    for (const profile of list ?? []) {
      if (profile.id.startsWith('e2e-inherit-')) {
        await request.delete(`/api/config-profiles/${profile.id}`).catch(() => undefined);
      }
    }
  }
  const creds = await request.get('/api/credentials');
  if (creds.ok()) {
    const { credentials } = (await creds.json()) as { credentials: { id: string }[] };
    for (const credential of credentials ?? []) {
      if (credential.id.startsWith('e2e-inherit-')) {
        await request.delete(`/api/credentials/${credential.id}`).catch(() => undefined);
      }
    }
  }
});

function httpWorkflow(id: string, httpConfig: Record<string, unknown> = {}) {
  return {
    id,
    name: `Inheritance ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'http',
        type: 'http.request',
        typeVersion: 1,
        name: 'Fetch',
        position: { x: 260, y: 0 },
        config: { method: 'GET', url: 'https://api.example.com/one', ...httpConfig },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
  };
}

async function createProfile(request: APIRequestContext, id: string, config: Record<string, unknown>, extra: Record<string, unknown> = {}) {
  const response = await request.post('/api/config-profiles', {
    data: {
      id,
      name: `Inheritance ${id}`,
      nodeType: 'http.request',
      config,
      ...extra,
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string; version: number };
}

test('form fields show inherited values, overrides reset, and publish keeps origins', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const profile = await createProfile(request, `e2e-inherit-p-${stamp}`, {
    timeout: 45,
    successCodes: '2xx',
  });
  const id = `e2e-inherit-${stamp}`;
  const seed = await request.post('/api/workflows', { data: httpWorkflow(id) });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(1).click();

  // Attach the profile in the inspector summary.
  await page.locator('#node-profile-ref').selectOption(profile.id);

  // Advanced fields display the inherited values with origin badges.
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.locator('#field-timeout')).toHaveValue('45');
  await expect(page.getByTestId('origin-badge-timeout')).toHaveText('Inherited');
  await expect(page.locator('#field-successCodes')).toHaveValue('2xx');
  await expect(page.getByTestId('origin-badge-successCodes')).toBeVisible();

  // Editing the field writes a node-level override: the badge goes
  // away and a reset control appears.
  await page.locator('#field-timeout').fill('90');
  await expect(page.getByTestId('origin-badge-timeout')).toHaveText('Override');
  await expect(page.getByTestId('reset-override-timeout')).toBeVisible();

  // Reset restores the inherited value without touching node.config.
  await page.getByTestId('reset-override-timeout').click();
  await expect(page.locator('#field-timeout')).toHaveValue('45');
  await expect(page.getByTestId('origin-badge-timeout')).toHaveText('Inherited');

  // Save and publish with the profile filling empty slots only: the
  // stored doc resolves timeout=45 into the node config.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const publish = await request.post(`/api/workflows/${id}/publish`);
  expect(publish.ok(), await publish.text()).toBeTruthy();
  const doc = await request.get(`/api/workflows/${id}`);
  const wf = (await doc.json()) as { nodes: { config: Record<string, unknown> }[] };
  expect(wf.nodes[1].config.timeout).toBe(45);

  // Reload: the ownership-inference rule keeps the resolved value
  // badged as profile-origin instead of mislabeling it a node value.
  await page.reload();
  await page.locator('.loom-node').nth(1).click();
  await page.getByTestId('inspector-tab-advanced').click();
  await expect(page.locator('#field-timeout')).toHaveValue('45');
  await expect(page.getByTestId('origin-badge-timeout')).toHaveText('Inherited');
});

test('parameter rows merge per name with row-level origin badges', async ({ page, request }) => {
  const stamp = Date.now();
  const profile = await createProfile(request, `e2e-inherit-rows-${stamp}`, {
    queryFields: [
      { name: 'limit', mode: 'static', type: 'number', value: 50 },
      { name: 'active', mode: 'static', type: 'boolean', value: true },
    ],
  });
  const id = `e2e-inherit-rows-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: httpWorkflow(id, {
      queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 10 }],
    }),
  });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(1).click();
  await page.locator('#node-profile-ref').selectOption(profile.id);

  // The Data tab shows the merged rows: the node's limit (10) and the
  // profile's active row, badged as profile-origin.
  await page.getByTestId('inspector-tab-data').click();
  const group = page.getByTestId('param-group-query');
  await expect(group).toBeVisible();
  const rows = group.locator('.value-row');
  await expect(rows).toHaveCount(2);
  const limitName = group.locator('[data-testid="param-query-row-0-name"]');
  const activeName = group.locator('[data-testid="param-query-row-1-name"]');
  await expect(limitName).toHaveValue('limit');
  await expect(activeName).toHaveValue('active');
  // The node's limit row carries no origin badge; the profile's
  // active row does.
  await expect(group.locator('[data-testid="param-query-row-0-origin"]')).toHaveCount(0);
  await expect(group.locator('[data-testid="param-query-row-1-origin"]')).toHaveText('Inherited');
});

test('profile credential refs fill empty credential slots with a badge', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const credId = `e2e-inherit-cred-${stamp}`;
  const cred = await request.post('/api/credentials', {
    data: { id: credId, type: 'http', data: { token: 'e2e-token' } },
  });
  expect(cred.status(), await cred.text()).toBe(201);

  const profile = await createProfile(request, `e2e-inherit-cred-p-${stamp}`, {
    timeout: 45,
  }, { credentialRefs: { http: credId } });
  const id = `e2e-inherit-cred-${stamp}`;
  const seed = await request.post('/api/workflows', { data: httpWorkflow(id) });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(1).click();
  await page.locator('#node-profile-ref').selectOption(profile.id);

  // Basics: the http credential slot shows the inherited ref, badged.
  await page.getByTestId('inspector-tab-configuration').click();
  const credField = page.locator('#cred-http');
  await expect(credField).toHaveValue(credId);
  await expect(
    page.locator('label[for="cred-http"]').getByText('Inherited'),
  ).toBeVisible();
});
