import { expect, test } from '@playwright/test';
import { login } from './auth';
import { openAssetMenu } from './helpers';

// 6.6: JSON Schema (draft-07) subset round-trip — export a Loom data
// contract as a clearly-marked subset document, then import that exact
// document back and land on the same fields; a foreign draft-07 schema
// has its subset-foreign keywords named, never silently dropped.

test.beforeEach(async ({ context, request }) => {
  await login(context.request);
  await login(request);
});

test('json schema: export then import round-trips the contract', async ({ page, request }) => {
  // Seed the contract through the API — the recursive editor is covered
  // by schemas.spec; this spec is about the round-trip.
  const name = `Round trip ${Date.now()}`;
  const created = await request.post('/api/data-schemas', {
    data: {
      name,
      description: 'Webhook lead payload',
      owner: 'growth-team',
      tags: ['webhook', 'leads'],
      fields: [
        { name: 'email', type: 'string', required: true, description: 'Lead email' },
        {
          name: 'address',
          type: 'object',
          fields: [
            { name: 'city', type: 'string' },
            { name: 'zip', type: 'string', required: true },
          ],
        },
        {
          name: 'tags',
          type: 'array',
          items: { name: 'event', type: 'string' },
        },
      ],
    },
  });
  expect(created.ok()).toBeTruthy();
  const seeded = await created.json();

  await page.goto('/schemas');
  await expect(page.getByRole('heading', { name: 'Data contracts' })).toBeVisible();

  // Export: the download must be the marked draft-07 subset document.
  const row = page.locator('.dashboard-run-row').filter({ hasText: name });
  await expect(row).toHaveCount(1);
  const downloadPromise = page.waitForEvent('download');
  // Export lives in the row kebab.
  await openAssetMenu(row);
  await row.getByLabel(`Export ${name} as JSON Schema`).click();
  const download = await downloadPromise;
  expect(download.suggestedFilename()).toBe(`loom-${seeded.id}-v1.schema.json`);
  const stream = await download.createReadStream();
  const chunks: Buffer[] = [];
  for await (const chunk of stream!) chunks.push(chunk as Buffer);
  const doc = JSON.parse(Buffer.concat(chunks).toString('utf8'));
  expect(doc['$schema']).toBe('http://json-schema.org/draft-07/schema#');
  expect(doc['x-loom-subset']).toBe('draft-07-subset-v1');
  expect(doc['title']).toBe(name);
  expect(doc['x-loom-owner']).toBe('growth-team');
  expect(doc['x-loom-tags']).toEqual(['webhook', 'leads']);
  expect(doc['required']).toEqual(['email']);
  expect(doc['properties']['address']['type']).toBe('object');
  expect(doc['properties']['address']['required']).toEqual(['zip']);
  expect(doc['properties']['tags']['items']['type']).toBe('string');
  expect(doc['properties']['tags']['x-loom-item-name']).toBe('event');

  // Import that exact document back: review reports a clean subset.
  await page.getByRole('button', { name: 'Import JSON Schema' }).click();
  await page.locator('#schema-import-text').fill(JSON.stringify(doc, null, 2));
  const review = page.getByTestId('schema-import-review');
  await expect(review).toBeVisible();
  await expect(review).toContainText(name);
  // countFields counts recursively: email, address, city, zip, tags and
  // the array's item contract.
  await expect(review).toContainText('6 fields');
  await expect(review).toContainText('clean subset');
  await page.getByTestId('schema-import-confirm').click();

  // The create form carries the imported contract, tags and owner included.
  await expect(page.locator('#schema-name')).toHaveValue(name);
  await expect(page.locator('#schema-owner')).toHaveValue('growth-team');
  await expect(page.locator('#schema-tags')).toHaveValue('webhook, leads');
  // .schema-field rows include the nested object fields and the array
  // item contract — 6 across the whole editor.
  await expect(page.locator('[data-testid="schema-fields"] .schema-field')).toHaveCount(6);
  const importedName = `${name} imported`;
  await page.locator('#schema-name').fill(importedName);
  await page.getByRole('button', { name: 'Save contract' }).click();
  await expect(page.getByText(`Created ${importedName}.`)).toBeVisible();
  const imported = page.locator('.dashboard-run-row').filter({ hasText: importedName });
  await expect(imported.locator('.meta-list')).toContainText('6 fields');
  await expect(imported.locator('.meta-list')).toContainText('v1');

  // The saved copy has identical fields (round-trip identity, live).
  const list = await request.get('/api/data-schemas');
  const { schemas } = await list.json();
  const copy = schemas.find((s: { name: string }) => s.name === importedName);
  expect(copy.fields).toEqual(seeded.fields);
});

test('json schema: import names subset-foreign keywords instead of dropping them', async ({
  page,
}) => {
  await page.goto('/schemas');
  await expect(page.getByRole('heading', { name: 'Data contracts' })).toBeVisible();

  await page.getByRole('button', { name: 'Import JSON Schema' }).click();
  await page.locator('#schema-import-text').fill(
    JSON.stringify({
      $schema: 'http://json-schema.org/draft-07/schema#',
      title: 'Foreign payload',
      type: 'object',
      properties: {
        email: { type: 'string', format: 'email', minLength: 3 },
        count: { type: 'integer' },
      },
      required: ['email'],
    }),
  );
  const review = page.getByTestId('schema-import-review');
  await expect(review).toBeVisible();
  await expect(review).toContainText('subset-foreign item(s) dropped');
  await expect(review).toContainText('"format" at $.email');
  await expect(review).toContainText('"minLength" at $.email');
  await expect(review).toContainText('"integer" at $.count (imported as number)');

  await page.getByTestId('schema-import-confirm').click();
  await expect(page.locator('#schema-name')).toHaveValue('Foreign payload');
  // integer came through as the closest subset type, named above.
  await expect(page.getByLabel('Field 2 type')).toHaveValue('number');
  await expect(page.getByLabel('Field 1 required')).toBeChecked();
});
