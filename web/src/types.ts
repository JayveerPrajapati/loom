export type PortType = 'json' | 'text' | 'event' | 'order' | 'message' | 'rows' | 'any';
export type FieldType = 'string' | 'number' | 'boolean' | 'array' | 'object' | 'any';

export interface FieldDef {
  type: FieldType;
  required?: boolean;
  /** Effective-value fallback; never persisted by makeNode as authored state. */
  default?: unknown;
  enum?: string[];
  pattern?: string;
  min?: number;
  max?: number;
  /** Curated label from the backend catalog (falls back to a key-derived one). */
  label?: string;
  /** One-line helper shown under the input. */
  description?: string;
  placeholder?: string;
  /** Moved into the inspector's Advanced disclosure. */
  advanced?: boolean;
  /** Rendered as a password input. */
  secret?: boolean;
  /** May be set by a Configuration Hub profile (allowlist marker). */
  profileable?: boolean;
}

export interface NodeDef {
  type: string;
  typeVersion: number;
  category: string;
  /** Human-facing name from the backend catalog; unique across types. */
  displayName?: string;
  description: string;
  inputs: string[];
  outputs: string[];
  inputType: PortType;
  outputType: PortType;
  outputTypeKey?: string;
  config?: Record<string, FieldDef>;
  credentials?: string[];
  optionalCredentials?: string[];
  /** EffectPolicy: what this node touches externally and how replay treats it. */
  effects?: string;
  /** Hub mapping kinds this type consumes (v3 D4): item on data.map, the parameter kinds on http.request. */
  mappingKinds?: string[];
}

export interface CredentialInfo {
  id: string;
  type: string;
  createdAt: string;
  usageCount: number;
}

export interface CredentialProfileUse {
  profileId: string;
  name: string;
  nodeType: string;
  version: number;
  visibility?: string;
  creatorId?: string;
  slots: string[];
}

export interface CredentialWorkflowUse {
  workflowId: string;
  name: string;
  version: number;
  current?: boolean;
  slots: string[];
}

export interface CredentialUsage {
  credentialId: string;
  profiles: CredentialProfileUse[];
  workflows: CredentialWorkflowUse[];
}

export interface AuthUser {
  id: string;
  email: string;
  name: string;
  role: 'viewer' | 'operator' | 'editor' | 'admin';
  disabled?: boolean;
  createdAt?: string;
  updatedAt?: string;
}

export interface AuthResponse {
  user: AuthUser;
  csrfToken?: string;
}

export interface APITokenInfo {
  id: string;
  userId: string;
  name: string;
  role: string;
  createdAt: string;
  lastUsedAt?: string;
  expiresAt?: string;
  revokedAt?: string;
}

export interface NodeData {
  nodeId: string;
  name: string;
  type: string;
  status: string;
  error?: string;
  outputs?: Record<string, Record<string, unknown>>;
  config?: Record<string, unknown>;
  credentials?: Record<string, string>;
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
  /** Configuration Hub profile id whose config merges under this node's own at publish. */
  profileRef?: string;
  /** Explicit ownership for node-authored keys a profile can also supply.
   * A missing map preserves legacy equality inference; a present map is authoritative. */
  profileOverrides?: Record<string, boolean>;
  /** Data contract id the trigger's inbound payload is validated against (v2 D11). */
  inputSchemaRef?: string;
  /** Data contract id checked against this node's main output (v2 D16, record + surface). */
  outputSchemaRef?: string;
  /** Publish-time resolved contracts (present on published snapshots, not drafts). */
  inputSchemaFields?: SchemaField[];
  outputSchemaFields?: SchemaField[];
  retry?: RetryPolicy;
  onError?: 'stop' | 'continue' | 'branch';
  batch?: BatchPolicy;
}

