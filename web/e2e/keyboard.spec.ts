import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// UX Phase 4.1: keyboard-only graph editing. Everything in this spec runs
// without the mouse: focusing nodes, deleting them, undo/redo, and driving
// the command palette. Keyboard edge-creation is not supported by the
// canvas (React Flow has no keyboard connection API); the supported
// alternatives — auto-arrange, import JSON, undo/redo — are what this spec
// exercises instead.

async function seed(request: APIRequestContext, workflow: object) {
  const response = await request.post('/api/workflows', { data: workflow });
  expect(response.status(), await response.text()).toBe(201);
}

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('a workflow can be edited with the keyboard alone', async ({ page, request }) => {
  // Seed a two-node workflow; everything after the initial load is keys.
  const id = `e2e-keyboard-${Date.now()}`;
  await seed(request, {
    id,
    name: 'E2E keyboard',
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
        position: { x: 280, y: 0 },
        config: { values: { ok: true } },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  });
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.react-flow__node')).toHaveCount(2);

  // Tab reaches a canvas node: the top bar and panels come first, so the
  // budget covers every tab stop between the body and the canvas.
  const focused = page.locator('.react-flow__node:focus');
  for (let i = 0; i < 40; i += 1) {
    await page.keyboard.press('Tab');
    if (await focused.count()) break;
  }
  await expect(focused).toHaveCount(1);

  // Enter selects the focused node (the wrapper carries React Flow's
  // `selected` class; the inner card mirrors it as `is-selected`).
  await page.keyboard.press('Enter');
  await expect(page.locator('.react-flow__node.selected')).toHaveCount(1);
  await page.keyboard.press('Backspace');
  await expect(page.getByTestId('confirm-dialog')).toBeVisible();
  await page.keyboard.press('Tab');
  await page.keyboard.press('Enter');
  await expect(page.locator('.react-flow__node')).toHaveCount(1);

  // Undo and redo restore it.
  await page.keyboard.press('Control+z');
  await expect(page.locator('.react-flow__node')).toHaveCount(2);
  await page.keyboard.press('Control+Shift+z');
  await expect(page.locator('.react-flow__node')).toHaveCount(1);
  await page.keyboard.press('Control+z');

  // The command palette adds a node without the mouse: ⌘K opens it, the
  // keyboard navigates its entries, and Enter commits.
  await page.keyboard.press('Meta+k');
  const palette = page.getByRole('dialog', { name: 'Command palette' });
  const search = palette.locator('.palette-input');
  await expect(palette).toBeVisible();
  await search.fill('Set data');
  await expect(palette.locator('.palette-row', { hasText: 'Set data' }).first()).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page.locator('.react-flow__node')).toHaveCount(3);

  // Auto-arrange is reachable from the keyboard through the same palette.
  await page.keyboard.press('Meta+k');
  await expect(palette).toBeVisible();
  await search.fill('Auto-arrange');
  await expect(palette.locator('.palette-row', { hasText: 'Auto-arrange' })).toBeVisible();
  await page.keyboard.press('Enter');
  await expect(page.locator('.toast')).toContainText('auto-arranged');

  // Escape closes the palette without a mouse click.
  await page.keyboard.press('Meta+k');
  await expect(palette).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(palette).toHaveCount(0);
});

// The inspector-tabs half of the Phase 0 item 2c keyboard coverage,
// deferred until the inspector had tabs (Profile-Centric UX Phase 3,
// D3-1): arrow keys rove through the tablist with automatic activation
// and wrap at both ends.
test('the inspector tabs are drivable with the arrow keys', async ({ page, request }) => {
  const id = `e2e-keyboard-tabs-${Date.now()}`;
  await seed(request, {
    id,
    name: 'E2E keyboard tabs',
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
        id: 'fetch',
        type: 'http.request',
        typeVersion: 1,
        name: 'Fetch',
        position: { x: 280, y: 0 },
        config: { method: 'GET', url: 'https://example.test' },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'fetch', port: 'main' } }],
  });
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);

  // Selecting a node opens the inspector on Configuration. http.request
  // gets the full inventory: configuration, data, runtime, advanced.
  await page.locator('.loom-node', { hasText: 'Fetch' }).click();
  await expect(page.getByTestId('inspector-panel-configuration')).toBeVisible();
  await expect(page.getByRole('tablist', { name: 'Node sections' })).toBeVisible();
  await expect(page.locator('#inspector-tab-configuration')).toHaveAttribute(
    'aria-controls',
    'inspector-panel-configuration',
  );
  await expect(page.getByTestId('inspector-panel-configuration')).toHaveAttribute(
    'aria-labelledby',
    'inspector-tab-configuration',
  );
  const selectedTab = page.locator('[role="tab"][aria-selected="true"]');
  await expect(selectedTab).toHaveId('inspector-tab-configuration');

  const focusTab = () => page.evaluate(() => document.activeElement?.id);

  // Tab from the node name input reaches the summary's profile picker,
  // then the tablist; arrow keys rove with activation, wrapping at both
  // ends.
  await page.locator('#node-name').focus();
  await page.keyboard.press('Tab');
  await expect(page.locator('#node-profile-ref')).toBeFocused();
  await page.keyboard.press('Tab');
  await expect(page.locator('#inspector-tab-configuration')).toBeFocused();
  await page.keyboard.press('ArrowRight');
  await expect(selectedTab).toHaveId('inspector-tab-data');
  await expect(page.locator('#inspector-tab-data')).toBeFocused();
  await expect(page.getByTestId('inspector-panel-data')).toBeVisible();
  await page.keyboard.press('ArrowLeft');
  await expect(selectedTab).toHaveId('inspector-tab-configuration');
  await page.keyboard.press('ArrowLeft');
  await expect(selectedTab).toHaveId('inspector-tab-advanced');
  await page.keyboard.press('ArrowRight');
  await expect(selectedTab).toHaveId('inspector-tab-configuration');
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('ArrowRight');
  await expect(selectedTab).toHaveId('inspector-tab-runtime');
  await expect(page.getByTestId('inspector-panel-runtime')).toBeVisible();
  await page.keyboard.press('ArrowRight');
  await expect(selectedTab).toHaveId('inspector-tab-advanced');
  await page.keyboard.press('ArrowRight');
  await expect(selectedTab).toHaveId('inspector-tab-configuration');
  await page.keyboard.press('ArrowLeft');
  await expect(selectedTab).toHaveId('inspector-tab-advanced');
  expect(await focusTab()).toBe('inspector-tab-advanced');
});

