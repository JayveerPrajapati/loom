import { expect, test, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UX Phase 2 D2-7 (plan P1-3): the profile form's Test tab runs the
// saved profile through POST /api/config-profiles/{id}/test —
// connectivity plus the bundle resolved against current asset versions.
// The e2e server runs a simulated host, so the connectivity half reports
// skipped with the actionable message while the bundle section resolves.

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

test('the Test tab resolves the bundle and reports skipped connectivity on a simulated server', async ({
  page,
  request,
}) => {
  const stamp = Date.now();

  const mappingResp = await request.post('/api/mappings', {
    data: {
      name: `e2e-test-map-${stamp}`,
      kind: 'query',
      rows: [{ target: 'limit', source: '20' }],
    },
  });
  expect(mappingResp.status()).toBe(201);
  const mapping = await mappingResp.json();

  const schemaResp = await request.post('/api/data-schemas', {
    data: { name: `e2e-test-schema-${stamp}`, fields: [{ name: 'total', type: 'number' }] },
  });
  expect(schemaResp.status()).toBe(201);
  const schema = await schemaResp.json();

  const profileResp = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-test-profile-${stamp}`,
      nodeType: 'http.request',
      config: {
        method: 'GET',
        url: 'https://api.example.com/x',
        queryFields: [{ name: 'limit', mode: 'static', type: 'number', value: 20 }],
      },
      bundle: { mappings: { query: mapping.id }, schemas: { input: schema.id } },
    },
  });
  expect(profileResp.status()).toBe(201);
  const profile = await profileResp.json();

  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit?tab=test`);
  await expect(page.getByTestId('profile-panel-test')).toBeVisible();

  // Nothing has run yet — no fabricated results.
  await expect(page.getByTestId('profile-test-output')).toHaveCount(0);

  await page.getByTestId('profile-test-run').click();

  // Simulated host: connectivity is skipped with the actionable message,
  // never an error banner.
  await expect(page.getByTestId('profile-test-connectivity')).toContainText('simulated host');
  await expect(page.getByTestId('profile-test-error')).toHaveCount(0);

  // The bundle resolves every ref to its current version.
  const output = page.getByTestId('profile-test-output');
  await expect(output).toContainText(`e2e-test-map-${stamp} (v1)`);
  await expect(output).toContainText('1 rows');
  await expect(output).toContainText(`e2e-test-schema-${stamp} (v1)`);
  await expect(output).toContainText('1 fields');
  await expect(output).toContainText('queryFields');
  await expect(output).toContainText('1 rows compile');
  // No issues: every ref exists and the rows compile.
  await expect(output).not.toContainText('no longer exists');
});

test('the Test tab surfaces endpoint errors when there is nothing to test', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  // No url, no bundle, no parameter rows. The simulated e2e host has no
  // tester, so the preserved 503 (not a 422) reaches the form as an
  // actionable error banner; the 422-on-live-server branch is covered by
  // the Go endpoint tests.
  const profileResp = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-empty-profile-${stamp}`,
      nodeType: 'http.request',
      config: { method: 'GET', timeout: 10 },
    },
  });
  expect(profileResp.status()).toBe(201);
  const profile = await profileResp.json();

  await signInPage(page);
  await page.goto(`/hub/http.request/${encodeURIComponent(profile.id)}/edit?tab=test`);
  await page.getByTestId('profile-test-run').click();
  await expect(page.getByTestId('profile-test-error')).toContainText('needs a live server');

  // The create page explains instead of offering a run.
  await page.goto('/hub/http.request/new?tab=test');
  await expect(page.getByTestId('profile-panel-test')).toContainText('Save the profile first');
  await expect(page.getByTestId('profile-test-run')).toHaveCount(0);
});

// Phase 5: code profiles carry a test fixture and execute their bundle
// script against it — JavaScript in-process, so it works on the
// simulated e2e host too.
test('code profiles run their bundle script against the saved fixture', async ({
  page,
  request,
}) => {
  const stamp = Date.now();

  const scriptResp = await request.post('/api/scripts', {
    data: {
      name: `e2e-fixture-script-${stamp}`,
      language: 'javascript',
      source: 'return { total: $item.count * 2 };',
    },
  });
  expect(scriptResp.status()).toBe(201);
  const script = await scriptResp.json();

  const profileResp = await request.post('/api/config-profiles', {
    data: {
      name: `e2e-code-profile-${stamp}`,
      nodeType: 'code.run',
      config: { language: 'javascript' },
      bundle: { script: script.id },
      testInput: { count: 21 },
    },
  });
  expect(profileResp.status()).toBe(201);
  const profile = await profileResp.json();

  await signInPage(page);
  await page.goto(`/hub/code.run/${encodeURIComponent(profile.id)}/edit?tab=test`);
  await expect(page.getByTestId('profile-panel-test')).toBeVisible();

  // The saved fixture prefills the editor.
  await expect(page.getByTestId('profile-test-input')).toHaveValue(/"count":\s*21/);

  await page.getByTestId('profile-test-run').click();
  await expect(page.getByTestId('profile-test-connectivity')).toContainText('executed');
  await expect(page.getByTestId('profile-test-result')).toContainText('42');
  await expect(page.getByTestId('profile-test-error')).toHaveCount(0);
});
