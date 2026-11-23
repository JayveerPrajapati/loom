import { expect, test } from '@playwright/test';
import { login } from './auth';

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test('admin capability page names the simulated host posture', async ({ page }) => {
  await page.goto('/capabilities');
  const root = page.getByTestId('capabilities-page');
  await expect(root).toBeVisible();
  await expect(root.getByRole('heading', { name: 'Capabilities' })).toBeVisible();
  await expect(page.getByTestId('capabilities-auth')).toContainText('not enabled');
  await expect(page.getByTestId('capabilities-subprocess')).toContainText(
    'Subprocess nodes are disabled',
  );
  await expect(page.getByTestId('capabilities-egress')).toContainText(
    'No live egress posture',
  );
  await expect(page.getByTestId('capabilities-warnings')).toContainText(
    'No capability warnings',
  );
  await expect(page.getByRole('link', { name: 'Capabilities' }).first()).toBeVisible();
});
