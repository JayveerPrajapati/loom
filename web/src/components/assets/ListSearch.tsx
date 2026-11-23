interface ListSearchProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  label?: string;
  busy?: boolean;
  count?: number;
  testId?: string;
}

/**
 * One search control for every browse list. The page owns the debounce
 * through usePagedList; this control only reports the immediate value.
 */
export function ListSearch({
  value,
  onChange,
  placeholder = 'Search',
  label = 'Search',
  busy = false,
  count,
  testId = 'list-search',
}: ListSearchProps) {
  return (
    <div className="list-search">
      <input
        className="search-input"
        type="search"
        value={value}
        placeholder={placeholder}
        aria-label={label}
        autoComplete="off"
        spellCheck={false}
        onChange={(event) => onChange(event.target.value)}
        data-testid={testId}
      />
      {value ? (
        <button
          type="button"
          className="icon-button"
          onClick={() => onChange('')}
          aria-label="Clear search"
          data-testid={`${testId}-clear`}
        >
          ×
        </button>
      ) : null}
      {typeof count === 'number' ? (
        <span className="muted small" aria-live="polite" data-testid={`${testId}-count`}>
          {busy ? 'Searching…' : `${count} ${count === 1 ? 'result' : 'results'}`}
        </span>
      ) : null}
    </div>
  );
}
