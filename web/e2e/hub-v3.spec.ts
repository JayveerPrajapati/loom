import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Configuration Hub v3 e2e: node-type hub pages, the shared parameter
// group editor in the Inspector, and the navbar entry. The API surface is
// proven by Go tests; this spec is the user path.

function httpWorkflow(id: string) {
  return {
    id,
    name: `Hub v3 ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'http',
        type: 'http.request',
        typeVersion: 1,
        name: 'Fetch',
        position: { x: 260, y: 0 },
        config: { method: 'GET', url: 'https://api.example.com/one', timeout: 60 },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
  };
}

async function createMapping(request: APIRequestContext, id: string, kind: string) {
  const response = await request.post('/api/mappings', {
    data: {
      id,
      name: `Paging ${id}`,
      kind,
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string; version: number };
}

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  const list = await request.get('/api/mappings');
  if (list.ok()) {
    const { mappings } = (await list.json()) as { mappings: { id: string }[] };
    for (const mapping of mappings ?? []) {
      if (!mapping.id.startsWith('e2e-')) continue;
      const del = await request.delete(`/api/mappings/${mapping.id}`);
      expect([204, 409]).toContain(del.status());
    }
  }
});

test('navbar links to the profiles landing with advanced assets one link away', async ({ page }) => {
  await page.goto('/workflows');
  await page.getByRole('link', { name: 'Profiles' }).click();

  // UX Phase 2 D2-1: the landing is profiles-first; the HTTP card opens
  // the type's merged page. Per-type mapping counts stay on that page
  // (asserted by the merged-page test below).
  const landing = page.locator('[data-testid="profiles-type-http.request"]');
  await expect(landing).toBeVisible();
  await expect(landing).toContainText('HTTP request');
  await expect(page.getByRole('link', { name: 'Profiles' })).toHaveClass(/active/);
  // The retired hub landing redirects to /profiles.
  await page.goto('/hub');
  await expect(page).toHaveURL(/\/profiles$/);
  await expect(landing).toBeVisible();

  // Data contracts and mappings are the advanced-assets group — one
  // kebab in the header instead of four sibling buttons.
  await page.getByTestId('advanced-assets').locator('summary').click();
  await page.getByRole('button', { name: 'Data contracts' }).click();
  await expect(page).toHaveURL(/\/schemas$/);
  await page.goto('/profiles');
  await page.getByTestId('advanced-assets').locator('summary').click();
  await page.getByRole('button', { name: 'Mappings' }).click();
  await expect(page).toHaveURL(/\/mappings$/);
});

test('merged hub node-type page lists profiles and related-asset counts', async ({ page, request }) => {
  const stamp = Date.now();
  await createMapping(request, `e2e-headers-${stamp}`, 'headers');
  await request.post('/api/config-profiles', {
    data: {
      id: `e2e-hub-profile-${stamp}`,
      name: 'House API',
      nodeType: 'http.request',
      config: { method: 'POST' },
    },
  });

  await page.goto('/hub/http.request');
  await expect(page.getByRole('heading', { name: 'HTTP request', exact: true })).toBeVisible();

  const profilesPanel = page.locator('section[aria-label="Profiles for HTTP request"]');
  await expect(profilesPanel.getByText('House API')).toBeVisible();
  await expect(profilesPanel.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();

  const related = page.locator('section[aria-label="Related assets for this node type"]');
  await expect(related).toContainText('1 mapping');
  await expect(related).toContainText('headers');
  await expect(related).toContainText('data contract');
});

test('hub stat rows deep-link to filtered mappings management', async ({ page, request }) => {
  const stamp = Date.now();
  await createMapping(request, `e2e-query-${stamp}`, 'query');
  await request.post('/api/mappings', {
    data: {
      id: `e2e-item-${stamp}`,
      name: `Item rows ${stamp}`,
      kind: 'item',
      rows: [{ target: 'out', source: 'value' }],
    },
  });

  await page.goto('/hub/http.request');
  await page.getByTestId('hub-manage-mappings').click();
  await expect(page).toHaveURL(/\/mappings\?nodeType=http\.request$/);

  // The notice names the filter; the server narrows the list to the
  // kinds this type consumes, so the item-kind mapping stays hidden.
  await expect(page.getByTestId('mapping-node-type-filter')).toContainText('http.request');
  await expect(page.getByText(`Paging e2e-query-${stamp}`)).toBeVisible();
  await expect(page.getByText(`Item rows ${stamp}`)).toHaveCount(0);
});

test('inspector edits HTTP parameters with typed rows and dual mode', async ({ page, request }) => {
  const id = `e2e-hubv3-${Date.now()}`;
  await request.post('/api/workflows', { data: httpWorkflow(id) });

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(1).click();

  // The parameter groups live on the inspector's Data tab (Phase 3).
  await page.getByTestId('inspector-tab-data').click();
  const groups = page.getByTestId('http-param-groups');
  await expect(groups).toBeVisible();
  await expect(groups.getByTestId('param-group-query')).toBeVisible();
  await expect(groups.getByTestId('param-group-headers')).toBeVisible();
  await expect(groups.getByTestId('param-group-pathParams')).toBeVisible();
  // The group hint states how the group reaches the request: query values
  // are appended; path values replace {name}/:name placeholders.
  await expect(groups.getByTestId('param-group-query')).toContainText(
    'Appended to the URL query string automatically',
  );
  await expect(groups.getByTestId('param-group-pathParams')).toContainText(
    'Path values are never appended automatically',
  );
  // The body group appears only for JSON bodies.
  await expect(groups.getByTestId('param-group-body')).toHaveCount(0);

  // Add a static number row and an expression row.
  await groups.getByTestId('param-query-add').click();
  await groups.getByTestId('param-query-row-0-name').fill('limit');
  await groups.getByTestId('param-query-row-0-type').selectOption('number');
  await groups.getByTestId('param-query-row-0-value').fill('20');
  await groups.getByTestId('param-query-add').click();
  await groups.getByTestId('param-query-row-1-name').fill('offset');
  await groups.getByTestId('param-query-row-1-remove').click();

  // Dual mode: switching to JSON compiles the rows into the raw object.
  await groups.getByRole('button', { name: 'JSON', exact: true }).first().click();
  await expect(groups.getByTestId('param-query-json')).toHaveValue(/^\s*{\s*"limit":\s*20\s*}\s*$/);
  await groups.getByRole('button', { name: 'Fields', exact: true }).first().click();
  await expect(groups.getByTestId('param-query-row-0-value')).toHaveValue('20');

  // Saving persists the rows on the node.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const doc = await request.get(`/api/workflows/${id}`);
  const wf = (await doc.json()) as {
    nodes: { config?: Record<string, unknown> }[];
  };
  const rows = wf.nodes[1].config?.queryFields as { name: string; mode: string; type?: string; value: unknown }[];
  expect(rows).toEqual([{ name: 'limit', mode: 'static', type: 'number', value: 20 }]);
});

test('inspector saves a query group as a mapping and attaches it', async ({ page, request }) => {
  const id = `e2e-hubv3-save-${Date.now()}`;
  await request.post('/api/workflows', { data: httpWorkflow(id) });

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(1).click();
  // The parameter groups live on the inspector's Data tab (Phase 3).
  await page.getByTestId('inspector-tab-data').click();

  const groups = page.getByTestId('http-param-groups');
  await groups.getByTestId('param-query-add').click();
  await groups.getByTestId('param-query-row-0-name').fill('limit');
  await groups.getByTestId('param-query-row-0-type').selectOption('number');
  await groups.getByTestId('param-query-row-0-value').fill('20');
  await groups.getByTestId('save-as-mapping-trigger-query').click();
  await groups.locator('.save-as-mapping input[aria-label="New mapping name"]').fill('E2E saved query');
  await groups.getByRole('button', { name: 'Save', exact: true }).click();

  await expect(page.getByTestId('param-group-notice')).toContainText('E2E saved query');
  await expect(page.locator('#mapping-ref-query')).toHaveValue(/e2e-|mapping/);

  // Persist, then verify the saved mapping and its kind through the API.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const doc = await request.get(`/api/workflows/${id}`);
  const wf = (await doc.json()) as { nodes: { config?: Record<string, unknown> }[] };
  const refs = wf.nodes[1].config?.mappingRefs as Record<string, string> | undefined;
  expect(refs?.query).toBeTruthy();
  const mapping = (await (
    await request.get(`/api/mappings/${refs!.query}`)
  ).json()) as { kind: string; rows: { target: string; source: string }[] };
  expect(mapping.kind).toBe('query');
  expect(mapping.rows).toEqual([{ target: 'limit', source: '20' }]);
});

test('merged page scopes profiles per type and legacy /profiles URLs redirect', async ({ page, request }) => {
  const stamp = Date.now();
  await request.post('/api/config-profiles', {
    data: {
      id: `e2e-filter-${stamp}`,
      name: 'Filter me',
      nodeType: 'http.request',
      config: { method: 'POST' },
    },
  });
  await request.post('/api/config-profiles', {
    data: {
      id: `e2e-filter-pg-${stamp}`,
      name: 'Not this one',
      nodeType: 'db.postgres',
      config: { operation: 'query' },
    },
  });

  // The merged page's profile list is scoped to that node type by
  // construction.
  await page.goto(`/hub/${encodeURIComponent('http.request')}`);
  const list = page.getByTestId('profile-list');
  await expect(list.getByText('Filter me')).toBeVisible();
  await expect(list.getByText('Not this one')).toHaveCount(0);

  // The pre-merge URL family redirects in place to the /hub page.
  await page.goto(`/profiles/${encodeURIComponent('http.request')}`);
  await expect(page).toHaveURL(/\/hub\/http\.request$/);
  await expect(page.getByTestId('profile-list').getByText('Filter me')).toBeVisible();

  // The landing card for another type opens that type's merged page.
  await page.goto('/profiles');
  await page.getByTestId('profiles-type-db.postgres').click();
  await expect(page).toHaveURL(/\/hub\/db\.postgres$/);
  await expect(page.getByTestId('profile-list').getByText('Not this one')).toBeVisible();
  await expect(page.getByTestId('profile-list').getByText('Filter me')).toHaveCount(0);
});

test('hub list: one inset, the row opens the profile, the kebab does not', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const profileId = `e2e-hub-affordance-${stamp}`;
  await request.post('/api/config-profiles', {
    data: {
      id: profileId,
      name: profileId,
      nodeType: 'http.request',
      config: { method: 'GET' },
    },
  });
  try {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/hub/http.request');
    const row = page.getByTestId('profile-list').locator('.dashboard-run-row').filter({
      hasText: profileId,
    });
    await expect(row).toBeVisible();

    // One inset: heading, search and row share the same left edge.
    const lefts = await page.evaluate(() => {
      const head = document.querySelector(
        '.hub-profiles-panel .dashboard-panel-head h2',
      ) as HTMLElement;
      const search = document.querySelector('.hub-profiles-panel .search-input') as HTMLElement;
      const rowEl = document.querySelector('.hub-profile-list .dashboard-run-row') as HTMLElement;
      return {
        head: head.getBoundingClientRect().left,
        search: search.getBoundingClientRect().left,
        row: rowEl.getBoundingClientRect().left,
      };
    });
    expect(Math.abs(lefts.head - lefts.row)).toBeLessThanOrEqual(1);
    expect(Math.abs(lefts.search - lefts.row)).toBeLessThanOrEqual(1);

    // Clicking the row body (not the kebab) opens the profile view. The
    // stretched link covers the row, so the click is dispatched at raw
    // coordinates like a real pointer would.
    const body = await row.locator('.dashboard-run-main').boundingBox();
    expect(body).not.toBeNull();
    await page.mouse.click(body!.x + 24, body!.y + body!.height / 2);
    await expect(page.getByTestId('profile-view-name')).toHaveText(profileId);
    await expect(page).toHaveURL(new RegExp(`/hub/http.request/${profileId}/?$`));

    // The kebab opens History without navigating anywhere.
    await page.goto('/hub/http.request');
    await row.locator('.dashboard-run-meta details.action-menu summary').click();
    await row.locator('.action-menu-list button', { hasText: 'History' }).click();
    await expect(page.getByTestId('profile-history-body')).toBeVisible();
    await expect(page).toHaveURL(/\/hub\/http\.request$/);
    await page.getByRole('button', { name: 'Close history' }).click();

    // Keyboard: the stretched link, then Edit, then the kebab stay
    // reachable in that order.
    await row.locator('.hub-row-link').focus();
    await page.keyboard.press('Tab');
    await expect(row.getByRole('button', { name: 'Edit', exact: true })).toBeFocused();
    await page.keyboard.press('Tab');
    await expect(row.locator('details.action-menu summary')).toBeFocused();

    // Row meta values sit on the same track across rows.
    const metaLefts = await page.evaluate(() => {
      const lists = [...document.querySelectorAll('.hub-profile-list .meta-list')];
      return lists.map((list) => list.getBoundingClientRect().left);
    });
    expect(metaLefts.length).toBeGreaterThanOrEqual(1);
    for (const left of metaLefts) {
      expect(Math.abs(left - metaLefts[0])).toBeLessThanOrEqual(1);
    }
  } finally {
    await request.delete(`/api/config-profiles/${profileId}`);
  }
});
