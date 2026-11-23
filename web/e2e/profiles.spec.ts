import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, openProfileTab } from './helpers';

// Configuration Hub e2e: profiles attach to nodes in the editor, the
// effective-config preview tells profile values from node overrides, and
// publish resolves the snapshot inline (plan §18 D4). The API surface is
// proven by Go tests; this spec is the user path.

function httpWorkflow(id: string) {
  return {
    id,
    name: `Profiles ${id}`,
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

async function createProfile(request: APIRequestContext, id: string) {
  const response = await request.post('/api/config-profiles', {
    data: {
      id,
      name: `House JSON API ${id}`,
      nodeType: 'http.request',
      description: 'Shared defaults for JSON APIs',
      config: {
        method: 'POST',
        url: 'https://api.example.com/resource',
        timeout: 30,
        successCodes: '2xx',
      },
      retry: { maxAttempts: 4, backoffMs: 500, maxDelayMs: 8000 },
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
  // Profiles carry the e2e- prefix and are removed by name so the suite
  // never accumulates them.
  const list = await request.get('/api/config-profiles');
  if (list.ok()) {
    const { profiles } = (await list.json()) as { profiles: { id: string; usageCount?: number }[] };
    for (const profile of profiles ?? []) {
      if (!profile.id.startsWith('e2e-')) continue;
      const del = await request.delete(`/api/config-profiles/${profile.id}`);
      // In-use profiles (published references) must survive; the test
      // that pinned them cleans its own workflows first via the sweep.
      expect([204, 409]).toContain(del.status());
    }
  }
});

test('inspector attaches a profile and previews the effective config', async ({ page, request }) => {
  const profile = await createProfile(request, `e2e-profile-${Date.now()}`);
  const id = `e2e-profiles-${Date.now()}`;
  const seed = await request.post('/api/workflows', { data: httpWorkflow(id) });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  // The http node offers profiles; the trigger node does not. Profile
  // identity and its picker live in the summary, not a tab.
  await page.locator('.loom-node').nth(0).click();
  await expect(page.getByTestId('profile-section')).toHaveCount(0);
  await page.locator('.loom-node').nth(1).click();
  await expect(page.getByTestId('profile-section')).toBeVisible();

  // Attach the profile.
  await page.locator('#node-profile-ref').selectOption(profile.id);

  // The preview separates profile values from this node's own overrides:
  // successCodes exists only in the profile; timeout exists in both, so the
  // node's own value (60) wins and is tagged as the node's.
  const preview = page.getByTestId('profile-preview');
  await expect(preview).toBeVisible();
  const row = (key: string) =>
    preview.locator('.profile-preview-row', { has: page.locator('code', { hasText: key }) });
  await expect(row('successCodes').locator('[data-origin="inherited"]')).toBeVisible();
  await expect(row('timeout').locator('[data-origin="local"]')).toBeVisible();
  await expect(row('timeout')).toContainText('60');

  // Saving persists the reference on the node.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const doc = await request.get(`/api/workflows/${id}`);
  const wf = (await doc.json()) as { nodes: { profileRef?: string }[] };
  expect(wf.nodes[1].profileRef).toBe(profile.id);

  // Reload: the attachment survives a fresh editor session.
  await page.reload();
  await page.locator('.loom-node').nth(1).click();
  await expect(page.locator('#node-profile-ref')).toHaveValue(profile.id);
});

// v4.1 E4: profile parameter rows fill around the node's own rows per
// name, and the inspector preview badges each merged row's origin.
test('inspector merges profile parameter rows per name with origin badges', async ({ page, request }) => {
  const stamp = Date.now();
  const created = await request.post('/api/config-profiles', {
    data: {
      name: `E2E Param Rows ${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'POST',
        url: 'https://api.example.com/resource',
        queryFields: [
          { name: 'limit', mode: 'static', type: 'number', value: 50 },
          { name: 'active', mode: 'static', type: 'boolean', value: true },
        ],
      },
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const profile = (await created.json()) as { id: string };

  const id = `e2e-param-rows-${stamp}`;
  const wf = httpWorkflow(id);
  wf.nodes[1].profileRef = profile.id;
  wf.nodes[1].config = {
    method: 'POST',
    url: 'https://api.example.com/resource',
    queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 25 }],
  };
  const seed = await request.post('/api/workflows', { data: wf });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  await page.locator('.loom-node').nth(1).click();
  await expect(page.getByTestId('profile-section')).toBeVisible();

  // The preview shows one row per merged parameter with its origin: the
  // node's limit wins, the profile's active fills in.
  const preview = page.getByTestId('profile-preview');
  await expect(preview).toBeVisible();
  const row = (key: string) =>
    preview.locator('.profile-preview-row', { has: page.locator('code', { hasText: key }) });
  await expect(row('queryFields.limit').locator('[data-origin="local"]')).toBeVisible();
  await expect(row('queryFields.limit')).toContainText('25');
  await expect(row('queryFields.active').locator('[data-origin="inherited"]')).toBeVisible();
});

test('publish resolves the profile inline into the version snapshot', async ({ page, request }) => {
  const profile = await createProfile(request, `e2e-pub-${Date.now()}`);
  const id = `e2e-pub-${Date.now()}`;
  const wf = httpWorkflow(id);
  wf.nodes[1].profileRef = profile.id;
  wf.nodes[1].config = { timeout: 60 };
  const seed = await request.post('/api/workflows', { data: wf });
  expect(seed.status(), await seed.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();

  // The sparse node saves cleanly because required fields arrive via the
  // profile (effective-config validation).
  await page.getByRole('button', { name: 'Publish', exact: true }).click();
  await expect(page.getByText(/Published v1\./)).toBeVisible();

  // The version snapshot carries the resolved config: profile fields with
  // the node's timeout override on top.
  const version = await request.get(`/api/workflows/${id}/versions/1`);
  expect(version.status()).toBe(200);
  const snap = (await version.json()) as {
    nodes: { config: Record<string, unknown>; retry?: { maxAttempts?: number } }[];
  };
  const config = snap.nodes[1].config;
  expect(config.method).toBe('POST');
  expect(config.url).toBe('https://api.example.com/resource');
  expect(config.timeout).toBe(60);
  // The profile-level retry policy lands in the snapshot for a node that
  // has none of its own.
  expect(snap.nodes[1].retry?.maxAttempts).toBe(4);

  // Usage pin blocks deletion of the referenced profile.
  const del = await request.delete(`/api/config-profiles/${profile.id}`);
  expect(del.status()).toBe(409);
});

test('profile picker hides for node types without profileable fields', async ({ page, request }) => {
  const id = `e2e-noprofile-${Date.now()}`;
  const seed = await request.post('/api/workflows', { data: httpWorkflow(id) });
  expect(seed.status(), await seed.text()).toBe(201);
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.locator('.loom-node').nth(0).click();
  await expect(page.getByTestId('profile-section')).toHaveCount(0);
});

// Configuration Hub v4: the dedicated profile surfaces. The landing lists
// profileable node types, each type owns its list page (kebab row actions,
// delete confirm), and create/edit share one typed form seeded from the
// catalog with mandatory-field enforcement (plan D1-A/D3-A/D4-A/D5-B).
test('dedicated profile pages manage profiles end to end', async ({ page }) => {
  // The landing groups node types by category.
  await page.goto('/profiles');
  await expect(page.getByRole('heading', { name: 'Profiles', exact: true })).toBeVisible();
  await page.getByTestId('profiles-type-http.request').click();
  await expect(page.getByRole('heading', { name: 'HTTP request', exact: true })).toBeVisible();

  // The type page offers the dedicated create form.
  await page.getByTestId('new-profile').click();
  await expect(page.getByRole('heading', { name: 'New HTTP request profile' })).toBeVisible();

  // D5-B: a name alone does not pass; at least one field is required.
  await page.getByTestId('profile-name').fill('E2E Typed House');
  await page.getByTestId('profile-save').click();
  await expect(page.getByTestId('profile-form-error')).toContainText('at least one');

  // UX Phase 2 D2-2: the typed fields live on the tabs — method/url on
  // Node defaults, the parameter-group row editors on Data, identity and
  // governance metadata on Basics.
  await openProfileTab(page, 'defaults');
  await page.getByTestId('profile-field-method').selectOption('POST');
  await page.getByTestId('profile-field-url').fill('https://api.example.com/typed');

  // v4.1 E4 (now on the Data tab): the typed parameter-group row
  // editors; a query row saves as part of the config. The group hints
  // teach the URL rule: query values append, path values need a
  // {name}/:name placeholder.
  await openProfileTab(page, 'data');
  await expect(page.getByText('appended to the URL query automatically')).toBeVisible();
  await expect(page.getByText('never appended automatically')).toBeVisible();
  await page.getByTestId('profile-queryFields-add').click();
  await page.getByTestId('profile-queryFields-row-0-name').fill('limit');
  await page.getByTestId('profile-queryFields-row-0-type').selectOption('number');
  await page.getByTestId('profile-queryFields-row-0-value').fill('25');
  await openProfileTab(page, 'identity');
  await page.locator('#profile-owner').fill('payments-team');
  await page.locator('#profile-tags').fill('payments, prod');
  await page.getByTestId('profile-save').click();

  // Creating lands on the edit page of the new profile at v1, with the
  // typed values seeded back into the form (edit is the same surface;
  // the edit page opens on Identity).
  await expect(page.getByTestId('profile-version-pill')).toHaveText('v1');
  await openProfileTab(page, 'defaults');
  await expect(page.getByTestId('profile-field-method')).toHaveValue('POST');
  await expect(page.getByTestId('profile-field-url')).toHaveValue('https://api.example.com/typed');
  // The saved query row seeds back into the group editor on the Data tab.
  await openProfileTab(page, 'data');
  await expect(page.getByTestId('profile-queryFields-row-0-name')).toHaveValue('limit');
  await expect(page.getByTestId('profile-queryFields-row-0-value')).toHaveValue('25');

  // The advanced disclosure on Node defaults holds the ranged inputs;
  // editing saves v2.
  await openProfileTab(page, 'defaults');
  await page.locator('.advanced-fields summary').click();
  await page.getByTestId('profile-field-timeout').fill('30');
  await page.getByTestId('profile-save').click();

  // After save the edit page returns to the type list, which shows the
  // row with its version, owner and tags. Rows are scoped by the exact
  // profile name so the later duplicate ("... (copy)") never matches.
  const list = page.getByTestId('profile-list');
  const exactRow = (name: string) =>
    list.locator('.dashboard-run-row').filter({ has: page.getByText(name, { exact: true }) });
  const row = exactRow('E2E Typed House');
  await expect(row).toHaveCount(1);
  await expect(row.first()).toContainText('v2');
  await expect(row.first().locator('[data-testid="profile-tags"]')).toContainText('payments');

  // Row actions: Edit stays visible, the rest live in the kebab menu. The
  // menu is a <details> that stays open after an action, so each helper
  // opens it only when the wanted button is not already visible.
  const menuAction = async (name: string) => {
    const button = row.first().getByRole('button', { name, exact: true });
    if (!(await button.isVisible().catch(() => false))) {
      await row.first().locator('.action-menu summary').click();
    }
    await button.click();
  };
  await menuAction('Duplicate');
  const copyRow = exactRow('E2E Typed House (copy)');
  await expect(copyRow).toHaveCount(1);

  // The testable-type Test action reports through the banner.
  await menuAction('Test');
  await expect(page.getByTestId('profile-test-result')).toBeVisible();

  // History opens the shared diff dialog from the list page (D2-C).
  await menuAction('History');
  const history = page.getByTestId('profile-history-body');
  await expect(history).toBeVisible();
  await history.getByRole('button', { name: 'Show version 2' }).click();
  await expect(page.getByTestId('profile-version-diff')).toContainText('config.timeout');
  await page.getByRole('button', { name: 'Close history' }).click();

  // Delete confirms before it acts (D3-A).
  await menuAction('Delete');
  await expect(page.getByTestId('confirm-dialog-backdrop')).toBeVisible();
  await page.getByTestId('confirm-dialog-confirm').click();
  await expect(exactRow('E2E Typed House')).toHaveCount(0);

  // The edit page carries history under Trust (Phase 4 D4-1),
  // sharing the list page's diff component with per-version Diff |
  // Full-config views on the copy's v1.
  await copyRow.first().getByRole('button', { name: 'Edit', exact: true }).click();
  await expect(page.getByTestId('profile-version-pill')).toHaveText('v1');
  await openProfileTab(page, 'history');
  await expect(page.getByTestId('profile-panel-history')).toBeVisible();
  await page.getByRole('button', { name: 'Show version 1' }).click();
  await expect(page.getByTestId('profile-version-diff')).toContainText('config.method');
  // The Full-config tab shows the whole version as read-only JSON.
  await page.getByTestId('profile-version-tab-full').click();
  await expect(page.getByTestId('profile-version-full')).toContainText('"method": "POST"');
  await page.getByTestId('profile-version-tab-diff').click();
  await expect(page.getByTestId('profile-version-diff')).toBeVisible();
});

// Governance through the dedicated surfaces: metadata renders on the
// type page, the usage graph answers who pins the profile, and version
// diffs show what a save changed.
test('governance: tags, owner, usage graph and version diff', async ({ page, request }) => {
  // Create a profile with governance metadata through the typed form.
  await page.goto(`/hub/${encodeURIComponent('http.request')}/new`);
  await page.getByTestId('profile-name').fill('Governed API');
  await openProfileTab(page, 'defaults');
  await page.getByTestId('profile-field-method').selectOption('POST');
  await page.getByTestId('profile-field-url').fill('https://api.example.com/x');
  await openProfileTab(page, 'identity');
  await page.locator('#profile-owner').fill('payments-team');
  await page.locator('#profile-tags').fill('payments, prod');
  await page.getByTestId('profile-save').click();
  await expect(page.getByTestId('profile-version-pill')).toHaveText('v1');

  const profileList = await request.get('/api/config-profiles');
  const { profiles } = (await profileList.json()) as { profiles: { id: string; name: string }[] };
  const profile = profiles.find((p) => p.name === 'Governed API');

  // Publish a workflow that pins it, then read the usage graph from the
  // type page (the publish happened behind the page's back).
  const wfId = `e2e-gov-${Date.now()}`;
  const wf = httpWorkflow(wfId);
  wf.nodes[1].profileRef = profile.id;
  wf.nodes[1].config = {};
  const seed = await request.post('/api/workflows', { data: wf });
  expect(seed.status(), await seed.text()).toBe(201);
  const publish = await request.post(`/api/workflows/${wfId}/publish`);
  expect(publish.status(), await publish.text()).toBe(201);

  await page.goto(`/hub/${encodeURIComponent('http.request')}`);
  const row = page.locator('.dashboard-run-row', { hasText: 'Governed API' });
  await expect(row).toHaveCount(1);
  await expect(row.locator('[data-testid="profile-tags"]')).toContainText('payments');
  await expect(row).toContainText('payments-team');
  await expect(row).toContainText('used by 1');

  await row.locator('.action-menu summary').click();
  await row.getByRole('button', { name: /Usage|Hide usage/ }).click();
  const usage = page.getByTestId('profile-usage');
  await expect(usage).toBeVisible();
  await expect(usage).toContainText(`Profiles ${wfId}`);
  await expect(usage).toContainText('pinned profile v1');

  // Edit to v2 with a changed field, then diff v2 against v1.
  await row.getByRole('button', { name: 'Edit', exact: true }).click();
  await openProfileTab(page, 'defaults');
  await page.getByTestId('profile-field-method').selectOption('PUT');
  await page.locator('.advanced-fields summary').click();
  await page.getByTestId('profile-field-timeout').fill('30');
  await page.getByTestId('profile-save').click();

  await page.goto(`/hub/${encodeURIComponent('http.request')}`);
  const row2 = page.locator('.dashboard-run-row', { hasText: 'Governed API' });
  await row2.locator('.action-menu summary').click();
  await row2.getByRole('button', { name: 'History' }).click();
  const history = page.getByTestId('profile-history-body');
  await expect(history).toBeVisible();
  await history.getByRole('button', { name: 'Show version 2' }).click();
  const diff = page.getByTestId('profile-version-diff');
  await expect(diff).toBeVisible();
  await expect(diff).toContainText('config.method');
  await expect(diff).toContainText('config.timeout');
  await page.getByRole('button', { name: 'Close history' }).click();
  await expect(page.getByTestId('profile-history-body')).toBeHidden();
});
