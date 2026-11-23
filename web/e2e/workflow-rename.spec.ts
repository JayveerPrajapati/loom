import { expect, test, type Page } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// U3 regression: the workflow list renames through the shared PromptDialog
// (labelled alertdialog, trapped focus, inline validation, in-dialog server
// errors, busy-Escape protection) instead of window.prompt.

test.beforeEach(async ({ request }) => {
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function signInPage(page: Page): Promise<void> {
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();
}

function workflow(id: string, name: string) {
  return {
    id,
    name,
    version: 1,
    nodes: [
      {
        id: 'start',
        type: 'trigger.manual',
        typeVersion: 1,
        name: 'Start',
        position: { x: 0, y: 0 },
        config: {},
      },
      {
        id: 'shape',
        type: 'data.set',
        typeVersion: 1,
        name: 'Shape',
        position: { x: 260, y: 0 },
        config: { values: { message: 'hello' }, keepOnlySet: true },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  };
}

test('rename workflow uses the prompt dialog with focus, validation and in-dialog errors', async ({
  page,
  request,
}) => {
  const id = `e2e-rename-${Date.now()}`;
  const name = `e2e Rename source ${id}`;
  const renamed = `e2e Rename target ${id}`;
  expect((await request.post('/api/workflows', { data: workflow(id, name) })).status()).toBe(201);

  await signInPage(page);
  await page.goto('/workflows');
  const card = page
    .locator('.template-card')
    .filter({ has: page.getByText(name, { exact: true }) });
  await expect(card).toBeVisible();

  // The rename opens the shared alertdialog with a labelled, focused input.
  await card.getByRole('button', { name: 'Rename', exact: true }).click();
  const dialog = page.getByTestId('prompt-dialog');
  await expect(dialog).toBeVisible();
  await expect(dialog).toHaveAttribute('role', 'alertdialog');
  const input = page.getByTestId('prompt-dialog-input');
  await expect(input).toBeFocused();
  await expect(input).toHaveValue(name);
  const labels = await input.evaluate((el) =>
    Array.from(el.labels ?? []).map((label) => label.textContent?.trim()),
  );
  expect(labels).toEqual(['Workflow name']);
  // Confirm stays disabled while the value equals the current name.
  const confirm = page.getByTestId('prompt-dialog-confirm');
  await expect(confirm).toBeDisabled();

  // An empty name is an inline validation error, not a submit.
  await input.fill('');
  await input.press('Enter');
  const error = page.getByTestId('prompt-dialog-error');
  await expect(error).toBeVisible();
  await expect(error).toHaveText('Workflow name is required.');
  await expect(dialog).toBeVisible();

  // Escape leaves the old name.
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(card).toContainText(name);

  // Cancel also leaves the old name.
  await card.getByRole('button', { name: 'Rename', exact: true }).click();
  await page.getByTestId('prompt-dialog-cancel').click();
  await expect(dialog).toBeHidden();
  await expect(card).toContainText(name);

  // A failing rename keeps its error inside the dialog, and Escape while
  // the request is in flight does not dismiss it.
  let failRename = true;
  await page.route(`**/api/workflows/${id}`, async (route) => {
    if (failRename && route.request().method() === 'PUT') {
      await new Promise((resolve) => setTimeout(resolve, 1000));
      await route.fulfill({ status: 500, body: 'rename boom' });
      return;
    }
    await route.continue();
  });
  await card.getByRole('button', { name: 'Rename', exact: true }).click();
  await input.fill(renamed);
  await confirm.click();
  await expect(confirm).toBeDisabled();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeVisible();
  await expect(error).toBeVisible();

  // The retry succeeds and the list shows the new name.
  failRename = false;
  await confirm.click();
  await expect(dialog).toBeHidden();
  const renamedCard = page
    .locator('.template-card')
    .filter({ has: page.getByText(renamed, { exact: true }) });
  await expect(renamedCard).toBeVisible();
});
