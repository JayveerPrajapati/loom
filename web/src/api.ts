import type {
  CredentialInfo,
  DataContext,
  Example,
  Job,
  NodeDef,
  ReplayResult,
  Run,
  RunDiff,
  RunEvent,
  RunStats,
  SchedulePreview,
  Workflow,
  WorkflowState,
  WorkflowSummary,
  WorkflowVersion,
} from './types';

let token = localStorage.getItem('loom.token') ?? '';

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

export function setToken(value: string) {
  token = value.trim();
  if (token) localStorage.setItem('loom.token', token);
  else localStorage.removeItem('loom.token');
}

export function getToken() {
  return token;
}

function headers(extra?: HeadersInit): Headers {
  const out = new Headers(extra);
  if (token) out.set('Authorization', `Bearer ${token}`);
  return out;
}

function reportConnectivity(online: boolean) {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent('loom:connectivity', { detail: { online } }));
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const h = headers(init.headers);
  if (init.body && !h.has('Content-Type')) h.set('Content-Type', 'application/json');
  let res: Response;
  try {
    res = await fetch(path, { ...init, headers: h });
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
    throw new ApiError(res.status, message);
  }
  return data as T;
}

export const api = {
  healthz: () => request<{ status: string }>('/healthz'),
  stats: () => request<RunStats>('/api/stats'),
  nodeTypes: () => request<{ nodeTypes: NodeDef[] }>('/api/node-types'),
  examples: () => request<{ examples: Example[] }>('/api/examples'),
  listCredentials: () =>
    request<{ credentials: CredentialInfo[]; count: number }>('/api/credentials'),
  createCredential: (credential: { id?: string; type: string; data: Record<string, unknown> }) =>
    request<CredentialInfo>('/api/credentials', {
      method: 'POST',
      body: JSON.stringify(credential),
    }),
  deleteCredential: (id: string) =>
    request<void>(`/api/credentials/${encodeURIComponent(id)}`, { method: 'DELETE' }),

  listWorkflows: () => request<{ workflows: WorkflowSummary[]; count: number }>('/api/workflows'),
  getWorkflow: (id: string) => request<Workflow>(`/api/workflows/${encodeURIComponent(id)}`),
  getWorkflowState: (id: string) =>
    request<WorkflowState>(`/api/workflows/${encodeURIComponent(id)}/state`),
  listWorkflowVersions: (id: string) =>
    request<{ versions: WorkflowVersion[]; count: number }>(
      `/api/workflows/${encodeURIComponent(id)}/versions`,
    ),
  publishWorkflow: (id: string) =>
    request<{ workflow: Workflow; version: WorkflowVersion }>(
      `/api/workflows/${encodeURIComponent(id)}/publish`,
      { method: 'POST' },
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
  listRuns: (workflowId: string | null | undefined, limit = 20) => {
    const workflow = workflowId ? `workflow=${encodeURIComponent(workflowId)}&` : '';
    return request<{ runs: Run[]; count: number }>(`/api/runs?${workflow}limit=${limit}`);
  },
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
  replayRun: (runId: string, mode: 'full' | 'step' | 'from', nodeId?: string) =>
    request<ReplayResult>(`/api/runs/${encodeURIComponent(runId)}/replay`, {
      method: 'POST',
      body: JSON.stringify({ mode, nodeId }),
    }),
  cancelRun: (runId: string) =>
    request<{ status: string }>(`/api/runs/${encodeURIComponent(runId)}/cancel`, { method: 'POST' }),
  resumeRun: (runId: string, decision: 'approved' | 'rejected') =>
    request<{ runId: string; status: string; events?: RunEvent[] }>(
      `/api/runs/${encodeURIComponent(runId)}/resume`,
      { method: 'POST', body: JSON.stringify({ decision }) },
    ),

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
