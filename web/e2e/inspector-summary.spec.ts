import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Apple-Grade Phase 3: the inspector summary is the node's single source
// of truth — identity, profile, effective behavior, overrides, bundled
// assets and validation — and the issue center aggregates every category
// that can block publish or a run.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

function workflow(
  id: string,
  httpConfig: Record<string, unknown>,
  extra: { profileRef?: string; profileOverrides?: Record<string, boolean> } = {},
) {
  return {
    id,
    name: `Inspector summary ${id}`,
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
        position: { x: 280, y: 0 },
        config: httpConfig,
        ...extra,
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
  };
}

async function createMapping(request: APIRequestContext, stamp: number) {
  const response = await request.post('/api/mappings', {
    data: {
      name: `e2e-summary-mapping-${stamp}`,
      kind: 'query',
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string; name: string };
}

async function createSchema(request: APIRequestContext, stamp: number) {
  const response = await request.post('/api/data-schemas', {
    data: {
      name: `e2e-summary-schema-${stamp}`,
      fields: [{ name: 'id', type: 'string' }],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string; name: string };
}

test('the summary composes profile, effective fields, overrides and bundle assets', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const mapping = await createMapping(request, stamp);
  const schema = await createSchema(request, stamp);
  const profileResponse = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-summary-profile-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'POST',
        url: 'https://api.example.com/profile',
        timeout: 45,
      },
      bundle: {
        mappings: { query: mapping.id },
        schemas: { output: schema.id },
      },
    },
  });
  expect(profileResponse.status(), await profileResponse.text()).toBe(201);
  const profile = (await profileResponse.json()) as { id: string };

  const id = `e2e-summary-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: workflow(
      id,
      { method: 'POST', url: 'https://api.example.com/override' },
      { profileRef: profile.id, profileOverrides: { url: true } },
    ),
  });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node', { hasText: 'Fetch' }).click();

  const summary = page.getByTestId('inspector-summary');
  await expect(summary).toBeVisible();
  await expect(page.getByTestId('summary-profile')).toContainText(`e2e-summary-profile-${stamp}`);
  await expect(page.getByTestId('summary-effective')).toContainText('2 inherited fields');
  await expect(page.getByTestId('summary-overrides')).toContainText('1 · url');
  await expect(page.getByTestId('summary-bundles')).toContainText(`e2e-summary-mapping-${stamp}`);
  await expect(page.getByTestId('summary-bundles')).toContainText(`e2e-summary-schema-${stamp}`);

  // One origin language: inherited, override with the profile's value,
  // and reset back to inherited.
  await expect(page.getByTestId('origin-badge-method')).toHaveText('Inherited');
  await expect(page.getByTestId('origin-badge-url')).toHaveText('Override');
  await expect(page.getByTestId('profile-value-url')).toContainText(
    'https://api.example.com/profile',
  );
  await page.getByTestId('reset-override-url').click();
  await expect(page.locator('#field-url')).toHaveValue('https://api.example.com/profile');
  await expect(page.getByTestId('origin-badge-url')).toHaveText('Inherited');
});

test('the issue center names a missing asset and its fix opens the field', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const mapping = await createMapping(request, stamp);
  const id = `e2e-summary-issue-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: workflow(id, {
      method: 'GET',
      url: 'https://api.example.com/missing',
      mappingRefs: { query: mapping.id },
    }),
  });
  expect(seed.status(), await seed.text()).toBe(201);

  // Deleting the referenced mapping leaves a dangling ref the draft
  // validator (and publish) must name.
  const deleted = await request.delete(`/api/mappings/${mapping.id}`);
  expect(deleted.status(), await deleted.text()).toBe(204);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node', { hasText: 'Fetch' }).click();

  const issues = page.getByTestId('inspector-issues');
  await expect(issues).toBeVisible();
  await expect(issues.getByTestId('inspector-issue-count')).toContainText('blocker');
  await expect(page.getByTestId('inspector-issues-clear')).toHaveCount(0);
  const assetIssue = page.getByTestId('inspector-issue-asset').first();
  await expect(assetIssue).toContainText('missing mapping');
  await assetIssue.getByRole('button', { name: 'Fix field' }).click();
  await expect(page.getByTestId('inspector-panel-data')).toBeVisible();
  await expect(page.locator('#mapping-ref-query')).toBeFocused();
});

test('a failed asset picker load is named with Retry instead of an empty select', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const id = `e2e-summary-load-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: workflow(id, { method: 'GET', url: 'https://api.example.com/load' }),
  });
  expect(seed.status(), await seed.text()).toBe(201);

  // Scripts are not part of the boot indexes, so this mock reaches the
  // Inspector's picker load. The boot load still succeeds.
  let fail = true;
  await page.route('**/api/scripts*', (route) => {
    if (!fail) return route.continue();
    return route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'scripts unavailable' }),
    });
  });
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.loom-node', { hasText: 'Fetch' })).toBeVisible();
  await page.locator('.loom-node', { hasText: 'Fetch' }).click();

  const failure = page.getByTestId('inspector-asset-error-scripts');
  await expect(failure).toBeVisible();
  await expect(failure).toContainText('Could not load');

  fail = false;
  await failure.getByTestId('asset-error-retry').click();
  await expect(failure).toHaveCount(0);
});

test('the Python gate surfaces as a server issue, not a clean node', async ({ page, request }) => {
  const stamp = Date.now();
  const id = `e2e-summary-python-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: {
      id,
      name: `Inspector python ${id}`,
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
          id: 'python',
          type: 'code.python',
          typeVersion: 1,
          name: 'Compute',
          position: { x: 280, y: 0 },
          config: { source: 'print("hi")', requirements: [] },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'python', port: 'main' } }],
    },
  });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node', { hasText: 'Compute' }).click();

  const gate = page.getByTestId('inspector-issue-python');
  await expect(gate).toBeVisible();
  await expect(gate).toContainText('Python nodes are disabled on this server');
  await expect(page.getByTestId('inspector-issues-clear')).toHaveCount(0);
  await expect(page.getByTestId('summary-validation')).toContainText('blocker');
});
