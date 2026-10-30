import { useEffect, useMemo, useRef, useState } from 'react';

export interface Command {
  id: string;
  label: string;
  hint?: string;
  group: string;
  run: () => void;
}

interface Props {
  open: boolean;
  commands: Command[];
  onClose: () => void;
}

export function CommandPalette({ open, commands, onClose }: Props) {
  const [query, setQuery] = useState('');
  const [active, setActive] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLDivElement>(null);

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase();
    if (!needle) return commands;
    return commands.filter((command) =>
      `${command.label} ${command.group} ${command.hint ?? ''}`.toLowerCase().includes(needle),
    );
  }, [commands, query]);

  const groups = useMemo(() => {
    const map = new Map<string, Command[]>();
    for (const command of filtered) {
      const list = map.get(command.group) ?? [];
      list.push(command);
      map.set(command.group, list);
    }
    return [...map.entries()];
  }, [filtered]);

  useEffect(() => {
    if (!open) return;
    setQuery('');
    setActive(0);
    const frame = requestAnimationFrame(() => inputRef.current?.focus());
    return () => cancelAnimationFrame(frame);
  }, [open]);

  useEffect(() => {
    setActive((index) => Math.min(index, Math.max(0, filtered.length - 1)));
  }, [filtered.length]);

  useEffect(() => {
    if (!open) return;
    const element = listRef.current?.querySelector<HTMLElement>(`[data-index="${active}"]`);
    element?.scrollIntoView({ block: 'nearest' });
  }, [active, open]);

  if (!open) return null;

  const run = (command: Command) => {
    onClose();
    command.run();
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.key === 'Escape') {
      event.preventDefault();
      onClose();
    } else if (event.key === 'ArrowDown') {
      event.preventDefault();
      setActive((index) => (filtered.length === 0 ? 0 : (index + 1) % filtered.length));
    } else if (event.key === 'ArrowUp') {
      event.preventDefault();
      setActive((index) => (filtered.length === 0 ? 0 : (index - 1 + filtered.length) % filtered.length));
    } else if (event.key === 'Enter') {
      event.preventDefault();
      const command = filtered[active];
      if (command) run(command);
    }
  };

  let flatIndex = -1;
  return (
    <div className="overlay palette-overlay" onMouseDown={(event) => event.target === event.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-modal="true" aria-label="Command palette" onKeyDown={onKeyDown}>
        <div className="palette-input-wrap">
          <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.7">
            <circle cx="11" cy="11" r="6.5" />
            <path d="M16 16l4 4" strokeLinecap="round" />
          </svg>
          <input
            ref={inputRef}
            className="palette-input"
            value={query}
            placeholder="Search actions, nodes, and workflows"
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
          />
          <kbd>esc</kbd>
        </div>
        <div className="palette-list" ref={listRef}>
          {filtered.length === 0 && <p className="muted small pad">Nothing matches “{query}”.</p>}
          {groups.map(([group, items]) => (
            <section key={group} className="palette-group">
              <h3>{group}</h3>
              {items.map((command) => {
                flatIndex += 1;
                const index = flatIndex;
                return (
                  <button
                    key={command.id}
                    data-index={index}
                    className={`palette-row ${index === active ? 'active' : ''}`}
                    onMouseMove={() => setActive(index)}
                    onClick={() => run(command)}
                  >
                    <span>{command.label}</span>
                    {command.hint && <kbd>{command.hint}</kbd>}
                  </button>
                );
              })}
            </section>
          ))}
        </div>
        <footer className="palette-foot">
          <span>↑↓ navigate</span>
          <span>↵ open</span>
          <span>⌘K toggle</span>
        </footer>
      </div>
    </div>
  );
}
