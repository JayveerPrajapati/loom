import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, openProfileTab } from './helpers';

// Phase 4 acceptance: the profile editor is three primary areas with a
// sticky status/save bar, a compact change summary, and included assets
// presented as one "Included behavior" section instead of a bundle.

async function createProfile(
  request: APIRequestContext,
  name: string,
  nodeType = 'http.request',
  config: Record<string, unknown> = {
    method: 'GET',
    url: 'https://api.example.com/editor',
  },
): Promise<{ id: string; version: number }> {
  const response = await request.post('/api/config-profiles', {
    data: { name, nodeType, config },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string; version: number };
}

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
  const profiles = await request.get('/api/config-profiles');
  if (profiles.ok()) {
    const { profiles: list } = (await profiles.json()) as { profiles: { id: string; name: string }[] };
    for (const profile of list ?? []) {
      if (!profile.name.startsWith('e2e-editor-')) continue;
      await request.delete(`/api/config-profiles/${profile.id}`);
    }
  }
  const mappings = await request.get('/api/mappings');
  if (mappings.ok()) {
    const { mappings: list } = (await mappings.json()) as { mappings: { id: string; name: string }[] };
    for (const mapping of list ?? []) {
      if (!mapping.name.startsWith('e2e-editor-')) continue;
      await request.delete(`/api/mappings/${mapping.id}`);
    }
  }
  const schemas = await request.get('/api/data-schemas');
  if (schemas.ok()) {
    const { schemas: list } = (await schemas.json()) as { schemas: { id: string; name: string }[] };
    for (const schema of list ?? []) {
      if (!schema.name.startsWith('e2e-editor-')) continue;
      await request.delete(`/api/data-schemas/${schema.id}`);
    }
  }
  const scripts = await request.get('/api/scripts');
  if (scripts.ok()) {
    const { scripts: list } = (await scripts.json()) as { scripts: { id: string; name: string }[] };
    for (const script of list ?? []) {
      if (!script.name.startsWith('e2e-editor-')) continue;
      await request.delete(`/api/scripts/${script.id}`);
    }
  }
});

test('three areas put identity, configuration and trust in one path', async ({ page }) => {
  const stamp = Date.now();
  const name = `e2e-editor-create-${stamp}`;
  await page.goto('/hub/http.request/new');

  // Three primary areas, and none of the old eight-tab ids.
  await expect(page.getByTestId('profile-tab-identity')).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('profile-tab-configuration')).toBeVisible();
  await expect(page.getByTestId('profile-tab-trust')).toBeVisible();
  await expect(page.getByTestId('profile-tab-defaults')).toHaveCount(0);

  // Identity owns the governance fields and a read-only visibility row;
  // new profiles are private until shared.
  await page.getByTestId('profile-name').fill(name);
  await expect(page.getByTestId('profile-identity-visibility')).toContainText('Private');
  await expect(page.getByTestId('profile-panel-basics')).toBeVisible();

  // Configuration exposes Defaults, Data and Included behavior; the
  // standalone Contracts section is gone.
  await page.getByTestId('profile-tab-configuration').click();
  await expect(page.getByTestId('profile-section-defaults')).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByTestId('profile-section-contracts')).toHaveCount(0);
  await page.getByTestId('profile-field-method').selectOption('POST');
  await page.getByTestId('profile-field-url').fill('https://api.example.com/created');
  await page.getByTestId('profile-retry-maxAttempts').fill('3');

  // The compact summary names what the profile changes.
  const summary = page.getByTestId('profile-changes-summary');
  await expect(summary).toContainText('This profile changes');
  await expect(page.getByTestId('profile-change-method')).toContainText('Method');
  await expect(page.getByTestId('profile-change-url')).toContainText('Url');
  await expect(page.getByTestId('profile-change-retry')).toContainText('Retry policy');

  // Included behavior is the one place for assets, with the bundle model
  // explained only in the advanced disclosure.
  await page.getByTestId('profile-section-included').click();
  const included = page.getByTestId('profile-panel-included');
  await expect(included).toBeVisible();
  await expect(included.getByRole('heading', { name: 'Included behavior' })).toBeVisible();
  await expect(page.getByTestId('profile-bundle-contracts')).toBeVisible();
  await expect(page.getByTestId('profile-bundle-script')).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Bundle mappings' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Data contracts' })).toHaveCount(0);
  await included.getByText('How this is stored').click();
  await expect(included).toContainText('bundle');

  // Trust on a create page offers Test and Preview, not History or Used by.
  await page.getByTestId('profile-tab-trust').click();
  await expect(page.getByTestId('profile-section-test')).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('profile-section-preview')).toBeVisible();
  await expect(page.getByTestId('profile-section-history')).toHaveCount(0);
  await expect(page.getByTestId('profile-section-used-by')).toHaveCount(0);

  // Save from the sticky bar creates v1 and lands on the edit page.
  await page.getByTestId('profile-save').click();
  await expect(page).toHaveURL(/\/hub\/http\.request\/profile-[^/]+\/edit$/);
  await expect(page.getByTestId('profile-sticky-meta')).toContainText(
    /v1 · (private|shared) · used by 0 workflows/,
  );
  await expect(page.getByTestId('profile-identity-visibility')).toContainText('Private');
});

