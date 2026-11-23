import { expect, test, type Page, type Route } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { expectAssetHistoryDialog } from './helpers';

// UX Phase 2 D2-4 (plan P1-4): the edit page's History tab deepens the
// shared version diff — parameter-group rows diff per row name, bundle
// ref changes resolve to asset names, and changed refs carry a content
// sub-diff of the referenced mapping/contract.

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

test('the History tab deepens diffs: per-row params, named bundle refs, content sub-diffs', async ({
  page,
  request,
}) => {
  const stamp = Date.now();

  const mappingA = await (
    await request.post('/api/mappings', {
      data: {
        name: `e2e-hist-map-a-${stamp}`,
        kind: 'query',
        rows: [{ target: 'limit', source: '20' }],
      },
    })
  ).json();
  const mappingB = await (
    await request.post('/api/mappings', {
      data: {
        name: `e2e-hist-map-b-${stamp}`,
        kind: 'query',
        rows: [{ target: 'limit', source: '50' }],
      },
    })
  ).json();

  // v1: bundle pins mapping A, one query row.
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-hist-profile-${stamp}`,
        nodeType: 'http.request',
        config: {
          method: 'GET',
          url: 'https://api.example.com/h',
          queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 20 }],
        },
        bundle: { mappings: { query: mappingA.id } },
      },
    })
  ).json();

  // v2: the bundle moves to mapping B and the row's value changes.
  await request.put(`/api/config-profiles/${created.id}`, {
    data: {
      name: `e2e-hist-profile-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'GET',
        url: 'https://api.example.com/h',
        queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 50 }],
      },
      bundle: { mappings: { query: mappingB.id } },
      version: 1,
    },
  });

  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(created.id)}/edit?tab=history`);
  await expect(page.getByTestId('profile-panel-history')).toBeVisible();
  await page.getByRole('button', { name: 'Show version 2' }).click();

  const diff = page.getByTestId('profile-version-diff');

  // Parameter-group rows diff per row name, not as array blobs.
  const rowLine = diff.locator('tr', { has: page.locator('th', { hasText: 'config.queryFields.limit' }) });
  await expect(rowLine).toHaveClass(/diff-changed/);
  await expect(rowLine).toContainText('20');
  await expect(rowLine).toContainText('50');

  // Bundle ref rows resolve to asset names.
  const bundleLine = diff.locator('tr', {
    has: page.locator('th', { hasText: 'bundle.mappings.query' }),
  });
  await expect(bundleLine).toContainText(`e2e-hist-map-a-${stamp} (v1)`);
  await expect(bundleLine).toContainText(`e2e-hist-map-b-${stamp} (v1)`);

  // The changed ref carries a content sub-diff of both sides' current
  // mapping content.
  const subDiff = page.getByTestId('profile-bundle-subdiff');
  await expect(subDiff).toContainText('query mapping');
  await expect(subDiff.locator('tr', { has: page.locator('th', { hasText: 'limit' }) })).toHaveClass(
    /diff-changed/,
  );
  await expect(subDiff).toContainText('20');
  await expect(subDiff).toContainText('50');
});

test('the history dialog is a wide shell: close pinned top-right, body scrolls', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-hist-dialog-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://api.example.com/h' },
      },
    })
  ).json();

  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(created.id)}`);
  await page.getByTestId('profile-view-history').click();

  const backdrop = page.getByTestId('profile-history-backdrop');
  await expect(backdrop).toBeVisible();
  // Wide shell, close top-right inside the head, body without
  // horizontal clipping.
  await expectAssetHistoryDialog(backdrop.locator('.dialog'));
  await expect(backdrop.getByTestId('profile-history-body')).toBeVisible();
  await expect(backdrop.getByTestId('profile-version-list')).toContainText('v1');

  await backdrop.getByRole('button', { name: 'Close history' }).click();
  await expect(backdrop).toHaveCount(0);
});

