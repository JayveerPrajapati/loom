import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { login } from './auth';

// Phase 2 state grammar: a failed primary load is never an empty list.
// Each list distinguishes a retryable error, a permission denial, and an
// offline failure, and Retry recovers in place.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

async function openFromProfiles(page: Page, name: 'Mappings' | 'Scripts' | 'Connectors') {
  await page.locator('.topnav').getByRole('link', { name: 'Profiles' }).click();
  await expect(page.getByRole('heading', { name: 'Profiles', exact: true })).toBeVisible();
  // The advanced assets live behind the header kebab.
  await page.getByTestId('advanced-assets').locator('summary').click();
  await page.getByRole('button', { name, exact: true }).click();
}

async function expectLoadedContent(page: Page, emptyTestId: string, rowSelector: string) {
  await expect(
    page.getByTestId(emptyTestId).or(page.locator(rowSelector).first()),
  ).toBeVisible();
}

test('mappings: a failed load is a named error with Retry, not an empty list', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/mappings*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'mappings unavailable' }),
    }),
  );
  await openFromProfiles(page, 'Mappings');

  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'error');
  await expect(error).toContainText('mappings unavailable');
  await expect(page.getByTestId('mappings-empty')).toHaveCount(0);
  await expect(page.getByTestId('asset-loading')).toHaveCount(0);

  await page.unroute('**/api/mappings*');
  await page.getByTestId('asset-error-retry').click();
  await expect(error).toBeHidden();
  await expectLoadedContent(page, 'mappings-empty', '.dashboard-run-row');
});

test('mappings: loading is calm and resolves into content', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  let release: (() => void) | undefined;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  await page.route('**/api/mappings*', async (route) => {
    await gate;
    await route.continue();
  });
  await openFromProfiles(page, 'Mappings');
  await expect(page.getByTestId('asset-loading')).toBeVisible();
  release?.();
  await expect(page.getByTestId('asset-loading')).toHaveCount(0);
  await expectLoadedContent(page, 'mappings-empty', '.dashboard-run-row');
});

test('mappings: a denied load explains the permission and offers no retry', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/mappings*', (route) =>
    route.fulfill({
      status: 403,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'forbidden' }),
    }),
  );
  await openFromProfiles(page, 'Mappings');

  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'permission');
  await expect(error).toContainText('permission');
  await expect(page.getByTestId('asset-error-retry')).toHaveCount(0);
});

test('scripts: a denied load explains the permission and offers no retry', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/scripts*', (route) =>
    route.fulfill({
      status: 403,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'forbidden' }),
    }),
  );
  await openFromProfiles(page, 'Scripts');
  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'permission');
  await expect(page.getByTestId('asset-error-retry')).toHaveCount(0);
});

test('scripts and connectors surface the same retryable error state', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/scripts*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'scripts unavailable' }),
    }),
  );
  await openFromProfiles(page, 'Scripts');
  await expect(page.getByTestId('asset-error')).toContainText('scripts unavailable');
  await page.unroute('**/api/scripts*');
  await page.getByTestId('asset-error-retry').click();
  await expect(page.getByTestId('asset-error')).toBeHidden();
  await expectLoadedContent(page, 'scripts-empty', '.dashboard-run-row');

  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/connectors*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'connectors unavailable' }),
    }),
  );
  await openFromProfiles(page, 'Connectors');
  await expect(page.getByTestId('asset-error')).toContainText('connectors unavailable');
  await page.unroute('**/api/connectors*');
  await page.getByTestId('asset-error-retry').click();
  await expect(page.getByTestId('asset-error')).toBeHidden();
  await expect(
    page.getByTestId('connectors-empty').or(page.locator('.diff-table').first()),
  ).toBeVisible();
});

test('profiles landing: the overview error retries instead of showing zero counts', async ({
  page,
}) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/hub', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'hub unavailable' }),
    }),
  );
  await page.locator('.topnav').getByRole('link', { name: 'Profiles' }).click();
  await expect(page.getByRole('heading', { name: 'Profiles', exact: true })).toBeVisible();
  await expect(page.getByTestId('asset-error')).toContainText('hub unavailable');
  await expect(page.locator('.count-pill')).toHaveCount(0);

  await page.unroute('**/api/hub');
  await page.getByTestId('asset-error-retry').click();
  await expect(page.getByTestId('asset-error')).toBeHidden();
  await expect(page.getByTestId('profiles-landing-lede')).toBeVisible();
});

