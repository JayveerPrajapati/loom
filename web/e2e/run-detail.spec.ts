import { expect, test } from '@playwright/test';
import { login } from './auth';

test.beforeEach(async ({ request, context }) => {
  await login(request);
  await login(context.request);
});

test('the run view grows with its panels instead of capping them', async ({
  page,
  request,
}) => {
  const wfId = `e2e-rd-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: 'Run detail probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  await request.post(`/api/workflows/${wfId}/publish`);
  const started = await request.post(`/api/workflows/${wfId}/runs?async=1`, {
    data: { input: {} },
  });
  const { runId } = await started.json();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const run = await (await request.get(`/api/runs/${runId}`)).json();
    if (['done', 'failed', 'cancelled', 'waiting'].includes(run.status)) break;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  try {
    await page.setViewportSize({ width: 1280, height: 600 });
    await page.goto(`/runs/${encodeURIComponent(runId)}`);
    await page.waitForSelector('.run-view', { timeout: 15000 });
    await page.waitForTimeout(1000);

    // L4: the run summary is a labeled, fully populated strip — not an
    // empty 5-segment bar. Every field has a visible label and value.
    const strip = page.locator('.job-summary-strip');
    await expect(strip).toBeVisible();
    await expect(strip).toHaveAttribute('aria-label', 'Run summary');
    const stripCells = strip.locator(':scope > div');
    await expect(stripCells).toHaveCount(7);
    for (const label of [
      'Status',
      'Trigger',
      'Duration',
      'Nodes',
      'Slowest',
      'Retries',
      'Version',
    ]) {
      await expect(
        stripCells.filter({ hasText: label }),
        `the ${label} summary field is labeled`,
      ).toHaveCount(1);
    }

    const facts = await page.evaluate(() => {
      const view = document.querySelector('.run-view');
      const shell = document.querySelector('.page-shell');
      if (!view || !shell) return null;
      return {
        heightStyle: window.getComputedStyle(view).height,
        hasMinHeight: window.getComputedStyle(view).minHeight !== 'auto',
        viewScrollsItself: view.scrollHeight > view.clientHeight,
        scrollHeight: view.scrollHeight,
        clientHeight: view.clientHeight,
        shellScrolls: shell.scrollHeight > shell.clientHeight,
      };
    });
    expect(facts).not.toBeNull();
    // The view has no viewport-derived height cap: its height follows
    // its content (min-height floor respected), and the view never
    // scrolls itself — the page shell owns the scroll.
    expect(facts!.hasMinHeight).toBe(true);
    expect(facts!.viewScrollsItself).toBe(false);
    expect(facts!.clientHeight).toBeGreaterThanOrEqual(facts!.scrollHeight - 1);
  } finally {
    await request.delete(`/api/workflows/${wfId}`);
  }
});

test('the raw events dock never lets the time overlap the label', async ({
  page,
  request,
}) => {
  // Node ids mirror the audit's overlap report (schedule/query/compose)
  // plus one realistic long id — unbreakable tokens are what slide under
  // the timestamp. The http node produces the "Recorded N external
  // effects" rows that wrap hardest.
  const wfId = `e2e-raw-layout-${Date.now()}`;
  const longId = 'fetchcomplianceattachmentsfrompartnerapibatch';
  await request.post('/api/workflows', {
    data: {
      id: wfId,
      name: 'Raw layout probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
        { id: 'schedule', type: 'data.set', typeVersion: 1, name: 'Schedule', position: { x: 260, y: 0 }, config: { values: { message: 'hi' }, keepOnlySet: true } },
        { id: 'query', type: 'http.request', typeVersion: 1, name: 'Query', position: { x: 520, y: 0 }, config: { method: 'GET', url: 'https://example.test/api' } },
        { id: longId, type: 'data.set', typeVersion: 1, name: 'Long', position: { x: 780, y: 0 }, config: { values: { summary: 'done' }, keepOnlySet: true } },
      ],
      edges: [
        { from: { node: 'start', port: 'main' }, to: { node: 'schedule', port: 'main' } },
        { from: { node: 'schedule', port: 'main' }, to: { node: 'query', port: 'main' } },
        { from: { node: 'query', port: 'main' }, to: { node: longId, port: 'main' } },
      ],
    },
  });
  await request.post(`/api/workflows/${wfId}/publish`);
  const started = await request.post(`/api/workflows/${wfId}/runs?async=1`, {
    data: { input: {} },
  });
  const { runId } = await started.json();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const run = await (await request.get(`/api/runs/${runId}`)).json();
    if (['done', 'failed', 'cancelled', 'waiting'].includes(run.status)) break;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  try {
    for (const viewport of [
      { width: 1440, height: 900 },
      { width: 1024, height: 768 },
    ]) {
      await page.setViewportSize(viewport);
      await page.goto(`/runs/${encodeURIComponent(runId)}`);
      await page.waitForSelector('.run-view', { timeout: 15000 });
      await page.getByRole('tab', { name: /Raw events/ }).click();
      await expect(page.getByTestId('run-event-list')).toBeVisible();

      const facts = await page.evaluate(() => {
        const measure = (row: Element) => {
          const main = row.querySelector('.event-main') as HTMLElement | null;
          const time = row.querySelector('.event-time') as HTMLElement | null;
          const rowMain = row.querySelector('.event-row-main') as HTMLElement | null;
          if (!main || !time || !rowMain) return null;
          const label = main.getBoundingClientRect();
          const stamp = time.getBoundingClientRect();
          const overlapX = Math.min(label.right, stamp.right) - Math.max(label.left, stamp.left);
          const overlapY = Math.min(label.bottom, stamp.bottom) - Math.max(label.top, stamp.top);
          return {
            overlapX,
            overlapY,
            rowOverflow: rowMain.scrollWidth - rowMain.clientWidth,
          };
        };
        return {
          started: [...document.querySelectorAll('.event-node-started')].map(measure),
          effects: [...document.querySelectorAll('.event-node-effects')].map(measure),
        };
      });
      expect(facts.started.length, 'node.started rows are rendered').toBeGreaterThan(0);
      expect(facts.effects.length, 'node.effects rows are rendered').toBeGreaterThan(0);
      for (const [group, rows] of [
        ['node.started', facts.started],
        ['node.effects', facts.effects],
      ] as const) {
        for (const row of rows) {
          expect(row, `${group} row geometry is measurable`).not.toBeNull();
          expect(
            row!.overlapX <= 0 || row!.overlapY <= 0,
            `${group}: the timestamp must not intersect the label (x=${row!.overlapX}, y=${row!.overlapY}) at ${viewport.width}x${viewport.height}`,
          ).toBeTruthy();
          expect(
            row!.rowOverflow,
            `${group}: the row has no horizontal overflow at ${viewport.width}x${viewport.height}`,
          ).toBeLessThanOrEqual(1);
        }
      }
    }
  } finally {
    await request.delete(`/api/workflows/${wfId}`);
  }
});

test('the schema editor rail has one scroll owner', async ({ page, request }) => {
  // A contract with many fields makes the editor body long.
  const fields = [];
  for (let i = 0; i < 24; i += 1) fields.push({ name: `field${i}`, type: 'string' });
  const created = await request.post('/api/data-schemas', {
    data: { name: `e2e-scroll-${Date.now()}`, fields },
  });
  expect(created.ok()).toBeTruthy();
  const schema = await created.json();
  try {
    await page.setViewportSize({ width: 1280, height: 600 });
    await page.goto('/schemas');
    const row = page.locator('.dashboard-run-row').filter({ hasText: `e2e-scroll` });
    await row.getByRole('button', { name: 'View', exact: true }).click();
    const panel = page.locator('.asset-editor');
    await expect(panel).toBeVisible();
    await panel.getByRole('button', { name: 'Edit', exact: true }).click();

    const facts = await page.evaluate(() => {
      const panel = document.querySelector('.asset-editor');
      const body = document.querySelector('.asset-editor-body');
      if (!panel || !body) return null;
      return {
        panelOverflow: panel.scrollHeight - panel.clientHeight,
        panelMaxHeight: window.getComputedStyle(panel).maxHeight,
        bodyOverflows: body.scrollHeight > body.clientHeight,
      };
    });
    expect(facts).not.toBeNull();
    // The panel never scrolls — the page shell owns the scroll.
    expect(facts!.panelMaxHeight).toBe('none');
    expect(facts!.panelOverflow).toBeLessThanOrEqual(1);
    expect(facts!.bodyOverflows).toBe(false);
  } finally {
    await request.delete(`/api/schemas/${schema.id}`);
  }
});
