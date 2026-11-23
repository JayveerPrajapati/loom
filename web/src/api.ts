import type {
  APITokenInfo,
  AuthResponse,
  AuthUser,
  ConfigProfile,
  ConfigProfileUsage,
  ConfigProfileVersion,
  BundleProfileUse,
  Capabilities,
  AuthEvent,
  CredentialInfo,
  CredentialUsage,
  ProfileTestResult,
  PythonEnv,
  RecordedScriptTest,
  DataContext,
  DataSchema,
  DataSchemaUsage,
  DataSchemaVersion,
  Mapping,
  MappingRow,
  MappingUsage,
  MappingVersion,
  Script,
  Connector,
  ConnectorUsage,
  ConnectorVersion,
  ConnectorOperation,
  ScriptTestResult,
  ScriptUsage,
  ScriptVersion,
  SchemaField,
  SchemaTestResult,
  Decision,
  DecisionStats,
  Example,
  Job,
  NodeDef,
  ReplayResult,
  Run,
  RunDiff,
  RunEvent,
  RunStats,
  SchedulePreview,
  SubprocessNodes,
  VerifyResult,
  Workflow,
  WorkflowState,
  WorkflowSummary,
  WorkflowUpgradePreview,
  WorkflowVersion,
} from './types';
import type { PageParams, PagedResponse } from './paging';
import type { PublishPhase } from './publishProgress';

export class ApiError extends Error {
  status: number;
  retryAfterSec?: number;
  constructor(status: number, message: string, retryAfterSec?: number) {
    super(message);
    this.status = status;
    if (retryAfterSec !== undefined) this.retryAfterSec = retryAfterSec;
  }
}

function readCookie(name: string): string {
  if (typeof document === 'undefined') return '';
  for (const part of document.cookie.split(';')) {
    const [key, ...rest] = part.trim().split('=');
    if (key === name) return decodeURIComponent(rest.join('='));
  }
  return '';
}

function headers(extra?: HeadersInit): Headers {
  return new Headers(extra);
}

function reportConnectivity(online: boolean) {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent('loom:connectivity', { detail: { online } }));
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const h = headers(init.headers);
  const method = (init.method ?? 'GET').toUpperCase();
  if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
    const csrf = readCookie('loom_csrf');
    if (csrf) h.set('X-CSRF-Token', csrf);
  }
  if (init.body && !h.has('Content-Type')) h.set('Content-Type', 'application/json');
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers: h, credentials: 'same-origin' });
  } catch (error) {
    reportConnectivity(false);
    throw error;
  }
  reportConnectivity(true);
  const text = await res.text();
  let data: unknown = undefined;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    let message = res.statusText;
    if (data && typeof data === 'object' && 'error' in data) message = String((data as { error: unknown }).error);
    else if (typeof data === 'string' && data) message = data;
    if (res.status === 401 && typeof window !== 'undefined') {
      window.dispatchEvent(new CustomEvent('loom:auth-required', { detail: { message } }));
    }
    const retryRaw =
      data && typeof data === 'object' && 'retryAfterSec' in data
        ? Number((data as { retryAfterSec: unknown }).retryAfterSec)
        : undefined;
    throw new ApiError(
      res.status,
      message,
      retryRaw !== undefined && Number.isFinite(retryRaw) && retryRaw > 0
        ? Math.floor(retryRaw)
        : undefined,
    );
  }
  return data as T;
}

function pageQuery(params?: PageParams, extra?: Record<string, string | undefined>): string {
  const query = new URLSearchParams();
  if (extra) {
    for (const [key, value] of Object.entries(extra)) {
      if (value) query.set(key, value);
    }
  }
  if (params?.limit) query.set('limit', String(params.limit));
  if (params?.cursor) query.set('cursor', params.cursor);
  if (params?.q) query.set('q', params.q);
  return query.size > 0 ? `?${query.toString()}` : '';
}

async function paged<T, K extends string>(path: string, key: K): Promise<PagedResponse<T>> {
  const result = await request<
    Record<K, T[]> & { count: number; nextCursor?: string; hasMore?: boolean }
  >(path);
  return {
    items: result[key] ?? [],
    count: result.count,
    nextCursor: result.nextCursor,
    hasMore: result.hasMore,
  };
}

