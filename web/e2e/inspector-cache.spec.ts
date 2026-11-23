import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Phase 5 D5-7: the Inspector shares one asset cache with the app
// indexes. Switching nodes must not refetch profiles, contracts, mappings,
// scripts or credentials.

const PREFIX = 'e2e-inspector-cache';

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

async function cleanupProfiles(request: APIRequestContext): Promise<void> {
  const listed = await request.get('/api/config-profiles');
  if (!listed.ok()) return;
  const body = (await listed.json()) as { profiles?: { id: string; name: string }[] };
  for (const profile of body.profiles ?? []) {
    if (!profile.name.startsWith(PREFIX)) continue;
    const deleted = await request.delete(`/api/config-profiles/${encodeURIComponent(profile.id)}`);
    if (deleted.status() !== 204) {
      console.warn(`cleanup profile ${profile.id} -> ${deleted.status()}`);
    }
  }
}

test.afterEach(async ({ request }) => {
  await cleanupProfiles(request);
  await deleteTestWorkflows(request);
});

function workflowBody(id: string) {
  return {
    id,
    name: `${PREFIX} flow`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'fetch',
        type: 'http.request',
        typeVersion: 1,
        name: 'Fetch',
        position: { x: 260, y: 0 },
        config: { method: 'GET', url: 'https://example.com/a' },
      },
      {
        id: 'shape',
        type: 'data.set',
        typeVersion: 1,
        name: 'Shape',
        position: { x: 520, y: 0 },
        config: { values: { message: 'hello' }, keepOnlySet: true },
      },
    ],
    edges: [
      { from: { node: 'start', port: 'main' }, to: { node: 'fetch', port: 'main' } },
      { from: { node: 'fetch', port: 'main' }, to: { node: 'shape', port: 'main' } },
    ],
  };
}

test('node switch reuses cached picker lists', async ({ page, request }) => {
  const stamp = Date.now();
  const profileName = `${PREFIX} ${stamp}`;
  const profile = await request.post('/api/config-profiles', {
    data: {
      name: profileName,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://example.com/profile' },
    },
  });
  expect(profile.status(), await profile.text()).toBe(201);

  const id = `${PREFIX}-${stamp}`;
  const created = await request.post('/api/workflows', { data: workflowBody(id) });
  expect(created.status(), await created.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  await expect(page.locator('.loom-node')).toHaveCount(3);

  const counts: Record<string, number> = {};
  const watched = [
    '/api/config-profiles',
    '/api/data-schemas',
    '/api/mappings',
    '/api/scripts',
    '/api/credentials',
  ];
  page.on('request', (outgoing) => {
    if (outgoing.method() !== 'GET') return;
    const path = new URL(outgoing.url()).pathname;
    if (watched.includes(path)) counts[path] = (counts[path] ?? 0) + 1;
  });

  await page.locator('.loom-node').nth(1).click();
  await expect(page.getByTestId('profile-section')).toBeVisible();
  await expect(page.locator('#node-profile-ref option', { hasText: profileName })).toHaveCount(1);
  // Let the first picker loads settle, then prove the second switch adds
  // no requests at all.
  await page.waitForTimeout(300);
  for (const path of watched) counts[path] = 0;

  await page.locator('.loom-node').nth(2).click();
  await expect(page.getByTestId('profile-section')).toHaveCount(0);
  await page.waitForTimeout(300);

  for (const path of watched) {
    expect(counts[path] ?? 0, `${path} was refetched on node switch`).toBe(0);
  }
});