/** Reusable, versioned, non-secret node configuration (Configuration Hub). */
export interface ConfigProfile {
  id: string;
  name: string;
  nodeType: string;
  description?: string;
  version: number;
  config: Record<string, unknown>;
  credentialRefs?: Record<string, string>;
  retry?: RetryPolicy;
  tags?: string[];
  owner?: string;
  /** Composed reusable assets (Phase 1 P0-1); absent on pre-bundle versions. */
  bundle?: ProfileBundle;
  /** Saved test fixture (Phase 5): JSON sample item test surfaces prefill. */
  testInput?: unknown;
  /** private | shared (Phase 1 D-P0c); absent on pre-visibility assets reads as shared. */
  visibility?: string;
  creatorId?: string;
  createdAt: string;
  updatedAt: string;
  usageCount?: number;
}

/** Bundle references a profile composes (Phase 1 P0-1): mapping ids per
 * group and schema ids per slot. Scripts join in Phase 4. */
export interface ProfileBundle {
  mappings?: Record<string, string>;
  schemas?: Record<string, string>;
  /** Phase 4 D4-1: fills a code node's empty scriptRef. */
  script?: string;
}

export interface ConfigProfileVersion {
  profileId: string;
  version: number;
  contentHash: string;
  actor: string;
  createdAt: string;
}

export interface ConfigProfileUsage {
  profileId: string;
  profileVersion: number;
  workflowId: string;
  workflowVersion: number;
  workflowName?: string;
  createdAt: string;
}

/** Bundle section of a profile test (UX Phase 2 D2-7): refs resolved to
 * current versions, parameter rows compiled, named issues. */
export interface ProfileBundleTestSection {
  mappings: { kind: string; id: string; name: string; version: number; rows: number }[];
  schemas: { slot: string; id: string; name: string; version: number; fields: number }[];
  script?: { id: string; name: string; version: number; language: string; bytes: number };
  paramRows: { group: string; rows: number }[];
  issues: { name: string; message: string }[];
}

export interface ProfileTestResult {
  ok: boolean;
  status?: number;
  latencyMs?: number;
  error?: string;
  skipped?: string;
  bundle?: ProfileBundleTestSection;
  bundleOk?: boolean;
  /** Code-profile execution (Phase 5): the fixture-driven run result. */
  result?: unknown;
  stdout?: string;
  stderr?: string;
  exitCode?: number;
  logs?: string[];
}

/** Data contract (Configuration Hub v2): a typed field list describing the shape of data. */
export interface SchemaField {
  name: string;
  type: 'string' | 'number' | 'boolean' | 'object' | 'array';
  required?: boolean;
  description?: string;
  /** Nested fields; object only. */
  fields?: SchemaField[];
  /** Element contract; array only. */
  items?: SchemaField;
}

export interface DataSchema {
  id: string;
  name: string;
  description?: string;
  version: number;
  fields: SchemaField[];
  tags?: string[];
  owner?: string;
  /** private | shared (Phase 1 D-P0c). */
  visibility?: string;
  creatorId?: string;
  createdAt: string;
  updatedAt: string;
  usageCount?: number;
}

export interface DataSchemaVersion {
  schemaId: string;
  version: number;
  contentHash: string;
  actor: string;
  createdAt: string;
}

export interface DataSchemaUsage {
  schemaId: string;
  schemaVersion: number;
  workflowId: string;
  workflowVersion: number;
  workflowName?: string;
  createdAt: string;
}

export interface SchemaViolation {
  path: string;
  message: string;
}

/** Reusable mapping row consumed by data.map nodes (Configuration Hub v2 D13). */
export interface MappingRow {
  target: string;
  source: string;
  required?: boolean;
}

export interface Mapping {
  id: string;
  name: string;
  description?: string;
  /** Row shape the mapping carries (v3 D4): item | body | query | headers | pathParams; empty means item. */
  kind?: string;
  version: number;
  rows: MappingRow[];
  tags?: string[];
  owner?: string;
  /** private | shared (Phase 1 D-P0c). */
  visibility?: string;
  creatorId?: string;
  createdAt: string;
  updatedAt: string;
  usageCount?: number;
}

export interface MappingVersion {
  mappingId: string;
  version: number;
  contentHash: string;
  actor: string;
  createdAt: string;
}

export interface MappingUsage {
  mappingId: string;
  mappingVersion: number;
  workflowId: string;
  workflowVersion: number;
  workflowName?: string;
  createdAt: string;
}

