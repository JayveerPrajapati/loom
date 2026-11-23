import { expect, test, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UX Phase 2 2.7b: the read-only profile view page — every signed-in
// user can inspect a profile without entering the edit form, the list
// page links profile names to it, and editors continue to the edit
// form from there.

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
});

test('the read-only view page shows profile details and links from the list', async ({
  page,
  request,
}) => {
  const stamp = Date.now();

  const mapping = await (
    await request.post('/api/mappings', {
      data: {
        name: `e2e-view-map-${stamp}`,
        kind: 'query',
        rows: [{ target: 'limit', source: '20' }],
      },
    })
  ).json();
  const credentialId = `e2e-view-cred-${stamp}`;
  const credentialResponse = await request.post('/api/credentials', {
    data: {
      id: credentialId,
      type: 'http',
      data: { token: 'e2e-view-token' },
    },
  });
  expect(credentialResponse.status()).toBe(201);
  const profile = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-view-profile-${stamp}`,
        nodeType: 'http.request',
        description: 'Read-only view fixture',
        config: {
          method: 'POST',
          url: 'https://api.example.com/view',
          timeout: 30,
          queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 20 }],
        },
        credentialRefs: { http: credentialId },
        retry: { maxAttempts: 3, backoffMs: 250 },
        tags: ['view', 'fixture'],
        owner: 'view-team',
        bundle: { mappings: { query: mapping.id } },
      },
    })
  ).json();

  // A published workflow pins the profile so the usage section has rows.
  const wfId = `e2e-view-wf-${stamp}`;
  await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `e2e-view-workflow-${stamp}`,
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
  await request.post(`/api/workflows/${wfId}/publish`);

  // The list page's profile name opens the read-only view.
  await signInPage(page);
  await page.goto('/hub/http.request');
  await page.getByTestId(`view-profile-e2e-view-profile-${stamp}`).click();
  await expect(page.getByTestId('profile-view-name')).toHaveText(`e2e-view-profile-${stamp}`);
  await expect(page).toHaveURL(new RegExp(`/hub/http.request/${profile.id}/?$`));

  // All sections render: basics, config (plain + param rows), credentials,
  // retry, bundle, usage. The subtitle sits in the page head.
  await expect(page.locator('.page-sub')).toContainText('HTTP request profile · v1');
  await expect(page.locator('.page-sub')).toContainText('owner view-team');
  const sections = page.getByTestId('profile-view-sections');
  await expect(sections).toContainText('Read-only view fixture');
  await expect(sections).toContainText('view, fixture');
  await expect(sections).toContainText('max attempts 3, backoff 250ms');

  const config = page.getByTestId('profile-view-config');
  await expect(config).toContainText('method');
  await expect(config).toContainText('"POST"');
  await expect(config).toContainText('https://api.example.com/view');
  await expect(config).toContainText('Query rows');
  await expect(config).toContainText('limit');

  await expect(sections).toContainText(credentialId);
  await expect(sections).toContainText(`query mapping: e2e-view-map-${stamp}`);
  // Usage pins render as de-dotted meta values.
  const pin = sections.locator('.profile-preview-row').filter({ hasText: `e2e-view-workflow-${stamp}` });
  await expect(pin.locator('.meta-list')).toContainText(`e2e-view-workflow-${stamp}`);
  await expect(pin.locator('.meta-list')).toContainText('v1');
  await expect(pin.locator('.meta-list')).toContainText('pins profile v1');

  // History opens the shared dialog from the view page too.
  await page.getByTestId('profile-view-history').click();
  await expect(page.getByTestId('profile-history-body')).toBeVisible();
  await page.getByRole('button', { name: 'Close history' }).click();

  // Editors continue to the edit form.
  await page.getByTestId('profile-view-edit').click();
  await expect(page).toHaveURL(new RegExp(`${profile.id}/edit`));
  await expect(page.getByTestId('profile-version-pill')).toHaveText('v1');
});

test('a failed usage lookup is an error with Retry, not an empty result', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const created = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-view-usage-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/usage' },
    },
  });
  const profile = (await created.json()) as { id: string };
  expect(created.status()).toBe(201);

  await signInPage(page);
  const route = `**/api/config-profiles/${profile.id}/usage`;
  await page.route(route, (intercepted) =>
    intercepted.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'usage boom' }),
    }),
  );
  try {
    await page.goto(`/hub/http.request/${profile.id}`);
    const usage = page.getByTestId('profile-view-usage');
    const failure = usage.getByTestId('profile-usage-error');
    await expect(failure).toBeVisible();
    await expect(failure).toContainText('Could not load');
    await expect(usage.getByText('No published workflow versions pin this profile.')).toHaveCount(0);

    // A retry that succeeds replaces the error with the real empty state.
    await page.unroute(route);
    await usage.getByTestId('profile-usage-error-retry').click();
    await expect(failure).toHaveCount(0);
    await expect(usage.getByText('No published workflow versions pin this profile.')).toBeVisible();
  } finally {
    await page.unroute(route);
    await request.delete(`/api/config-profiles/${profile.id}`);
  }
});

test('view-page meta values render de-dotted', async ({ page, request }) => {
  // Seed one profile: the list may otherwise be empty when this test
  // runs standalone.
  const id = `e2e-view-meta-${Date.now()}`;
  await request.post('/api/config-profiles', {
    data: {
      id,
      name: id,
      nodeType: 'http.request',
      config: { method: 'GET' },
      owner: 'view-team',
    },
  });
  await signInPage(page);
  await page.goto('/hub/http.request');
  const first = page
    .getByTestId('profile-list')
    .locator('.dashboard-run-row')
    .filter({ hasText: id })
    .locator('.meta-list');
  await expect(first).toBeVisible();
  const children = await first.locator(':scope > *').count();
  expect(children).toBeGreaterThanOrEqual(2);
  await expect(first).not.toContainText('·');
  await request.delete(`/api/config-profiles/${id}`);
});

test('view-page layout: sticky summary rail, content sections in order, stacked labels', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const id = `e2e-layout-${stamp}`;
  await request.post('/api/config-profiles', {
    data: {
      id,
      name: id,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com', timeout: 30 },
      owner: 'layout-team',
      tags: ['layout', 'probe'],
    },
  });
  try {
    await signInPage(page);
    await page.goto(`/hub/http.request`);
    await page.getByTestId(`view-profile-${id}`).click();
    await expect(page.getByTestId('profile-view-name')).toHaveText(id);

    // Section order: Basics, Configuration, References.
    const order = await page.evaluate(() => {
      const labels = Array.from(
        document.querySelectorAll('.profile-view-content > section'),
      ).map((section) => section.getAttribute('aria-label'));
      return labels;
    });
    expect(order).toEqual(['Profile basics', 'Profile configuration values', 'Profile references']);

    // Label-above-value: each definition cell stacks its pair.
    const stacked = await page.evaluate(() => {
      const cell = document.querySelector('.definition-cell');
      if (!cell) return null;
      const label = cell.querySelector('dt')?.getBoundingClientRect();
      const value = cell.querySelector('dd')?.getBoundingClientRect();
      if (!label || !value) return null;
      return value.top - label.bottom;
    });
    expect(stacked).not.toBeNull();
    expect(stacked!).toBeGreaterThanOrEqual(0);

    // The rail keeps its width band and sticks while the page scrolls.
    const rail = page.getByTestId('profile-view-rail');
    const geometry = await rail.evaluate((element) => {
      const style = window.getComputedStyle(element);
      return {
        width: element.getBoundingClientRect().width,
        position: style.position,
        top: style.top,
      };
    });
    expect(geometry.width).toBeGreaterThanOrEqual(300);
    expect(geometry.width).toBeLessThanOrEqual(340);
    expect(geometry.position).toBe('sticky');

    const shell = page.locator('.page-shell');
    await shell.evaluate((element) => {
      element.scrollTop = element.scrollHeight;
    });
    await page.waitForTimeout(100);
    const railBox = await rail.evaluate((element) => {
      const box = element.getBoundingClientRect();
      return { top: box.top, bottom: box.bottom, viewport: window.innerHeight };
    });
    expect(railBox.top).toBeGreaterThanOrEqual(0);
    expect(railBox.bottom).toBeLessThanOrEqual(railBox.viewport + 1);

    // No horizontal overflow at desktop width.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(1);
  } finally {
    await request.delete(`/api/config-profiles/${id}`);
  }
});

test('view-page at 390px: single column, nothing clipped', async ({ page, request }) => {
  const stamp = Date.now();
  const id = `e2e-mobile-${stamp}`;
  await request.post('/api/config-profiles', {
    data: {
      id,
      name: id,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com' },
      owner: 'layout-team',
    },
  });
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await signInPage(page);
    await page.goto('/hub/http.request');
    await page.getByTestId(`view-profile-${id}`).click();
    await expect(page.getByTestId('profile-view-name')).toHaveText(id);

    // One column: the rail precedes the content and unsticks.
    const columns = await page.evaluate(() => {
      const grid = document.querySelector('.profile-view-grid');
      const rail = document.querySelector('.profile-view-rail');
      const content = document.querySelector('.profile-view-content');
      if (!grid || !rail || !content) return null;
      return {
        tracks: window.getComputedStyle(grid).gridTemplateColumns.split(' ').length,
        railStatic: window.getComputedStyle(rail).position,
        railBeforeContent: rail.compareDocumentPosition(content) === Node.DOCUMENT_POSITION_FOLLOWING,
      };
    });
    expect(columns).not.toBeNull();
    expect(columns!.tracks).toBe(1);
    expect(columns!.railStatic).toBe('static');
    expect(columns!.railBeforeContent).toBe(true);

    // No horizontal overflow and no clipped values.
    const overflow = await page.evaluate(() => {
      const shell = document.querySelector('.page-shell');
      if (!shell) return null;
      return shell.scrollWidth - shell.clientWidth;
    });
    expect(overflow).not.toBeNull();
    expect(overflow!).toBeLessThanOrEqual(1);
  } finally {
    await request.delete(`/api/config-profiles/${id}`);
  }
});
