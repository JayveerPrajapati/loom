import AxeBuilder from '@axe-core/playwright';
import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { MODERATE_BASELINE } from './a11y-baseline';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

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

function workflowFor(id: string) {
  return {
    id,
    name: `A11y workflow ${id}`,
    version: 1,
    nodes: [
      {
        id: 'start',
        type: 'trigger.manual',
        typeVersion: 1,
        name: 'Start',
        position: { x: 0, y: 0 },
        config: {},
      },
      {
        id: 'shape',
        type: 'data.set',
        typeVersion: 1,
        name: 'Shape',
        position: { x: 260, y: 0 },
        config: { values: { message: 'hello' }, keepOnlySet: true },
      },
    ],
    edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  };
}

async function seedRun(request: APIRequestContext, id: string): Promise<string> {
  expect((await request.post('/api/workflows', { data: workflowFor(id) })).status()).toBe(201);
  expect((await request.post(`/api/workflows/${id}/publish`)).status()).toBe(201);
  const started = await request.post(`/api/workflows/${id}/runs?async=1`, { data: { input: {} } });
  expect([201, 202]).toContain(started.status());
  const { runId } = await started.json();
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const response = await request.get(`/api/runs/${runId}`);
    if (response.ok()) {
      const run = await response.json();
      if (['done', 'failed', 'cancelled', 'waiting'].includes(run.status)) return runId;
    }
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  return runId;
}

interface AccessibilityViolation {
  id: string;
  impact: 'moderate' | 'serious' | 'critical';
  findings: string[];
}

async function accessibilityViolations(page: Page): Promise<AccessibilityViolation[]> {
  const report = await new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa']).analyze();
  return report.violations
    .filter((violation) =>
      ['moderate', 'serious', 'critical'].includes(violation.impact ?? ''),
    )
    .map((violation) => ({
      id: violation.id,
      impact: violation.impact as AccessibilityViolation['impact'],
      findings: violation.nodes.map(
        (node) => `${violation.id} ${JSON.stringify(node.target)} ${node.html}`,
      ),
    }));
}

/**
 * Serious and critical findings always fail. Moderate findings fail unless the
 * exact surface and rule count are recorded in MODERATE_BASELINE; a new
 * instance of an already-baselined rule still fails once it exceeds the count.
 */
function newViolations(surface: string, violations: AccessibilityViolation[]): string[] {
  const failures: string[] = [];
  const seen: Record<string, number> = {};
  for (const violation of violations) {
    if (violation.impact !== 'moderate') {
      failures.push(...violation.findings);
      continue;
    }
    seen[violation.id] = (seen[violation.id] ?? 0) + violation.findings.length;
    const allowed = MODERATE_BASELINE[surface]?.[violation.id] ?? 0;
    if (seen[violation.id] > allowed) {
      failures.push(...violation.findings.slice(0, seen[violation.id] - allowed));
    }
  }
  return failures;
}