export interface BundleProfileUse {
  id: string;
  name: string;
  nodeType: string;
  version: number;
}

export interface SchemaTestResult {
  ok: boolean;
  violations: SchemaViolation[];
}

/** Author-facing field on a connector operation (Campaign 4). */
export interface ConnectorField {
  type: 'string' | 'number' | 'boolean' | 'array' | 'object' | 'any';
  required?: boolean;
  description?: string;
  default?: unknown;
}

/** One operation of a connector: a request template over http.request's
 *  trusted pipeline. Placeholders: {{config.<field>}}, {{cred.<field>}},
 *  item scope left for execution. */
export interface ConnectorOperation {
  op: string;
  label?: string;
  description?: string;
  method: string;
  url: string;
  query?: Record<string, string>;
  headers?: Record<string, string>;
  body?: string;
  successCodes?: string;
  responseMode?: string;
  fields?: Record<string, ConnectorField>;
}

/** A connector asset (Campaign 4): versioned, its operations become
 * connector.<id>.<op> node types. */
export interface ConnectorVersion {
  connectorId: string;
  version: number;
  contentHash: string;
  actor: string;
  createdAt: string;
}

export interface ConnectorUsage {
  connectorId: string;
  connectorVersion: number;
  workflowId: string;
  workflowVersion: number;
  workflowName?: string;
  createdAt: string;
}
export interface Connector {
  id: string;
  name: string;
  description?: string;
  operations: ConnectorOperation[];
  tags?: string[];
  owner?: string;
  visibility?: string;
  creatorId?: string;
  version: number;
  usageCount?: number;
}

export interface Script {  id: string;
  name: string;
  description?: string;
  /** javascript | python. Immutable after create (the language burns no version). */
  language: string;
  version: number;
  source: string;
  /** Pinned entries like "idna==3.10" (python only). */
  requirements?: string[];
  /** Runtime defaults applied when the node sets none of its own (D4-4). */
  timeoutSeconds?: number;
  maxMemoryMB?: number;
  tags?: string[];
  owner?: string;
  /** private | shared. */
  visibility?: string;
  creatorId?: string;
  createdAt: string;
  updatedAt: string;
  usageCount?: number;
}

export interface ScriptVersion {
  scriptId: string;
  version: number;
  contentHash: string;
  actor: string;
  createdAt: string;
}

export interface ScriptUsage {
  scriptId: string;
  scriptVersion: number;
  workflowId: string;
  workflowVersion: number;
  workflowName?: string;
  createdAt: string;
}

/** POST /api/scripts/{id}/test response: js returns result+logs; python
 * returns exitCode/stdout/stderr (503 on sim hosts for python). */
export interface ScriptTestResult {
  ok: boolean;
  result?: unknown;
  logs?: string[];
  exitCode?: number;
  stdout?: string;
  stderr?: string;
  error?: string;
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
  /** Workflow-level contracts: the shape subflow callers see going in and get back. */
  inputSchemaRef?: string;
  outputSchemaRef?: string;
  inputSchemaFields?: SchemaField[];
  outputSchemaFields?: SchemaField[];
  /** The workflow's own outbound-destination allowlist (operational-trust
   *  campaign): undefined/empty = no workflow-level restriction; the
   *  server's global egress policy still applies. */
  egress?: { allowlist: string[] };
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
  /** When the approval link stops working (set by the engine). */
  resumeTokenExpiresAt?: string;
  /** When the approval started waiting, for age/SLA display. */
  resumeRequestedAt?: string;
  parentRunId?: string;
  parentNodeId?: string;
  actor?: string;
}

export interface Decision {
  id: number;
  runId: string;
  workflowId?: string;
  nodeId?: string;
  decision: 'approved' | 'rejected' | string;
  note?: string;
  actorId?: string;
  actorLabel?: string;
  source?: string;
  requestedAt?: string;
  decidedAt: string;
  waitedMs: number;
}

