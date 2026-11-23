import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Storage-operations campaign: the /ops page and its endpoint. The e2e
// server runs with the admin bootstrap account, so the page is
// reachable; a viewer session proves the role gate.

test.beforeEach(async ({ request, context }) => {
  await login(request);
  await login(context.request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('the ops page reports storage totals and reclaimable horizons', async ({ page }) => {
  await page.goto('/ops');
  await expect(page.getByTestId('ops-totals-card')).toBeVisible();
  await expect(page.getByTestId('ops-totals-card')).toContainText('Runs');
  const headers = page.getByRole('columnheader');
  await expect(headers.first()).toBeVisible();
  for (const header of await headers.all()) {
    await expect(header).toHaveAttribute('scope', 'col');
  }
  await expect(page.getByTestId('ops-growth-projection')).toBeVisible();
  await expect(page.getByTestId('ops-dedup-card')).toContainText('stored once');
  await expect(page.getByTestId('ops-reclaim-card')).toContainText('7 days');
  await expect(page.getByTestId('ops-reclaim-card')).toContainText('90 days');
});

test('the growth chart renders one deterministic bar per recorded day', async ({ page, request }) => {
  // Seed two runs so the ledger has events today.
  for (let i = 0; i < 2; i += 1) {
    const id = `ops-chart-${Date.now()}-${i}`;
    await request.post('/api/workflows', {
      data: {
        id,
        name: `Ops chart ${i}`,
        version: 1,
        nodes: [
          { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start' },
          { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
        ],
        edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
      },
    });
    const run = await request.post(`/api/workflows/${id}/runs`, { data: {} });
    expect(run.ok()).toBeTruthy();
  }

  await page.goto('/ops');
  const chart = page.getByTestId('ops-growth-chart');
  await expect(chart).toBeVisible();
  const days = await request.get('/api/ops/storage');
  const body = (await days.json()) as { stats: { eventsPerDay: { day: string }[] | null } };
  const recordedDays = (body.stats.eventsPerDay ?? []).filter((day) => day.day).length;
  // The chart carries exactly one bar per day the API reports.
  await expect(chart.locator('.ops-chart-bar')).toHaveCount(recordedDays);
  // The tallest bar carries the max value and its title names the day.
  const fills = chart.locator('.ops-chart-fill');
  const heights = await fills.evaluateAll((nodes) =>
    nodes.map((node) => (node as HTMLElement).getBoundingClientRect().height),
  );
  const trackHeight = Math.max(...heights);
  expect(trackHeight).toBeGreaterThan(0);
  expect(new Set(heights).size).toBeLessThanOrEqual(recordedDays);
  // Every bar is within the chart's track height.
  const chartHeight = await chart.evaluate((node) => node.getBoundingClientRect().height);
  for (const height of heights) expect(height).toBeLessThanOrEqual(chartHeight);
  // The CSV button offers the raw series.
  await expect(page.getByTestId('ops-growth-csv')).toBeVisible();

  // L7: with everything recorded today (fewer than 3 days of history)
  // the sparse summary names the actual history and the projection
  // line declines to forecast instead of presenting 0 as the future.
  const storage = (await (
    await request.get('/api/ops/storage')
  ).json()) as { stats: { eventsPerDay: { day: string; events: number }[] | null } };
  const recordedEventDays = (storage.stats.eventsPerDay ?? []).filter((day) => day.day);
  const totalEvents = recordedEventDays.reduce((sum, day) => sum + day.events, 0);
  expect(recordedEventDays.length).toBeLessThan(3);
  const sparse = page.getByTestId('ops-growth-sparse');
  await expect(sparse).toBeVisible();
  await expect(sparse).toContainText(`${totalEvents.toLocaleString()} event`);
  await expect(sparse).toContainText(`${recordedEventDays.length} day`);
  await expect(sparse).toContainText('at least 3 days of history');
  const projection = page.getByTestId('ops-growth-projection');
  await expect(projection).toContainText('Projection unavailable');
  await expect(projection).toContainText('at least 3 days of history is needed');
});

test('the storage ops endpoint serves coherent numbers', async ({ request }) => {
  const response = await request.get('/api/ops/storage');
  expect(response.ok()).toBeTruthy();
  const body = (await response.json()) as {
    stats: {
      runs: number;
      events: number;
      reclaimable: { days: number; runs: number }[];
    };
    hint: string;
  };
  expect(body.hint.length).toBeGreaterThan(0);
  expect(body.stats.reclaimable.map((h) => h.days)).toEqual([7, 30, 90]);
  // Reclaimed runs can never exceed the stored total at every horizon.
  for (const horizon of body.stats.reclaimable) {
    expect(horizon.runs).toBeLessThanOrEqual(body.stats.runs);
  }
});

test('a viewer is forbidden from the ops endpoint', async ({ request }) => {
  const create = await request.post('/api/users', {
    data: { email: 'ops-viewer-e2e@example.com', name: 'Ops Viewer', role: 'viewer', password: 'ops-viewer-pass-1' },
  });
  expect([200, 201]).toContain(create.status());
  // Sign in as the viewer; the login response replaces this context's
  // admin session, so the follow-up request carries only viewer rights.
  const loginResponse = await request.post('/api/auth/login', {
    data: { email: 'ops-viewer-e2e@example.com', password: 'ops-viewer-pass-1' },
  });
  expect(loginResponse.ok()).toBeTruthy();
  const forbidden = await request.get('/api/ops/storage');
  expect(forbidden.status()).toBe(403);
});

// Phase 1A (campaign 1.1): the page owns its scroll. The shell is the
// only scroll container (body clips), so geometry is asserted on the
// live DOM, not inferred from a screenshot.
test('the ops page scrolls in its shell and reaches the largest-runs panel', async ({ page, request }) => {
  // Seed a run so the largest-runs panel renders (any run qualifies;
  // the list orders by bytes then recency).
  const id = `e2e-ops-scroll-${Date.now()}`;
  const create = await request.post('/api/workflows', {
    data: {
      id,
      name: 'Ops scroll probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start' },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  expect(create.ok()).toBeTruthy();
  const run = await request.post(`/api/workflows/${id}/runs`, { data: {} });
  expect(run.ok()).toBeTruthy();

  await page.goto('/ops');
  const shell = page.locator('main#main-stage.page-shell');
  await expect(shell).toBeVisible();
  // The skip link targets the scrolling stage itself.
  await expect(page.locator('a.skip-link')).toHaveAttribute('href', '#main-stage');

  const geometry = await shell.evaluate((el) => ({
    scrollHeight: el.scrollHeight,
    clientHeight: el.clientHeight,
  }));
  expect(geometry.scrollHeight).toBeGreaterThan(geometry.clientHeight);

  await shell.evaluate((el) => {
    el.scrollTop = el.scrollHeight;
  });
  const last = page.getByTestId('ops-runs-card');
  await expect(last).toBeVisible();
  const revealed = await last.evaluate((el) => {
    const rect = el.getBoundingClientRect();
    return rect.bottom > 0 && rect.bottom <= window.innerHeight + 1;
  });
  expect(revealed).toBe(true);
});

// Phase 1A (campaign 1.1): the error banner is page-level, not panel
// content — it must span both grid tracks, not squeeze into the first.
test('the ops error banner spans the full grid width', async ({ page }) => {
  await page.route('**/api/ops/storage*', (route) =>
    route.fulfill({
      status: 500,
      contentType: 'application/json',
      body: JSON.stringify({ error: 'ops geometry probe' }),
    }),
  );
  await page.goto('/ops');
  const banner = page.getByTestId('ops-error');
  await expect(banner).toBeVisible();
  await expect(banner).toContainText('ops geometry probe');
  const widths = await page.evaluate(() => {
    const grid = document.querySelector('.dashboard-grid');
    const banner = document.querySelector('[data-testid="ops-error"]');
    if (!grid || !banner) return null;
    return {
      grid: grid.getBoundingClientRect().width,
      banner: banner.getBoundingClientRect().width,
    };
  });
  expect(widths).not.toBeNull();
  expect(widths!.banner).toBeGreaterThanOrEqual(widths!.grid * 0.9);
});

test('the ops ledger renders full-width rows with chips, ids, bars and recency', async ({
  page,
  request,
}) => {
  // Seed a run so the largest-runs ledger has a row.
  const id = `e2e-ops-ledger-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id,
      name: 'Ops ledger probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start' },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  await request.post(`/api/workflows/${id}/runs`, { data: {} });

  // The ledger is the top-N by footprint, so the seeded run is not
  // guaranteed to be first once the whole suite has run. Read the API's
  // first row and assert the UI renders that same row.
  const statsResponse = await request.get('/api/ops/storage');
  expect(statsResponse.ok()).toBeTruthy();
  const stats = (await statsResponse.json()) as {
    stats: {
      largestRuns: Array<{
        runId: string;
        workflowId: string;
        workflowName: string;
      }> | null;
    };
  };
  const expectedRun = stats.stats.largestRuns?.[0];
  expect(expectedRun).toBeDefined();

  await page.goto('/ops');
  const grid = page.locator('.dashboard-grid');
  await expect(grid).toBeVisible();
  const gridWidth = await grid.evaluate((element) => element.getBoundingClientRect().width);

  // Both ledger panels span the grid width (full width, not a track).
  for (const testId of ['ops-workflows-card', 'ops-runs-card']) {
    const panel = page.getByTestId(testId);
    await expect(panel).toBeVisible();
    const width = await panel.evaluate((element) => element.getBoundingClientRect().width);
    expect(width).toBeGreaterThanOrEqual(gridWidth * 0.9);
  }

  // The seeded run has a ledger row with a chip, a short id titled with
  // the full id, labeled metrics, a proportional bar and a recency
  // line — and no horizontal overflow.
  const row = page.getByTestId('ops-runs-ledger').locator('li').first();
  await expect(row).toBeVisible();
  await expect(row.locator('.ops-chip')).toBeVisible();
  await expect(row.locator('.ops-chip')).toContainText(/done|running|failed|waiting|queued/);
  const link = row.locator('.ops-run-link');
  const fullId = await link.getAttribute('title');
  expect(fullId).toBe(expectedRun?.runId);
  expect(await link.textContent()).toBe((fullId ?? '').slice(0, 8));
  // The ledger shows the workflow name, with the id still available on hover.
  await expect(row.locator('.ops-ledger-workflow')).toHaveText(
    expectedRun?.workflowName || expectedRun?.workflowId || '',
  );
  await expect(row.locator('.ops-ledger-workflow')).toHaveAttribute(
    'title',
    new RegExp(expectedRun?.workflowId ?? ''),
  );
  await expect(row.locator('.ops-metric-label')).toHaveCount(2);
  await expect(row.locator('.ops-bar')).toBeVisible();
  await expect(row.locator('.ops-ledger-recency')).toContainText(/ago|just now/);

  const rows = page.getByTestId('ops-runs-ledger').locator('li');
  const overflow = await rows.evaluateAll((nodes) =>
    nodes.some((node) => node.scrollWidth - node.clientWidth > 1),
  );
  expect(overflow).toBe(false);

  // Bar widths are proportional to the set's max (largest = 100%).
  const allWidths = await page
    .getByTestId('ops-runs-ledger')
    .locator('.ops-bar-fill')
    .evaluateAll((nodes) => nodes.map((node) => (node as HTMLElement).style.width));
  const percents = allWidths.map((value) => Number.parseInt(value, 10));
  expect(percents.length).toBeGreaterThan(0);
  expect(Math.max(...percents)).toBe(100);
  for (const percent of percents) {
    expect(percent).toBeGreaterThanOrEqual(2);
    expect(percent).toBeLessThanOrEqual(100);
  }

  // The short id navigates to the run page.
  await link.click();
  await expect(page.locator('main')).toBeVisible();
  expect(page.url()).toContain('/runs/');
});

test('every run status gets a non-transparent chip, and the empty states render', async ({
  page,
}) => {
  await page.goto('/ops');
  await expect(page.getByTestId('ops-runs-card')).toBeVisible();
  await expect(page.getByTestId('ops-workflows-card')).toBeVisible();

  // The chip styles paint for every status the ledger can show,
  // including cancelled.
  for (const status of [
    'running',
    'done',
    'failed',
    'waiting',
    'queued',
    'skipped',
    'cancelled',
  ]) {
    const painted = await page.evaluate((name) => {
      const probe = document.createElement('span');
      probe.className = `ops-chip status-${name}`;
      document.body.appendChild(probe);
      const style = window.getComputedStyle(probe);
      const result = {
        background: style.backgroundColor,
        color: style.color,
      };
      probe.remove();
      return result;
    }, status);
    expect(painted.background).not.toBe('rgba(0, 0, 0, 0)');
    expect(painted.background).not.toBe('transparent');
    expect(painted.color).not.toBe('');
  }

  // With an empty storage response both ledgers show their empty states.
  await page.route('**/api/ops/storage', async (route) => {
    await route.fulfill({
      contentType: 'application/json',
      body: JSON.stringify({
        stats: {
          runs: 0,
          activeRuns: 0,
          events: 0,
          blobs: 0,
          blobBytes: 0,
          databaseBytes: 0,
          eventsPerDay: null,
          projectedEvents30d: 0,
          sharedBlobs: 0,
          sharedBlobBytes: 0,
          largestRuns: [],
          largestWorkflows: [],
          reclaimable: [],
        },
      }),
    });
  });
  await page.reload();
  await expect(page.getByTestId('ops-runs-empty')).toHaveText('No runs recorded yet.');
  await expect(page.getByTestId('ops-workflows-empty')).toHaveText('No blobs recorded yet.');
  await expect(page.getByTestId('ops-retention-copy')).toBeVisible();
});

// U6: the clipboard write's outcome is visible — a rejection shows
// "Copy failed" instead of silently leaving the button unchanged, and
// the existing "Copied" success state is kept.
test('the retention copy command shows Copy failed when the clipboard rejects it', async ({
  page,
}) => {
  await page.goto('/ops');
  const button = page.getByTestId('ops-retention-copy');
  // The live region stays mounted and silent while idle.
  const status = page.getByTestId('ops-retention-copy-status');
  await expect(button).toHaveText('Copy command');
  await expect(status).toHaveText('');

  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: () => Promise.reject(new Error('clipboard denied')) },
      configurable: true,
    });
  });
  await button.click();
  await expect(button).toHaveText('Copy failed');
  await expect(status).toHaveText('Copy failed');

  // A working clipboard still shows the unchanged success state, and the
  // live region announces it politely.
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', {
      value: { writeText: () => Promise.resolve() },
      configurable: true,
    });
  });
  await button.click();
  await expect(button).toHaveText('Copied');
  await expect(status).toHaveText('Command copied');
});

test('the ops ledger stacks at 390px without horizontal overflow', async ({ page, request }) => {
  const id = `e2e-ops-mobile-${Date.now()}`;
  await request.post('/api/workflows', {
    data: {
      id,
      name: 'Ops mobile probe',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start' },
        { id: 'n', type: 'code.run', typeVersion: 1, name: 'N', config: { language: 'javascript', source: 'return 1' } },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'n', port: 'main' } }],
    },
  });
  await request.post(`/api/workflows/${id}/runs`, { data: {} });

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/ops');
  const row = page.getByTestId('ops-runs-ledger').locator('li').first();
  await expect(row).toBeVisible();

  const facts = await row.evaluate((element) => {
    const styles = window.getComputedStyle(element);
    const box = element.getBoundingClientRect();
    const areas = styles.gridTemplateAreas ?? '';
    return {
      overflow: element.scrollWidth - element.clientWidth,
      inside: box.left >= 0 && box.right <= window.innerWidth,
      stacked: areas.includes('metrics metrics metrics'),
    };
  });
  expect(facts.overflow).toBeLessThanOrEqual(1);
  expect(facts.inside).toBe(true);
  expect(facts.stacked).toBe(true);
});