for (const theme of ['light', 'dark'] as const) {
  test(`${theme}: every route has no serious or unbaselined moderate accessibility violations`, async ({ page, request }) => {
    const id = `e2e-a11y-${theme}-${Date.now()}`;
    const runId = await seedRun(request, id);
    const job = await (
      await request.post('/api/jobs', {
        data: {
          name: `e2e-a11y-job-${theme}-${Date.now()}`,
          templateId: id,
          templateVersion: 1,
        },
      })
    ).json();
    const profileResponse = await request.post('/api/config-profiles', {
      data: {
        name: `e2e-a11y-profile-${theme}-${Date.now()}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://example.test/a11y' },
      },
    });
    expect(profileResponse.status(), await profileResponse.text()).toBe(201);
    const profile = (await profileResponse.json()) as { id: string };
    const failures: string[] = [];
    await page.addInitScript((value) => window.localStorage.setItem('loom.theme', value), theme);

    const routes: Array<[string, string]> = [
      ['home', '/'],
      ['workflows', '/workflows'],
      ['workflow detail', `/workflows/${id}`],
      ['template detail (legacy alias)', `/templates/${id}`],
      ['editor', `/workflows/${id}/edit`],
      ['jobs', '/jobs'],
      ['job new', '/jobs/new'],
      ['job detail', `/jobs/${encodeURIComponent(job.id)}`],
      ['runs', '/runs'],
      ['run detail', `/runs/${runId}`],
      ['tasks', '/tasks'],
      ['credentials', '/credentials'],
      // Configuration Hub surfaces (Profile-Centric UX Phase 0 item 2:
      // the axe audit now covers the hub routes too).
      ['profiles landing', '/profiles'],
      ['hub node type', '/hub/http.request'],
      ['profile form', '/hub/http.request/new'],
      ['profile view', `/hub/http.request/${encodeURIComponent(profile.id)}`],
      ['schemas', '/schemas'],
      ['mappings', '/mappings'],
      ['scripts', '/scripts'],
      ['connectors', '/connectors'],
      ['audit', '/audit'],
      ['capabilities', '/capabilities'],
      ['ops', '/ops'],
    ];

    try {
      for (const [name, url] of routes) {
        await page.goto(url);
        await expect(page.locator('#main-stage'), `${theme} ${name} did not render`).toBeVisible();
        failures.push(
          ...newViolations(`${theme} ${name}`, await accessibilityViolations(page)).map(
            (finding) => `${theme} ${name}: ${finding}`,
          ),
        );
      }
      expect(failures).toEqual([]);
    } finally {
      await request.delete(`/api/config-profiles/${encodeURIComponent(profile.id)}`);
      await request.delete(`/api/jobs/${encodeURIComponent(job.id)}`);
    }
  });
}

test('profile history dialog has no serious or unbaselined moderate accessibility violations', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-a11y-history-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://example.test/history' },
      },
    })
  ).json();
  try {
    await page.goto(`/hub/http.request/${encodeURIComponent(created.id)}`);
    await page.getByTestId('profile-view-history').click();
    await expect(page.getByTestId('profile-history-backdrop')).toBeVisible();
    expect(
      newViolations('profile history dialog', await accessibilityViolations(page)),
      'profile history dialog',
    ).toEqual([]);
  } finally {
    await request.delete(`/api/config-profiles/${encodeURIComponent(created.id)}`);
  }
});

test('destructive confirm dialog has no serious or unbaselined moderate accessibility violations', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const created = await (
    await request.post('/api/config-profiles', {
      data: {
        name: `e2e-a11y-confirm-${stamp}`,
        nodeType: 'http.request',
        config: { method: 'GET', url: 'https://example.test/confirm' },
      },
    })
  ).json();
  try {
    await page.goto('/hub/http.request');
    const row = page
      .getByTestId('profile-list')
      .locator('.dashboard-run-row')
      .filter({ hasText: created.name })
      .first();
    await expect(row).toBeVisible();
    // The profile list has its own row markup; its kebab is `.action-menu`.
    await row.locator('.action-menu summary').click();
    await row.getByRole('button', { name: 'Delete', exact: true }).click();
    const backdrop = page.getByTestId('confirm-dialog-backdrop');
    await expect(backdrop).toBeVisible();
    expect(
      newViolations('confirm dialog', await accessibilityViolations(page)),
      'confirm dialog',
    ).toEqual([]);
    await backdrop.getByRole('button', { name: /cancel/i }).click();
    await expect(backdrop).toHaveCount(0);
  } finally {
    await request.delete(`/api/config-profiles/${encodeURIComponent(created.id)}`);
  }
});

test('account dialog has no serious or unbaselined moderate accessibility violations', async ({
  page,
}) => {
  await page.goto('/');
  await page.locator('details.settings-menu summary').click();
  await page.getByRole('button', { name: 'Account' }).click();
  await expect(page.getByRole('dialog', { name: 'Account and users' })).toBeVisible();
  expect(newViolations('account dialog', await accessibilityViolations(page)), 'account dialog').toEqual(
    [],
  );
});

test('account dialog traps focus and restores the trigger', async ({ page }) => {
  await page.goto('/');
  await page.locator('details.settings-menu summary').click();
  const trigger = page.getByRole('button', { name: 'Account' });
  await trigger.focus();
  await trigger.click();
  const dialog = page.getByRole('dialog', { name: 'Account and users' });
  await expect(dialog).toBeVisible();
  await expect(page.locator('details.settings-menu')).toHaveAttribute('open', '');

  const focusInside = () => dialog.evaluate((el) => el.contains(document.activeElement));
  expect(await focusInside()).toBe(true);
  for (let i = 0; i < 12; i += 1) {
    await page.keyboard.press('Tab');
    expect(await focusInside()).toBe(true);
  }

  await page.keyboard.press('Escape');
  await expect(dialog).toHaveCount(0);
  await expect(page.locator('details.settings-menu')).toHaveAttribute('open', '');
  await expect(trigger).toBeFocused();
});
