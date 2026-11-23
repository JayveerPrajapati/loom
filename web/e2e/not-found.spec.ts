import { expect, test } from '@playwright/test';
import { login } from './auth';

// U10: an unknown path gets a named not-found page and a real way home.
test.beforeEach(async ({ context }) => {
  await login(context.request);
});

test('unknown paths show a not-found page that returns home', async ({ page }) => {
  await page.goto('/no-such-page');
  await expect(page.getByRole('heading', { name: 'Page not found' })).toBeVisible();
  await expect(page.locator('code').filter({ hasText: '/no-such-page' })).toBeVisible();

  await page.getByRole('button', { name: 'Back to home' }).click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole('heading', { name: 'Page not found' })).toHaveCount(0);
});
