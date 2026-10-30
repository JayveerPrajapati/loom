import { useMemo, useState } from 'react';
import { defaultNodeName } from '../catalog';
import type { NodeDef } from '../types';

const CATEGORY_LABEL: Record<string, string> = {
  trigger: 'Triggers',
  action: 'Actions',
  logic: 'Logic',
};

const CATEGORY_ORDER = ['trigger', 'action', 'logic'];

interface Props {
  defs: NodeDef[];
  onAdd: (def: NodeDef) => void;
  onDragStart: (def: NodeDef) => void;
}

export function NodePanel({ defs, onAdd, onDragStart }: Props) {
  const [query, setQuery] = useState('');
  const groups = useMemo(() => {
    const filtered = defs.filter((def) => {
      if (!query.trim()) return true;
      const haystack = `${def.type} ${def.category} ${def.description}`.toLowerCase();
      return haystack.includes(query.trim().toLowerCase());
    });
    const byCategory = new Map<string, NodeDef[]>();
    for (const def of filtered) {
      const list = byCategory.get(def.category) ?? [];
      list.push(def);
      byCategory.set(def.category, list);
    }
    return [...byCategory.entries()].sort(
      (a, b) => CATEGORY_ORDER.indexOf(a[0]) - CATEGORY_ORDER.indexOf(b[0]),
    );
  }, [defs, query]);

  return (
    <aside className="panel nodes-panel" aria-label="Node library">
      <div className="panel-head">
        <div>
          <p className="eyebrow">Library</p>
          <h2>Nodes</h2>
        </div>
        <span className="count-pill">{defs.length}</span>
      </div>
      <div className="search-wrap">
        <input
          className="search-input"
          type="search"
          value={query}
          placeholder="Search nodes"
          aria-label="Search nodes"
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>
      <div className="node-list">
        {groups.length === 0 && <p className="muted small pad">No nodes match “{query}”.</p>}
        {groups.map(([category, items]) => (
          <section key={category} className="node-group">
            <h3>{CATEGORY_LABEL[category] ?? category}</h3>
            {items.map((def) => (
              <button
                key={def.type}
                className="node-item"
                draggable
                onClick={() => onAdd(def)}
                onDragStart={(event) => {
                  event.dataTransfer.setData('application/loom-node', def.type);
                  event.dataTransfer.effectAllowed = 'move';
                  onDragStart(def);
                }}
                title={def.description}
              >
                <span className={`node-dot dot-${def.category}`} aria-hidden="true" />
                <span className="node-item-copy">
                  <span className="node-item-name">{defaultNodeName(def)}</span>
                  <span className="node-item-desc">{def.description}</span>
                </span>
                <span className="node-item-ports">
                  {(def.inputs?.length ?? 0) + (def.outputs?.length ?? 0)}
                </span>
              </button>
            ))}
          </section>
        ))}
      </div>
      <p className="panel-foot">Drag a node onto the canvas, or click to add it.</p>
    </aside>
  );
}
