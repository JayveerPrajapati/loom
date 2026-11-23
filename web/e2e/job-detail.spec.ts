import { expect, test } from '@playwright/test';
import { login } from './auth';

test.beforeEach(async ({ request, context }) => {
  await login(request);
  await login(context.request);
});

async function seedJob(request: any, tag: string) {
  const wfId = `e2e-job-${tag}-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: `Job WF ${tag}`,
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  await request.post(`/api/workflows/${wfId}/publish`);
  const job = await (
    await request.post('/api/jobs', {
      data: { name: `e2e-job-${tag}`, templateId: wfId, templateVersion: 1 },
    })
  ).json();
  return { wfId, job };
}

test('the job header keeps the title wide at 1024 and its actions reachable', async ({
  page,
  request,
}) => {
  const { wfId, job } = await seedJob(request, 'head');
  try {
    await page.setViewportSize({ width: 1024, height: 700 });
    await page.goto(`/jobs/${encodeURIComponent(job.id)}`);
    await page.waitForSelector('.job-detail-head h1');

    // The title column keeps at least 40% of the header width with the
    // secondary actions folded into the kebab.
    const facts = await page.evaluate(() => {
      const head = document.querySelector('.job-detail-head');
      const titleBlock = head?.querySelector('div');
      if (!head || !titleBlock) return null;
      const hb = head.getBoundingClientRect();
      const tb = titleBlock.getBoundingClientRect();
      return { pct: (tb.width / hb.width) * 100 };
    });
    expect(facts).not.toBeNull();
    expect(facts!.pct).toBeGreaterThanOrEqual(40);

    // The visible actions are Run batch, More and Run now; the rest
    // live in the kebab, which stays inside the viewport.
    await expect(page.getByRole('button', { name: 'Run now' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Run batch' })).toBeVisible();
    const kebab = page.locator('.job-actions .action-menu');
    await kebab.locator('summary').click();
    for (const action of ['Pause', 'Edit settings', 'Duplicate', 'Delete']) {
      await expect(kebab.getByRole('button', { name: action, exact: true })).toBeVisible();
    }
    const menuBox = await kebab.locator('.action-menu-list').evaluate((el) => {
      const box = el.getBoundingClientRect();
      return { left: box.left, right: box.right, top: box.top, bottom: box.bottom };
    });
    expect(menuBox.left).toBeGreaterThanOrEqual(0);
    expect(menuBox.right).toBeLessThanOrEqual(1024);
    await page.keyboard.press('Escape');
  } finally {
    await request.delete(`/api/jobs/${job.id}`);
    await request.delete(`/api/workflows/${wfId}`);
  }
});

test('the job header wraps without clipping at 390', async ({ page, request }) => {
  const { wfId, job } = await seedJob(request, 'mobile');
  try {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(`/jobs/${encodeURIComponent(job.id)}`);
    await page.waitForSelector('.job-detail-head h1');
    const facts = await page.evaluate(() => {
      const shell = document.querySelector('.page-shell');
      const actions = document.querySelector('.job-actions');
      const buttons = actions ? Array.from(actions.querySelectorAll('button')) : [];
      if (!shell || !actions) return null;
      return {
        overflow: shell.scrollWidth - shell.clientWidth,
        buttonsInside: buttons.every(
          (button) =>
            button.getBoundingClientRect().right <= window.innerWidth + 1 &&
            button.getBoundingClientRect().left >= -1,
        ),
      };
    });
    expect(facts).not.toBeNull();
    expect(facts!.overflow).toBeLessThanOrEqual(1);
    expect(facts!.buttonsInside).toBe(true);
  } finally {
    await request.delete(`/api/jobs/${job.id}`);
    await request.delete(`/api/workflows/${wfId}`);
  }
});

test('the kebab is keyboard reachable and the moved actions focus visibly', async ({
  page,
  request,
}) => {
  const { wfId, job } = await seedJob(request, 'kb');
  try {
    await page.setViewportSize({ width: 1024, height: 700 });
    await page.goto(`/jobs/${encodeURIComponent(job.id)}`);
    await page.waitForSelector('.job-detail-head h1');

    // The kebab opens with the keyboard and the moved actions are
    // focusable inside it.
    const summary = page.locator('.job-actions .action-menu > summary');
    await summary.focus();
    await expect(summary).toBeFocused();
    await page.keyboard.press('Enter');
    const pause = page.locator('.job-actions .action-menu').getByRole('button', {
      name: 'Pause',
      exact: true,
    });
    await expect(pause).toBeVisible();
    await pause.focus();
    await expect(pause).toBeFocused();
  } finally {
    await request.delete(`/api/jobs/${job.id}`);
    await request.delete(`/api/workflows/${wfId}`);
  }
});
