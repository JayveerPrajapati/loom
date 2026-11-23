import { expect, test } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test('initial unauthenticated load shows no error banner', async ({ page, context }) => {
  // UX Phase 2.1: the initial expected 401 is not an error — the sign-in
  // screen renders bare, and startup makes exactly one /api/auth/me call.
  await context.clearCookies();
  let meCalls = 0;
  page.on('request', (request) => {
    if (request.url().includes('/api/auth/me')) meCalls += 1;
  });
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(page.locator('.banner.error')).toHaveCount(0);
  await expect(page.locator('.banner.notice')).toHaveCount(0);
  expect(meCalls).toBe(1);
});

test('sign-in failure shows an actionable error', async ({ page, context }) => {
  await context.clearCookies();
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill('wrong-password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.banner.error[role="alert"]')).toBeVisible();

  // The failed fields are programmatically tied to the error message.
  const email = page.locator('#auth-email');
  const password = page.locator('#auth-password');
  await expect(email).toHaveAttribute('aria-invalid', 'true');
  await expect(password).toHaveAttribute('aria-invalid', 'true');
  const describedBy = await email.getAttribute('aria-describedby');
  expect(describedBy).toBeTruthy();
  expect(await password.getAttribute('aria-describedby')).toBe(describedBy);
  // The description target is the visible alert itself.
  const alert = page.locator(`[id="${describedBy}"]`);
  await expect(alert).toBeVisible();
  await expect(alert).toHaveAttribute('role', 'alert');
  await expect(alert).toContainText('Invalid email or password');
});

test('expired session shows a non-error notice', async ({ page, context }) => {
  // UX Phase 2.1: a mid-session expiry is normal operation — the sign-in
  // screen explains it with a notice, not an error banner. Expiry is
  // simulated the real way: the session is revoked server-side.
  await context.clearCookies();
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();

  await context.request.post('/api/auth/logout');
  // In-app navigation (not a full reload) so the still-mounted app hits the
  // revoked session and shows the expiry notice.
  await page.getByRole('link', { name: 'Runs', exact: true }).first().click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
  await expect(page.locator('.banner.notice')).toContainText('session ended');
  await expect(page.locator('.banner.error')).toHaveCount(0);
  // The expiry notice is informational: the fields are not marked invalid.
  await expect(page.locator('#auth-email')).not.toHaveAttribute('aria-invalid');
  await expect(page.locator('#auth-password')).not.toHaveAttribute('aria-invalid');
});

test('signs in, opens the account panel, and signs out', async ({ page, context }) => {
  await context.clearCookies();
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();

  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill(e2eCode);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();

  await page.locator('.settings-menu summary').click();
  await page.getByRole('button', { name: 'Account' }).click();
  const panel = page.locator('.admin-dialog');
  await expect(panel).toBeVisible();
  await expect(panel).toContainText(e2eUser);

  // Escape closes the account panel.
  await page.keyboard.press('Escape');
  await expect(panel).toBeHidden();

  const accountButton = page.getByRole('button', { name: 'Account' });
  if (!(await accountButton.isVisible())) {
    await page.locator('.settings-menu summary').click();
  }
  await accountButton.click();
  await expect(panel).toBeVisible();

  await panel.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();
});

test('creates a user and an API token from the account panel', async ({ page }) => {
  await page.goto('/');
  await page.locator('.settings-menu summary').click();
  await page.getByRole('button', { name: 'Account' }).click();
  const panel = page.locator('.admin-dialog');
  await expect(panel).toBeVisible();

  const email = `e2e-user-${Date.now()}@example.com`;
  await panel.locator('#user-email').fill(email);
  await panel.locator('#user-name').fill('E2E User');
  await panel.locator('#user-role').selectOption('viewer');
  await panel.locator('#user-password').fill('e2e-viewer-password');
  await panel.getByRole('button', { name: 'Create user' }).click();
  await expect(panel.getByRole('list', { name: 'Users' })).toContainText(email);

  await panel.locator('#token-name').fill('e2e token');
  await panel.getByRole('button', { name: 'Create token' }).click();
  await expect(panel.locator('pre[aria-label="New API token"]')).toBeVisible();
  await expect(panel.locator('.banner.success')).toBeVisible();
  await expect(panel.locator('.banner.success')).toContainText('Token created');
});

