import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { login } from './auth';
import { deleteTestWorkflows } from './helpers';

// Profile-Centric UX Phase 0, item 1: freeze the rendered field visibility
// of every catalog node type. The snapshot records, per node type, which
// config field keys the inspector renders as common fields (always
// visible) and which live in the Advanced disclosure. Any change to the
// catalog or the visibility rules changes this output — Phase 3's
// progressive-disclosure work must update this snapshot consciously.
//
// The snapshot was recorded against the live catalog at commit e902c70
// (21 node types). A new catalog node type fails the set check below
// until a snapshot is recorded for it.

// The snapshot records fields that render a `field-<key>` input id.
// Since the Phase 3 JsonField a11y fix, object/array/any fields render an
// id too, so they are captured here (e.g. data.set.values,
// flow.switch.cases, trigger.manual.fields). The catalog's declaration
// order is preserved within each list (comparison is sorted).
const SNAPSHOT: Record<string, { common: string[]; advanced: string[] }> = {
  'code.python': {
    common: ['requirements', 'scriptRef', 'source'],
    advanced: ['maxMemoryMB', 'timeoutSeconds'],
  },
  'code.run': { common: ['language', 'scriptRef', 'source'], advanced: ['outputType', 'timeoutSeconds'] },
  'data.json.parse': { common: ['text'], advanced: [] },
  'data.map': { common: ['mappingRef', 'outputType', 'overrides'], advanced: [] },
  'data.set': { common: ['keepOnlySet', 'outputType', 'values'], advanced: [] },
  'db.postgres': { common: ['operation', 'query'], advanced: [] },
  'flow.approval': {
    common: ['message'],
    advanced: [
      'channel',
      'credential',
      'linkExpiresMinutes',
      'notifyUrl',
      'onTimeout',
      'reminderMinutes',
      'timeoutSeconds',
    ],
  },
  'flow.emit': { common: ['event', 'payload'], advanced: [] },
  'flow.if': { common: ['condition'], advanced: [] },
  'flow.loop': { common: ['concurrency', 'itemsPath', 'maxIterations', 'workflowId'], advanced: [] },
  'flow.merge': { common: ['mode'], advanced: [] },
  'flow.subflow': { common: ['wait', 'workflowId'], advanced: [] },
  'flow.switch': { common: ['cases'], advanced: [] },
  'flow.wait': { common: ['seconds'], advanced: [] },
  'http.request': {
    common: ['auth', 'bodyType', 'method', 'url'],
    advanced: [
      'failOnError',
      'inlineThresholdBytes',
      'pagination',
      'rateLimitPerMinute',
      'responseMode',
      'successCodes',
      'timeout',
    ],
  },
  'text.template': { common: ['template'], advanced: [] },
  'trigger.cron': { common: [], advanced: [] },
  'trigger.event': { common: ['event'], advanced: [] },
  'trigger.manual': { common: ['fields'], advanced: [] },
  'trigger.subflow': { common: [], advanced: [] },
  'trigger.webhook': {
    common: ['method', 'path'],
    advanced: [
      'hmacHeader',
      'hmacSecret',
      'hmacTimestamps',
      'secret',
      'secretHeader',
      'strictSchema',
      'timestampHeader',
    ],
  },
};

interface SeededNode {
  type: string;
  id: string;
  name: string;
  config: Record<string, unknown>;
  credentials?: Record<string, string>;
}

test.beforeEach(async ({ request, context }) => {
  await login(context.request);
  await login(request);
});

test.afterEach(async ({ request }) => {
  await deleteTestWorkflows(request);
});

async function seedWorkflow(request: APIRequestContext, id: string, nodes: SeededNode[]) {
  const response = await request.post('/api/workflows', {
    data: {
      id,
      name: `Fields freeze ${id}`,
      version: 1,
      nodes: nodes.map((node, index) => ({
        id: node.id,
        type: node.type,
        typeVersion: 1,
        name: node.name,
        // A tight grid keeps every node inside the default viewport: the
        // React Flow canvas does not scroll nodes into view for clicks.
        position: { x: (index % 4) * 200, y: Math.floor(index / 4) * 110 },
        config: node.config,
        ...(node.credentials ? { credentials: node.credentials } : {}),
      })),
      edges: [],
    },
  });
  expect(response.status(), await response.text()).toBe(201);
}

