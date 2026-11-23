import { expect, test, type APIRequestContext } from '@playwright/test';
import { e2eCode, e2eUser, login } from './auth';
import { deleteTestWorkflows } from './helpers';

// The config binds the throwaway Go server to 127.0.0.1:5700 by default.
const baseURL = `http://127.0.0.1:${process.env.LOOM_E2E_PORT ?? 5700}`;

const WEBHOOK_SECRET = 'E2E-WEBHOOK-SECRET';
const HMAC_SECRET = 'E2E-HMAC-KEY';

let sequence = 0;

// Workflow ids/paths must be unique across runs: the data dir is a throwaway
// temp directory, but a reused LOOM_E2E_DATA would otherwise collide.
function unique(prefix: string): string {
  sequence += 1;
  return `e2e-${prefix}-${Date.now().toString(36)}-${sequence}-${Math.random().toString(36).slice(2, 8)}`;
}

function manualNode() {
  return {
    id: 'start',
    type: 'trigger.manual',
    typeVersion: 1,
    name: 'Start',
    position: { x: 0, y: 0 },
    config: {},
  };
}

function webhookNode(path: string, withHmac: boolean) {
  const config: Record<string, unknown> = { path, method: 'POST', secret: WEBHOOK_SECRET };
  if (withHmac) config.hmacSecret = HMAC_SECRET;
  return {
    id: 'hook',
    type: 'trigger.webhook',
    typeVersion: 1,
    name: 'Webhook',
    position: { x: 0, y: 0 },
    config,
  };
}

function dataNode(batch?: Record<string, unknown>) {
  return {
    id: 'shape',
    type: 'data.set',
    typeVersion: 1,
    name: 'Shape',
    position: { x: 260, y: 0 },
    config: { values: { ok: true }, keepOnlySet: true },
    ...(batch ? { batch } : {}),
  };
}

function workflow(id: string, nodes: unknown[], edges: Array<{ from: { node: string; port: string }; to: { node: string; port: string } }>) {
  return { id, name: `Security audit ${id}`, version: 1, nodes, edges };
}

async function createWorkflow(request: APIRequestContext, body: unknown): Promise<void> {
  const response = await request.post('/api/workflows', { data: body });
  expect(response.status()).toBe(201);
}

