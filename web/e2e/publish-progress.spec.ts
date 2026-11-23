import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Audit 4E: a publish stays a synchronous POST, but the editor polls the
// server's in-memory progress registry and names the running phase. This
// spec mocks a slow publish plus a running "environments" phase and checks
// the label appears while the POST is in flight and disappears after the
// response.
test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function seedWorkflow(request: APIRequestContext): Promise<string> {
  const id = `e2e-publish-progress-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
  const created = await request.post('/api/workflows', {
    data: {
      id,
      name: 'Publish progress',
      version: 1,
      nodes: [{ id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} }],
      edges: [],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  return id;
}

test('the editor names the running publish phase while the POST is in flight', async ({
  page,
  request,
}) => {
  const id = await seedWorkflow(request);

  // Distinct matchers: the status GET must not be caught by the publish
  // POST route (and vice versa).
  await page.route(/\/api\/workflows\/[^/]+\/publish\/status(?:\?.*)?$/, async (route) => {
    const url = new URL(route.request().url());
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({
        publishId: url.searchParams.get('publishId') ?? '',
        workflowId: id,
        done: false,
        phases: [{ name: 'environments', status: 'running' }],
      }),
    });
  });
  await page.route(/\/api\/workflows\/[^/]+\/publish(?:\?.*)?$/, async (route) => {
    const response = await route.fetch();
    await new Promise((resolve) => setTimeout(resolve, 1200));
    await route.fulfill({ response });
  });

  await page.goto(`/workflows/${id}/edit`);
  await expect(page.locator('.publish-controls')).toBeVisible();

  await page.getByRole('button', { name: 'Publish', exact: true }).click();

  const phase = page.getByTestId('publish-phase');
  await expect(phase).toBeVisible();
  await expect(phase).toContainText('Building Python environments');

  // The response lands and the lane clears; the existing toast remains.
  await expect(phase).toHaveCount(0);
  await expect(page.locator('.toast')).toContainText('Published v');
});
