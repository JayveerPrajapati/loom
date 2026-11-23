import { expect, test } from '@playwright/test';
import { login } from './auth';

// Phase 1A (campaign 1.2 + 1.9) regression: the phantom layer — classes
// markup referenced with no rule anywhere — plus the page-header type
// role. Live computed styles, per the campaign's verification rule;
// nothing here asserts layouts that later phases redesign.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  const profiles = await request.get('/api/config-profiles');
  if (profiles.ok()) {
    const { profiles: list } = (await profiles.json()) as {
      profiles: { id: string; name: string }[];
    };
    for (const profile of list ?? []) {
      if (profile.name.startsWith('e2e-')) {
        await request.delete(`/api/config-profiles/${profile.id}`);
      }
    }
  }
});

test('scripts page: page-sub is 13px muted and the h1 carries the title role', async ({ page }) => {
  await page.goto('/scripts');
  // React renders the page shell after load; wait for the element the
  // assertion reads instead of evaluating against a half-mounted DOM.
  await expect(page.locator('.page-head h1')).toBeVisible();
  await expect(page.locator('.page-sub')).toBeVisible();

  const facts = await page.evaluate(() => {
    const sub = document.querySelector('.page-sub');
    const h1 = document.querySelector('.page-head h1');
    if (!sub || !h1) return null;
    // Resolve --muted through a probe element so the comparison holds
    // in every theme without hardcoding a colour.
    const probe = document.createElement('span');
    probe.style.color = 'var(--muted)';
    document.body.appendChild(probe);
    const muted = getComputedStyle(probe).color;
    probe.remove();
    const subStyles = getComputedStyle(sub);
    const h1Styles = getComputedStyle(h1);
    return {
      subColor: subStyles.color,
      muted,
      subSize: subStyles.fontSize,
      h1Size: h1Styles.fontSize,
      h1Margin: h1Styles.margin,
    };
  });
  expect(facts).not.toBeNull();
  expect(facts!.subSize).toBe('13px');
  expect(facts!.subColor).toBe(facts!.muted);
  expect(facts!.h1Size).toBe('28px');
  expect(facts!.h1Margin).toBe('0px');
});

test('scripts page: the filter label is visually hidden (clipped to 1px)', async ({ page }) => {
  await page.goto('/scripts');

  const hidden = page.locator('.visually-hidden').first();
  await expect(hidden).toBeAttached();
  const width = await hidden.evaluate((el) => el.getBoundingClientRect().width);
  expect(width).toBeLessThanOrEqual(1);
});

test('all eight token aliases resolve to non-empty values on the root', async ({ page }) => {
  await page.goto('/scripts');

  const aliases = await page.evaluate(() => {
    const styles = getComputedStyle(document.documentElement);
    const names = [
      '--radius',
      '--radius-s',
      '--focus',
      '--mono',
      '--text-s',
      '--text-2xs',
      '--text-muted',
      '--panel-3',
    ];
    return names.map((name) => ({ name, value: styles.getPropertyValue(name).trim() }));
  });
  for (const { name, value } of aliases) {
    expect(value, `${name} must resolve`).not.toBe('');
  }
});

test('profile history dialog: the head is a flex bar and Close sits inside it', async ({ page, request }) => {
  const stamp = Date.now();
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-ui-head-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://api.example.com/ui' },
      },
    })
  ).json();

  await page.goto(`/hub/http.request/${encodeURIComponent(created.id)}`);
  await page.getByTestId('profile-view-history').click();

  const head = page.locator('.dialog-head');
  await expect(head).toBeVisible();
  const display = await head.evaluate((el) => getComputedStyle(el).display);
  expect(display).toBe('flex');

  const close = page.getByRole('button', { name: 'Close history' });
  await expect(close).toBeVisible();
  const inHead = await close.evaluate((el) => el.closest('.dialog-head') !== null);
  expect(inHead).toBe(true);
});
