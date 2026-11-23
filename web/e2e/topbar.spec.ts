import { expect, test } from '@playwright/test';
import { login } from './auth';

// Phase 2 top bar: primary actions (Run, Publish) stay visible, and every
// secondary policy control lives in one keyboard-reachable More menu.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

async function seedEditor(
  request: Parameters<typeof login>[0],
  page: import('@playwright/test').Page,
) {
  const id = `e2e-topbar-${Date.now()}-${Math.random().toString(36).slice(2, 6)}`;
  const created = await request.post('/api/workflows', {
    data: {
      id,
      name: 'Topbar grammar',
      version: 1,
      nodes: [{ id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} }],
      edges: [],
    },
  });
  expect(created.status(), await created.text()).toBe(201);
  await page.goto(`/workflows/${encodeURIComponent(id)}/edit`);
  await expect(page.locator('.workspace')).toBeVisible();
  return id;
}

test('primary actions stay visible and More opens with the keyboard', async ({ page, request }) => {
  const id = await seedEditor(request, page);
  try {
    await expect(page.getByRole('button', { name: 'Run', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Publish', exact: true })).toBeVisible();
    const more = page.getByTestId('topbar-more');
    await expect(more).toBeVisible();

    await more.locator('summary').focus();
    await page.keyboard.press('Enter');
    await expect(more).toHaveAttribute('open', '');
    await expect(page.locator('.version-chip')).toBeVisible();
    await expect(page.getByTestId('workflow-egress-chip')).toBeVisible();

    await page.keyboard.press('Escape');
    await expect(more).not.toHaveAttribute('open', '');
  } finally {
    await request.delete(`/api/workflows/${id}`);
  }
});

test('Run, Publish and More remain visible on a phone without overflow', async ({
  page,
  request,
}) => {
  const id = await seedEditor(request, page);
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.reload();
    await expect(page.locator('.workspace')).toBeVisible();
    await expect(page.getByRole('button', { name: 'Run', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Publish', exact: true })).toBeVisible();
    await expect(page.getByTestId('topbar-more')).toBeVisible();

    const overflow = await page.evaluate(() => {
      const bar = document.querySelector<HTMLElement>('.topbar');
      return {
        document: document.documentElement.scrollWidth - window.innerWidth,
        bar: bar ? bar.scrollWidth - bar.clientWidth : 0,
      };
    });
    expect(overflow.document).toBeLessThanOrEqual(1);
    expect(overflow.bar).toBeLessThanOrEqual(1);
  } finally {
    await request.delete(`/api/workflows/${id}`);
  }
});
