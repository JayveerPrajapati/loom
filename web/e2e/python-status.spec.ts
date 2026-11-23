import { expect, test } from '@playwright/test';
import { login } from './auth';

// The /scripts page carries the Python runtime status as a compact
// strip — the sim e2e server reports subprocess disabled, so the strip
// states that up front and a Details disclosure explains why, how to
// enable it, and that hermetic replay still works.
test('scripts page shows the Python runtime status strip (disabled on sim)', async ({ page }) => {
  await login(page.context().request);
  await page.goto('/scripts');
  const card = page.getByTestId('python-status-card');
  await expect(card).toBeVisible();
  await expect(card.getByTestId('python-status-disabled')).toHaveText('disabled');
  // Collapsed by default: one line with the state and the summary.
  await expect(card).toContainText('JavaScript tests and hermetic replay keep working');
  await expect(card.locator('details')).not.toHaveAttribute('open');

  // Expanding surfaces the full explanation.
  await card.getByText('Details').click();
  await expect(card.locator('details')).toHaveAttribute('open');
  await expect(card).toContainText('largest security surface in Loom');
  await expect(card).toContainText('--enable-subprocess-nodes');
  await expect(card).toContainText('LOOM_ENABLE_SUBPROCESS_NODES=1');
  await expect(card).toContainText('docs/security.md');
  await expect(card).toContainText('replay verifies the recorded tape');
  // Disabled server: no envs listing is fetched or fabricated.
  await expect(page.getByTestId('python-envs-list')).toHaveCount(0);
  await expect(page.getByTestId('python-envs-empty')).toHaveCount(0);
});

// Phase 5 D5-4: the recorded mode reads a finished run's effect tape —
// it works on the simulated e2e server and answers found:false with an
// explanation rather than an error.
test("script tester offers a recorded mode that explains when nothing is taped", async ({ page, request }) => {
  await login(request);
  await login(page.context().request);
  const stamp = Date.now();
  const scriptResp = await request.post('/api/scripts', {
    data: { name: `e2e-recorded-${stamp}`, language: 'javascript', source: 'return 1;' },
  });
  expect(scriptResp.status()).toBe(201);
  const script = await scriptResp.json();

  await page.goto('/scripts');
  const row = page.locator('.dashboard-run-row', { hasText: `e2e-recorded-${stamp}` });
  await row.getByRole('button', { name: 'Test' }).click();
  await expect(page.getByTestId('script-tester')).toBeVisible();
  await page.getByRole('button', { name: 'Recorded result' }).click();
  const recorded = page.getByTestId('script-recorded-result');
  await expect(recorded).toBeVisible();
  await expect(recorded).toContainText('javascript runs in-process and is not taped');
});
