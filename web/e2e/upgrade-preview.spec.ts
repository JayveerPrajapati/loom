import { expect, test, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UX Phase 2 D2-6 (plan P1-6): when a published workflow's profile pins
// go stale, the editor shows an indicator, the preview dialog lists the
// per-key changes, and "Save and publish with upgrades" republishes
// with the profile's current version.

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
});

test('the editor flags stale profile pins and republishes with upgrades', async ({
  page,
  request,
}) => {
  const stamp = Date.now();

  const profile = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-upgrade-profile-${stamp}`,
        nodeType: 'http.request',
        config: {
          method: 'POST',
          url: 'https://api.example.com/s',
          timeout: 30,
        },
      },
    })
  ).json();

  const wfId = `e2e-upgrade-wf-${stamp}`;
  const seed = await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `e2e-upgrade-workflow-${stamp}`,
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          config: { timeout: 60 },
          profileRef: profile.id,
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
    },
  });
  expect(seed.status()).toBe(201);
  const firstPublish = await request.post(`/api/workflows/${wfId}/publish`);
  expect(firstPublish.status()).toBe(201);

  // The profile moves to v2: method and url change, timeout stays 30
  // (the workflow's own 60 must survive).
  const update = await request.put(`/api/config-profiles/${profile.id}`, {
    data: {
      name: `e2e-upgrade-profile-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'GET',
        url: 'https://api.example.com/v2',
        timeout: 30,
      },
      version: 1,
    },
  });
  expect(update.status()).toBe(200);

  await signInPage(page);
  await page.goto(`/workflows/${wfId}/edit`);
  await expect(page.locator('.publish-controls')).toBeVisible();

  // The indicator appears in the editor's More menu.
  await page.getByTestId('topbar-more').locator('summary').click();
  const chip = page.getByTestId('upgrade-available-chip');
  await expect(chip).toBeVisible();
  await expect(chip).toContainText(/profile upgrade/);

  // The preview dialog lists the profile-owned changes — the node's own
  // timeout override is not among them.
  await chip.click();
  const dialog = page.getByTestId('upgrade-preview-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('e2e-upgrade-profile-');
  await expect(dialog).toContainText('(v1 → v2)');
  await expect(dialog).toContainText('config.method');
  await expect(dialog).toContainText('config.url');
  await expect(dialog).not.toContainText('config.timeout');

  // Apply: a new published version carries the upgraded values.
  await page.getByTestId('upgrade-apply').click();
  await expect(page.getByTestId('upgrade-preview-backdrop')).toHaveCount(0);
  await expect(page.getByTestId('upgrade-available-chip')).toHaveCount(0);

  const published = await request.get(`/api/workflows/${wfId}/versions/2`);
  expect(published.status()).toBe(200);
  const snap = (await published.json()) as {
    nodes: { id: string; config: Record<string, unknown> }[];
  };
  const http = snap.nodes.find((node) => node.id === 'http');
  expect(http?.config['method']).toBe('GET');
  expect(http?.config['url']).toBe('https://api.example.com/v2');
  expect(http?.config['timeout']).toBe(60);
});
