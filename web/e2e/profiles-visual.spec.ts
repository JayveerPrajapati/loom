import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows, openProfileTab } from './helpers';

// Configuration Hub visual regression (plan §18 D9, Phase 7): gates are
// deterministic DOM-geometry assertions measured from the live rendered
// page; screenshots are captured as artifacts only, never compared
// pixel-by-pixel (CI fonts and rendering make that flaky).

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

function httpWorkflow(id: string) {
  return {
    id,
    name: `Profiles visual ${id}`,
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
      name: `Visual API ${id}`,
      nodeType: 'http.request',
      description: 'Geometry gate fixture',
      config: {
        method: 'POST',
        url: 'https://api.example.com/resource',
        timeout: 30,
      },
      owner: 'geometry-team',
      tags: ['visual'],
      retry: { maxAttempts: 3, backoffMs: 250 },
    },
  });
  expect(response.status(), await response.text()).toBe(201);
  return (await response.json()) as { id: string };
}

async function openEditorWithProfile(
  page: Page,
  request: APIRequestContext,
  profileId: string,
): Promise<string> {
  const wfId = `e2e-visual-${Date.now()}`;
  const wf = httpWorkflow(wfId);
  wf.nodes[1].profileRef = profileId;
  const seed = await request.post('/api/workflows', { data: wf });
  expect(seed.status(), await seed.text()).toBe(201);
  await page.goto(`/workflows/${encodeURIComponent(wfId)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  // Select the http node; the profile section lives in the summary.
  await page.locator('.react-flow__node', { hasText: 'Fetch' }).first().click();
  await expect(page.getByTestId('profile-section')).toBeVisible();
  return wfId;
}

test('profile form page: form fits the viewport and the history dialog opens inside it', async ({ page, request }) => {
  const profile = await createProfile(request, `visual-profiles-${Date.now()}`);
  await page.goto(`/hub/${encodeURIComponent('http.request')}/${encodeURIComponent(profile.id)}/edit`);
  await expect(page.getByTestId('profile-form')).toBeVisible();

  const form = page.getByTestId('profile-form');
  const formBox = await form.boundingBox();
  expect(formBox).toBeTruthy();
  // The form panel sits fully inside the viewport at desktop width.
  expect(formBox!.x).toBeGreaterThanOrEqual(0);
  expect(formBox!.x + formBox!.width).toBeLessThanOrEqual(page.viewportSize()!.width);

  // The History tab (UX Phase 2 D2-4) holds the shared diff view and
  // stays inside the viewport too.
  await openProfileTab(page, 'history');
  await page.getByRole('button', { name: 'Show version 1' }).click();
  await page.getByTestId('profile-version-tab-full').click();
  const json = page.getByTestId('profile-version-full');
  await expect(json).toBeVisible();
  const jsonBox = await json.boundingBox();
  expect(jsonBox).toBeTruthy();
  expect(jsonBox!.x).toBeGreaterThanOrEqual(0);
  expect(jsonBox!.x + jsonBox!.width).toBeLessThanOrEqual(page.viewportSize()!.width);

  await page.screenshot({
    path: 'e2e-artifacts/visual-profiles-page.png',
    fullPage: true,
  });
});

test('inspector: effective-config preview rows align inside the panel', async ({ page, request }) => {
  const profile = await createProfile(request, `visual-inspector-${Date.now()}`);
  await openEditorWithProfile(page, request, profile.id);

  const preview = page.getByTestId('profile-preview');
  await expect(preview).toBeVisible();
  const box = await preview.boundingBox();
  expect(box).toBeTruthy();
  expect(box!.width).toBeGreaterThan(0);
  expect(box!.height).toBeGreaterThan(0);

  // Every row is fully inside the preview box, rows stack vertically
  // without overlap, and each row's origin badge stays within its row.
  const rows = preview.locator('.profile-preview-row');
  const count = await rows.count();
  expect(count).toBeGreaterThan(0);
  const boxes = [];
  for (let i = 0; i < count; i += 1) {
    const rowBox = await rows.nth(i).boundingBox();
    expect(rowBox).toBeTruthy();
    expect(rowBox!.x).toBeGreaterThanOrEqual(box!.x - 1);
    expect(rowBox!.x + rowBox!.width).toBeLessThanOrEqual(box!.x + box!.width + 1);
    boxes.push(rowBox!);
  }
  for (let i = 1; i < boxes.length; i += 1) {
    expect(boxes[i].y).toBeGreaterThanOrEqual(boxes[i - 1].y + boxes[i - 1].height - 1);
  }

  // The preview stays inside its own section: in-flow content cannot
  // cover the sections below it, so the section bound is the gate.
  const section = await page.getByTestId('profile-section').boundingBox();
  expect(section).toBeTruthy();
  expect(box!.y).toBeGreaterThanOrEqual(section!.y);
  expect(box!.y + box!.height).toBeLessThanOrEqual(section!.y + section!.height + 1);
  expect(box!.x).toBeGreaterThanOrEqual(section!.x);
  expect(box!.x + box!.width).toBeLessThanOrEqual(section!.x + section!.width + 1);

  await page.screenshot({ path: 'e2e-artifacts/visual-profile-preview.png' });
});

test('inspector: canvas clears the preview column at desktop width', async ({ page, request }) => {
  const profile = await createProfile(request, `visual-canvas-${Date.now()}`);
  await openEditorWithProfile(page, request, profile.id);

  const preview = await page.getByTestId('profile-preview').boundingBox();
  expect(preview).toBeTruthy();
  const canvas = await page.locator('.react-flow').first().boundingBox();
  expect(canvas).toBeTruthy();
  // The inspector column and the canvas do not overlap horizontally.
  expect(canvas!.x + canvas!.width).toBeLessThanOrEqual(preview!.x + 1);

  await page.screenshot({ path: 'e2e-artifacts/visual-canvas-inspector.png' });
});

test('profiles landing: one content rail and one action line', async ({ page }) => {
  for (const width of [1440, 1024]) {
    await page.setViewportSize({ width, height: 900 });
    await page.goto('/profiles');
    await expect(page.getByTestId('profiles-landing-grid').first()).toBeVisible();

    // The header and the first card grid share the same rail: left and
    // right edges match within 1px.
    const rail = await page.evaluate(() => {
      const head = document.querySelector('.page-head') as HTMLElement;
      const grid = document.querySelector('[data-testid="profiles-landing-grid"]') as HTMLElement;
      const h = head.getBoundingClientRect();
      const g = grid.getBoundingClientRect();
      return { headLeft: h.left, headRight: h.right, gridLeft: g.left, gridRight: g.right };
    });
    expect(Math.abs(rail.headLeft - rail.gridLeft)).toBeLessThanOrEqual(1);
    expect(Math.abs(rail.headRight - rail.gridRight)).toBeLessThanOrEqual(1);

    // The action cluster is one line: New profile and the kebab share
    // the same top edge.
    const oneLine = await page.evaluate(() => {
      const actions = document.querySelector('.hub-head-actions') as HTMLElement;
      const kids = [...actions.children].map((child) => child.getBoundingClientRect());
      return kids.every((box) => Math.abs(box.top - kids[0].top) < 1);
    });
    expect(oneLine).toBe(true);
  }

  // The advanced-assets kebab holds the four advanced links.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/profiles');
  const menu = page.getByTestId('advanced-assets');
  await menu.locator('summary').click();
  const list = menu.locator('.action-menu-list');
  await expect(list).toBeVisible();
  for (const name of ['Data contracts', 'Mappings', 'Scripts', 'Connectors']) {
    await expect(list.getByRole('button', { name, exact: true })).toBeVisible();
  }
});

test('profile edit page: header, card, tab strip, table and sticky bar share one rail', async ({
  page,
  request,
}) => {
  const profile = await createProfile(request, `e2e-edit-rail-${Date.now()}`);
  await page.goto(`/hub/${encodeURIComponent('http.request')}/${encodeURIComponent(profile.id)}/edit`);
  await expect(page.getByTestId('profile-form')).toBeVisible();
  await page.setViewportSize({ width: 1440, height: 900 });

  // Header title, card edge, tab strip and the first value row start
  // on the same left x within 1px.
  const edges = await page.evaluate(() => {
    const title = document.querySelector('.page-head h1')?.getBoundingClientRect();
    const card = document.querySelector('[data-testid="profile-form"]')?.getBoundingClientRect();
    const tabs = document.querySelector('.profile-tabs')?.getBoundingClientRect();
    const sticky = document.querySelector('[data-testid="profile-sticky"]')?.getBoundingClientRect();
    if (!title || !card || !tabs || !sticky) return null;
    return {
      title: title.left,
      card: card.left,
      cardRight: card.right,
      tabs: tabs.left,
      sticky: sticky.left,
      stickyRight: sticky.right,
    };
  });
  expect(edges).not.toBeNull();
  expect(Math.abs(edges!.title - edges!.card)).toBeLessThanOrEqual(1);
  expect(Math.abs(edges!.card - edges!.tabs)).toBeLessThanOrEqual(1);
  expect(Math.abs(edges!.card - edges!.sticky)).toBeLessThanOrEqual(1);
  // The sticky bar spans the form width: its right edge sits on the
  // card's edge (within the card's 1px border), not one gutter inside.
  expect(Math.abs(edges!.cardRight - edges!.stickyRight)).toBeLessThanOrEqual(1);

  // A value row shares the card's content gutter with the rest of the
  // form (one grid inside the card, labels aligned across rows).
  await page.getByTestId('profile-tab-trust').click();
  await page.getByTestId('profile-section-preview').click();
  const row = await page.evaluate(() => {
    const card = document.querySelector('[data-testid="profile-form"]');
    const rows = document.querySelectorAll('.profile-form .profile-preview-row');
    if (!card || rows.length === 0) return null;
    const box = card.getBoundingClientRect();
    const first = rows[0].getBoundingClientRect();
    const aligned = Array.from(rows).every(
      (element) => Math.abs(element.getBoundingClientRect().left - first.left) <= 1,
    );
    return {
      rowLeft: first.left,
      rowRight: first.right,
      cardLeft: box.left,
      cardRight: box.right,
      aligned,
    };
  });
  expect(row).not.toBeNull();
  expect(row!.rowLeft).toBeGreaterThan(row!.cardLeft);
  expect(row!.rowRight).toBeLessThanOrEqual(row!.cardRight);
  expect(row!.aligned).toBe(true);
});

test('profile edit page at 390px: the sticky bar wraps and nothing clips', async ({ page, request }) => {
  const profile = await createProfile(request, `e2e-edit-mobile-${Date.now()}`);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/hub/${encodeURIComponent('http.request')}/${encodeURIComponent(profile.id)}/edit`);
  await expect(page.getByTestId('profile-form')).toBeVisible();

  const facts = await page.evaluate(() => {
    const shell = document.querySelector('.page-shell');
    const sticky = document.querySelector('[data-testid="profile-sticky"]');
    if (!shell || !sticky) return null;
    const buttons = Array.from(sticky.querySelectorAll('button'));
    const allInside = buttons.every(
      (button) =>
        button.getBoundingClientRect().right <= window.innerWidth + 1 &&
        button.getBoundingClientRect().left >= -1,
    );
    return {
      overflow: shell.scrollWidth - shell.clientWidth,
      barInside:
        sticky.getBoundingClientRect().left >= 0 &&
        sticky.getBoundingClientRect().right <= window.innerWidth,
      buttonsInside: allInside,
      wraps: window.getComputedStyle(sticky).flexWrap === 'wrap',
    };
  });
  expect(facts).not.toBeNull();
  expect(facts!.overflow).toBeLessThanOrEqual(1);
  expect(facts!.barInside).toBe(true);
  expect(facts!.buttonsInside).toBe(true);
  expect(facts!.wraps).toBe(true);
});
