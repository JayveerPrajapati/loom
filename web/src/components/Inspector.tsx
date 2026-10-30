import { useEffect, useState } from 'react';
import { api } from '../api';
import { defaultNodeName, outputType, PORT_COLORS } from '../catalog';
import type {
  CredentialInfo,
  DataContext,
  BatchPolicy,
  FieldDef,
  NodeDef,
  RetryPolicy,
  RunEvent,
  ValidationIssue,
  Workflow,
  WorkflowNode,
} from '../types';

function labelFor(key: string): string {
  return key
    .replace(/([a-z0-9])([A-Z])/g, '$1 $2')
    .replace(/^./, (char) => char.toUpperCase());
}

function collectFieldPaths(value: unknown, prefix: string, out: string[], depth = 0): void {
  if (depth > 3) return;
  if (value === null || value === undefined) {
    if (prefix) out.push(prefix);
    return;
  }
  if (Array.isArray(value)) {
    if (prefix) out.push(prefix);
    if (value.length > 0) collectFieldPaths(value[0], `${prefix}[0]`, out, depth + 1);
    return;
  }
  if (typeof value === 'object') {
    const entries = Object.entries(value as Record<string, unknown>);
    if (entries.length === 0 && prefix) out.push(prefix);
    for (const [key, inner] of entries) {
      const path = prefix ? `${prefix}.${key}` : key;
      collectFieldPaths(inner, path, out, depth + 1);
    }
    return;
  }
  if (prefix) out.push(prefix);
}

function expressionFor(nodeId: string, port: string, path: string): string {
  const base = port === 'main' ? `$node["${nodeId}"].json` : `$node["${nodeId}"].ports["${port}"]`;
  const suffix = path ? (path.startsWith('[') ? path : `.${path}`) : '';
  return `{{ ${base}${suffix} }}`;
}

function issueTouchesNode(issue: ValidationIssue, nodeId: string): boolean {
  if (issue.node === nodeId) return true;
  if (issue.edge) return issue.edge.from.node === nodeId || issue.edge.to.node === nodeId;
  return false;
}