export const api = {
  me: () => request<AuthResponse>('/api/auth/me'),
  login: (email: string, password: string) =>
    request<AuthResponse>('/api/auth/login', {
      method: 'POST',
      body: JSON.stringify({ email, password }),
    }),
  logout: () => request<void>('/api/auth/logout', { method: 'POST' }),
  changePassword: (current: string, next: string) =>
    request<{ status: string }>('/api/auth/password', {
      method: 'POST',
      body: JSON.stringify({ current, new: next }),
    }),
  listUsers: () => request<{ users: AuthUser[] }>('/api/users'),
  createUser: (user: { email: string; name?: string; role: string; password: string }) =>
    request<AuthUser>('/api/users', { method: 'POST', body: JSON.stringify(user) }),
  updateUser: (id: string, patch: { role?: string; disabled?: boolean }) =>
    request<AuthUser>(`/api/users/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(patch),
    }),
  resetUserPassword: (id: string, password: string) =>
    request<{ status: string }>(`/api/users/${encodeURIComponent(id)}/password`, {
      method: 'POST',
      body: JSON.stringify({ password }),
    }),
  listTokens: (userId?: string) =>
    request<{ tokens: APITokenInfo[] }>(
      `/api/tokens${userId ? `?user=${encodeURIComponent(userId)}` : ''}`,
    ),
  createToken: (token: { name: string; role?: string; expiresAt?: string }) =>
    request<{ token: string; apiToken: APITokenInfo }>('/api/tokens', {
      method: 'POST',
      body: JSON.stringify(token),
    }),
  revokeToken: (id: string) =>
    request<{ status: string }>(`/api/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  healthz: () =>
    request<{
      status: string;
      subprocessNodes?: SubprocessNodes;
      /** Operational-trust campaign: the live host's egress posture
       *  (absent on sim servers, which never dial out). */
      egress?: { mode: 'allow' | 'deny'; allowlist?: number };
    }>('/healthz'),
  capabilities: () => request<Capabilities>('/api/capabilities'),
  listAuthEvents: () => request<{ events: AuthEvent[] }>('/api/auth/events'),
  listPythonEnvs: () =>
    request<{ envs: PythonEnv[]; enabled: boolean }>('/api/python-envs'),
  // Storage-operations campaign: the read-only operational view.
  storageOps: () =>
    request<{
      stats: {
        runs: number;
        activeRuns: number;
        events: number;
        blobs: number;
        blobBytes: number;
        databaseBytes: number;
        eventsPerDay: { day: string; events: number }[] | null;
        projectedEvents30d: number;
        sharedBlobs: number;
        sharedBlobBytes: number;
        largestRuns: {
          runId: string;
          workflowId: string;
          workflowName: string;
          status: string;
          events: number;
          bytes: number;
          at: string;
        }[] | null;
        largestWorkflows: {
          workflowId: string;
          name: string;
          runs: number;
          events: number;
          bytes: number;
          lastActive: string;
        }[] | null;
        reclaimable: {
          days: number;
          runs: number;
          events: number;
          blobs: number;
          bytes: number;
        }[] | null;
      };
      hint: string;
      retention: { command: string; note: string };
    }>('/api/ops/storage'),
  recordedScriptTest: (id: string) =>
    request<RecordedScriptTest>(`/api/scripts/${encodeURIComponent(id)}/recorded-test`),
  stats: () => request<RunStats>('/api/stats'),
  nodeTypes: () => request<{ nodeTypes: NodeDef[] }>('/api/node-types'),
  examples: () => request<{ examples: Example[] }>('/api/examples'),
  listCredentials: () =>
    request<{ credentials: CredentialInfo[]; count: number }>('/api/credentials'),
  listCredentialsPage: (params?: PageParams) =>
    paged<CredentialInfo, 'credentials'>(`/api/credentials${pageQuery(params)}`, 'credentials'),
  createCredential: (credential: { id?: string; type: string; data: Record<string, unknown> }) =>
    request<CredentialInfo>('/api/credentials', {
      method: 'POST',
      body: JSON.stringify(credential),
    }),
  deleteCredential: (id: string) =>
    request<void>(`/api/credentials/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  credentialUsage: (id: string) =>
    request<CredentialUsage>(`/api/credentials/${encodeURIComponent(id)}/usage`),
  rotateCredential: (id: string, data: Record<string, unknown>) =>
    request<{ credential: CredentialInfo; references: number }>(
      `/api/credentials/${encodeURIComponent(id)}/rotate`,
      { method: 'POST', body: JSON.stringify({ data }) },
    ),

  listConfigProfiles: () =>
    request<{ profiles: ConfigProfile[]; count: number }>('/api/config-profiles'),
  listConfigProfilesPage: (nodeType?: string, params?: PageParams) =>
    paged<ConfigProfile, 'profiles'>(
      `/api/config-profiles${pageQuery(params, { nodeType })}`,
      'profiles',
    ),
  getConfigProfile: (id: string) =>
    request<ConfigProfile>(`/api/config-profiles/${encodeURIComponent(id)}`),
  createConfigProfile: (profile: {
    name: string;
    nodeType: string;
    description?: string;
    config: Record<string, unknown>;
    credentialRefs?: Record<string, string>;
    retry?: ConfigProfile['retry'];
    tags?: string[];
    owner?: string;
    bundle?: ConfigProfile['bundle'];
    testInput?: unknown;
    visibility?: string;
  }) =>
    request<ConfigProfile>('/api/config-profiles', {
      method: 'POST',
      body: JSON.stringify(profile),
    }),
  updateConfigProfile: (
    id: string,
    profile: {
      name: string;
      nodeType: string;
      description?: string;
      config: Record<string, unknown>;
      credentialRefs?: Record<string, string>;
      retry?: ConfigProfile['retry'];
      tags?: string[];
      owner?: string;
      bundle?: ConfigProfile['bundle'];
      testInput?: unknown;
      visibility?: string;
      version: number;
    },
  ) =>
    request<ConfigProfile>(`/api/config-profiles/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(profile),
    }),
  shareConfigProfile: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/config-profiles/${encodeURIComponent(id)}/share`, {
      method: 'POST',
    }),
  unshareConfigProfile: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/config-profiles/${encodeURIComponent(id)}/unshare`, {
      method: 'POST',
    }),
  deleteConfigProfile: (id: string) =>
    request<void>(`/api/config-profiles/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  duplicateConfigProfile: (id: string) =>
    request<ConfigProfile>(`/api/config-profiles/${encodeURIComponent(id)}/duplicate`, {
      method: 'POST',
    }),
  testConfigProfile: (id: string) =>
    request<ProfileTestResult>(`/api/config-profiles/${encodeURIComponent(id)}/test`, {
      method: 'POST',
    }),
  listConfigProfileVersions: (id: string) =>
    request<{ versions: ConfigProfileVersion[]; count: number }>(
      `/api/config-profiles/${encodeURIComponent(id)}/versions`,
    ),
  getConfigProfileVersion: (id: string, version: number) =>
    request<ConfigProfile>(
      `/api/config-profiles/${encodeURIComponent(id)}/versions/${version}`,
    ),
  configProfileUsage: (id: string) =>
    request<{ usage: ConfigProfileUsage[]; count: number }>(
      `/api/config-profiles/${encodeURIComponent(id)}/usage`,
    ),

  listDataSchemas: () => request<{ schemas: DataSchema[]; count: number }>('/api/data-schemas'),
  listDataSchemasPage: (params?: PageParams) =>
    paged<DataSchema, 'schemas'>(`/api/data-schemas${pageQuery(params)}`, 'schemas'),
  createDataSchema: (schema: {
    name: string;
    description?: string;
    fields: SchemaField[];
    tags?: string[];
    owner?: string;
    visibility?: string;
  }) =>
    request<DataSchema>('/api/data-schemas', {
      method: 'POST',
      body: JSON.stringify(schema),
    }),
  updateDataSchema: (
    id: string,
    schema: {
      name: string;
      description?: string;
      fields: SchemaField[];
      tags?: string[];
      owner?: string;
      visibility?: string;
      version: number;
    },
  ) =>
    request<DataSchema>(`/api/data-schemas/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(schema),
    }),
  deleteDataSchema: (id: string) =>
    request<void>(`/api/data-schemas/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  shareDataSchema: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/data-schemas/${encodeURIComponent(id)}/share`, {
      method: 'POST',
    }),
  unshareDataSchema: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/data-schemas/${encodeURIComponent(id)}/unshare`, {
      method: 'POST',
    }),
  getDataSchema: (id: string) =>
    request<DataSchema>(`/api/data-schemas/${encodeURIComponent(id)}`),
  listMappings: (kind?: string, nodeType?: string) => {
    const params = new URLSearchParams();
    if (kind) params.set('kind', kind);
    if (nodeType) params.set('nodeType', nodeType);
    const query = params.size > 0 ? `?${params.toString()}` : '';
    return request<{ mappings: Mapping[]; count: number }>(`/api/mappings${query}`);
  },
  listMappingsPage: (kind?: string, nodeType?: string, params?: PageParams) =>
    paged<Mapping, 'mappings'>(
      `/api/mappings${pageQuery(params, { kind, nodeType })}`,
      'mappings',
    ),
  evalExpression: (expression: string, sample: Record<string, unknown>) =>
    request<{ ok: boolean; value?: string; error?: string }>('/api/expressions/eval', {
      method: 'POST',
      body: JSON.stringify({ expression, sample }),
    }),
  getMapping: (id: string) => request<Mapping>(`/api/mappings/${encodeURIComponent(id)}`),
  createMapping: (mapping: {
    name: string;
    description?: string;
    kind?: string;
    rows: MappingRow[];
    tags?: string[];
    owner?: string;
    visibility?: string;
  }) =>
    request<Mapping>('/api/mappings', {
      method: 'POST',
      body: JSON.stringify(mapping),
    }),
  updateMapping: (
    id: string,
    mapping: {
      name: string;
      description?: string;
      kind?: string;
      rows: MappingRow[];
      tags?: string[];
      owner?: string;
      visibility?: string;
      version: number;
    },
  ) =>
    request<Mapping>(`/api/mappings/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(mapping),
    }),
  deleteMapping: (id: string) =>
    request<void>(`/api/mappings/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  duplicateMapping: (id: string) =>
    request<Mapping>(`/api/mappings/${encodeURIComponent(id)}/duplicate`, { method: 'POST' }),
  shareMapping: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/mappings/${encodeURIComponent(id)}/share`, {
      method: 'POST',
    }),
  unshareMapping: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/mappings/${encodeURIComponent(id)}/unshare`, {
      method: 'POST',
    }),
  hubOverview: () =>
    request<{
      profiles: Record<string, number>;
      mappingKinds: Record<string, number>;
      schemas: number;
    }>('/api/hub'),
  hubNodeType: (nodeType: string, includeProfiles = true) =>
    request<{
      nodeType: string;
      mappingKinds: string[];
      profiles: ConfigProfile[];
      mappings: { id: string; name: string }[];
      schemas: { id: string; name: string; version: number }[];
      schemaSlot: 'input' | 'output';
    }>(
      `/api/hub/${encodeURIComponent(nodeType)}${includeProfiles ? '' : '?profiles=0'}`,
    ),
  mappingVersions: (id: string) =>
    request<{ versions: MappingVersion[]; count: number }>(
      `/api/mappings/${encodeURIComponent(id)}/versions`,
    ),
  mappingUsage: (id: string) =>
    request<{ usage: MappingUsage[]; count: number; pins: number; profiles: BundleProfileUse[] }>(
      `/api/mappings/${encodeURIComponent(id)}/usage`,
    ),
  listConnectors: () =>
    request<{ connectors: Connector[]; count: number }>('/api/connectors'),
  listConnectorsPage: (params?: PageParams) =>
    paged<Connector, 'connectors'>(`/api/connectors${pageQuery(params)}`, 'connectors'),
  createConnector: (connector: {
    id?: string;
    name: string;
    description?: string;
    operations: ConnectorOperation[];
    tags?: string[];
    owner?: string;
    visibility?: string;
  }) =>
    request<Connector>('/api/connectors', {
      method: 'POST',
      body: JSON.stringify(connector),
    }),
  getConnector: (id: string) =>
    request<{ connector: Connector }>(`/api/connectors/${encodeURIComponent(id)}`),
  duplicateConnector: (id: string) =>
    request<{ connector: Connector }>(`/api/connectors/${encodeURIComponent(id)}/duplicate`, {
      method: 'POST',
    }),
  updateConnector: (
    id: string,
    connector: {
      name: string;
      description?: string;
      operations: ConnectorOperation[];
      tags?: string[];
      owner?: string;
      version: number;
    },
  ) =>
    request<Connector>(`/api/connectors/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(connector),
    }),
  deleteConnector: (id: string) =>
    request<void>(`/api/connectors/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  connectorVersions: (id: string) =>
    request<{ versions: ConnectorVersion[] }>(`/api/connectors/${encodeURIComponent(id)}/versions`),
  connectorUsage: (id: string) =>
    request<{ usage: ConnectorUsage[] }>(`/api/connectors/${encodeURIComponent(id)}/usage`),
  shareConnector: (id: string, visibility: 'private' | 'shared') =>
    request<void>(`/api/connectors/${encodeURIComponent(id)}/${visibility === 'shared' ? 'share' : 'unshare'}`, {
      method: 'POST',
    }),
  listScripts: (language?: string) => {
    const query = language ? `?language=${encodeURIComponent(language)}` : '';
    return request<{ scripts: Script[]; count: number }>(`/api/scripts${query}`);
  },
  listScriptsPage: (language?: string, params?: PageParams) =>
    paged<Script, 'scripts'>(`/api/scripts${pageQuery(params, { language })}`, 'scripts'),
  createScript: (script: {
    name: string;
    language: string;
    description?: string;
    source: string;
    requirements?: string[];
    timeoutSeconds?: number;
    maxMemoryMB?: number;
    tags?: string[];
    owner?: string;
    visibility?: string;
  }) =>
    request<Script>('/api/scripts', {
      method: 'POST',
      body: JSON.stringify(script),
    }),
  updateScript: (
    id: string,
    script: {
      name: string;
      description?: string;
      source: string;
      requirements?: string[];
      timeoutSeconds?: number;
      maxMemoryMB?: number;
      tags?: string[];
      owner?: string;
      version: number;
    },
  ) =>
    request<Script>(`/api/scripts/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(script),
    }),
  deleteScript: (id: string) =>
    request<void>(`/api/scripts/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  shareScript: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/scripts/${encodeURIComponent(id)}/share`, {
      method: 'POST',
    }),
  unshareScript: (id: string) =>
    request<{ id: string; visibility: string }>(`/api/scripts/${encodeURIComponent(id)}/unshare`, {
      method: 'POST',
    }),
  duplicateScript: (id: string) =>
    request<Script>(`/api/scripts/${encodeURIComponent(id)}/duplicate`, { method: 'POST' }),
  scriptVersions: (id: string) =>
    request<{ versions: ScriptVersion[]; count: number }>(
      `/api/scripts/${encodeURIComponent(id)}/versions`,
    ),
  scriptUsage: (id: string) =>
    request<{ usage: ScriptUsage[]; count: number; pins: number; profiles: BundleProfileUse[] }>(
      `/api/scripts/${encodeURIComponent(id)}/usage`,
    ),
  testScript: (id: string, input: unknown) =>
    request<ScriptTestResult>(`/api/scripts/${encodeURIComponent(id)}/test`, {
      method: 'POST',
      body: JSON.stringify({ input }),
    }),
  duplicateDataSchema: (id: string) =>
    request<DataSchema>(`/api/data-schemas/${encodeURIComponent(id)}/duplicate`, {
      method: 'POST',
    }),
  testDataSchema: (id: string, sample: unknown) =>
    request<SchemaTestResult>(`/api/data-schemas/${encodeURIComponent(id)}/test`, {
      method: 'POST',
      body: JSON.stringify({ sample }),
    }),
  listDataSchemaVersions: (id: string) =>
    request<{ versions: DataSchemaVersion[]; count: number }>(
      `/api/data-schemas/${encodeURIComponent(id)}/versions`,
    ),
  getDataSchemaVersion: (id: string, version: number) =>
    request<DataSchema>(`/api/data-schemas/${encodeURIComponent(id)}/versions/${version}`),
  dataSchemaUsage: (id: string) =>
    request<{ usage: DataSchemaUsage[]; count: number; pins: number; profiles: BundleProfileUse[] }>(
      `/api/data-schemas/${encodeURIComponent(id)}/usage`,
    ),

  listWorkflows: () => request<{ workflows: WorkflowSummary[]; count: number }>('/api/workflows'),
  listWorkflowsPage: (params?: PageParams) =>
    paged<WorkflowSummary, 'workflows'>(`/api/workflows${pageQuery(params)}`, 'workflows'),
  getWorkflow: (id: string) => request<Workflow>(`/api/workflows/${encodeURIComponent(id)}`),
  getWorkflowState: (id: string) =>
    request<WorkflowState>(`/api/workflows/${encodeURIComponent(id)}/state`),
  listWorkflowVersions: (id: string) =>
    request<{ versions: WorkflowVersion[]; count: number }>(
      `/api/workflows/${encodeURIComponent(id)}/versions`,
    ),
  publishWorkflow: (id: string, publishId?: string) =>
    request<{
      workflow: Workflow;
      version: WorkflowVersion;
      // Phase 6 D6-4: non-blocking mapping-contract compatibility
      // observations recorded at publish.
      mappingWarnings?: { node: string; message: string }[];
    }>(
      `/api/workflows/${encodeURIComponent(id)}/publish${
        publishId ? `?publishId=${encodeURIComponent(publishId)}` : ''
      }`,
      {
        method: 'POST',
      },
    ),
  // Audit 4E: poll the live phases of one publish. The publish POST
  // itself still decides success/failure.
  publishStatus: (id: string, publishId: string) =>
    request<{
      publishId: string;
      workflowId: string;
      done: boolean;
      error?: string;
      phases: PublishPhase[];
    }>(
      `/api/workflows/${encodeURIComponent(id)}/publish/status?publishId=${encodeURIComponent(publishId)}`,
    ),
  workflowUpgradePreview: (id: string, apply?: boolean) =>
    request<WorkflowUpgradePreview>(
      `/api/workflows/${encodeURIComponent(id)}/upgrade-preview`,
      { method: 'POST', body: JSON.stringify({ apply: apply ?? false }) },
    ),
  getWorkflowVersion: (id: string, version: number) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(id)}/versions/${version}`),
  getRunbook: (id: string) => request<string>(`/api/workflows/${encodeURIComponent(id)}/runbook`),
  createWorkflow: (workflow: Workflow) =>
    request<Workflow>('/api/workflows', { method: 'POST', body: JSON.stringify(workflow) }),
  saveWorkflow: (workflow: Workflow) =>
    request<Workflow>(`/api/workflows/${encodeURIComponent(workflow.id)}`, {
      method: 'PUT',
      body: JSON.stringify(workflow),
    }),
  deleteWorkflow: (id: string) =>
    request<void>(`/api/workflows/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  listJobs: () => request<{ jobs: Job[]; count: number }>('/api/jobs'),
  getJob: (id: string) => request<Job>(`/api/jobs/${encodeURIComponent(id)}`),
  createJob: (job: {
    name: string;
    templateId: string;
    templateVersion: number;
    inputDefaults?: Record<string, unknown>;
    triggerType?: string;
    triggerConfig?: Record<string, unknown>;
  }) => request<Job>('/api/jobs', { method: 'POST', body: JSON.stringify(job) }),
  updateJob: (
    id: string,
    job: {
      name?: string;
      triggerType?: string;
      inputDefaults?: Record<string, unknown>;
      triggerConfig?: Record<string, unknown>;
    },
  ) =>
    request<Job>(`/api/jobs/${encodeURIComponent(id)}`, {
      method: 'PUT',
      body: JSON.stringify(job),
    }),
  duplicateJob: (id: string) =>
    request<Job>(`/api/jobs/${encodeURIComponent(id)}/duplicate`, { method: 'POST' }),
  deleteJob: (id: string) =>
    request<void>(`/api/jobs/${encodeURIComponent(id)}`, { method: 'DELETE' }),
  runJob: (id: string, input: Record<string, unknown> = {}) =>
    request<{ runId: string; status: string; jobId: string }>(
      `/api/jobs/${encodeURIComponent(id)}/run?async=1`,
      { method: 'POST', body: JSON.stringify({ input }) },
    ),
  startBatch: (
    id: string,
    payload: { items?: unknown[]; csv?: string; concurrency: number },
  ) =>
    request<{ runId: string; status: string; jobId: string; total: number; concurrency: number }>(
      `/api/jobs/${encodeURIComponent(id)}/batch`,
      { method: 'POST', body: JSON.stringify(payload) },
    ),
  retryRun: (runId: string) =>
    request<{ runId: string; status: string; jobId: string; total: number; retryOf: string }>(
      `/api/runs/${encodeURIComponent(runId)}/retry`,
      { method: 'POST' },
    ),
  setJobEnabled: (id: string, enabled: boolean) =>
    request<Job>(`/api/jobs/${encodeURIComponent(id)}/enabled`, {
      method: 'POST',
      body: JSON.stringify({ enabled }),
    }),
  upgradeJob: (id: string, templateVersion: number) =>
    request<Job>(`/api/jobs/${encodeURIComponent(id)}/upgrade`, {
      method: 'POST',
      body: JSON.stringify({ templateVersion }),
    }),

  startRun: (workflowId: string, input: Record<string, unknown> = {}) =>
    request<{ runId: string; status: string }>(`/api/workflows/${encodeURIComponent(workflowId)}/runs?async=1`, {
      method: 'POST',
      body: JSON.stringify({ input }),
    }),
  listRuns: (
    workflowId: string | null | undefined,
    limit = 20,
    opts: {
      status?: string;
      q?: string;
      cursor?: string;
      offset?: number;
      trigger?: string;
      job?: string;
      roots?: boolean;
    } = {},
  ) => {
    const params = new URLSearchParams();
    if (workflowId) params.set('workflow', workflowId);
    params.set('limit', String(limit));
    if (opts.status) params.set('status', opts.status);
    if (opts.q) params.set('q', opts.q);
    if (opts.cursor) params.set('cursor', opts.cursor);
    if (opts.offset) params.set('offset', String(opts.offset));
    if (opts.trigger) params.set('trigger', opts.trigger);
    if (opts.job) params.set('job', opts.job);
    if (opts.roots) params.set('roots', '1');
    return request<{
      runs: Run[];
      count: number;
      limit?: number;
      offset?: number;
      nextCursor?: string;
    }>(`/api/runs?${params.toString()}`);
  },
  waitingRuns: (limit = 100) =>
    request<{ runs: Run[]; count: number }>(`/api/runs?status=waiting&limit=${limit}`),
  listAllRuns: (limit = 50, jobId?: string) => {
    const job = jobId ? `job=${encodeURIComponent(jobId)}&` : '';
    return request<{ runs: Run[]; count: number }>(`/api/runs?${job}limit=${limit}`);
  },
  listJobRuns: (jobId: string, limit = 50) =>
    request<{ runs: Run[]; count: number }>(
      `/api/runs?job=${encodeURIComponent(jobId)}&limit=${limit}`,
    ),
  previewSchedule: (
    triggerType: string,
    triggerConfig?: Record<string, unknown>,
    count = 3,
  ) =>
    request<SchedulePreview>('/api/schedules/preview', {
      method: 'POST',
      body: JSON.stringify({ triggerType, triggerConfig, count }),
    }),
  getRun: (runId: string) => request<Run>(`/api/runs/${encodeURIComponent(runId)}`),
  getRunWorkflow: (runId: string) =>
    request<Workflow>(`/api/runs/${encodeURIComponent(runId)}/workflow`),
  listChildRuns: (runId: string, limit = 20) =>
    request<{ runs: Run[]; count: number }>(
      `/api/runs?parent=${encodeURIComponent(runId)}&limit=${limit}`,
    ),
  listEvents: (runId: string) =>
    request<{ events: RunEvent[] }>(`/api/runs/${encodeURIComponent(runId)}/events?format=json`),
  dataContext: (runId: string, nodeId?: string) =>
    request<DataContext>(
      `/api/runs/${encodeURIComponent(runId)}/data-context${nodeId ? `?nodeId=${encodeURIComponent(nodeId)}` : ''}`,
    ),
  previewExpression: (workflowId: string, payload: { runId?: string; nodeId?: string; expression: string }) =>
    request<{ runId: string; nodeId?: string; expression: string; value: string }>(
      `/api/workflows/${encodeURIComponent(workflowId)}/preview`,
      { method: 'POST', body: JSON.stringify(payload) },
    ),
  getBlob: (hash: string) => request<unknown>(`/api/blobs/${encodeURIComponent(hash)}`),
  diffRuns: (runId: string, against: string) =>
    request<RunDiff>(
      `/api/runs/${encodeURIComponent(runId)}/diff?against=${encodeURIComponent(against)}`,
    ),
  replayRun: (
    runId: string,
    mode: 'full' | 'step' | 'from',
    nodeId?: string,
    execute?: boolean,
    input?: Record<string, unknown>,
    hermetic?: boolean,
  ) =>
    request<ReplayResult>(`/api/runs/${encodeURIComponent(runId)}/replay`, {
      method: 'POST',
      body: JSON.stringify({
        mode,
        nodeId,
        execute: Boolean(execute),
        hermetic: Boolean(hermetic),
        input,
      }),
    }),
  verifyWorkflow: (workflowId: string, runId: string, version?: number) =>
    request<VerifyResult>(`/api/workflows/${encodeURIComponent(workflowId)}/verify`, {
      method: 'POST',
      body: JSON.stringify({ runId, version }),
    }),
  cancelRun: (runId: string) =>
    request<{ status: string }>(`/api/runs/${encodeURIComponent(runId)}/cancel`, { method: 'POST' }),
  resumeRun: (runId: string, decision: 'approved' | 'rejected', note?: string) =>
    request<{ runId: string; status: string; events?: RunEvent[] }>(
      `/api/runs/${encodeURIComponent(runId)}/resume`,
      { method: 'POST', body: JSON.stringify({ decision, note }) },
    ),
  revokeApproval: (runId: string) =>
    request<{ runId: string; revoked: boolean }>(
      `/api/runs/${encodeURIComponent(runId)}/revoke-approval`,
      { method: 'POST' },
    ),
  listDecisions: (
    opts: { workflowId?: string; source?: string; decision?: string; limit?: number; offset?: number } = {},
  ) => {
    const params = new URLSearchParams();
    if (opts.workflowId) params.set('workflowId', opts.workflowId);
    if (opts.source) params.set('source', opts.source);
    if (opts.decision) params.set('decision', opts.decision);
    params.set('limit', String(opts.limit ?? 50));
    if (opts.offset) params.set('offset', String(opts.offset));
    return request<{ decisions: Decision[]; count: number }>(`/api/decisions?${params.toString()}`);
  },
  decisionMetrics: (windowHours = 24) =>
    request<DecisionStats>(`/api/metrics/decisions?windowHours=${windowHours}`),

  async streamRun(runId: string, onEvent: (event: RunEvent) => void, signal: AbortSignal) {
    let res: Response;
    try {
      res = await fetch(`/api/runs/${encodeURIComponent(runId)}/events`, { headers: headers(), signal });
    } catch (error) {
      if (!signal.aborted) reportConnectivity(false);
      throw error;
    }
    reportConnectivity(true);
    if (!res.ok || !res.body) throw new ApiError(res.status, 'could not open the event stream');
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buffer = '';
    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let index = buffer.indexOf('\n\n');
        while (index >= 0) {
          const chunk = buffer.slice(0, index);
          buffer = buffer.slice(index + 2);
          const data = chunk
            .split('\n')
            .filter((line) => line.startsWith('data:'))
            .map((line) => line.slice(5).trimStart())
            .join('\n');
          if (data) {
            try {
              onEvent(JSON.parse(data) as RunEvent);
            } catch {
              // Ignore a malformed frame; the event log remains the source of truth.
            }
          }
          index = buffer.indexOf('\n\n');
        }
      }
    } catch (error) {
      if (!signal.aborted) reportConnectivity(false);
      throw error;
    }
  },
};
