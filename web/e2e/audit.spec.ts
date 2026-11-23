import { expect, test } from '@playwright/test';
import { login } from './auth';

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test('publish events are visible and filterable in the audit log', async ({ page, request }) => {
  const id = `e2e-audit-${Date.now()}`;
  const created = await request.post('/api/workflows', {
    data: {
      id,
      name: 'Audit workflow',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'http',
          type: 'http.request',
          typeVersion: 1,
          name: 'Fetch',
          config: { method: 'GET', url: 'https://api.example.com/audit' },
          credentials: { http: 'none' },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'http', port: 'main' } }],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  const published = await request.post(`/api/workflows/${id}/publish`);
  expect(published.status(), await published.text()).toBe(201);

  try {
    await page.goto('/audit');
    await expect(page.getByTestId('audit-page')).toBeVisible();
    await expect(
      page.locator('[data-testid="audit-event"][data-type="workflow.published"]').first(),
    ).toBeVisible();
    await expect(page.getByTestId('audit-type-filter')).toContainText('workflow.published');
    await page.getByTestId('audit-type-filter').selectOption('workflow.published');
    await expect(page.locator('[data-testid="audit-event"]').first()).toHaveAttribute(
      'data-type',
      'workflow.published',
    );
  } finally {
    await request.delete(`/api/workflows/${id}`);
  }
});

test('audit rows render de-dotted meta values', async ({ page }) => {
  await page.goto('/audit');
  const first = page.locator('.audit-row .meta-list').first();
  await expect(first).toBeVisible();
  const children = await first.locator(':scope > *').count();
  expect(children).toBeGreaterThanOrEqual(2);
  await expect(first).not.toContainText('·');
});

test('the audit list has list semantics, named rows, and a retry that recovers', async ({
  page,
  request,
}) => {
  // Seed one publish event so the list has a row.
  const id = `e2e-audit-a11y-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id,
      name: 'Audit a11y probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start' },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  await request.post(`/api/workflows/${id}/publish`);

  // First load fails; the error offers Retry.
  await page.route('**/api/auth/events', async (route) => {
    await route.fulfill({ status: 500, contentType: 'application/json', body: '{"error":"boom"}' });
  });
  await page.goto('/audit');
  await expect(page.getByTestId('audit-error')).toBeVisible();
  await expect(page.getByTestId('audit-error')).toContainText('boom');

  // Recover: stop intercepting and retry.
  await page.unroute('**/api/auth/events');
  await page.getByTestId('audit-error-retry').click();
  const list = page.locator('.audit-list');
  await expect(list).toBeVisible();

  // The list carries list semantics and every row has an accessible
  // name (actor + action + time).
  const semantics = await list.evaluate((element) => ({
    role: element.getAttribute('role'),
    tag: element.tagName,
  }));
  expect(semantics.role === 'list' || semantics.tag === 'UL').toBe(true);
  const named = await page
    .locator('[data-testid="audit-event"]')
    .evaluateAll((nodes) =>
      nodes.slice(0, 5).map((node) => (node as HTMLElement).ariaLabel ?? ''),
    );
  expect(named.length).toBeGreaterThan(0);
  for (const label of named) {
    expect(label.length).toBeGreaterThan(0);
  }
});