function IssueList({
  issues,
  onSelectNode,
}: {
  issues: ValidationIssue[];
  onSelectNode: (id: string | null) => void;
}) {
  if (issues.length === 0) {
    return <p className="inspector-ok">No issues here.</p>;
  }
  return (
    <div className="issue-list">
      {issues.map((issue, index) => (
        <div className={`issue issue-${issue.severity}`} key={`${issue.code}-${issue.node ?? ''}-${index}`}>
          <span className="issue-badge">{issue.severity === 'error' ? 'Error' : 'Warning'}</span>
          <p>{issue.message}</p>
          {issue.node && (
            <button className="issue-link" onClick={() => onSelectNode(issue.node ?? null)}>
              Go to node
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

function JsonField({
  value,
  type,
  onChange,
  onFocus,
}: {
  value: unknown;
  type: 'object' | 'array';
  onChange: (value: unknown) => void;
  onFocus?: () => void;
}) {
  const [text, setText] = useState(() => JSON.stringify(value ?? (type === 'array' ? [] : {}), null, 2));
  const [error, setError] = useState('');

  useEffect(() => {
    setText(JSON.stringify(value ?? (type === 'array' ? [] : {}), null, 2));
    setError('');
  }, [value, type]);

  const commit = () => {
    try {
      const parsed: unknown = JSON.parse(text);
      if (type === 'array' && !Array.isArray(parsed)) throw new Error('array');
      if (type === 'object' && (parsed === null || Array.isArray(parsed) || typeof parsed !== 'object')) {
        throw new Error('object');
      }
      setError('');
      onChange(parsed);
    } catch {
      setError(`Expected a valid JSON ${type}.`);
    }
  };

  return (
    <div className="json-field">
      <textarea
        className={`text-input code ${error ? 'invalid' : ''}`}
        value={text}
        spellCheck={false}
        rows={type === 'object' ? 5 : 4}
        onFocus={onFocus}
        onChange={(event) => setText(event.target.value)}
        onBlur={commit}
      />
      {error && <p className="field-error">{error}</p>}
      <p className="field-hint">Edited as JSON. Changes apply when the field loses focus.</p>
    </div>
  );
}

function ConfigField({
  fieldKey,
  spec,
  value,
  onChange,
  onFocus,
}: {
  fieldKey: string;
  spec: FieldDef;
  value: unknown;
  onChange: (value: unknown) => void;
  onFocus?: () => void;
}) {
  const label = labelFor(fieldKey);
  const id = `field-${fieldKey}`;

  if (spec.enum) {
    return (
      <label className="field" htmlFor={id}>
        <span className="field-label">
          {label}
          {spec.required && <span className="required">*</span>}
        </span>
        <select
          id={id}
          className="text-input"
          value={typeof value === 'string' ? value : ''}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value)}
        >
          {!value && <option value="">Choose…</option>}
          {spec.enum.map((option) => (
            <option key={option} value={option}>
              {option}
            </option>
          ))}
        </select>
      </label>
    );
  }

  if (spec.type === 'boolean') {
    return (
      <label className="field field-check" htmlFor={id}>
        <input
          id={id}
          type="checkbox"
          checked={Boolean(value)}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.checked)}
        />
        <span>{label}</span>
      </label>
    );
  }

  if (spec.type === 'number') {
    return (
      <label className="field" htmlFor={id}>
        <span className="field-label">
          {label}
          {spec.required && <span className="required">*</span>}
        </span>
        <input
          id={id}
          className="text-input"
          type="number"
          value={typeof value === 'number' ? value : ''}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value === '' ? 0 : Number(event.target.value))}
        />
      </label>
    );
  }

  if (spec.type === 'object' || spec.type === 'array') {
    return (
      <div className="field">
        <span className="field-label">
          {label}
          {spec.required && <span className="required">*</span>}
        </span>
        <JsonField value={value} type={spec.type} onChange={onChange} onFocus={onFocus} />
      </div>
    );
  }

  const isCode = fieldKey === 'source';
  return (
    <label className="field" htmlFor={id}>
      <span className="field-label">
        {label}
        {spec.required && <span className="required">*</span>}
      </span>
      {isCode ? (
        <textarea
          id={id}
          className="text-input code"
          value={typeof value === 'string' ? value : ''}
          spellCheck={false}
          rows={7}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : (
        <input
          id={id}
          className="text-input"
          type="text"
          value={typeof value === 'string' ? value : ''}
          onFocus={onFocus}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
    </label>
  );
}

interface Props {
  workflow: Workflow;
  node: WorkflowNode | null;
  def?: NodeDef;
  issues: ValidationIssue[];
  runEvents: RunEvent[];
  runId?: string | null;
  onWorkflowName: (name: string) => void;
  onNodePatch: (patch: Partial<WorkflowNode>) => void;
  onConfigChange: (key: string, value: unknown) => void;
  onCredentialChange: (key: string, value: string) => void;
  onDeleteNode: () => void;
  onSelectNode: (id: string | null) => void;
  onOpenRunbook: () => void;
}

function WebhookTester({ path, method }: { path: string; method: string }) {
  const [requestMethod, setRequestMethod] = useState<'GET' | 'POST'>(
    method === 'GET' ? 'GET' : 'POST',
  );
  const [body, setBody] = useState('{\n  "hello": "world"\n}');
  const [asyncMode, setAsyncMode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [result, setResult] = useState('');

  const send = async () => {
    if (!path.trim()) {
      setError('Set a webhook path first.');
      return;
    }
    if (requestMethod === 'POST') {
      try {
        JSON.parse(body);
      } catch {
        setError('Body must be valid JSON.');
        return;
      }
    }
    setBusy(true);
    setError('');
    setResult('');
    try {
      const response = await fetch(
        `/hook/${path
          .split('/')
          .filter(Boolean)
          .map(encodeURIComponent)
          .join('/')}${asyncMode ? '?async=1' : ''}`,
        {
          method: requestMethod,
          headers: requestMethod === 'POST' ? { 'content-type': 'application/json' } : undefined,
          body: requestMethod === 'POST' ? body : undefined,
        },
      );
      const text = await response.text();
      let formatted = text;
      try {
        formatted = JSON.stringify(JSON.parse(text), null, 2);
      } catch {
        // Keep the raw response.
      }
      setResult(`${response.status} ${response.statusText}\n${formatted}`);
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setBusy(false);
    }
  };

  return (
    <section className="inspector-section">
      <h3>Test webhook</h3>
      <p className="field-hint">
        Sends a real request to <code>/hook/{path || '…'}</code>. Use <code>?async=1</code>{' '}
        for an immediate 202.
      </p>
      <div className="webhook-test-row">
        <label className="field" htmlFor="webhook-method">
          <span className="field-label">Method</span>
          <select
            id="webhook-method"
            className="text-input"
            value={requestMethod}
            onChange={(event) => setRequestMethod(event.target.value as 'GET' | 'POST')}
          >
            <option value="POST">POST</option>
            <option value="GET">GET</option>
          </select>
        </label>
        <button className="button primary" onClick={() => void send()} disabled={busy}>
          {busy ? 'Sending…' : 'Send test'}
        </button>
      </div>
      <label className="checkbox-field" htmlFor="webhook-async">
        <input
          id="webhook-async"
          type="checkbox"
          checked={asyncMode}
          onChange={(event) => setAsyncMode(event.target.checked)}
        />
        <span>Return 202 immediately</span>
      </label>
      {requestMethod === 'POST' && (
        <label className="field" htmlFor="webhook-body">
          <span className="field-label">JSON body</span>
          <textarea
            id="webhook-body"
            className="text-input code-input"
            rows={6}
            value={body}
            onChange={(event) => setBody(event.target.value)}
            spellCheck={false}
          />
        </label>
      )}
      {error && <p className="field-error">{error}</p>}
      {result && <pre className="json-view webhook-result">{result}</pre>}
    </section>
  );
}

export function Inspector({
  workflow,
  node,
  def,
  issues,
  runEvents,
  runId,
  onWorkflowName,
  onNodePatch,
  onConfigChange,
  onCredentialChange,
  onDeleteNode,
  onSelectNode,
  onOpenRunbook,
}: Props) {
  const [dataContext, setDataContext] = useState<DataContext | null>(null);
  const [contextRunId, setContextRunId] = useState<string | null>(null);
  const [pickedNode, setPickedNode] = useState('');
  const [activeField, setActiveField] = useState('');
  const [previewValue, setPreviewValue] = useState('');
  const [previewError, setPreviewError] = useState('');
  const [debugExpr, setDebugExpr] = useState('');
  const [debugResult, setDebugResult] = useState<string | null>(null);
  const [debugError, setDebugError] = useState('');
  const [debugBusy, setDebugBusy] = useState(false);
  const [credentials, setCredentials] = useState<CredentialInfo[]>([]);
  const activeValue = activeField ? node?.config?.[activeField] : undefined;

  useEffect(() => {
    setActiveField('');
    setPreviewValue('');
    setPreviewError('');
  }, [node?.id]);

  useEffect(() => {
    let cancelled = false;
    api
      .listCredentials()
      .then((result) => {
        if (!cancelled) setCredentials(result.credentials ?? []);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    if (!node) {
      setDataContext(null);
      setContextRunId(null);
      return;
    }
    const nodeId = node.id;
    void (async () => {
      let id = runId ?? null;
      if (!id) {
        try {
          const result = await api.listRuns(workflow.id, 1);
          id = result.runs?.[0]?.id ?? null;
        } catch {
          id = null;
        }
      }
      if (!id) {
        if (!cancelled) {
          setDataContext(null);
          setContextRunId(null);
        }
        return;
      }
      try {
        const context = await api.dataContext(id, nodeId);
        if (!cancelled) {
          setDataContext(context);
          setContextRunId(id);
        }
      } catch {
        if (!cancelled) {
          setDataContext(null);
          setContextRunId(null);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [node?.id, runId, runEvents.length, workflow.id]);

  const evaluate = async () => {
    if (!contextRunId || !debugExpr.trim()) return;
    setDebugBusy(true);
    setDebugError('');
    setDebugResult(null);
    try {
      const result = await api.previewExpression(workflow.id, {
        runId: contextRunId,
        nodeId: node?.id,
        expression: debugExpr,
      });
      setDebugResult(result.value);
    } catch (reason) {
      setDebugError(reason instanceof Error ? reason.message : String(reason));
    } finally {
      setDebugBusy(false);
    }
  };

  useEffect(() => {
    if (!dataContext) {
      setPickedNode('');
      return;
    }
    setPickedNode((current) =>
      dataContext.upstream.includes(current) ? current : (dataContext.upstream[0] ?? ''),
    );
  }, [dataContext]);

  useEffect(() => {
    setPreviewValue('');
    setPreviewError('');
    if (!contextRunId || !activeField || typeof activeValue !== 'string' || !activeValue.includes('{{')) return;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      api
        .previewExpression(workflow.id, {
          runId: contextRunId,
          nodeId: node?.id,
          expression: activeValue,
        })
        .then((result) => {
          if (!cancelled) setPreviewValue(result.value);
        })
        .catch((error: unknown) => {
          if (!cancelled) setPreviewError(error instanceof Error ? error.message : String(error));
        });
    }, 350);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [activeField, activeValue, contextRunId, node?.id, workflow.id]);

  if (!node) {
    const errors = issues.filter((issue) => issue.severity === 'error').length;
    const trigger = workflow.nodes.find((item) => item.type.startsWith('trigger.'));
    return (
      <aside className="panel inspector" aria-label="Workflow inspector">
        <div className="panel-head">
          <div>
            <p className="eyebrow">Inspector</p>
            <h2>Workflow</h2>
          </div>
          <span className={`count-pill ${errors ? 'bad' : 'good'}`}>{errors ? `${errors} errors` : 'Valid'}</span>
        </div>
        <div className="inspector-scroll">
          <label className="field" htmlFor="workflow-name">
            <span className="field-label">Name</span>
            <input
              id="workflow-name"
              className="text-input"
              value={workflow.name}
              onChange={(event) => onWorkflowName(event.target.value)}
            />
          </label>
          <div className="meta-grid">
            <div>
              <span>Id</span>
              <code>{workflow.id}</code>
            </div>
            <div>
              <span>Version</span>
              <code>{workflow.version}</code>
            </div>
            <div>
              <span>Nodes</span>
              <code>{workflow.nodes.length}</code>
            </div>
            <div>
              <span>Edges</span>
              <code>{workflow.edges.length}</code>
            </div>
          </div>
          <button className="button subtle runbook-button" onClick={onOpenRunbook}>
            Download runbook
          </button>
          {trigger && (
            <div className="callout">
              <p className="eyebrow">Trigger</p>
              <strong>{trigger.name || trigger.type}</strong>
              <span>
                {String(trigger.config.path ?? trigger.config.schedule ?? trigger.type.replace('trigger.', ''))}
              </span>
            </div>
          )}
          <section className="inspector-section">
            <h3>Validation</h3>
            <IssueList issues={issues} onSelectNode={onSelectNode} />
          </section>
          <section className="inspector-section">
            <h3>Shortcuts</h3>
            <dl className="shortcut-list">
              <div>
                <dt>Run</dt>
                <dd>⌘ ⏎</dd>
              </div>
              <div>
                <dt>Save</dt>
                <dd>⌘ S</dd>
              </div>
              <div>
                <dt>Undo</dt>
                <dd>⌘ Z</dd>
              </div>
              <div>
                <dt>Command palette</dt>
                <dd>⌘ K</dd>
              </div>
            </dl>
          </section>
        </div>
      </aside>
    );
  }

  const config = node.config ?? {};
  const nodeIssues = issues.filter((issue) => issueTouchesNode(issue, node.id));
  const outType = outputType(def, config);
  const fields = Object.entries(def?.config ?? {});
  const bodyType =
    node.type === 'http.request'
      ? String(
          config.bodyType ??
            (config.rawBody !== undefined ? 'raw' : config.body !== undefined ? 'json' : 'none'),
        )
      : '';
  const visibleFields = fields.filter(([key]) => {
    if (node.type !== 'http.request') return true;
    if (key === 'body') return bodyType === 'json' || bodyType === 'form';
    if (key === 'rawBody' || key === 'contentType') return bodyType === 'raw';
    return true;
  });
  const upstream = dataContext
    ? dataContext.upstream
        .map((id) => dataContext.nodes.find((item) => item.nodeId === id))
        .filter((item): item is NonNullable<typeof item> => Boolean(item))
    : [];
  const picked = upstream.find((item) => item.nodeId === pickedNode) ?? upstream[0];
  const activeSpec = activeField ? def?.config?.[activeField] : undefined;
  const canInsert = activeSpec?.type === 'string';

  const insertExpression = (expression: string) => {
    if (!activeField || !canInsert) return;
    const current = config[activeField];
    onConfigChange(activeField, typeof current === 'string' && current.trim() ? `${current} ${expression}` : expression);
  };
  const retry = node.retry ?? {};
  const updateRetry = (patch: Partial<RetryPolicy>) => {
    const next = { ...retry, ...patch };
    const hasValue = Object.values(next).some((value) => typeof value === 'number' && value > 0);
    onNodePatch({ retry: hasValue ? next : undefined });
  };
  const batch = node.batch ?? {};
  const updateBatch = (patch: Partial<BatchPolicy>) => {
    const next = { ...batch, ...patch };
    const hasValue = (next.itemsPath ?? '').trim() !== '' || next.continueOnItemError === true;
    onNodePatch({ batch: hasValue ? next : undefined });
  };

  return (
    <aside className="panel inspector" aria-label="Node inspector">
      <div className="panel-head">
        <div>
          <p className="eyebrow">{def?.category ?? 'Node'}</p>
          <h2>{node.name || defaultNodeName(def ?? ({ type: node.type } as NodeDef))}</h2>
        </div>
        <button className="icon-button danger" onClick={onDeleteNode} aria-label="Delete node" title="Delete node">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
            <path d="M4 7h16M9 7V5h6v2M7 7l1 13h8l1-13" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
      </div>
      <div className="inspector-scroll">
        <label className="field" htmlFor="node-name">
          <span className="field-label">Name</span>
          <input
            id="node-name"
            className="text-input"
            value={node.name}
            onChange={(event) => onNodePatch({ name: event.target.value })}
          />
        </label>
        <div className="node-identity">
          <span className={`node-dot dot-${def?.category ?? 'action'}`} aria-hidden="true" />
          <code>{node.type}</code>
          <span className="muted">v{node.typeVersion}</span>
          <code className="node-id">{node.id}</code>
        </div>
        {def?.description && <p className="node-description">{def.description}</p>}

        <section className="inspector-section">
          <div className="section-head-with-hint">
            <h3>Configuration</h3>
            {activeField && (
              <span className="field-hint-active">Editing: <code>{activeField}</code></span>
            )}
          </div>

          {activeField && (
            <div className="expression-quick-toolbar" aria-label="Dynamic expression helpers">
              <span className="quick-label">⚡ Insert data:</span>
              <button
                type="button"
                className="expression-pill"
                title="Insert {{ $json }} (incoming JSON item)"
                onClick={() => insertExpression('{{ $json }}')}
              >
                $json
              </button>
              <button
                type="button"
                className="expression-pill"
                title="Insert {{ $json.id }}"
                onClick={() => insertExpression('{{ $json.id }}')}
              >
                $json.id
              </button>
              <button
                type="button"
                className="expression-pill"
                title="Insert {{ $json.body }}"
                onClick={() => insertExpression('{{ $json.body }}')}
              >
                $json.body
              </button>
              <button
                type="button"
                className="expression-pill"
                title="Insert current ISO timestamp"
                onClick={() => insertExpression('{{ $now }}')}
              >
                $now
              </button>
              <button
                type="button"
                className="expression-pill"
                title="Insert environment variable, e.g. {{ $env.API_KEY }}"
                onClick={() => insertExpression('{{ $env. }}')}
              >
                $env
              </button>
            </div>
          )}

          {fields.length === 0 && <p className="muted small">This node has no configuration.</p>}
          {visibleFields.map(([key, spec]) => (
            <ConfigField
              key={key}
              fieldKey={key}
              spec={spec}
              value={config[key]}
              onChange={(value) => onConfigChange(key, value)}
              onFocus={() => setActiveField(key)}
            />
          ))}
        </section>

        {node.type === 'trigger.webhook' && (
          <WebhookTester path={String(config.path ?? '')} method={String(config.method ?? 'POST')} />
        )}

        <section className="inspector-section">
          <h3>Retry</h3>
          <div className="retry-fields">
            <label className="field" htmlFor="retry-attempts">
              <span className="field-label">Max attempts</span>
              <input
                id="retry-attempts"
                className="text-input"
                type="number"
                min={0}
                max={20}
                value={retry.maxAttempts ?? ''}
                onChange={(event) =>
                  updateRetry({ maxAttempts: Math.max(0, Number(event.target.value) || 0) })
                }
              />
            </label>
            <label className="field" htmlFor="retry-backoff">
              <span className="field-label">Backoff (ms)</span>
              <input
                id="retry-backoff"
                className="text-input"
                type="number"
                min={0}
                max={600000}
                value={retry.backoffMs ?? ''}
                onChange={(event) =>
                  updateRetry({ backoffMs: Math.max(0, Number(event.target.value) || 0) })
                }
              />
            </label>
            <label className="field" htmlFor="retry-max-delay">
              <span className="field-label">Max delay (ms)</span>
              <input
                id="retry-max-delay"
                className="text-input"
                type="number"
                min={0}
                max={3600000}
                value={retry.maxDelayMs ?? ''}
                onChange={(event) =>
                  updateRetry({ maxDelayMs: Math.max(0, Number(event.target.value) || 0) })
                }
              />
            </label>
          </div>
          <p className="field-hint">
            Leave at 0 to use the engine default (3 attempts, 100 ms quadratic
            backoff). A policy uses doubling backoff capped at the max delay.
          </p>
        </section>

        <section className="inspector-section">
          <h3>Failure handling</h3>
          <label className="field" htmlFor="on-error">
            <span className="field-label">When this node fails</span>
            <select
              id="on-error"
              className="text-input"
              value={node.onError ?? 'stop'}
              onChange={(event) =>
                onNodePatch({
                  onError:
                    event.target.value === 'stop'
                      ? undefined
                      : (event.target.value as 'continue' | 'branch'),
                })
              }
            >
              <option value="stop">Stop the run</option>
              <option value="continue">Continue with an error item</option>
              <option value="branch">Route to the error output</option>
            </select>
          </label>
          <p className="field-hint">
            Continue passes <code>{'{ error, nodeId, attempts }'}</code> on the main
            output. Branch sends it to the <code>error</code> output instead and
            skips main-output nodes.
          </p>
        </section>

        <section className="inspector-section">
          <h3>Batch items</h3>
          <label className="field" htmlFor="batch-path">
            <span className="field-label">Array path</span>
            <input
              id="batch-path"
              className="text-input"
              placeholder="items"
              value={batch.itemsPath ?? ''}
              onChange={(event) => updateBatch({ itemsPath: event.target.value })}
            />
          </label>
          <label className="checkbox-field" htmlFor="batch-continue">
            <input
              id="batch-continue"
              type="checkbox"
              checked={batch.continueOnItemError === true}
              onChange={(event) => updateBatch({ continueOnItemError: event.target.checked })}
            />
            <span>Continue after item failures</span>
          </label>
          <p className="field-hint">
            Runs this node once per object in the array (default <code>items</code>).
            The output is <code>{'{ items, count, failed }'}</code>; each failure
            appends an <code>item.failed</code> event.
          </p>
        </section>

        <section className="inspector-section">
          <h3>Data from upstream</h3>
          {!dataContext && (
            <p className="field-hint">Run this workflow once to load fields from upstream nodes.</p>
          )}
          {dataContext && upstream.length === 0 && (
            <p className="field-hint">This node has no upstream nodes yet.</p>
          )}
          {dataContext && upstream.length > 0 && (
            <>
              <label className="field" htmlFor="data-source">
                <span className="field-label">Source node</span>
                <select
                  id="data-source"
                  className="text-input"
                  value={picked?.nodeId ?? ''}
                  onChange={(event) => setPickedNode(event.target.value)}
                >
                  {upstream.map((item) => (
                    <option key={item.nodeId} value={item.nodeId}>
                      {item.name} ({item.nodeId}) · {item.status}
                    </option>
                  ))}
                </select>
              </label>
              {!picked?.outputs && <p className="field-hint">No output recorded for this node.</p>}
              {picked && picked.outputs && (
                <div className="data-fields">
                  {Object.entries(picked.outputs).map(([port, item]) => {
                    const paths: string[] = [];
                    collectFieldPaths(item, '', paths);
                    return (
                      <div className="data-port" key={port}>
                        <span className="field-label">{port}</span>
                        {paths.length === 0 && <span className="muted small">empty</span>}
                        {paths.map((path) => {
                          const expression = expressionFor(picked.nodeId, port, path);
                          return (
                            <button
                              type="button"
                              className="data-field"
                              key={path}
                              disabled={!canInsert}
                              title={canInsert ? `Insert ${expression}` : 'Focus a text field first'}
                              onClick={() => insertExpression(expression)}
                            >
                              {path}
                            </button>
                          );
                        })}
                      </div>
                    );
                  })}
                </div>
              )}
              <p className="field-hint">
                {canInsert
                  ? `Click a field to insert it into “${labelFor(activeField)}”.`
                  : 'Focus a text field, then click a field to insert it.'}
              </p>
              {previewValue && (
                <div className="data-preview">
                  <span className="field-label">Preview</span>
                  <code>{previewValue}</code>
                </div>
              )}
              {previewError && <p className="field-error">{previewError}</p>}
              {contextRunId && (
                <div className="debug-eval">
                  <label className="field" htmlFor="debug-expression">
                    <span className="field-label">Evaluate expression</span>
                    <input
                      id="debug-expression"
                      className="text-input code-input"
                      value={debugExpr}
                      onChange={(event) => setDebugExpr(event.target.value)}
                      placeholder='{{ $json.email }}'
                      spellCheck={false}
                      onKeyDown={(event) => {
                        if (event.key === 'Enter') void evaluate();
                      }}
                    />
                  </label>
                  <button
                    type="button"
                    className="button subtle"
                    onClick={() => void evaluate()}
                    disabled={debugBusy || !debugExpr.trim()}
                  >
                    {debugBusy ? 'Evaluating…' : 'Evaluate'}
                  </button>
                  {debugError && <p className="field-error">{debugError}</p>}
                  {debugResult !== null && (
                    <div className="data-preview">
                      <span className="field-label">Result</span>
                      <code>{debugResult}</code>
                    </div>
                  )}
                </div>
              )}
            </>
          )}
        </section>

        {(def?.credentials?.length ?? 0) > 0 && (
          <section className="inspector-section">
            <h3>Credentials</h3>
            <p className="field-hint">
              Reference a credential stored by the server. Optional credentials may be left blank.
            </p>
            {def?.credentials?.map((credential) => {
              const optional = (def.optionalCredentials ?? []).includes(credential);
              return (
                <label className="field" key={credential} htmlFor={`cred-${credential}`}>
                  <span className="field-label">
                    {labelFor(credential)}
                    {optional && <span className="muted small"> (optional)</span>}
                  </span>
                  <input
                    id={`cred-${credential}`}
                    className="text-input"
                    list={`cred-list-${credential}`}
                    value={node.credentials?.[credential] ?? ''}
                    placeholder={optional ? 'None' : credential}
                    onChange={(event) => onCredentialChange(credential, event.target.value)}
                  />
                  <datalist id={`cred-list-${credential}`}>
                    {credentials
                      .filter((item) => item.type === credential)
                      .map((item) => (
                        <option key={item.id} value={item.id}>
                          {item.id}
                        </option>
                      ))}
                  </datalist>
                </label>
              );
            })}
          </section>
        )}

        <section className="inspector-section">
          <h3>Ports</h3>
          <div className="port-table">
            <div>
              <span className="field-label">Inputs</span>
              {(def?.inputs ?? []).length === 0 && <em className="muted small">none</em>}
              {(def?.inputs ?? []).map((port) => (
                <span className="port-chip" key={port}>
                  <i style={{ background: PORT_COLORS[def?.inputType ?? 'any'] }} />
                  {port}
                  <em>{def?.inputType}</em>
                </span>
              ))}
            </div>
            <div>
              <span className="field-label">Outputs</span>
              {(def?.outputs ?? []).map((port) => (
                <span className="port-chip" key={port}>
                  <i style={{ background: PORT_COLORS[outType] }} />
                  {port}
                  <em>{outType}</em>
                </span>
              ))}
            </div>
          </div>
        </section>

        <section className="inspector-section">
          <h3>Validation</h3>
          <IssueList issues={nodeIssues} onSelectNode={onSelectNode} />
        </section>
      </div>
    </aside>
  );
}