// Returns the rendered field keys of the selected node, split into
// Basics-tab fields and Advanced-tab fields (Phase 3 moved the advanced
// disclosure onto a dedicated tab; only the active panel renders).
async function collectFields(page: Page) {
  await page.getByTestId('inspector-tab-configuration').click();
  const common = await page.evaluate(() =>
    Array.from(document.querySelectorAll('[data-testid="inspector-panel-configuration"] [id^="field-"]')).map(
      (el) => el.id.slice('field-'.length),
    ),
  );
  await page.getByTestId('inspector-tab-advanced').click();
  const advanced = await page.evaluate(() =>
    Array.from(document.querySelectorAll('[data-testid="inspector-panel-advanced"] [id^="field-"]')).map(
      (el) => el.id.slice('field-'.length),
    ),
  );
  return { common, advanced };
}

async function selectNode(page: Page, workflowId: string, nodeId: string) {
  await page.goto(`/workflows/${encodeURIComponent(workflowId)}/edit`);
  // This sweep asserts field visibility, not hit-testing. The floating
  // Auto-arrange control sits over the canvas' bottom-left; a node placed
  // there would otherwise make Playwright retry the center click until the
  // test budget expires. Dispatch the node's own click so selection still
  // runs through React Flow's handler.
  await page.locator(`.react-flow__node[data-id="${nodeId}"]`).dispatchEvent('click');
  await expect(page.locator('aside.inspector')).toContainText(nodeId);
}

