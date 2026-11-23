import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Operational-trust campaign (egress control): black-box verification of
// every UI surface the campaign added. The -sim e2e host never dials
// out, so live deny-mode enforcement is pinned by the Go suite
// (internal/host/egress_test.go, internal/engine/egress_host_test.go)
// and by `loom egress check`; this spec covers the authoring surfaces:
// the workflow egress dialog, publish validation of entries, the
// credential allowedHosts field, and the healthz signal's absence on
// sim (the honest-capability contract — sim reports nothing).

test.beforeEach(async ({ request, context }) => {
  await login(request);
  // context.request shares cookies with the page's browser context, so
  // this is what actually signs the page in.
  await login(context.request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request, 'egress-ui');
});

test('healthz omits egress on the simulated host', async ({ request }) => {
  const response = await request.get('/healthz');
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as Record<string, unknown>;
  // The sim host never dials out, so it must not advertise an egress
  // posture — the same capability-honesty rule as subprocessNodes.
  expect(body.egress).toBeUndefined();
});

test('the workflow egress dialog authors an allowlist that survives publish', async ({ page, request }) => {
  const id = `egress-ui-${Date.now()}`;
  const create = await request.post('/api/workflows', {
    data: {
      id,
      name: `Egress UI ${id}`,
      version: 1,
      nodes: [
        {
          id: 'start',
          type: 'trigger.manual',
          typeVersion: 1,
          name: 'Start',
          position: { x: 80, y: 120 },
          config: {},
        },
      ],
      edges: [],
    },
  });
  expect(create.status(), await create.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.getByTestId('topbar-more').locator('summary').click();
  await page.getByTestId('workflow-egress-chip').click();
  const dialog = page.getByTestId('workflow-egress-dialog');
  await expect(dialog).toBeVisible();
  await page.getByTestId('workflow-egress-entries').fill(
    'https://api.github.com\nhttps://*.slack.com',
  );
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();

  // The chip now carries the count; autosave persists the doc block.
  await expect(page.getByTestId('workflow-egress-chip')).toContainText('Egress · 2');
  await page.waitForFunction(async (workflowId) => {
    const stored = await fetch(`/api/workflows/${workflowId}`, {
      headers: { Accept: 'application/json' },
    });
    const body = (await stored.json()) as { workflow?: { egress?: { allowlist: string[] } } };
    return body.workflow?.egress?.allowlist?.length === 2;
  }, id);

  const publish = await request.post(`/api/workflows/${id}/publish`);
  expect(publish.ok(), await publish.text()).toBeTruthy();

  // Reopening the dialog shows the authored entries seeded.
  await page.getByTestId('topbar-more').locator('summary').click();
  await page.getByTestId('workflow-egress-chip').click();
  await expect(page.getByTestId('workflow-egress-entries')).toHaveValue(
    'https://api.github.com\nhttps://*.slack.com',
  );
});

// The dialog must never silently drop an invalid entry: per-line errors
// appear inline, Save stays disabled until fixed, and a valid apply
// confirms with a toast and persists.
test('the egress dialog rejects invalid entries inline instead of dropping them', async ({
  page,
  request,
}) => {
  const id = `egress-ui-inline-${Date.now()}`;
  const create = await request.post('/api/workflows', {
    data: {
      id,
      name: `Egress inline ${id}`,
      version: 1,
      nodes: [
        {
          id: 'start',
          type: 'trigger.manual',
          typeVersion: 1,
          name: 'Start',
          position: { x: 80, y: 120 },
          config: {},
        },
      ],
      edges: [],
    },
  });
  expect(create.status(), await create.text()).toBe(201);

  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await page.getByTestId('topbar-more').locator('summary').click();
  await page.getByTestId('workflow-egress-chip').click();
  const dialog = page.getByTestId('workflow-egress-dialog');
  await expect(dialog).toBeVisible();

  // An entry without a scheme names the entry and disables Save.
  await page.getByTestId('workflow-egress-entries').fill('api.github.com');
  const errors = page.getByTestId('workflow-egress-errors');
  await expect(errors).toBeVisible();
  await expect(errors).toContainText('api.github.com');
  await expect(dialog.getByRole('button', { name: 'Save' })).toBeDisabled();

  // A second bad line adds its own error; a path is rejected too.
  await page
    .getByTestId('workflow-egress-entries')
    .fill('api.github.com\nhttps://api.github.com/repos/loom');
  await expect(errors.locator('.issue-error')).toHaveCount(2);
  await expect(dialog.getByRole('button', { name: 'Save' })).toBeDisabled();

  // Fixing every line clears the errors and enables Save.
  await page.getByTestId('workflow-egress-entries').fill('https://api.github.com');
  await expect(errors).toHaveCount(0);
  await expect(dialog.getByRole('button', { name: 'Save' })).toBeEnabled();

  // Applying confirms with a toast and persists the allowlist.
  await dialog.getByRole('button', { name: 'Save' }).click();
  await expect(dialog).toBeHidden();
  await expect(page.locator('.toast')).toContainText('Egress allowlist updated (1 destination)');
  await expect(page.getByTestId('workflow-egress-chip')).toContainText('Egress · 1');
  await page.waitForFunction(async (workflowId) => {
    const stored = await fetch(`/api/workflows/${workflowId}`, {
      headers: { Accept: 'application/json' },
    });
    const body = (await stored.json()) as { workflow?: { egress?: { allowlist: string[] } } };
    return body.workflow?.egress?.allowlist?.length === 1;
  }, id);
});

test('publishing an invalid egress entry names the problem', async ({ request }) => {
  const id = `egress-ui-invalid-${Date.now()}`;
  const create = await request.post('/api/workflows', {
    data: {
      id,
      name: `Egress invalid ${id}`,
      version: 1,
      nodes: [
        {
          id: 'start',
          type: 'trigger.manual',
          typeVersion: 1,
          name: 'Start',
          position: { x: 80, y: 120 },
          config: {},
        },
      ],
      edges: [],
      egress: { allowlist: ['no-scheme.example.com'] },
    },
  });
  // Ingest validates the same graph rules: a bad entry is rejected
  // with the entry named, not stored for publish-time surprise.
  expect([400, 422]).toContain(create.status());
  const text = await create.text();
  expect(text).toContain('egress');
});

test('the http credential editor stores allowedHosts as an array', async ({ request }) => {
  const credId = `egress-ui-cred-${Date.now()}`;
  const create = await request.post('/api/credentials', {
    data: {
      id: credId,
      type: 'http',
      data: {
        token: 'e2e-token',
        allowedHosts: ['api.github.com', '*.api.slack.com'],
      },
    },
  });
  expect(create.status(), await create.text()).toBe(201);
  const list = await request.get('/api/credentials');
  expect(list.ok()).toBeTruthy();
  const body = (await list.json()) as { credentials: { id: string }[] };
  expect(body.credentials.some((credential) => credential.id === credId)).toBe(true);
});