test('sticky status stays visible while the form scrolls and saves a new version', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const name = `e2e-editor-sticky-${stamp}`;
  const profile = await createProfile(request, name);
  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit`);

  await expect(page.getByTestId('profile-sticky-name')).toHaveText(name);
  await expect(page.getByTestId('profile-sticky-meta')).toContainText('v1 ·');
  await expect(page.getByTestId('profile-dirty-state')).toHaveText('No unsaved changes');

  // The bar stays reachable after scrolling to the bottom of the form.
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  await expect(page.getByTestId('profile-save')).toBeInViewport();

  await page.getByTestId('profile-name').fill(`${name} renamed`);
  await expect(page.getByTestId('profile-dirty-state')).toHaveText('Unsaved changes');
  await page.getByTestId('profile-save').click();

  await expect(page.getByText(/Saved .* as v2\./)).toBeVisible();
  await expect(page).toHaveURL(/\/hub\/http\.request$/);
  const stored = await request.get(`/api/config-profiles/${profile.id}`);
  const body = (await stored.json()) as { version: number; name: string };
  expect(body.version).toBe(2);
  expect(body.name).toBe(`${name} renamed`);

  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit`);
  await expect(page.getByTestId('profile-sticky-meta')).toContainText('v2 ·');
});

test('legacy deep links resolve into the three areas', async ({ page, request }) => {
  const stamp = Date.now();
  const profile = await createProfile(request, `e2e-editor-links-${stamp}`);
  const base = `/hub/http.request/${encodeURIComponent(profile.id)}/edit`;

  await page.goto(`${base}?tab=defaults`);
  await expect(page.getByTestId('profile-tab-configuration')).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByTestId('profile-section-defaults')).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByTestId('profile-field-url')).toBeVisible();

  await page.goto(`${base}?tab=contracts`);
  await expect(page.getByTestId('profile-section-included')).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByTestId('profile-panel-included')).toBeVisible();

  await page.goto(`${base}?tab=used-by`);
  await expect(page.getByTestId('profile-tab-trust')).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('profile-section-used-by')).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByTestId('profile-panel-used-by')).toBeVisible();

  await page.goto(`${base}?tab=history`);
  await expect(page.getByTestId('profile-section-history')).toHaveAttribute(
    'aria-selected',
    'true',
  );
  await expect(page.getByTestId('profile-panel-history')).toBeVisible();

  // Create pages have no history, so the alias falls back to Test.
  await page.goto('/hub/http.request/new?tab=history');
  await expect(page.getByTestId('profile-tab-trust')).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('profile-section-test')).toHaveAttribute('aria-selected', 'true');
  await expect(page.getByTestId('profile-panel-test')).toBeVisible();
});