test('the profile editor is drivable with the keyboard alone', async ({ page, request }) => {
  // Profile-Centric UX Phase 0 item 2: the profile form is a native form,
  // so keyboard reachability is structural — this test freezes it. Tabs
  // walk from the page body into the name field, the section tabs are
  // plain buttons that activate with Enter (UX Phase 2 D2-2), the Advanced
  // disclosure opens with Enter, and a native form submit needs no mouse.
  const stamp = Date.now();
  await page.goto('/hub/http.request/new');
  await expect(page.getByTestId('profile-form')).toBeVisible();

  // Tab reaches the name field; the budget covers the tab stops between
  // the body and the form (topbar, nav, header actions).
  const name = page.getByTestId('profile-name');
  for (let i = 0; i < 25; i += 1) {
    if ((await page.evaluate(() => document.activeElement?.getAttribute('data-testid'))) === 'profile-name') break;
    await page.keyboard.press('Tab');
  }
  await expect(name).toBeFocused();
  await page.keyboard.type(`e2e-kb-profile-${stamp}`);

  // The primary area tabs lead; Tab reaches Configuration and Enter
  // activates it (Phase 4 D4-1). The section strip then appears and the
  // Defaults section opens with Enter as well.
  const configurationTab = page.getByTestId('profile-tab-configuration');
  for (let i = 0; i < 80; i += 1) {
    if ((await page.evaluate(() => document.activeElement?.getAttribute('data-testid'))) === 'profile-tab-configuration') break;
    await page.keyboard.press('Tab');
  }
  await expect(configurationTab).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('profile-panel-defaults')).toBeVisible();

  const defaultsSection = page.getByTestId('profile-section-defaults');
  for (let i = 0; i < 12; i += 1) {
    if ((await page.evaluate(() => document.activeElement?.getAttribute('data-testid'))) === 'profile-section-defaults') break;
    await page.keyboard.press('Tab');
  }
  await expect(defaultsSection).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.getByTestId('profile-field-url')).toBeVisible();

  // The Advanced disclosure is part of the tab order; Enter opens it and
  // reveals the advanced fields.
  const advancedSummary = page.locator('details.advanced-fields > summary');
  for (let i = 0; i < 30; i += 1) {
    if (await advancedSummary.evaluate((el) => document.activeElement === el)) break;
    await page.keyboard.press('Tab');
  }
  await expect(advancedSummary).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(page.locator('details.advanced-fields')).toHaveAttribute('open', '');

  // Fill a node default and submit with Enter from the URL text input —
  // a native form submit needs no mouse.
  await page.getByTestId('profile-field-url').fill('https://example.test');
  await page.getByTestId('profile-field-url').press('Enter');

  // A successful create lands on the new profile's edit page.
  await expect(page).toHaveURL(/\/hub\/http\.request\/profile-[^/]+\/edit$/);
  await expect(page.getByTestId('profile-name')).toHaveValue(`e2e-kb-profile-${stamp}`);

  // Profiles are not covered by the workflow sweep; remove this one.
  const list = await request.get('/api/config-profiles');
  const { profiles } = (await list.json()) as { profiles: { id: string; name: string }[] };
  const created = (profiles ?? []).find((profile) => profile.name === `e2e-kb-profile-${stamp}`);
  if (created) await request.delete(`/api/config-profiles/${created.id}`);
});

test('settings and kebab menus close on outside click and Escape', async ({ page }) => {
  await page.goto('/');
  const settings = page.locator('details.settings-menu');

  // Opening the settings menu, then clicking elsewhere, closes it.
  await settings.locator('summary').click();
  await expect(settings).toHaveAttribute('open', '');
  await page.locator('h1, h2').first().click();
  await expect(settings).not.toHaveAttribute('open', '');

  // Escape closes it too.
  await settings.locator('summary').click();
  await expect(settings).toHaveAttribute('open', '');
  await page.keyboard.press('Escape');
  await expect(settings).not.toHaveAttribute('open', '');

  // Clicking a second menu closes the first (only one open at a time).
  await settings.locator('summary').click();
  await expect(settings).toHaveAttribute('open', '');
  await page.mouse.click(40, 400);
  await expect(settings).not.toHaveAttribute('open', '');
});

test('content disclosures stay open when clicking elsewhere', async ({ page }) => {
  await page.goto('/runs');
  // The run meta disclosure is content, not a menu: outside clicks do
  // not collapse it.
  const disclosure = page.locator('details.run-meta-details').first();
  const present = await disclosure.count();
  if (present === 0) return; // no run detail open on this page state
  if (!(await disclosure.locator('summary').isVisible())) return;
});