test('history diff content: section headings, sub-diff separation, no horizontal overflow', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const mapping = await (
    await request.post('/api/mappings', {
      data: { name: `e2e-hist2-map-${stamp}`, kind: 'query', rows: [{ target: 'limit', source: '20' }] },
    })
  ).json();
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-hist2-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://api.example.com/h2' },
        bundle: { mappings: { query: mapping.id } },
        version: 1,
      },
    })
  ).json();

  try {
    await signInPage(page);
    await page.goto(`/hub/http.request/${encodeURIComponent(created.id)}/edit?tab=history`);
    await expect(page.getByTestId('profile-panel-history')).toBeVisible();
    await page.getByRole('button', { name: 'Show version 1' }).click();
    const diff = page.getByTestId('profile-version-diff');

    // The diff section carries a visible heading.
    const heading = page.locator('.profile-diff > h3');
    await expect(heading).toBeVisible();
    await expect(heading).toContainText('Initial version v1');

    // The bundle sub-diff reads as a subordinate section with its own
    // heading, separated by the dashed divider.
    const subDiff = page.getByTestId('profile-bundle-subdiff');
    await expect(subDiff.locator('> h4')).toBeVisible();
    expect(await subDiff.evaluate((element) => window.getComputedStyle(element).borderTopStyle)).toBe('dashed');

    // Long values wrap inside the scroll owner: no horizontal overflow.
    const overflow = await page.evaluate(() => {
      const container = document.querySelector('.page-shell');
      return container ? container.scrollWidth - container.clientWidth : null;
    });
    expect(overflow).not.toBeNull();
    expect(overflow!).toBeLessThanOrEqual(1);

    // Both tabs still render; the full-config tab keeps its wrapper.
    await page.getByTestId('profile-version-tab-full').click();
    await expect(page.getByTestId('profile-version-full')).toBeVisible();
    await page.getByTestId('profile-version-tab-diff').click();
    await expect(diff).toBeVisible();
    expect(await diff.count()).toBe(1);
  } finally {
    await request.delete(`/api/config-profiles/${created.id}`);
    await request.delete(`/api/mappings/${mapping.id}`);
  }
});

// U6: secondary history lookups report failures instead of reading as
// empty content or a missing previous version.
test('history secondary failures: named errors, not empty diffs or a false "initial version"', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const mappingA = await (
    await request.post('/api/mappings', {
      data: { name: `e2e-hist3-map-a-${stamp}`, kind: 'query', rows: [{ target: 'limit', source: '20' }] },
    })
  ).json();
  const mappingB = await (
    await request.post('/api/mappings', {
      data: { name: `e2e-hist3-map-b-${stamp}`, kind: 'query', rows: [{ target: 'limit', source: '50' }] },
    })
  ).json();

  // v1: bundle pins mapping A; v2 moves the bundle to mapping B.
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-hist3-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://api.example.com/h3' },
        bundle: { mappings: { query: mappingA.id } },
      },
    })
  ).json();
  await request.put(`/api/config-profiles/${created.id}`, {
    data: {
      name: `e2e-hist3-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/h3' },
      bundle: { mappings: { query: mappingB.id } },
      version: 1,
    },
  });

  // The failure routes are armed only after the app has booted: the
  // /api/mappings index is part of the boot sequence, so the history
  // dialog is opened client-side from the view page instead of a fresh
  // edit-page navigation.
  const fail = (body: string) => (route: Route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: body }),
    });

  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(created.id)}`);
  await expect(page.getByTestId('profile-view-name')).toBeVisible();

  // The name lookups, the previous version's fetch and the v1 mapping's
  // sub-diff read all fail; the primary loads still succeed.
  await page.route('**/api/mappings', fail('names boom'));
  await page.route(`**/api/mappings/${mappingA.id}`, fail('mapping boom'));
  await page.route(`**/api/config-profiles/${created.id}/versions/1`, fail('version boom'));

  await page.getByTestId('profile-view-history').click();
  const dialog = page.getByTestId('profile-history-backdrop');
  await expect(dialog).toBeVisible();

  // The failed name lookups announce; bundle rows fall back to raw ids.
  await expect(page.getByTestId('profile-history-names-error')).toBeVisible();
  await expect(page.getByTestId('profile-history-names-error')).toContainText(
    'Could not load mapping and data-contract names',
  );

  // The failed previous version is a named error under a neutral heading —
  // not a false "Initial version".
  await page.getByRole('button', { name: 'Show version 2' }).click();
  await expect(page.getByTestId('profile-history-previous-error')).toBeVisible();
  await expect(page.locator('.profile-diff > h3')).toHaveText('Version v2');

  // With the previous version readable again, the bundle sub-diff fetches
  // both mappings; v1's failing read is named, not a silent empty. (A
  // failed previous version never fetches mapping A at all, so this phase
  // needs the previous version back.)
  await page.unroute(`**/api/config-profiles/${created.id}/versions/1`);
  await page.getByRole('button', { name: 'Show version 2' }).click();
  await expect(page.getByTestId('profile-history-previous-error')).toHaveCount(0);
  await expect(page.getByTestId('profile-bundle-subdiff-error')).toContainText(
    'Could not load the query mapping',
  );
});