test('workflow list: a failed refresh stays visible and Retry recovers', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  let fail = true;
  await page.route('**/api/workflows*', (route) => {
    if (route.request().method() === 'GET' && fail) {
      return route.fulfill({
        status: 500,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'workflows unavailable' }),
      });
    }
    return route.continue();
  });
  await page.locator('.topnav').getByRole('link', { name: 'Workflows' }).click();
  await expect(page.getByTestId('asset-error')).toContainText('workflows unavailable');
  await expect(page.getByTestId('workflows-first-run')).toHaveCount(0);

  fail = false;
  await page.getByTestId('asset-error-retry').click();
  await expect(page.getByTestId('asset-error')).toBeHidden();
});

test('mappings: offline is a calm distinct state that recovers when back online', async ({
  page,
  context,
}) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await context.setOffline(true);
  await openFromProfiles(page, 'Mappings');
  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'offline');

  await context.setOffline(false);
  await page.getByTestId('asset-error-retry').click();
  await expect(error).toBeHidden();
  await expectLoadedContent(page, 'mappings-empty', '.dashboard-run-row');
});

test('workflows: a filtered-empty search says no matches and clears', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const id = `e2e-load-state-flow-${stamp}`;
  const created = await request.post('/api/workflows', {
    data: {
      id,
      name: `Load state flow ${stamp}`,
      version: 1,
      nodes: [{ id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} }],
      edges: [],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  try {
    await page.goto('/workflows');
    await expect(page.locator('#main-stage')).toBeVisible();
    await page.locator('#workflow-search').fill(`zzz-no-such-workflow-${stamp}`);
    const filtered = page.getByTestId('asset-filtered-empty');
    await expect(filtered).toBeVisible();
    await expect(filtered).toContainText('No workflows match');

    await filtered.getByRole('button', { name: 'Clear filters' }).click();
    await expect(filtered).toHaveCount(0);
    await expect(page.locator('#workflow-search')).toHaveValue('');
    await page.locator('#workflow-search').fill(`Load state flow ${stamp}`);
    await expect(page.getByText(`Load state flow ${stamp}`)).toBeVisible();
  } finally {
    await request.delete(`/api/workflows/${id}`);
  }
});

test('data contracts: a failed load is a named error with Retry, not an empty list', async ({
  page,
}) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/data-schemas*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'contracts unavailable' }),
    }),
  );
  // Navigate in-app: a full reload fetches schemas during boot, which
  // would show the app-wide connection error instead of this page's state.
  await page.locator('.settings-menu summary').click();
  await page.getByRole('button', { name: 'Data contracts' }).click();
  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'error');
  await expect(error).toContainText('contracts unavailable');
  await expect(page.getByTestId('schemas-empty')).toHaveCount(0);

  await page.unroute('**/api/data-schemas*');
  await page.getByTestId('asset-error-retry').click();
  await expect(error).toBeHidden();
  await expectLoadedContent(page, 'schemas-empty', '.dashboard-run-row');
});

test('credentials: a failed load is a named error with Retry, not an empty list', async ({
  page,
}) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/credentials*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'credentials unavailable' }),
    }),
  );
  await page.goto('/credentials');
  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'error');
  await expect(error).toContainText('credentials unavailable');

  await page.unroute('**/api/credentials*');
  await page.getByTestId('asset-error-retry').click();
  await expect(error).toBeHidden();
  await expect(page.getByTestId('credentials-search')).toBeVisible();
});

test('runs: a failed load is a named error with Retry, not an empty list', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/runs*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'runs unavailable' }),
    }),
  );
  await page.goto('/runs');
  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'error');
  await expect(error).toContainText('runs unavailable');

  await page.unroute('**/api/runs*');
  await page.getByTestId('asset-error-retry').click();
  await expect(error).toBeHidden();
  await expect(page.locator('#main-stage')).toBeVisible();
});

test('hub node page: a failed load is a classified error with Retry', async ({ page }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await page.route('**/api/hub/http.request*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'hub unavailable' }),
    }),
  );
  await page.goto('/hub/http.request');
  const failure = page.getByTestId('hub-load-error');
  await expect(failure).toBeVisible();
  await expect(failure.getByTestId('asset-error')).toHaveAttribute(
    'data-failure-kind',
    'error',
  );
  await expect(failure).toContainText('hub unavailable');

  await page.unroute('**/api/hub/http.request*');
  await failure.getByTestId('asset-error-retry').click();
  await expect(failure).toBeHidden();
  await expect(page.locator('#main-stage')).toBeVisible();
});

