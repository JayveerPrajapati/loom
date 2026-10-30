export type PortType = 'json' | 'text' | 'event' | 'order' | 'message' | 'rows' | 'any';
export type FieldType = 'string' | 'number' | 'boolean' | 'array' | 'object';

export interface FieldDef {
  type: FieldType;
  required?: boolean;
  enum?: string[];
}

export interface NodeDef {
  type: string;
  typeVersion: number;
  category: string;
  description: string;
  inputs: string[];
  outputs: string[];
  inputType: PortType;
  outputType: PortType;
  outputTypeKey?: string;
  config?: Record<string, FieldDef>;
  credentials?: string[];
  optionalCredentials?: string[];
}

export interface CredentialInfo {
  id: string;
  type: string;
  createdAt: string;
}

export interface NodeData {
  nodeId: string;
  name: string;
  type: string;
  status: string;
  error?: string;
  outputs?: Record<string, Record<string, unknown>>;
}

export interface DataContext {
  runId: string;
  workflowId: string;
  nodeId?: string;
  upstream: string[];
  nodes: NodeData[];
}

export interface Endpoint {
  node: string;
  port: string;
}

export interface RetryPolicy {
  maxAttempts?: number;
  backoffMs?: number;
  maxDelayMs?: number;
}

export interface BatchPolicy {
  itemsPath?: string;
  continueOnItemError?: boolean;
}

export interface WorkflowNode {
  id: string;
  type: string;
  typeVersion: number;
  name: string;
  position: { x: number; y: number };
  config: Record<string, unknown>;
  credentials?: Record<string, string>;
  retry?: RetryPolicy;
  onError?: 'stop' | 'continue' | 'branch';
  batch?: BatchPolicy;
}

export interface WorkflowEdge {
  from: Endpoint;
  to: Endpoint;
}

export interface Workflow {
  id: string;
  name: string;
  version: number;
  settings?: Record<string, unknown>;
  nodes: WorkflowNode[];
  edges: WorkflowEdge[];
}

export interface WorkflowSummary {
  id: string;
  name: string;
  version: number;
  publishedVersion: number;
  draft: boolean;
  updatedAt: string;
  jobCount?: number;
  activeJobCount?: number;
}

export interface WorkflowVersion {
  workflowId: string;
  version: number;
  contentHash: string;
  nodeCount: number;
  publishedAt: string;
}

export interface WorkflowState {
  publishedVersion: number;
  draft: boolean;
  contentHash: string;
  versions: WorkflowVersion[];
}

export interface Job {
  id: string;
  name: string;
  templateId: string;
  templateName?: string;
  templateVersion: number;
  enabled: boolean;
  triggerType: string;
  inputDefaults?: Record<string, unknown>;
  triggerConfig?: Record<string, unknown>;
  createdAt: string;
  updatedAt: string;
  lastRunAt?: string;
  nextRunAt?: string;
}

export interface SchedulePreview {
  valid: boolean;
  next: string[];
  error?: string;
}

export interface RunStats {
  total: number;
  done: number;
  failed: number;
  waiting: number;
  active: number;
  last24h: number;
}

export interface Run {
  id: string;
  workflowId: string;
  workflowVersion: string;
  templateVersion?: number;
  jobId?: string;
  status: string;
  trigger: string;
  startedAt: string;
  finishedAt?: string;
  error?: string;
  resumeNode?: string;
  resumeToken?: string;
  parentRunId?: string;
  parentNodeId?: string;
}

export interface RunEvent {
  seq: number;
  type: string;
  at: string;
  nodeId?: string;
  attempt?: number;
  inputHash?: string;
  outputHash?: string;
  durationMs?: number;
  error?: string;
  data?: Record<string, unknown>;
}

export interface ReplayResult {
  runId: string;
  mode: 'full' | 'step' | 'from';
  nodeId?: string;
  match: boolean;
  recordedHash?: string;
  actualHash?: string;
  status: string;
  outputs?: Record<string, unknown>;
  events?: RunEvent[];
}

export interface NodeDiff {
  nodeId: string;
  name?: string;
  baseStatus: string;
  againstStatus: string;
  baseDurationMs?: number;
  againstDurationMs?: number;
  inputMatch: boolean;
  outputMatch: boolean;
  baseInputHash?: string;
  againstInputHash?: string;
  baseOutputHash?: string;
  againstOutputHash?: string;
  baseOutput?: unknown;
  againstOutput?: unknown;
}

export interface RunDiff {
  base: string;
  against: string;
  workflowId: string;
  changed: number;
  nodes: NodeDiff[];
}

export interface Example {
  id: string;
  name: string;
  workflow: Workflow;
}

export interface ValidationIssue {
  severity: 'error' | 'warning';
  code: string;
  node?: string;
  edge?: WorkflowEdge;
  message: string;
}