export interface DecisionStats {
  total: number;
  approved: number;
  rejected: number;
  waitingNow: number;
  waitedP50Ms: number;
  waitedP95Ms: number;
  oldestWaitMs: number;
  windowHours: number;
}

export interface RunEvent {
  seq: number;
  type: string;
  at: string;
  actor?: string;
  nodeId?: string;
  attempt?: number;
  inputHash?: string;
  outputHash?: string;
  durationMs?: number;
  error?: string;
  data?: Record<string, unknown>;
}

export interface EffectDiff {
  kind: string;
  index: number;
  recordedHash?: string;
  actualHash?: string;
}

export interface EffectMiss {
  kind: string;
  index: number;
}

export interface ReplayResult {
  runId: string;
  mode: 'full' | 'step' | 'step-execute' | 'from' | 'step-hermetic' | 'from-hermetic';
  nodeId?: string;
  match: boolean;
  recordedHash?: string;
  actualHash?: string;
  status: string;
  outputs?: Record<string, unknown>;
  effects?: number;
  diffs?: EffectDiff[];
  misses?: EffectMiss[];
  config?: Record<string, unknown>;
  credentials?: Record<string, string>;
  events?: RunEvent[];
}

export interface NodeEffectsData {
  blob?: string;
  count: number;
  itemIndex?: number;
}

export interface NodeEffectsDriftData {
  diffs: EffectDiff[];
  misses: EffectMiss[];
  itemIndex?: number;
}

export type VerifyNodeStatus = 'match' | 'drift' | 'added' | 'removed' | 'blocked';

export interface VerifyNode {
  nodeId: string;
  name?: string;
  status: VerifyNodeStatus;
  recordedHash?: string;
  actualHash?: string;
  diffs?: EffectDiff[];
  misses?: EffectMiss[];
}

export interface VerifyResult {
  sourceRunId: string;
  runId: string;
  workflowId: string;
  status: string;
  passed: boolean;
  error?: string;
  nodes: VerifyNode[];
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
  /** Config key, credential slot or ref the issue points at, when known.
   * The inspector uses it for inline field errors and issue fix actions. */
  field?: string;
  message: string;
}

/** One key-level change an upgrade applies (UX Phase 2 D2-6). */
export interface ProfileUpgradeChange {
  nodeId: string;
  nodeName: string;
  profileId: string;
  profileName: string;
  pinnedVersion: number;
  latestVersion: number;
  key: string;
  before: string;
  after: string;
}

/** POST /api/workflows/{id}/upgrade-preview response. */
export interface WorkflowUpgradePreview {
  applied: boolean;
  plan: ProfileUpgradeChange[];
  issues?: ValidationIssue[];
  workflow?: Workflow;
  version?: WorkflowVersion;
}

export interface SubprocessNodes {
  enabled: boolean;
  policy?: string;
  capMB?: number;
  osLevel?: string;
  containerRecommended?: string;
  /** Isolation-runner campaign: "allow" (scripts may call out) or
   *  "deny" (the OS cuts python's network). */
  network?: string;
}

/** Admin-only /api/capabilities posture. */
export interface Capabilities {
  prod: boolean;
  anonymous: boolean;
  serviceToken: boolean;
  subprocess: SubprocessNodes;
  egress: { mode: string; allowlist?: number };
  warnings: string[];
}

/** One row from the admin-only auth/audit event log. */
export interface AuthEvent {
  id: number;
  at: string;
  type: string;
  userId?: string;
  actor?: string;
  ip?: string;
  detail?: string;
}

/** One built Python dependency environment (Phase 5): hash, requirements, size. */
export interface PythonEnv {
  key: string;
  requirements: string[];
  uvVersion?: string;
  sizeMB: number;
}

/** The recorded execution of a script from a finished run's effect tape (Phase 5). */
export interface RecordedScriptTest {
  found: boolean;
  message?: string;
  runId?: string;
  workflowName?: string;
  workflowVersion?: number;
  scriptVersion?: number;
  nodeId?: string;
  nodeName?: string;
  exitCode?: number;
  stdout?: string;
  stderr?: string;
  result?: unknown;
  recordedAt?: string;
}
