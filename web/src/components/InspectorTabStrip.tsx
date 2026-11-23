import type { InspectorTab } from '../formLayout';

const TAB_LABEL: Record<InspectorTab, string> = {
  configuration: 'Configuration',
  data: 'Data',
  runtime: 'Runtime',
  advanced: 'Advanced',
};

interface Props {
  tabs: InspectorTab[];
  activeTab: InspectorTab;
  onSelect: (tab: InspectorTab) => void;
}

// The inspector's progressive-disclosure tab strip (Profile-Centric UX
// Phase 3, D3-1): ProfileFormPage-style tablist semantics with
// automatic-activation roving focus — arrow keys move focus and select
// together, wrapping at the ends.
export function InspectorTabStrip({ tabs, activeTab, onSelect }: Props) {
  const onTabKeyDown = (event: React.KeyboardEvent, index: number) => {
    if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft') return;
    event.preventDefault();
    const next =
      event.key === 'ArrowRight' ? (index + 1) % tabs.length : (index - 1 + tabs.length) % tabs.length;
    onSelect(tabs[next]);
    document.getElementById(`inspector-tab-${tabs[next]}`)?.focus();
  };

  return (
    <div className="profile-tabs" role="tablist" aria-label="Node sections">
      {tabs.map((id, index) => (
        <button
          type="button"
          role="tab"
          key={id}
          id={`inspector-tab-${id}`}
          data-testid={`inspector-tab-${id}`}
          aria-selected={activeTab === id}
          aria-controls={`inspector-panel-${id}`}
          className={activeTab === id ? 'active' : ''}
          tabIndex={activeTab === id ? 0 : -1}
          onKeyDown={(event) => onTabKeyDown(event, index)}
          onClick={() => onSelect(id)}
        >
          {TAB_LABEL[id]}
        </button>
      ))}
    </div>
  );
}
