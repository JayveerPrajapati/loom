import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

function historyWorkflow(id: string) {
  return {
    id,
    name: `History panel ${id}`,
    version: 1,
    nodes: [
      { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', position: { x: 0, y: 0 }, config: {} },
      {
        id: 'approve',
        type: 'flow.approval',
        typeVersion: 1,
        name: 'Approve publish',
        position: { x: 260, y: 0 },
        config: { channel: '#content', message: 'Publish?' },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'approve', port: 'main' } }],
  };
}

async function seedRun(request: APIRequestContext, id: string): Promise<void> {
  expect((await request.post('/api/workflows', { data: historyWorkflow(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs?async=1`, { data: { input: {} } });
  expect([201, 202]).toContain(started.status());
  const { runId } = await started.json();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const res = await request.get(`/api/runs/${runId}`);
    if (res.ok()) {
      const run = await res.json();
      if (['done', 'failed', 'cancelled', 'waiting'].includes(run.status)) return;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  throw new Error('seeded run did not settle');
}

async function openHistory(page: Page, id: string) {
  await page.goto(`/workflows/${id}/edit`);
  await page.locator('.workspace').waitFor({ state: 'visible' });
  // The dialog opens from the canvas More menu.
  await expect(page.locator('.runbar-menu summary')).toHaveText('More');
  await page.locator('.runbar-menu summary').click();
  await page.getByRole('button', { name: 'Run history' }).click();
  const panel = page.locator('.history-dialog');
  await panel.waitFor({ state: 'visible' });
  return panel;
}

// Smallest computed font size of every visible element inside the dialog that
// renders its own text, plus the offenders below 11px if any.
function minTextSize(el: HTMLElement) {
  let min = Infinity;
  const offenders: string[] = [];
  for (const node of el.querySelectorAll('*')) {
    if (!(node instanceof HTMLElement)) continue;
    if (node.getClientRects().length === 0) continue;
    const ownText = [...node.childNodes].some(
      (child) => child.nodeType === Node.TEXT_NODE && (child.textContent ?? '').trim().length > 0,
    );
    if (!ownText) continue;
    const size = parseFloat(getComputedStyle(node).fontSize);
    if (size < min) min = size;
    if (size < 11) {
      offenders.push(`${node.tagName.toLowerCase()}.${node.classList[0] ?? ''} ${size}px`);
    }
  }
  return { min: min === Infinity ? 0 : min, offenders };
}

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

// Tests only delete resources they created: every test resource carries an
// `e2e-` id, and this sweep removes exactly those (jobs first, then the
// workflows themselves) so state never accumulates across the suite.
test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('run history panel renders styled and rows open the detail pane', async ({ page, request }) => {
  const id = `e2e-history-panel-${Date.now()}`;
  await seedRun(request, id);

  const panel = await openHistory(page, id);

  const styles = await panel.evaluate((el) => {
    const row = el.querySelector('.history-row');
    const rowStyle = row ? getComputedStyle(row) : null;
    const panelStyle = getComputedStyle(el);
    return {
      panelBg: panelStyle.backgroundColor,
      panelRadius: panelStyle.borderRadius,
      panelWidth: el.getBoundingClientRect().width,
      rowDisplay: rowStyle?.display ?? 'none',
      rowBorderTop: rowStyle?.borderTopStyle ?? '',
      rowWidth: row ? row.getBoundingClientRect().width : 0,
    };
  });
  expect(styles.panelBg).not.toBe('rgba(0, 0, 0, 0)');
  expect(styles.panelWidth).toBeGreaterThan(600);
  expect(styles.rowDisplay).toBe('flex');
  expect(styles.rowBorderTop).not.toBe('outset');
  expect(styles.rowWidth).toBeGreaterThan(200);

  // The header actions must share one row: same height and the same vertical
  // centre, regardless of the title size.
  const geometry = await panel.evaluate((el) => {
    const head = el.querySelector('.history-head-actions');
    const buttons = head ? [...head.querySelectorAll('button')] : [];
    return buttons.map((button) => {
      const box = button.getBoundingClientRect();
      return { height: box.height, center: box.top + box.height / 2 };
    });
  });
  expect(geometry).toHaveLength(2);
  expect(Math.abs(geometry[0].height - geometry[1].height)).toBeLessThanOrEqual(1);
  expect(Math.abs(geometry[0].center - geometry[1].center)).toBeLessThanOrEqual(1);

  // Run rows are present and selecting one opens the detail pane and its
  // event rows.
  const rows = panel.locator('.history-row');
  await expect(rows.first()).toBeVisible();
  expect(await rows.count()).toBeGreaterThan(0);
  await rows.first().click();
  await expect(panel.locator('.history-summary')).toBeVisible();
  await expect(panel.locator('.event-row').first()).toBeVisible();

  // No visible text in the dialog computes below 11px.
  const textSize = await panel.evaluate(minTextSize);
  expect(textSize.offenders).toEqual([]);
  expect(textSize.min).toBeGreaterThanOrEqual(11);
});

test('run history dialog type scale holds at mobile width', async ({ page, request }) => {
  const id = `e2e-history-panel-${Date.now()}`;
  await seedRun(request, id);
  await page.setViewportSize({ width: 390, height: 844 });
  const panel = await openHistory(page, id);
  await page.locator('.history-row').first().click();
  await expect(panel.locator('.history-summary')).toBeVisible();
  const textSize = await panel.evaluate(minTextSize);
  expect(textSize.offenders).toEqual([]);
  expect(textSize.min).toBeGreaterThanOrEqual(11);
});

test('run history dialog screenshots and measured sizes', async ({ page, request }) => {
  const id = `e2e-history-panel-${Date.now()}`;
  await seedRun(request, id);
  const variants = [
    { name: '1440x900-light', width: 1440, height: 900, theme: 'light' },
    { name: '1440x900-dark', width: 1440, height: 900, theme: 'dark' },
    { name: '390x844-light', width: 390, height: 844, theme: 'light' },
    { name: '390x844-dark', width: 390, height: 844, theme: 'dark' },
  ];
  for (const variant of variants) {
    await page.setViewportSize({ width: variant.width, height: variant.height });
    await page.goto(`/workflows/${id}/edit`);
    await page.evaluate((theme) => window.localStorage.setItem('loom.theme', theme), variant.theme);
    await page.reload();
    await page.locator('.workspace').waitFor({ state: 'visible' });
    await page.locator('.runbar-menu summary').click();
    await page.getByRole('button', { name: 'Run history' }).click();
    const panel = page.locator('.history-dialog');
    await panel.waitFor({ state: 'visible' });
    await page.locator('.history-row').first().click();
    await expect(panel.locator('.event-row').first()).toBeVisible();

    const sizes = await panel.evaluate((el) => {
      const pick = (selector: string) => {
        const node = el.querySelector(selector);
        return node ? getComputedStyle(node).fontSize : null;
      };
      return {
        title: pick('.history-head h2'),
        runStatus: pick('.history-row-main .status-text'),
        runMeta: pick('.history-row-meta'),
        summaryLabel: pick('.history-summary .field-label'),
        summaryValue: pick('.history-summary strong'),
        eventTitle: pick('.event-row strong'),
        eventTime: pick('.event-time'),
        eventSeq: pick('.event-seq'),
      };
    });
    console.info(`[history-panel] ${variant.name} ${JSON.stringify(sizes)}`);
    await page.screenshot({
      path: `test-results/history-panel/${variant.name}.png`,
      fullPage: true,
    });
  }
});

test('run history dialog geometry: rows, summary, events, filters', async ({ page, request }) => {
  const id = `e2e-history-geo-${Date.now()}`;
  await seedRun(request, id);
  const panel = await openHistory(page, id);
  await page.locator('.history-row').first().click();
  await expect(panel.locator('.history-summary')).toBeVisible();
  await expect(panel.locator('.event-row').first()).toBeVisible();

  // --- Left rows: truncation, no collision with the meta column. ---
  const rows = await panel.evaluate((el) => {
    const row = el.querySelector('.history-row') as HTMLElement;
    const main = row.querySelector('.history-row-main') as HTMLElement;
    const status = main.querySelector('.status-text') as HTMLElement;
    const trigger = main.querySelector('.meta-list') as HTMLElement;
    const meta = row.querySelector('.history-row-meta') as HTMLElement;
    const date = meta.querySelector('em') as HTMLElement;
    const filters = el.querySelector('.history-filters') as HTMLElement;
    const dateStyle = getComputedStyle(date);
    const lineHeight =
      parseFloat(dateStyle.lineHeight) || parseFloat(dateStyle.fontSize) * 1.4;
    return {
      rowOverflow: row.scrollWidth - row.clientWidth,
      statusCollision: status.getBoundingClientRect().right - meta.getBoundingClientRect().left,
      triggerCollision: trigger.getBoundingClientRect().right - meta.getBoundingClientRect().left,
      dateHeight: date.getBoundingClientRect().height,
      lineHeight,
      listWidth: (el.querySelector('.history-list') as HTMLElement).getBoundingClientRect().width,
      filtersOverflow: filters.scrollWidth - filters.clientWidth,
    };
  });
  expect(rows.rowOverflow).toBeLessThanOrEqual(1);
  expect(rows.statusCollision).toBeLessThanOrEqual(0);
  expect(rows.triggerCollision).toBeLessThanOrEqual(0);
  expect(rows.dateHeight).toBeLessThanOrEqual(rows.lineHeight * 1.5);
  expect(rows.listWidth).toBe(288);
  expect(rows.filtersOverflow).toBeLessThanOrEqual(1);

  // The filter header stays put while the run rows scroll under it.
  const list = panel.locator('.history-scroll');
  await list.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  const sticky = await panel.evaluate((el) => {
    const filters = el.querySelector('.history-filters') as HTMLElement;
    const list = el.querySelector('.history-list') as HTMLElement;
    return {
      filterTop: filters.getBoundingClientRect().top,
      listTop: list.getBoundingClientRect().top,
    };
  });
  expect(sticky.filterTop).toBeGreaterThanOrEqual(sticky.listTop - 1);

  // --- Summary: three equal columns, label over value, run id spans. ---
  const summary = await panel.evaluate((el) => {
    const grid = el.querySelector('.history-summary') as HTMLElement;
    const cells = [...grid.querySelectorAll(':scope > div')] as HTMLElement[];
    const first = cells.slice(0, 3).map((cell) => {
      const label = cell.querySelector('.field-label') as HTMLElement;
      const value = cell.querySelector('strong') as HTMLElement;
      return {
        width: cell.getBoundingClientRect().width,
        left: cell.getBoundingClientRect().left,
        right: cell.getBoundingClientRect().right,
        labelLeft: label.getBoundingClientRect().left,
        valueLeft: value.getBoundingClientRect().left,
      };
    });
    const runCell = grid.querySelector('.history-summary-run') as HTMLElement;
    return {
      gridWidth: grid.getBoundingClientRect().width,
      first,
      runWidth: runCell.getBoundingClientRect().width,
      runLeft: runCell.getBoundingClientRect().left,
    };
  });
  const [a, b, c] = summary.first;
  expect(Math.abs(a.width - b.width)).toBeLessThanOrEqual(1);
  expect(Math.abs(b.width - c.width)).toBeLessThanOrEqual(1);
  expect(a.right).toBeLessThanOrEqual(b.left + 1);
  expect(b.right).toBeLessThanOrEqual(c.left + 1);
  for (const cell of summary.first) {
    expect(Math.abs(cell.labelLeft - cell.valueLeft)).toBeLessThanOrEqual(1);
  }
  expect(summary.runWidth).toBeGreaterThanOrEqual(summary.gridWidth - 2);
  expect(summary.runLeft).toBeLessThanOrEqual(summary.first[0].left + 1);

  // --- Event rows: 4-track order, no overlap, Inspect inside the row,
  //     identical track positions with and without an Inspect control
  //     (times right-anchor to the fixed Inspect track). ---
  const events = await panel.evaluate((el) => {
    const rows = [...el.querySelectorAll('.event-row')] as HTMLElement[];
    const read = (row: HTMLElement) => {
      const seq = row.querySelector('.event-seq') as HTMLElement;
      const content = row.querySelector(':scope > div') as HTMLElement;
      const time = row.querySelector('.event-time') as HTMLElement;
      const inspect = row.querySelector('.event-inspect') as HTMLElement | null;
      return {
        seq: seq.getBoundingClientRect().left,
        content: content.getBoundingClientRect().left,
        time: time.getBoundingClientRect().left,
        timeRight: time.getBoundingClientRect().right,
        inspect: inspect ? inspect.getBoundingClientRect().left : null,
        inspectRight: inspect ? inspect.getBoundingClientRect().right : null,
        rowRight: row.getBoundingClientRect().right,
        childrenOverlap: (() => {
          const rects = [seq, content, time].map((n) => n.getBoundingClientRect());
          return rects.some((r, i) => rects.slice(i + 1).some((o) => r.right > o.left + 1 && r.left < o.right - 1 && r.bottom > o.top + 1 && r.top < o.bottom - 1));
        })(),
      };
    };
    const withInspect = rows.filter((row) => row.querySelector('.event-inspect'));
    const without = rows.filter((row) => !row.querySelector('.event-inspect'));
    return {
      withInspect: withInspect.slice(0, 3).map(read),
      without: without.slice(0, 3).map(read),
    };
  });
  for (const row of events.withInspect) {
    expect(row.seq).toBeLessThan(row.content);
    expect(row.content).toBeLessThan(row.time);
    expect(row.time).toBeLessThan(row.inspect!);
    expect(row.childrenOverlap).toBe(false);
    expect(row.inspectRight!).toBeLessThanOrEqual(row.rowRight + 1);
  }
  // Rows without an Inspect control keep the same seq/content tracks
  // and the same right-anchored time edge.
  if (events.without.length > 0 && events.withInspect.length > 0) {
    for (const row of events.without) {
      expect(row.seq).toBeCloseTo(events.withInspect[0].seq, 0);
      expect(row.content).toBeCloseTo(events.withInspect[0].content, 0);
      expect(row.timeRight).toBeCloseTo(events.withInspect[0].timeRight, 0);
    }
  }
});

test('run history dialog row geometry holds at 390px', async ({ page, request }) => {
  const id = `e2e-history-mobile-${Date.now()}`;
  await seedRun(request, id);
  await page.setViewportSize({ width: 390, height: 844 });
  const panel = await openHistory(page, id);
  await page.locator('.history-row').first().click();
  await expect(panel.locator('.history-summary')).toBeVisible();

  const rows = await panel.evaluate((el) => {
    const row = el.querySelector('.history-row') as HTMLElement;
    const main = row.querySelector('.history-row-main') as HTMLElement;
    const status = main.querySelector('.status-text') as HTMLElement;
    const trigger = main.querySelector('.meta-list') as HTMLElement;
    const meta = row.querySelector('.history-row-meta') as HTMLElement;
    const date = meta.querySelector('em') as HTMLElement;
    const dateStyle = getComputedStyle(date);
    const lineHeight =
      parseFloat(dateStyle.lineHeight) || parseFloat(dateStyle.fontSize) * 1.4;
    return {
      rowOverflow: row.scrollWidth - row.clientWidth,
      statusCollision: status.getBoundingClientRect().right - meta.getBoundingClientRect().left,
      triggerCollision: trigger.getBoundingClientRect().right - meta.getBoundingClientRect().left,
      dateHeight: date.getBoundingClientRect().height,
      lineHeight,
    };
  });
  expect(rows.rowOverflow).toBeLessThanOrEqual(1);
  expect(rows.statusCollision).toBeLessThanOrEqual(0);
  expect(rows.triggerCollision).toBeLessThanOrEqual(0);
  expect(rows.dateHeight).toBeLessThanOrEqual(rows.lineHeight * 1.5);
});
