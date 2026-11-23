import { OriginBadge } from './assets/OriginBadge';
import { countFields } from '../schemaFields';
import { effectiveSchemaRefs } from '../effectiveNode';
import type { NodeEffective } from '../effectiveNode';
import type { DataSchema, Mapping, WorkflowNode } from '../types';
import type { ConfigProfile } from '../types';

interface Props {
  node: WorkflowNode;
  schemas: DataSchema[];
  mappings: Mapping[];
  calleeContract: DataSchema | null;
  profile: ConfigProfile | null;
  effective: NodeEffective | null;
  onNodePatch: (patch: Partial<WorkflowNode>) => void;
}

// The "Data contract" inspector section (v2): triggers pick an input
// contract, every other node an output contract; subflows also show the
// callee's declared input shape and data.map previews the referenced
// mapping's rows. Extracted verbatim from Inspector's contracts IIFE
// (Profile-Centric UX Phase 3 item 3.2).
export function InspectorDataSection({
  node,
  schemas,
  mappings,
  calleeContract,
  profile,
  effective,
  onNodePatch,
}: Props) {
  const isInput = node.type === 'trigger.webhook';
  const schemaRefs = effectiveSchemaRefs(node, profile);
  const schemaInfo = isInput ? schemaRefs.input : schemaRefs.output;
  const ref = typeof schemaInfo?.value === 'string' ? schemaInfo.value : undefined;
  const selected = schemas.find((s) => s.id === ref);
  const mappingInfo = effective?.field('mappingRef') ?? null;
  const nodeMappingRef = node.config?.mappingRef;
  const mappingRef =
    typeof mappingInfo?.value === 'string'
      ? mappingInfo.value
      : typeof nodeMappingRef === 'string'
        ? nodeMappingRef
        : undefined;
  const mapping = node.type === 'data.map' ? mappings.find((m) => m.id === mappingRef) : undefined;
  const scriptInfo = effective?.field('scriptRef') ?? null;
  return (
    <section className="inspector-section" data-testid="schema-section">
      <h3>Data contract</h3>
      <label className="field" htmlFor="node-schema-ref">
        <span className="field-label">{isInput ? 'Input contract' : 'Output contract'}</span>
        <select
          id="node-schema-ref"
          className="text-input"
          value={ref ?? ''}
          onChange={(event) => {
            const value = event.target.value || undefined;
            onNodePatch(isInput ? { inputSchemaRef: value } : { outputSchemaRef: value });
          }}
        >
          <option value="">None</option>
          {schemas.map((s) => (
            <option key={s.id} value={s.id}>
              {s.name} ({countFields(s.fields)} fields)
            </option>
          ))}
        </select>
        <span className="field-hint">
          {isInput
            ? 'Inbound payloads are validated against this contract; the verdict rides the trigger item (strict mode rejects).'
            : 'The node’s main output is checked against this contract and the result is recorded on its finished event — never a gate.'}
        </span>
        {schemaInfo && schemaInfo.origin !== 'node' && (
          <OriginBadge
            state={schemaInfo.origin === 'bundle' ? 'bundle' : 'inherited'}
            fieldKey={isInput ? 'inputSchemaRef' : 'outputSchemaRef'}
          />
        )}
      </label>
      {selected && (
        <div className="profile-preview" data-testid="schema-preview">
          <p className="field-label">
            {selected.name} · v{selected.version}
          </p>
          {selected.fields.map((field) => (
            <div className="profile-preview-row" key={field.name}>
              <code>{field.name}</code>
              <span>
                {field.type}
                {field.type === 'array' && field.items ? ` of ${field.items.type}` : ''}
              </span>
              <em className={field.required ? 'from-profile' : 'from-node'}>
                {field.required ? 'required' : 'optional'}
              </em>
            </div>
          ))}
        </div>
      )}
      {node.type === 'flow.subflow' &&
        (calleeContract ? (
          <div className="profile-preview" data-testid="callee-contract">
            <p className="field-label">Callee expects ({calleeContract.name})</p>
            {calleeContract.fields.map((field) => (
              <div className="profile-preview-row" key={field.name}>
                <code>{field.name}</code>
                <span>{field.type}</span>
                <em className={field.required ? 'from-profile' : 'from-node'}>
                  {field.required ? 'required' : 'optional'}
                </em>
              </div>
            ))}
          </div>
        ) : (
          <p className="field-hint" data-testid="callee-contract-empty">
            The target workflow declares no input contract.
          </p>
        ))}
      {node.type === 'data.map' && (
        <div className="profile-preview" data-testid="mapping-preview">
          {mappingInfo && mappingInfo.origin !== 'node' && (
            <OriginBadge
              state={mappingInfo.origin === 'bundle' ? 'bundle' : 'inherited'}
              fieldKey="mappingRef"
            />
          )}
          {mapping ? (
            <>
              <p className="field-label">
                {mapping.name} · v{mapping.version}
              </p>
              {mapping.rows.map((row) => (
                <div className="profile-preview-row" key={row.target}>
                  <code>{row.target}</code>
                  <span>{row.source}</span>
                  <em className={row.required ? 'from-profile' : 'from-node'}>
                    {row.required ? 'required' : 'optional'}
                  </em>
                </div>
              ))}
              <span className="field-hint">
                Rows resolve into the node at publish; per-target overrides below win.
              </span>
            </>
          ) : (
            <span className="field-hint">Pick a mapping to preview its rows here.</span>
          )}
        </div>
      )}
      {scriptInfo && (
        <div className="profile-preview" data-testid="effective-script">
          <p className="field-label">Script</p>
          <div className="profile-preview-row">
            <code>scriptRef</code>
            <span>{JSON.stringify(scriptInfo.value)}</span>
            <OriginBadge state={scriptInfo.origin === 'bundle' ? 'bundle' : 'inherited'} />
          </div>
        </div>
      )}
    </section>
  );
}
