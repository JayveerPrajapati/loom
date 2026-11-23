import { useEffect, useMemo, useState } from 'react';
import { defaultNodeName, outputType } from '../catalog';
import { formFieldGroups, hasProfileableFields, inspectorTabs, type InspectorTab } from '../formLayout';
import { nodeEffective } from '../effectiveNode';
import { collectNodeIssues, type InspectorIssue } from '../inspectorIssues';
import type { LoadFailure } from '../loadState';
import { AssetErrorState } from './assets/AssetState';
import { IssueCenter } from './IssueCenter';
import { ConfigField } from './InspectorFields';
import { bundleAssetLabels } from './inspectorHelpers';
import { InspectorCredentialsSection } from './InspectorCredentialsSection';
import { InspectorDataContextPanel } from './InspectorDataContext';
import { InspectorExecution } from './InspectorExecution';
import { InspectorPortsSection } from './InspectorPortsSection';
import { InspectorTabStrip } from './InspectorTabStrip';
import { NodeRawConfigEditor } from './NodeRawConfigEditor';
import { WebhookTester } from './InspectorTriggers';
import { formDataFor, formFieldsFor } from './nodeForms/registry';
import { FieldErrorsContext, ScriptRefPicker } from './nodeForms/shared';
import { ExpressionTester } from './ExpressionTester';
import { InspectorWorkflowPanel } from './InspectorWorkflowPanel';
import { InspectorProfileSection } from './InspectorProfileSection';
import { InspectorDataSection } from './InspectorDataSection';
import { useInspectorAssets } from './useInspectorAssets';
import type {
  NodeDef,
  PortType,
  ValidationIssue,
  Workflow,
  WorkflowNode,
  RunEvent,
} from '../types';

interface Props {
  workflow: Workflow;
  node: WorkflowNode | null;
  def?: NodeDef;
  issues: ValidationIssue[];
  runEvents: RunEvent[];
  runId?: string | null;
  subprocessEnabled?: boolean;
  onWorkflowName: (name: string) => void;
  onWorkflowSettings: (patch: Record<string, unknown>) => void;
  onNodePatch: (patch: Partial<WorkflowNode>) => void;
  onConfigChange: (key: string, value: unknown) => void;
  onCredentialChange: (key: string, value: string) => void;
  onDeleteNode: () => void;
  onSelectNode: (id: string | null) => void;
  onOpenRunbook: () => void;
  /** Deep-links into the Configuration Hub page for a node type (v3 D5). */
  onOpenHub?: (nodeType: string) => void;
  /** Notifies the app when save-as-mapping creates an asset, so the
   * draft validator's mappings cache refreshes (Phase 1). */
  onMappingsChanged?: () => void;
}

const RESOURCE_LABEL: Record<string, string> = {
  credentials: 'Credentials',
  profiles: 'Profiles',
  schemas: 'Contracts',
  mappings: 'Mappings',
  scripts: 'Scripts',
  callee: 'Callee input contract',
};