test('inspector field visibility is frozen for every catalog node type', async ({ page, request }) => {
  // The sweep reloads the editor for every catalog type; the 30s default is
  // too tight once the suite has accumulated workflows and the Python gate
  // banner, and a timeout mid-sweep is not a catalog regression.
  test.setTimeout(120_000);
  const stamp = Date.now();

  // The live catalog must match the recorded snapshot set exactly.
  // Connector-authored types (Campaign 4) are user data, not the
  // compiled catalog: the listing merges them per instance, so the
  // freeze pins built-ins only.
  const defsResponse = await request.get('/api/node-types');
  expect(defsResponse.status()).toBe(200);
  const { nodeTypes } = (await defsResponse.json()) as {
    nodeTypes: Array<{ type: string; category: string }>;
  };
  expect(
    nodeTypes
      .filter((def) => def.category !== 'connector')
      .map((def) => def.type)
      .sort(),
    'the catalog changed: record (or update) a visibility snapshot for every new node type',
  ).toEqual(Object.keys(SNAPSHOT).sort());

  // A target workflow for subflow/loop references (a self-reference would
  // be a cycle), and a mapping for data.map's required mappingRef.
  await seedWorkflow(request, `e2e-fields-target-${stamp}`, [
    { type: 'trigger.manual', id: 'target-start', name: 'Target start', config: {} },
  ]);
  const mappingResponse = await request.post('/api/mappings', {
    data: {
      name: `e2e-fields-map-${stamp}`,
      rows: [{ target: 'email', source: '{{ $json.email }}', required: true }],
    },
  });
  expect(mappingResponse.status(), await mappingResponse.text()).toBe(201);
  const mappingId = ((await mappingResponse.json()) as { id: string }).id;

  // db.postgres validates a real credential reference at draft save.
  const credentialId = `e2e-fields-pg-${stamp}`;
  const credentialResponse = await request.post('/api/credentials', {
    data: { id: credentialId, type: 'postgres', data: { url: 'postgres://user:pass@localhost:5432/e2e' } },
  });
  expect(credentialResponse.status(), await credentialResponse.text()).toBe(201);

  // One trigger per workflow (graph.Validate rejects more), so the cron,
  // webhook, event, and subflow triggers each get their own seed; every
  // other node rides along with the manual trigger. Unreachable nodes are
  // warnings only, so no edges are needed.
  const mainNodes: SeededNode[] = [
    { type: 'trigger.manual', id: 'n-manual', name: 'Manual', config: {} },
    { type: 'code.python', id: 'n-python', name: 'Python', config: { source: 'pass' } },
    { type: 'code.run', id: 'n-run', name: 'Code', config: { language: 'javascript', source: '1' } },
    { type: 'data.json.parse', id: 'n-parse', name: 'Parse', config: { text: '{}' } },
    { type: 'data.map', id: 'n-map', name: 'Map', config: { mappingRef: mappingId } },
    { type: 'data.set', id: 'n-set', name: 'Set', config: { values: { a: 1 } } },
    { type: 'db.postgres', id: 'n-pg', name: 'Postgres', config: { query: 'SELECT 1' }, credentials: { postgres: credentialId } },
    { type: 'flow.approval', id: 'n-approval', name: 'Approval', config: { message: 'ok?' } },
    { type: 'flow.emit', id: 'n-emit', name: 'Emit', config: { event: 'x' } },
    { type: 'flow.if', id: 'n-if', name: 'If', config: { condition: 'true' } },
    { type: 'flow.loop', id: 'n-loop', name: 'Loop', config: { workflowId: `e2e-fields-target-${stamp}` } },
    { type: 'flow.merge', id: 'n-merge', name: 'Merge', config: {} },
    { type: 'flow.subflow', id: 'n-subflow', name: 'Subflow', config: { workflowId: `e2e-fields-target-${stamp}` } },
    { type: 'flow.switch', id: 'n-switch', name: 'Switch', config: { cases: ['true'] } },
    { type: 'flow.wait', id: 'n-wait', name: 'Wait', config: { seconds: 1 } },
    { type: 'http.request', id: 'n-http', name: 'HTTP', config: { method: 'GET', url: 'https://example.test' } },
    { type: 'text.template', id: 'n-template', name: 'Template', config: { template: 'x' } },
  ];
  const mainId = `e2e-fields-main-${stamp}`;
  await seedWorkflow(request, mainId, mainNodes);

  const triggerOnly: Array<[string, SeededNode]> = [
    [`e2e-fields-cron-${stamp}`, { type: 'trigger.cron', id: 'n-cron', name: 'Schedule', config: { schedule: '*/5 * * * *' } }],
    [
      `e2e-fields-webhook-${stamp}`,
      { type: 'trigger.webhook', id: 'n-webhook', name: 'Webhook', config: { method: 'POST', path: `fields-freeze-${stamp}` } },
    ],
    [`e2e-fields-event-${stamp}`, { type: 'trigger.event', id: 'n-event', name: 'Event', config: { event: 'x' } }],
    [`e2e-fields-subflow-${stamp}`, { type: 'trigger.subflow', id: 'n-subflow-trigger', name: 'Subflow trigger', config: {} }],
  ];
  for (const [workflowId, node] of triggerOnly) {
    await seedWorkflow(request, workflowId, [node]);
  }

  for (const node of mainNodes) {
    await selectNode(page, mainId, node.id);
    const fields = await collectFields(page);
    expect(fields.common.sort(), `${node.type} common fields`).toEqual([...SNAPSHOT[node.type].common].sort());
    expect(fields.advanced.sort(), `${node.type} advanced fields`).toEqual([...SNAPSHOT[node.type].advanced].sort());
  }

  for (const [workflowId, node] of triggerOnly) {
    await selectNode(page, workflowId, node.id);
    const fields = await collectFields(page);
    expect(fields.common.sort(), `${node.type} common fields`).toEqual([...SNAPSHOT[node.type].common].sort());
    expect(fields.advanced.sort(), `${node.type} advanced fields`).toEqual([...SNAPSHOT[node.type].advanced].sort());
  }

  // Clean up the mapping and credential the workflow sweep does not know about.
  await request.delete(`/api/mappings/${encodeURIComponent(mappingId)}`);
  await request.delete(`/api/credentials/${encodeURIComponent(credentialId)}`);
});