test('account dialog does not reappear after a revoked session and relogin', async ({
  page,
  browser,
  context,
  request,
}) => {
  const email = `e2e-revoke-${Date.now()}@example.com`;
  const password = 'e2e-revoke-password-1';
  await request.post('/api/users', {
    data: { email, name: 'Revoke Probe', role: 'admin', password },
  });

  await context.clearCookies();
  await page.goto('/');
  await page.locator('#auth-email').fill(email);
  await page.locator('#auth-password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();

  await page.locator('.settings-menu summary').click();
  await page.getByRole('button', { name: 'Account' }).click();
  const panel = page.locator('.admin-dialog');
  await expect(panel).toBeVisible();

  // Revoke the page's session by changing the password from another context.
  const other = await browser.newContext();
  await other.request.post('/api/auth/login', { data: { email, password } });
  const change = await other.request.post('/api/auth/password', {
    data: { current: password, new: `${password}-2` },
  });
  expect(change.status()).toBe(200);

  // The open dialog's own password update now 401s and forces the login card.
  await panel.locator('#account-current').fill(password);
  await panel.locator('#account-next').fill(`${password}-3`);
  await panel.getByRole('button', { name: 'Update password' }).click();
  await expect(page.getByRole('heading', { name: 'Sign in' })).toBeVisible();

  // Sign back in: the account dialog must not remount over the topbar.
  await page.locator('#auth-email').fill(email);
  await page.locator('#auth-password').fill(`${password}-2`);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.locator('.topbar')).toBeVisible();
  await expect(page.locator('.admin-dialog')).toHaveCount(0);
  await page.locator('.settings-menu summary').click();
  await expect(page.getByRole('button', { name: 'Account' })).toBeVisible();
  await other.close();
});

test('a disabled account gets a distinct sign-in error', async ({ page, context, request }) => {
  const email = `e2e-disabled-${Date.now()}@example.com`;
  const password = 'e2e-disabled-password-1';
  await request.post('/api/users', {
    data: { email, name: 'Disabled Probe', role: 'viewer', password },
  });
  const users = (await (await request.get('/api/users')).json()).users ?? [];
  const user = users.find((u: { email: string }) => u.email === email);
  await request.put(`/api/users/${user.id}`, { data: { disabled: true } });

  await context.clearCookies();
  await page.goto('/');
  await page.locator('#auth-email').fill(email);
  await page.locator('#auth-password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('disabled');

  await page.locator('#auth-password').fill('wrong-password');
  await page.getByRole('button', { name: 'Sign in' }).click();
  await expect(page.getByRole('alert')).toContainText('Invalid email or password');
});

test('a locked-out account shows the retry countdown', async ({ page, context, request }) => {
  const email = `e2e-lockout-${Date.now()}@example.com`;
  const password = 'e2e-lockout-password-1';
  await request.post('/api/users', {
    data: { email, name: 'Lockout Probe', role: 'viewer', password },
  });

  for (let i = 0; i < 10; i += 1) {
    const failed = await request.post('/api/auth/login', {
      data: { email, password: 'wrong-password' },
    });
    expect(failed.status()).toBe(401);
  }
  const locked = await request.post('/api/auth/login', { data: { email, password } });
  expect(locked.status()).toBe(429);
  expect((await locked.json()).retryAfterSec).toBeGreaterThan(0);

  await context.clearCookies();
  await page.goto('/');
  await page.locator('#auth-email').fill(email);
  await page.locator('#auth-password').fill(password);
  await page.getByRole('button', { name: 'Sign in' }).click();
  // The lockout start is announced once by the alert; the visible countdown
  // updates per second below it and must not itself be a live region.
  await expect(page.getByRole('alert')).toContainText('Too many attempts. Temporarily locked.');
  const countdown = page.getByTestId('auth-lockout-countdown');
  await expect(countdown).toContainText('Try again in about');
  expect(await countdown.getAttribute('role')).toBeNull();
  expect(await countdown.getAttribute('aria-live')).toBeNull();
});

test('the lockout countdown ends with one polite announcement', async ({ page, context }) => {
  // A one-second server lockout: the announcement lifecycle is the point,
  // not the real lockout duration.
  await context.clearCookies();
  await page.route('**/api/auth/login', (route) =>
    route.fulfill({
      status: 429,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'Too many attempts.', retryAfterSec: 1 }),
    }),
  );
  await page.goto('/');
  await page.locator('#auth-email').fill(e2eUser);
  await page.locator('#auth-password').fill('irrelevant-while-locked');
  await page.getByRole('button', { name: 'Sign in' }).click();

  const countdown = page.getByTestId('auth-lockout-countdown');
  await expect(countdown).toContainText('Try again in about');
  await expect(countdown).toHaveCount(0);

  // The countdown is replaced by a single polite unlock note.
  const status = page.getByTestId('auth-lockout-status');
  await expect(status).toBeVisible();
  await expect(status).toHaveText('You can try again.');
  await expect(status).toHaveAttribute('role', 'status');
});
