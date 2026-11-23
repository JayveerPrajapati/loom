import { expect, test, type APIRequestContext } from '@playwright/test';
import { login } from './auth';

// Phase 2 row/editor grammar: every reusable asset presents the same
// identity -> description -> owner/version/usage -> one Edit primary
// action shape, and every asset editor keeps Save visible while the body
// scrolls, including at phone width.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

async function createdID(response: Awaited<ReturnType<APIRequestContext['post']>>): Promise<string> {
  const body = await response.json();
  expect([200, 201], JSON.stringify(body)).toContain(response.status());
  // Most create endpoints return the entity directly; connectors wrap it.
  return body.id ?? body.connector?.id;
}

async function deleteQuietly(
  request: APIRequestContext,
  path: string,
  id: string | undefined,
) {
  if (!id) return;
  await request.delete(`${path}/${id}`);
}

test('every asset row exposes identity, description, owner, version, usage and one Edit', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  let mappingId: string | undefined;
  let schemaId: string | undefined;
  let scriptId: string | undefined;
  let connectorId: string | undefined;
  const owner = 'grammar-owner';
  try {
    mappingId = await createdID(
      await request.post('/api/mappings', {
        data: {
          name: `e2e-grammar-map-${stamp}`,
          description: 'Mapping description',
          owner,
          rows: [{ target: 'email', source: '{{ $json.email }}' }],
        },
      }),
    );
    schemaId = await createdID(
      await request.post('/api/data-schemas', {
        data: {
          name: `e2e-grammar-schema-${stamp}`,
          description: 'Schema description',
          owner,
          fields: [{ name: 'email', type: 'string' }],
        },
      }),
    );
    scriptId = await createdID(
      await request.post('/api/scripts', {
        data: {
          name: `e2e-grammar-script-${stamp}`,
          description: 'Script description',
          owner,
          language: 'javascript',
          source: 'return 1;',
        },
      }),
    );
    connectorId = await createdID(
      await request.post('/api/connectors', {
        data: {
          name: `e2e-grammar-connector-${stamp}`,
          description: 'Connector description',
          owner,
          operations: [
            {
              op: 'list',
              label: 'List things',
              method: 'GET',
              url: '{{cred.baseUrl}}/things',
              successCodes: '2xx',
              fields: {},
            },
          ],
        },
      }),
    );

    await page.goto('/mappings');
    const mappingRow = page.locator('.dashboard-run-row').filter({ hasText: `e2e-grammar-map-${stamp}` });
    await expect(mappingRow).toContainText('Mapping description');
    await expect(mappingRow).toContainText('v1');
    await expect(mappingRow).toContainText('0 published pins');
    await expect(mappingRow).toContainText(owner);
    await expect(mappingRow.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();

    await page.goto('/schemas');
    const schemaRow = page.locator('.dashboard-run-row').filter({ hasText: `e2e-grammar-schema-${stamp}` });
    await expect(schemaRow).toContainText('Schema description');
    await expect(schemaRow).toContainText('v1');
    await expect(schemaRow).toContainText('0 published pins');
    await expect(schemaRow).toContainText(owner);
    await expect(schemaRow.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();

    await page.goto('/scripts');
    const scriptRow = page.locator('.dashboard-run-row').filter({ hasText: `e2e-grammar-script-${stamp}` });
    await expect(scriptRow).toContainText('Script description');
    await expect(scriptRow).toContainText('v1');
    await expect(scriptRow).toContainText('0 published pins');
    await expect(scriptRow).toContainText(owner);
    await expect(scriptRow.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();

    await page.goto('/connectors');
    const connectorRow = page.getByTestId('connector-row').filter({ hasText: `e2e-grammar-connector-${stamp}` });
    await expect(connectorRow).toContainText('Connector description');
    await expect(connectorRow).toContainText('v1');
    await expect(connectorRow).toContainText('0 published pins');
    await expect(connectorRow).toContainText(owner);
    await expect(connectorRow.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
  } finally {
    await deleteQuietly(request, '/api/mappings', mappingId);
    await deleteQuietly(request, '/api/data-schemas', schemaId);
    await deleteQuietly(request, '/api/scripts', scriptId);
    await deleteQuietly(request, '/api/connectors', connectorId);
  }
});

test('profiles landing offers one direct create action and the hub row keeps the grammar', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  let profileId: string | undefined;
  try {
    const created = await request.post('/api/config-profiles', {
      data: {
        name: `e2e-grammar-profile-${stamp}`,
        nodeType: 'http.request',
        description: 'Profile description',
        owner: 'grammar-owner',
        config: { method: 'GET', url: 'https://api.example.com/grammar' },
      },
    });
    expect(created.status(), await created.text()).toBe(201);
    profileId = (await created.json()).id;

    await page.goto('/profiles');
    const create = page.getByTestId('profiles-new');
    await expect(create).toBeVisible();
    await create.click();
    await expect(page).toHaveURL(/\/hub\/http\.request$/);

    const row = page.locator('.dashboard-run-row').filter({ hasText: `e2e-grammar-profile-${stamp}` });
    await expect(row).toContainText('Profile description');
    await expect(row).toContainText('v1');
    await expect(row).toContainText('grammar-owner');
    await expect(row.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
  } finally {
    await deleteQuietly(request, '/api/config-profiles', profileId);
  }
});

test('the editor Save stays in view while the body scrolls at phone width', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  let scriptId: string | undefined;
  try {
    const source = Array.from({ length: 60 }, (_, index) => `// line ${index + 1}`).join('\n') +
      '\nreturn { ok: true };';
    scriptId = await createdID(
      await request.post('/api/scripts', {
        data: {
          name: `e2e-grammar-long-${stamp}`,
          language: 'javascript',
          source,
        },
      }),
    );

    await page.setViewportSize({ width: 390, height: 844 });
    await page.goto('/scripts');
    const row = page.locator('.dashboard-run-row').filter({ hasText: `e2e-grammar-long-${stamp}` });
    await row.getByRole('button', { name: 'Edit', exact: true }).click();

    const save = page.getByTestId('asset-editor-save');
    await expect(save).toBeVisible();
    await page.getByTestId('asset-editor').scrollIntoViewIfNeeded();
    await expect(save).toBeInViewport();
    await page.locator('.asset-editor-body').evaluate((element) => {
      element.scrollTop = element.scrollHeight;
    });
    await expect(save).toBeInViewport();

    await page.getByTestId('asset-editor-cancel').click();
    // Cancel closes the editor — the list returns to full width until
    // the next create/edit opens it.
    await expect(page.getByTestId('asset-editor')).toHaveCount(0);
  } finally {
    await deleteQuietly(request, '/api/scripts', scriptId);
  }
});

// Phase 2A row grammar: exactly one Edit plus one contextual action
// stay visible per row; every secondary action — including a
// danger-styled Delete at the end — lives in the kebab, which closes on
// Escape and outside click and never leaves the viewport.
test('every asset row keeps two visible actions and a kebab for the rest', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const owner = 'grammar-owner';
  const created: { path: string; id: string }[] = [];
  const seed = async (path: string, data: Record<string, unknown>) => {
    const id = await createdID(await request.post(path, { data: { owner, ...data } }));
    created.push({ path, id });
    return id;
  };
  try {
    await seed('/api/mappings', {
      name: `e2e-grammar-map-${stamp}`,
      rows: [{ target: 'email', source: '{{ $json.email }}' }],
    });
    await seed('/api/data-schemas', {
      name: `e2e-grammar-schema-${stamp}`,
      fields: [{ name: 'email', type: 'string' }],
    });
    await seed('/api/scripts', {
      name: `e2e-grammar-script-${stamp}`,
      language: 'javascript',
      source: 'return 1;',
    });
    await seed('/api/connectors', {
      name: `e2e-grammar-connector-${stamp}`,
      operations: [
        {
          op: 'list',
          label: 'List things',
          method: 'GET',
          url: 'https://example.test/things',
          successCodes: '2xx',
          fields: {},
        },
      ],
    });

    const pages: { url: string; row: string; contextual: string }[] = [
      { url: '/schemas', row: `e2e-grammar-schema-${stamp}`, contextual: 'View' },
      { url: '/mappings', row: `e2e-grammar-map-${stamp}`, contextual: 'View' },
      { url: '/scripts', row: `e2e-grammar-script-${stamp}`, contextual: 'Test' },
      { url: '/connectors', row: `e2e-grammar-connector-${stamp}`, contextual: 'View' },
    ];
    for (const target of pages) {
      await page.goto(target.url);
      const row = page
        .locator('.dashboard-run-row')
        .filter({ hasText: target.row })
        .first();

      // Exactly one visible Edit and one contextual action.
      await expect(row.getByRole('button', { name: 'Edit', exact: true })).toBeVisible();
      await expect(
        row.getByRole('button', { name: target.contextual }),
      ).toBeVisible();
      const visibleActions = await row.locator('.asset-row-actions > .text-button').count();
      expect(visibleActions).toBe(2);

      // The kebab owns the secondary actions; Delete is last and red.
      // Centre the row in the viewport first so the menu has room below
      // it instead of opening past the fold.
      await row.evaluate((el) => el.scrollIntoView({ block: 'center' }));
      const kebab = row.getByTestId('asset-actions');
      await expect(kebab).toBeVisible();
      await kebab.locator('summary').click();
      const list = kebab.locator('.action-menu-list');
      await expect(list).toBeVisible();
      // The list may flip upward for space; it settles inside the
      // viewport either way.
      await expect
        .poll(async () =>
          list.evaluate((el) => {
            const box = el.getBoundingClientRect();
            return (
              box.top >= 0 &&
              box.bottom <= window.innerHeight &&
              box.left >= 0 &&
              box.right <= window.innerWidth
            );
          }),
        )
        .toBe(true);

      const facts = await kebab.evaluate((el) => {
        const listEl = el.querySelector('.action-menu-list') as HTMLElement;
        const buttons = [...listEl.querySelectorAll('button')] as HTMLElement[];
        const last = buttons[buttons.length - 1];
        const box = listEl.getBoundingClientRect();
        return {
          lastText: (last.textContent ?? '').trim(),
          lastDanger: last.classList.contains('danger'),
          inViewport:
            box.left >= 0 && box.right <= window.innerWidth && box.top >= 0 && box.bottom <= window.innerHeight,
          buttonCount: buttons.length,
        };
      });
      expect(facts.lastText).toBe('Delete');
      expect(facts.lastDanger).toBe(true);
      expect(facts.buttonCount).toBeGreaterThanOrEqual(2);
      expect(facts.inViewport).toBe(true);

      // Escape closes the menu and returns focus to the trigger.
      await page.keyboard.press('Escape');
      await expect(list).toBeHidden();
      await expect(kebab.locator('summary')).toBeFocused();

      // An outside click closes it too.
      await kebab.locator('summary').click();
      await expect(list).toBeVisible();
      await page.locator('h1').click();
      await expect(list).toBeHidden();
    }
  } finally {
    for (const { path, id } of created) {
      await request.delete(`${path}/${id}`);
    }
  }
});