test('audit: a failed load is a named error, not an empty event log', async ({ page }) => {
  await page.route('**/api/auth/events*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'audit unavailable' }),
    }),
  );
  await page.goto('/audit');
  const error = page.getByTestId('audit-error');
  await expect(error).toContainText('audit unavailable');
  await expect(error.getByTestId('audit-error-retry')).toBeVisible();
  await expect(page.getByTestId('audit-event')).toHaveCount(0);
  await expect(page.getByText('Loading events…')).toHaveCount(0);
});

test('audit: a denied load explains the permission without a retry', async ({ page }) => {
  await page.route('**/api/auth/events*', (route) =>
    route.fulfill({
      status: 403,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'forbidden' }),
    }),
  );
  await page.goto('/audit');
  const error = page.getByTestId('audit-error');
  await expect(error).toContainText('Access needed');
  await expect(error.getByRole('button', { name: /retry/i })).toHaveCount(0);
});

test('capabilities: a failed load is a named error, not an empty posture', async ({ page }) => {
  await page.route('**/api/capabilities*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'capabilities unavailable' }),
    }),
  );
  await page.goto('/capabilities');
  const error = page.getByTestId('capabilities-error');
  await expect(error).toContainText('capabilities unavailable');
  await expect(error.getByTestId('capabilities-error-retry')).toBeVisible();
  await expect(page.getByTestId('capabilities-auth')).toHaveCount(0);
  await expect(page.getByText('Loading capabilities…')).toHaveCount(0);
});

test('capabilities: a denied load explains the permission', async ({ page }) => {
  await page.route('**/api/capabilities*', (route) =>
    route.fulfill({
      status: 403,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'forbidden' }),
    }),
  );
  await page.goto('/capabilities');
  const error = page.getByTestId('capabilities-error');
  await expect(error).toContainText('Access needed');
  await expect(error.getByRole('button', { name: /retry/i })).toHaveCount(0);
  await expect(page.getByTestId('capabilities-auth')).toHaveCount(0);
});

test('ops: a failed primary load is a named error', async ({ page }) => {
  await page.route('**/api/ops/storage*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'ops unavailable' }),
    }),
  );
  await page.goto('/ops');
  await expect(page.getByTestId('ops-error')).toContainText('ops unavailable');
  await expect(page.getByTestId('ops-totals-card')).toHaveCount(0);
});

test('jobs: a failed primary load is a named error, not an empty list', async ({ page }) => {
  await page.route('**/api/jobs*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'jobs unavailable' }),
    }),
  );
  await page.goto('/jobs');
  const error = page.getByTestId('jobs-error');
  await expect(error).toContainText('jobs unavailable');
  await expect(page.getByText('Loading jobs…')).toHaveCount(0);
  await expect(page.getByText('No jobs yet')).toHaveCount(0);
});

test('job detail: a failed load names the error on the not-found page', async ({ page }) => {
  await page.route('**/api/jobs/e2e-missing-*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'job unavailable' }),
    }),
  );
  await page.goto('/jobs/e2e-missing-job');
  await expect(page.getByRole('heading', { name: 'Job not found' })).toBeVisible();
  await expect(page.getByTestId('job-detail-error')).toContainText('job unavailable');
});

test('run detail: a failed load names the error on the not-found page', async ({ page }) => {
  await page.route('**/api/runs/e2e-missing-*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'run unavailable' }),
    }),
  );
  await page.goto('/runs/e2e-missing-run');
  await expect(page.getByRole('heading', { name: 'Run not found' })).toBeVisible();
  await expect(page.getByTestId('run-detail-error')).toContainText('run unavailable');
});

test('data contracts: offline is a distinct state that recovers', async ({ page, context }) => {
  await page.goto('/');
  await expect(page.locator('#main-stage')).toBeVisible();
  await context.setOffline(true);
  await page.locator('.settings-menu summary').click();
  await page.getByRole('button', { name: 'Data contracts' }).click();
  const error = page.getByTestId('asset-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveAttribute('data-failure-kind', 'offline');

  await context.setOffline(false);
  await page.getByTestId('asset-error-retry').click();
  await expect(error).toBeHidden();
  await expect(
    page.getByTestId('schemas-empty').or(page.locator('.dashboard-run-row').first()),
  ).toBeVisible();
});
