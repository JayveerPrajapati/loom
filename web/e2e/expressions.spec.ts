import { expect, test } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Expression tester (V4, D14): the Inspector's tester runs through the
// production renderer on the server, and the run-input dialog remembers
// the last sample used per workflow.

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

test('inspector: expression tester renders samples through the production renderer', async ({ page, request }) => {
  await request.post('/api/workflows', {
    data: {
      id: 'e2e-expr-tester',
      name: 'Expr tester',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'echo',
          type: 'data.set',
          typeVersion: 1,
          name: 'Echo',
          config: { values: { greeting: 'hi {{ $json.name }}' } },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'echo', port: 'main' } }],
    },
  });

  await page.goto('/workflows/e2e-expr-tester/edit');
  await page.locator('.loom-node', { hasText: 'Echo' }).click();
  const tester = page.getByTestId('expression-tester');
  await tester.locator('summary').click();
  await tester.locator('#tester-expression').fill('Hello {{ $json.name }} ({{ $json.count * 2 }})');
  await tester.locator('#tester-sample').fill('{"name": "Ada", "count": 21}');
  await tester.getByRole('button', { name: 'Test' }).click();
  const result = page.getByTestId('expression-test-result');
  await expect(result).toBeVisible({ timeout: 10000 });
  await expect(result).toContainText('Hello Ada (42)');

  // Failing expressions name the error, matching what nodes render as empty.
  await tester.locator('#tester-expression').fill('{{ nope.thing }}');
  await tester.getByRole('button', { name: 'Test' }).click();
  await expect(result).toContainText('nope.thing');
});

test('run input dialog remembers the last sample per workflow', async ({ page, request }) => {
  await request.post('/api/workflows', {
    data: {
      id: 'e2e-run-sample',
      name: 'Run sample',
      version: 1,
      nodes: [
        { id: 'start', type: 'trigger.manual', typeVersion: 1, name: 'Start', config: {} },
        {
          id: 'echo',
          type: 'data.set',
          typeVersion: 1,
          name: 'Echo',
          config: { values: { got: '{{ $json.seed }}' } },
        },
      ],
      edges: [{ from: { node: 'start', port: 'main' }, to: { node: 'echo', port: 'main' } }],
    },
  });

  await page.goto('/workflows/e2e-run-sample/edit');
  await page.locator('.runbar-menu > summary').click();
  await page.getByRole('button', { name: /Run with input/ }).click();
  await page.locator('.dialog textarea').fill('{"seed": 11}');
  await page.locator('.dialog button[type="submit"]').click();
  await expect(page.locator('.runbar-state strong')).toHaveText('Complete', { timeout: 15000 });

  // Reopening the dialog pre-fills the remembered sample.
  await page.goto('/workflows/e2e-run-sample/edit');
  await page.locator('.runbar-menu > summary').click();
  await page.getByRole('button', { name: /Run with input/ }).click();
  await expect(page.locator('.dialog textarea')).toHaveValue(/"seed": 11/);
});