test('http.request parameter groups follow the body type', async ({ page, request }) => {
  const stamp = Date.now();
  await seedWorkflow(request, `e2e-fields-http-${stamp}`, [
    { type: 'trigger.manual', id: 'start', name: 'Start', config: {} },
    { type: 'http.request', id: 'http', name: 'HTTP', config: { method: 'GET', url: 'https://example.test' } },
  ]);

  await selectNode(page, `e2e-fields-http-${stamp}`, 'http');
  // The parameter groups live on the Data tab (Phase 3).
  await page.getByTestId('inspector-tab-data').click();
  await expect(page.getByTestId('http-param-groups')).toBeVisible();

  // No body config: bodyType is none, so the body group stays hidden while
  // the other three groups render.
  await expect(page.getByTestId('param-group-body')).toHaveCount(0);
  for (const group of ['query', 'headers', 'pathParams']) {
    await expect(page.getByTestId(`param-group-${group}`)).toHaveCount(1);
  }

  // Switching the body type to json (Basics) reveals the body group on
  // the Data tab in place.
  await page.getByTestId('inspector-tab-configuration').click();
  await page.locator('#field-bodyType').selectOption('json');
  await page.getByTestId('inspector-tab-data').click();
  await expect(page.getByTestId('param-group-body')).toHaveCount(1);
});

test('Advanced raw JSON mode edits the node config directly, including internal keys', async ({
  page,
  request,
}) => {
  const stamp = Date.now();
  const mappingResponse = await request.post('/api/mappings', {
    data: {
      name: `e2e-raw-json-${stamp}`,
      rows: [{ target: 'email', source: '{{ $json.email }}', required: true }],
    },
  });
  expect(mappingResponse.status(), await mappingResponse.text()).toBe(201);
  const mappingId = (await mappingResponse.json()).id;

  await seedWorkflow(request, `e2e-raw-json-${stamp}`, [
    { type: 'trigger.manual', id: 'start', name: 'Start', config: {} },
    {
      type: 'data.map',
      id: 'map',
      name: 'Map',
      config: { mappingRef: mappingId, outputType: 'json' },
    },
  ]);

  await selectNode(page, `e2e-raw-json-${stamp}`, 'map');
  await page.getByTestId('inspector-tab-advanced').click();
  const editor = page.locator('#node-raw-config');
  await expect(editor).toBeVisible();
  await expect(editor).toHaveValue(/"mappingRef"/);

  // mappingRows is hidden from normal mode but editable here — the
  // deliberate power-user escape hatch (D3-5/D3-6).
  await editor.fill(
    JSON.stringify({ mappingRef: mappingId, mappingRows: [{ target: 'x', source: 'y' }], outputType: 'json' }, null, 2),
  );
  await editor.blur();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const doc = await request.get(`/api/workflows/e2e-raw-json-${stamp}`);
  const wf = (await doc.json()) as { nodes: { config?: Record<string, unknown> }[] };
  expect(wf.nodes[1].config?.mappingRows).toEqual([{ target: 'x', source: 'y' }]);

  // Invalid JSON keeps the draft and shows an inline error; the node
  // config is untouched.
  await editor.fill('{"a":');
  await editor.blur();
  await expect(page.locator('#node-raw-config + .field-error, .field-error').first()).toContainText('valid JSON');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Workflow saved.')).toBeVisible();
  const after = (await (await request.get(`/api/workflows/e2e-raw-json-${stamp}`)).json()) as {
    nodes: { config?: Record<string, unknown> }[];
  };
  expect(after.nodes[1].config?.mappingRows).toEqual([{ target: 'x', source: 'y' }]);

  await request.delete(`/api/mappings/${mappingId}`).catch(() => undefined);
});