test('included behavior groups mappings and contracts under one heading', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const mappingResponse = await request.post('/api/mappings', {
    data: {
      name: `e2e-editor-mapping-${stamp}`,
      kind: 'query',
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(mappingResponse.status(), await mappingResponse.text()).toBe(201);
  const mapping = (await mappingResponse.json()) as { id: string };
  const schemaResponse = await request.post('/api/data-schemas', {
    data: {
      name: `e2e-editor-schema-${stamp}`,
      fields: [{ name: 'id', type: 'string' }],
    },
  });
  expect(schemaResponse.status(), await schemaResponse.text()).toBe(201);
  const schema = (await schemaResponse.json()) as { id: string };
  const profile = await createProfile(request, `e2e-editor-included-${stamp}`);

  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit`);
  await openProfileTab(page, 'included');

  const included = page.getByTestId('profile-panel-included');
  await expect(included.getByRole('heading', { name: 'Included behavior' })).toBeVisible();
  await page.getByTestId('bundle-mapping-query').selectOption(mapping.id);
  await page.getByTestId('bundle-schema-input').selectOption(schema.id);
  await expect(page.getByTestId('bundle-mapping-query')).toHaveValue(mapping.id);
  await expect(page.getByTestId('bundle-schema-input')).toHaveValue(schema.id);

  await page.getByTestId('profile-save').click();
  const stored = await request.get(`/api/config-profiles/${profile.id}`);
  const body = (await stored.json()) as {
    bundle?: { mappings?: Record<string, string>; schemas?: Record<string, string> };
  };
  expect(body.bundle?.mappings?.query).toBe(mapping.id);
  expect(body.bundle?.schemas?.input).toBe(schema.id);
});

test('code profiles include their script under Included behavior', async ({ page, request }) => {
  const stamp = Date.now();
  const scriptResponse = await request.post('/api/scripts', {
    data: {
      name: `e2e-editor-script-${stamp}`,
      language: 'javascript',
      source: 'return input;',
    },
  });
  expect(scriptResponse.status(), await scriptResponse.text()).toBe(201);
  const script = (await scriptResponse.json()) as { id: string };
  const profile = await createProfile(
    request,
    `e2e-editor-code-${stamp}`,
    'code.run',
    { outputType: 'json' },
  );

  await page.goto(`/hub/code.run/${encodeURIComponent(profile.id)}/edit`);
  await openProfileTab(page, 'included');

  const included = page.getByTestId('profile-panel-included');
  await expect(included.getByRole('heading', { name: 'Included behavior' })).toBeVisible();
  await expect(included.getByRole('heading', { name: 'Script' })).toBeVisible();
  await page.getByTestId('bundle-script').selectOption(script.id);
  await page.getByTestId('profile-save').click();

  const stored = await request.get(`/api/config-profiles/${profile.id}`);
  const body = (await stored.json()) as { bundle?: { script?: string } };
  expect(body.bundle?.script).toBe(script.id);
});

test('a stale profile save explains the conflict and offers reload', async ({ page, request }) => {
  const stamp = Date.now();
  const name = `e2e-editor-stale-${stamp}`;
  const profile = await createProfile(request, name);
  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit`);
  await expect(page.getByTestId('profile-name')).toHaveValue(name);

  // Move the server version after the editor loaded: the next save is
  // based on v1 and must surface the 409 as a recoverable stale banner.
  const bumped = await request.put(`/api/config-profiles/${encodeURIComponent(profile.id)}`, {
    data: {
      name,
      nodeType: 'http.request',
      config: { method: 'GET', url: 'https://api.example.com/stale-v2' },
      version: profile.version,
    },
  });
  expect(bumped.status(), await bumped.text()).toBe(200);

  await page.getByTestId('profile-save').click();
  const stale = page.getByTestId('profile-form-stale');
  await expect(stale).toBeVisible();
  await expect(stale).toContainText('Reload latest version');
  await expect(page.getByTestId('profile-form-error')).toContainText(
    'changed while you were editing',
  );

  await stale.getByRole('button', { name: 'Reload latest version' }).click();
  await expect(stale).toHaveCount(0);
  await expect(page.getByTestId('profile-name')).toHaveValue(name);
});
