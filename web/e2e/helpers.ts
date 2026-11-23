import { expect, type APIRequestContext, type Locator, type Page } from '@playwright/test';

// Shared E2E cleanup (P2-8). Every spec creates its resources with
// `e2e-`-prefixed ids, so cleanup only ever removes data a test run
// created — never a workflow or job the run did not make. Failures are
// logged, not thrown: cleanup must not fail an otherwise-passing test.
export async function deleteTestWorkflows(request: APIRequestContext): Promise<void> {
  const jobsResponse = await request.get('/api/jobs');
  if (jobsResponse.ok()) {
    const jobs = (await jobsResponse.json()) as { jobs?: { id: string; templateId?: string }[] };
    for (const job of jobs.jobs ?? []) {
      if (!(job.templateId ?? '').startsWith('e2e-')) continue;
      const deleted = await request.delete(`/api/jobs/${encodeURIComponent(job.id)}`);
      if (deleted.status() !== 204) {
        console.warn(`cleanup: job ${job.id} -> ${deleted.status()}`);
      }
    }
  }
  const response = await request.get('/api/workflows');
  if (!response.ok()) return;
  const body = (await response.json()) as { workflows?: { id: string }[] };
  for (const workflow of body.workflows ?? []) {
    if (!workflow.id.startsWith('e2e-')) continue;
    const deleted = await request.delete(`/api/workflows/${encodeURIComponent(workflow.id)}`);
    if (deleted.status() !== 204) {
      console.warn(`cleanup: workflow ${workflow.id} -> ${deleted.status()}`);
    }
  }
}

/** Phase 4 D4-7: one place that knows the profile editor's area model.
 *  Legacy section names still resolve, so specs keep naming the section
 *  they care about rather than the navigation path. */
export async function openProfileTab(page: Page, tab: string): Promise<void> {
  if (tab === 'identity' || tab === 'basics') {
    await page.getByTestId('profile-tab-identity').click();
    return;
  }
  const section = tab === 'contracts' || tab === 'bundle' ? 'included' : tab;
  const area = ['defaults', 'data', 'included'].includes(section) ? 'configuration' : 'trust';
  await page.getByTestId(`profile-tab-${area}`).click();
  await page.getByTestId(`profile-section-${section}`).click();
}

/** One contract for every asset history dialog: the wide shell mounts,
 *  the close control sits inside the head at the top-right, and the
 *  body scrolls without horizontal clipping. Asserts computed
 *  geometry on the live dialog element. */
export async function expectAssetHistoryDialog(dialog: Locator): Promise<void> {
  await expect(dialog).toBeVisible();
  const facts = await dialog.evaluate((element) => {
    const head = element.querySelector('.dialog-head');
    const body = element.querySelector('.dialog-body');
    const close = head?.querySelector('button');
    if (!head || !body || !(close instanceof HTMLElement)) return null;
    const box = element.getBoundingClientRect();
    const closeBox = close.getBoundingClientRect();
    return {
      width: box.width,
      closeInHead: close.closest('.dialog-head') !== null,
      closeRightGap: box.right - closeBox.right,
      closeTopGap: closeBox.top - box.top,
      bodyOverflow: body.scrollWidth - body.clientWidth,
    };
  });
  expect(facts).not.toBeNull();
  expect(facts!.width).toBeGreaterThanOrEqual(720);
  expect(facts!.closeInHead).toBe(true);
  expect(facts!.closeRightGap).toBeLessThanOrEqual(24);
  expect(facts!.closeRightGap).toBeGreaterThanOrEqual(-1);
  expect(facts!.closeTopGap).toBeGreaterThanOrEqual(0);
  expect(facts!.closeTopGap).toBeLessThanOrEqual(80);
  expect(facts!.bodyOverflow).toBeLessThanOrEqual(1);
}

/** Opens a row's secondary-action kebab so menu items become visible
 *  and clickable. All destructive and history actions live there. */
export async function openAssetMenu(row: Locator): Promise<void> {
  await row.getByTestId('asset-actions').locator('summary').click();
}
