import { expect, test, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UX Phase 2 D2-5 (plan P1-5): the edit page's Used-by tab shows the
// workflow versions pinning the profile and, through the bundle, the
// bundled assets — impact before edits.

test.beforeEach(async ({ request }) => {
  await login(request);
});

async function signInPage(page: Page): Promise<void> {
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();
}

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
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

test('the Used-by tab lists profile and bundled-asset pins', async ({ page, request }) => {
  const stamp = Date.now();

  const mapping = await (
    await request.post('/api/mappings', {
      data: {
        name: `e2e-usedby-map-${stamp}`,
        kind: 'query',
        rows: [{ target: 'limit', source: '20' }],
      },
    })
  ).json();
  const profile = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-usedby-profile-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://api.example.com/u' },
        bundle: { mappings: { query: mapping.id } },
      },
    })
  ).json();

  const wfId = `e2e-usedby-wf-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `e2e-usedby-workflow-${stamp}`,
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          config: {},
          profileRef: profile.id,
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
    },
  });
  expect(seed.status()).toBe(201);
  const publish = await request.post(`/api/workflows/${wfId}/publish`);
  expect(publish.status()).toBe(201);

  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit?tab=used-by`);
  await expect(page.getByTestId('profile-panel-used-by')).toBeVisible();

  // The profile's own pin: workflow v1 pins profile v1.
  await expect(page.getByTestId('profile-own-usage')).toContainText(
    `e2e-usedby-workflow-${stamp} · v1 (pins profile v1)`,
  );

  // The bundled mapping's pin flows through the published workflow.
  const bundleUsage = page.getByTestId('profile-bundle-usage');
  await expect(bundleUsage).toContainText(`query mapping: e2e-usedby-map-${stamp}`);
  await expect(bundleUsage).toContainText(`e2e-usedby-workflow-${stamp} · v1`);

  // A create page has no Used-by tab at all.
  await page.goto('/hub/http.request/new');
  await expect(page.getByTestId('profile-section-used-by')).toHaveCount(0);
});