async function putWorkflow(request: APIRequestContext, body: unknown): Promise<number> {
  const response = await request.put(`/api/workflows/${(body as { id: string }).id}`, { data: body });
  return response.status();
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

test('S1 viewer gets masked webhook secrets while admin still gets the plaintext', async ({
  request,
  playwright,
}) => {
  const stamp = unique('s1');
  const id = stamp;
  const path = `hook-${stamp}`;
  await createWorkflow(
    request,
    workflow(
      id,
      [webhookNode(path, true), dataNode()],
      [{ from: { node: 'hook', port: 'main' }, to: { node: 'shape', port: 'main' } }],
    ),
  );

  const adminResponse = await request.get(`/api/workflows/${id}`);
  expect(adminResponse.status()).toBe(200);
  const adminText = await adminResponse.text();
  expect(adminText).toContain(WEBHOOK_SECRET);
  expect(adminText).toContain(HMAC_SECRET);

  const viewerEmail = `viewer-${stamp}@e2e.local`;
  const viewerPassword = 'e2e-viewer-password-1';
  const userResponse = await request.post('/api/users', {
    data: { email: viewerEmail, name: 'S1 Viewer', role: 'viewer', password: viewerPassword },
  });
  if (userResponse.status() !== 201) {
    test.skip(
      true,
      `harness cannot mint a viewer: POST /api/users returned ${userResponse.status()} ${await userResponse.text()}`,
    );
  }

  const viewer = await playwright.request.newContext({ baseURL });
  try {
    const viewerLogin = await viewer.post('/api/auth/login', {
      data: { email: viewerEmail, password: viewerPassword },
    });
    expect(viewerLogin.status()).toBe(200);

    const viewerResponse = await viewer.get(`/api/workflows/${id}`);
    expect(viewerResponse.status()).toBe(200);
    const viewerText = await viewerResponse.text();
    expect(viewerText).not.toContain(WEBHOOK_SECRET);
    expect(viewerText).not.toContain(HMAC_SECRET);

    const body = await viewerResponse.json();
    const hook = body.nodes.find((node: { type: string }) => node.type === 'trigger.webhook');
    expect(hook.config.secret).toBe('[redacted]');
    expect(hook.config.hmacSecret).toBe('[redacted]');
    expect(hook.config.secretSet).toBe(true);
  } finally {
    await viewer.dispose();
  }
});

test('S2 a webhook run does not persist or expose the authenticating secret', async ({ request }) => {
  const stamp = unique('s2');
  const id = stamp;
  const path = `hook-${stamp}`;
  await createWorkflow(
    request,
    workflow(
      id,
      [webhookNode(path, false), dataNode()],
      [{ from: { node: 'hook', port: 'main' }, to: { node: 'shape', port: 'main' } }],
    ),
  );

  const hookResponse = await request.post(`/hook/${path}?secret=${encodeURIComponent(WEBHOOK_SECRET)}`, {
    data: { hello: 'world' },
  });
  expect(hookResponse.status()).toBe(200);
  const hookBody = await hookResponse.json();
  expect(Object.keys(hookBody).sort()).toEqual(['runId', 'status']);
  expect(JSON.stringify(hookBody)).not.toContain(WEBHOOK_SECRET);

  expect(hookBody.status).toBe('done');
  const contextResponse = await request.get(`/api/runs/${hookBody.runId}/data-context`);
  expect(contextResponse.status()).toBe(200);
  const contextText = await contextResponse.text();
  expect(contextText).not.toContain(WEBHOOK_SECRET);
  expect(contextText).toContain('[redacted]');
});

test('S5 login rejects a cross-origin Origin and accepts same-origin / no-Origin', async ({ request }) => {
  const credentials = { email: e2eUser, password: e2eCode };

  const crossOrigin = await request.post('/api/auth/login', {
    headers: { Origin: 'https://evil.example' },
    data: credentials,
  });
  expect(crossOrigin.status()).toBe(403);

  const sameOrigin = await request.post('/api/auth/login', {
    headers: { Origin: baseURL },
    data: credentials,
  });
  expect(sameOrigin.status()).toBe(200);

  const noOrigin = await request.post('/api/auth/login', { data: credentials });
  expect(noOrigin.status()).toBe(200);
});

test('N2-2 batch concurrency caps at 32', async ({ request }) => {
  const id = unique('n22');
  await createWorkflow(
    request,
    workflow(
      id,
      [manualNode(), dataNode()],
      [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
    ),
  );

  const overCap = workflow(
    id,
    [manualNode(), dataNode({ itemsPath: 'items', concurrency: 33 })],
    [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  );
  expect(await putWorkflow(request, overCap)).toBe(422);

  const atCap = workflow(
    id,
    [manualNode(), dataNode({ itemsPath: 'items', concurrency: 32 })],
    [{ from: { node: 'start', port: 'main' }, to: { node: 'shape', port: 'main' } }],
  );
  expect(await putWorkflow(request, atCap)).toBe(200);
});

test('N2-3 flow.wait seconds caps at 30 days', async ({ request }) => {
  const id = unique('n23');
  const waitNodes = (seconds: number) => [
    manualNode(),
    {
      id: 'wait',
      type: 'flow.wait',
      typeVersion: 1,
      name: 'Wait',
      position: { x: 260, y: 0 },
      config: { seconds },
    },
  ];
  const waitEdges = [{ from: { node: 'start', port: 'main' }, to: { node: 'wait', port: 'main' } }];

  await createWorkflow(request, workflow(id, waitNodes(1), waitEdges));

  expect(await putWorkflow(request, workflow(id, waitNodes(2_592_001), waitEdges))).toBe(422);
  expect(await putWorkflow(request, workflow(id, waitNodes(86_400), waitEdges))).toBe(200);
});

test('N2-4 flow.switch accepts at most three cases', async ({ request }) => {
  const id = unique('n24');
  const switchNodes = (cases: string[]) => [
    manualNode(),
    {
      id: 'switch',
      type: 'flow.switch',
      typeVersion: 1,
      name: 'Switch',
      position: { x: 260, y: 0 },
      config: { cases },
    },
  ];
  const switchEdges = [{ from: { node: 'start', port: 'main' }, to: { node: 'switch', port: 'main' } }];

  await createWorkflow(request, workflow(id, switchNodes(['a']), switchEdges));

  expect(await putWorkflow(request, workflow(id, switchNodes(['a', 'b', 'c', 'd']), switchEdges))).toBe(422);
  expect(await putWorkflow(request, workflow(id, switchNodes(['a', 'b', 'c']), switchEdges))).toBe(200);
});
