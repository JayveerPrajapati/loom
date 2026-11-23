import { expect, test } from '@playwright/test';
import { login } from './auth';

test.beforeEach(async ({ request, context }) => {
  await login(request);
  await login(context.request);
});

test('the template view lets the shell scroll and the last card is reachable', async ({
  page,
  request,
}) => {
  const wfId = `e2e-tv-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: 'Template view probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  await request.post(`/api/workflows/${wfId}/publish`);
  try {
    // A short viewport is where the fixed height used to clip: the
    // side cards run taller than the viewport-derived track.
    await page.setViewportSize({ width: 1280, height: 600 });
    await page.goto(`/workflows/${encodeURIComponent(wfId)}`);
    await page.waitForSelector('.template-view-layout', { timeout: 15000 });

    const facts = await page.evaluate(() => {
      const shell = document.querySelector('.page-shell');
      const side = document.querySelector('.template-view-side');
      const last = side ? Array.from(side.children).pop() : null;
      if (!shell || !side || !last) return null;
      return {
        shellScrolls: shell.scrollHeight > shell.clientHeight,
        sideOverflow: side.scrollHeight - side.clientHeight,
        sideClips: window.getComputedStyle(side).overflow,
      };
    });
    expect(facts).not.toBeNull();
    // The side column never clips (no overflow: hidden) — when the
    // cards run past the fold the shell owns the scroll.
    expect(['visible', 'clip']).not.toContain('hidden');
    expect(Math.abs(facts!.sideOverflow)).toBeLessThanOrEqual(1);

    if (facts!.shellScrolls) {
      const revealed = await page.evaluate(() => {
        const shell = document.querySelector('.page-shell')!;
        shell.scrollTop = shell.scrollHeight;
        const last = Array.from(
          document.querySelector('.template-view-side')?.children ?? [],
        ).pop();
        if (!last) return false;
        const box = last.getBoundingClientRect();
        return box.bottom > 0 && box.bottom <= window.innerHeight + 1;
      });
      expect(revealed).toBe(true);
    }
  } finally {
    await request.delete(`/api/workflows/${wfId}`);
  }
});
