import { PORT_COLORS } from '../catalog';
import type { NodeDef, PortType } from '../types';

interface Props {
  def: NodeDef | undefined;
  outType: PortType;
}

// The static Ports table (input/output chips with type colors).
// Extracted verbatim from Inspector (Profile-Centric UX Phase 3 item
// 3.4); it lives on the Advanced tab — reference information, not
// day-to-day authoring.
export function InspectorPortsSection({ def, outType }: Props) {
  return (
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
  );
}