// The inspector shell (Apple-Grade Phase 3): a stable at-a-glance summary
// — identity, profile, effective behavior, overrides, bundled assets and
// validation — above four detail tabs (Configuration / Data / Runtime /
// Advanced). The profile picker is part of the summary, not a tab, so
// attaching or detaching a profile never hides the node's state. The
// bottom issue center aggregates every category that can block publish or
// a run.
export function Inspector({
  workflow,
  node,
  def,
  issues,
  runEvents,
  runId,
  subprocessEnabled,
  onWorkflowName,
  onWorkflowSettings,
  onNodePatch,
  onConfigChange,
  onCredentialChange,
  onDeleteNode,
  onSelectNode,
  onOpenRunbook,
  onOpenHub,
  onMappingsChanged,
}: Props) {
  const [activeField, setActiveField] = useState('');
  const [tab, setTab] = useState<InspectorTab>('configuration');
  const {
    credentials,
    profiles,
    schemas,
    mappings,
    scripts,
    calleeContract,
    candidates,
    failures,
    retryAssets,
    refreshMappings,
  } = useInspectorAssets(workflow, node, def, runEvents);

  useEffect(() => {
    setActiveField('');
    setTab('configuration');
  }, [node?.id]);

  // Effective values under the pinned profile (UI-audit B1): form
  // fields display the value publish would resolve, with its origin.
  // The same equality rule UpgradeWorkflowProfiles uses means published
  // sites — whose node configs already carry resolved values — still
  // badge as profile-origin.
  const pinnedProfile = useMemo(
    () => (node?.profileRef ? profiles.find((p) => p.id === node.profileRef) ?? null : null),
    [node?.profileRef, profiles],
  );
  const effective = useMemo(
    () => (node && pinnedProfile ? nodeEffective(node, pinnedProfile) : null),
    [node, pinnedProfile],
  );

  const config = node?.config ?? {};
  const unsetConfigKey = (key: string) => {
    if (!node) return;
    const next: Record<string, unknown> = { ...(node.config ?? {}) };
    delete next[key];
    const overrides = { ...(node.profileOverrides ?? {}) };
    delete overrides[key];
    onNodePatch({ config: next, profileOverrides: overrides });
  };

  if (!node) {
    return (
      <InspectorWorkflowPanel
        workflow={workflow}
        issues={issues}
        onWorkflowName={onWorkflowName}
        onWorkflowSettings={onWorkflowSettings}
        onOpenRunbook={onOpenRunbook}
        onSelectNode={onSelectNode}
      />
    );
  }

  const nodeIssues = collectNodeIssues({ issues, node, subprocessEnabled, runEvents });
  const blockerCount = nodeIssues.filter((issue) => issue.severity === 'error').length;
  const warningCount = nodeIssues.length - blockerCount;
  const fieldIssue = (key: string) =>
    nodeIssues.find((issue) => issue.field === key && issue.severity === 'error')?.message;
  const overrides = effective
    ? Object.keys(node.config ?? {}).filter(
        (key) => effective.field(key)?.overridden === true,
      )
    : Object.entries(node.profileOverrides ?? {})
        .filter(([, value]) => value === true)
        .map(([key]) => key);
  const bundleLabels = bundleAssetLabels(pinnedProfile, mappings, schemas, scripts);
  const inheritedCount = pinnedProfile
    ? Object.keys(pinnedProfile.config ?? {}).filter((key) => !overrides.includes(key)).length
    : 0;
  const assetFailureEntries = Object.entries(failures) as Array<[string, LoadFailure]>;
  const profileAware = hasProfileableFields(def) || !!node.profileRef;

  const outType: PortType = outputType(def, config);
  const groups = formFieldGroups(node.type, config, def);
  const Fields = formFieldsFor(node.type);
  const DataContent = formDataFor(node.type);
  const fieldCount = Object.keys(def?.config ?? {}).length;
  const activeSpec = activeField ? def?.config?.[activeField] : undefined;
  const canInsert = activeSpec?.type === 'string';
  const tabs = inspectorTabs(node.type);
  const activeTab = tabs.includes(tab) ? tab : 'configuration';

  const insertExpression = (expression: string) => {
    if (!activeField || !canInsert) return;
    const current = config[activeField];
    onConfigChange(activeField, typeof current === 'string' && current.trim() ? `${current} ${expression}` : expression);
  };

  const focusIssue = (issue: InspectorIssue) => {
    if (issue.category === 'profile' || issue.field === 'profileRef') {
      document.getElementById('node-profile-ref')?.focus();
      return;
    }
    if (issue.category === 'runtime' || issue.category === 'python') {
      setTab('runtime');
      return;
    }
    if (issue.field?.startsWith('credential:')) {
      setTab('configuration');
      requestAnimationFrame(() =>
        document.getElementById('credentials-section')?.scrollIntoView({ block: 'center' }),
      );
      return;
    }
    if (issue.field === 'inputSchemaRef' || issue.field === 'outputSchemaRef') {
      setTab('data');
      requestAnimationFrame(() => document.getElementById('node-schema-ref')?.focus());
      return;
    }
    if (issue.field?.startsWith('mappingRefs.')) {
      const group = issue.field.split('.')[1];
      setTab('data');
      requestAnimationFrame(() => document.getElementById(`mapping-ref-${group}`)?.focus());
      return;
    }
    if (issue.field) {
      setTab('configuration');
      setActiveField(issue.field);
      requestAnimationFrame(() => {
        const element = document.getElementById(`field-${issue.field}`);
        element?.focus();
        element?.scrollIntoView({ block: 'center' });
      });
      return;
    }
    setTab('configuration');
  };

  // Expression helpers follow the focused field: they render on every
  // tab that hosts config fields (Configuration and Advanced).
  const expressionPills = activeField && (
    <div className="expression-quick-toolbar" aria-label="Dynamic expression helpers" data-testid="expression-pills">
      <span className="quick-label">Insert data:</span>
      {candidates.map((candidate) => (
        <button
          type="button"
          className="expression-pill"
          key={candidate.path}
          data-source={candidate.source}
          title={`Insert {{ ${candidate.path} }} (${candidate.source})`}
          onClick={() => insertExpression(`{{ ${candidate.path} }}`)}
        >
          {candidate.path}
        </button>
      ))}
    </div>
  );

  return (
    <aside className="panel inspector" aria-label="Node inspector">
      <div className="panel-head">
        <div>
          <h2>{node.name || defaultNodeName(def ?? ({ type: node.type } as NodeDef))}</h2>
        </div>
        <button className="icon-button danger" onClick={onDeleteNode} aria-label="Delete node" title="Delete node">
          <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8">
            <path d="M4 7h16M9 7V5h6v2M7 7l1 13h8l1-13" strokeLinecap="round" strokeLinejoin="round" />
          </svg>
        </button>
      </div>
      <div className="inspector-scroll">
        <section
          className="inspector-summary"
          data-testid="inspector-summary"
          aria-label="Node summary"
        >
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
          {def?.effects && (
            <p className="node-effects" data-testid="node-effects">
              <span className="node-effects-label">Effects</span> {def.effects}
            </p>
          )}

          <dl className="inspector-facts">
            {profileAware && (
              <>
                <div className="inspector-fact" data-testid="summary-profile">
                  <dt>Profile</dt>
                  <dd>
                    {pinnedProfile
                      ? `${pinnedProfile.name} · v${pinnedProfile.version}`
                      : 'None — inline config'}
                  </dd>
                </div>
                <div className="inspector-fact" data-testid="summary-effective">
                  <dt>Effective behavior</dt>
                  <dd>
                    {pinnedProfile
                      ? `${inheritedCount} inherited field${inheritedCount === 1 ? '' : 's'}`
                      : 'Node config only'}
                  </dd>
                </div>
                <div className="inspector-fact" data-testid="summary-overrides">
                  <dt>Overrides</dt>
                  <dd>
                    {overrides.length === 0
                      ? 'None'
                      : `${overrides.length} · ${overrides.slice(0, 3).join(', ')}${overrides.length > 3 ? '…' : ''}`}
                  </dd>
                </div>
                <div className="inspector-fact" data-testid="summary-bundles">
                  <dt>Bundled assets</dt>
                  <dd>{bundleLabels.length === 0 ? 'None' : bundleLabels.join(' · ')}</dd>
                </div>
              </>
            )}
            <div className="inspector-fact" data-testid="summary-validation">
              <dt>Validation</dt>
              <dd>
                <span>
                  {nodeIssues.length === 0
                    ? 'Clean'
                    : `${blockerCount} blocker${blockerCount === 1 ? '' : 's'} · ${warningCount} warning${warningCount === 1 ? '' : 's'}`}
                </span>
                {nodeIssues.length > 0 && (
                  <button
                    type="button"
                    className="issue-link"
                    onClick={() => focusIssue(nodeIssues[0])}
                  >
                    Review
                  </button>
                )}
              </dd>
            </div>
          </dl>

          {profileAware && (
            <InspectorProfileSection node={node} profiles={profiles} onNodePatch={onNodePatch} />
          )}

          {assetFailureEntries.map(([resource, failure]) => (
            <div
              className="inspector-asset-failure"
              key={resource}
              data-testid={`inspector-asset-error-${resource}`}
            >
              <p className="field-hint">{RESOURCE_LABEL[resource] ?? resource}</p>
              <AssetErrorState failure={failure} onRetry={retryAssets} />
            </div>
          ))}
        </section>

        <InspectorTabStrip tabs={tabs} activeTab={activeTab} onSelect={setTab} />

        {activeTab === 'configuration' && (
          <div
            role="tabpanel"
            id="inspector-panel-configuration"
            aria-labelledby="inspector-tab-configuration"
            data-testid="inspector-panel-configuration"
          >
            <section className="inspector-section">
              <div className="section-head-with-hint">
                <h3>Configuration</h3>
                {activeField && (
                  <span className="field-hint-active">Editing: <code>{activeField}</code></span>
                )}
              </div>
              {expressionPills}
              <ExpressionTester />
              {fieldCount === 0 && <p className="muted small">This node has no configuration.</p>}
              <FieldErrorsContext.Provider value={fieldIssue}>
                <Fields
                  node={node}
                  def={def}
                  mappings={mappings}
                  scripts={scripts}
                  effective={effective}
                  onConfigUnset={unsetConfigKey}
                  scriptRefSlot={
                    node.type === 'code.run' || node.type === 'code.python' ? (
                      <ScriptRefPicker
                        node={node}
                        scripts={scripts}
                        onConfigChange={onConfigChange}
                        effective={effective}
                      />
                    ) : undefined
                  }
                  onConfigChange={onConfigChange}
                  onFocusField={setActiveField}
                />
              </FieldErrorsContext.Provider>
            </section>
            <InspectorCredentialsSection
              def={def}
              node={node}
              credentials={credentials}
              onCredentialChange={onCredentialChange}
              profile={pinnedProfile}
              issueFor={fieldIssue}
            />
          </div>
        )}

        {activeTab === 'data' && (
          <div
            role="tabpanel"
            id="inspector-panel-data"
            aria-labelledby="inspector-tab-data"
            data-testid="inspector-panel-data"
          >
            {DataContent && (
              <DataContent
                node={node}
                onConfigChange={onConfigChange}
                candidates={candidates}
                mappings={mappings}
                effective={effective}
                onMappingsChanged={() => {
                  refreshMappings();
                  onMappingsChanged?.();
                }}
                onOpenHub={onOpenHub ?? (() => undefined)}
              />
            )}
            <InspectorDataSection
              node={node}
              schemas={schemas}
              mappings={mappings}
              calleeContract={calleeContract}
              profile={pinnedProfile}
              effective={effective}
              onNodePatch={onNodePatch}
            />
          </div>
        )}

        {activeTab === 'runtime' && (
          <div
            role="tabpanel"
            id="inspector-panel-runtime"
            aria-labelledby="inspector-tab-runtime"
            data-testid="inspector-panel-runtime"
          >
            {node.type === 'trigger.webhook' && (
              <WebhookTester path={String(config.path ?? '')} method={String(config.method ?? 'POST')} />
            )}
            <InspectorExecution
              node={node}
              onNodePatch={onNodePatch}
              profileRetry={pinnedProfile?.retry ?? null}
            />
            <InspectorDataContextPanel
              workflowId={workflow.id}
              node={node}
              runId={runId}
              runEventsCount={runEvents.length}
              activeField={activeField}
              canInsert={canInsert}
              insertExpression={insertExpression}
            />
          </div>
        )}

        {activeTab === 'advanced' && (
          <div
            role="tabpanel"
            id="inspector-panel-advanced"
            aria-labelledby="inspector-tab-advanced"
            data-testid="inspector-panel-advanced"
          >
            <section className="inspector-section">
              <h3>Advanced</h3>
              {expressionPills}
              {groups.advanced.length === 0 && (
                <p className="muted small">This node has no advanced fields.</p>
              )}
              {groups.advanced.map(([key, spec]) => {
                const info = effective?.field(key) ?? null;
                return (
                  <ConfigField
                    key={key}
                    fieldKey={key}
                    spec={spec}
                    value={info ? info.value : config[key]}
                    onChange={(value) => onConfigChange(key, value)}
                    onFocus={() => setActiveField(key)}
                    origin={info ? info.origin : undefined}
                    overridden={info?.overridden === true}
                    profileValue={
                      info?.overridden && effective ? effective.inheritedValue(key) : undefined
                    }
                    issue={fieldIssue(key)}
                    onReset={
                      info?.overridden && effective?.canReset(key) === true
                        ? () => unsetConfigKey(key)
                        : undefined
                    }
                  />
                );
              })}
            </section>
            <NodeRawConfigEditor
              config={node.config}
              onReplace={(next) => {
                const previousOverrides = node.profileOverrides ?? {};
                const explicitOwnership = node.profileOverrides !== undefined;
                const profileConfig = pinnedProfile?.config;
                const overrides: Record<string, boolean> = {};
                for (const key of Object.keys(next)) {
                  if (previousOverrides[key] === true) {
                    overrides[key] = true;
                    continue;
                  }
                  if (!profileConfig || !Object.prototype.hasOwnProperty.call(profileConfig, key)) {
                    continue;
                  }
                  const changedFromProfile =
                    JSON.stringify(next[key]) !== JSON.stringify(profileConfig[key]);
                  if (!explicitOwnership || changedFromProfile) overrides[key] = true;
                }
                onNodePatch({ config: next, profileOverrides: overrides });
              }}
            />
            <InspectorPortsSection def={def} outType={outType} />
          </div>
        )}

        <IssueCenter issues={nodeIssues} onFix={focusIssue} />
      </div>
    </aside>
  );
}
