import { useEffect, useState } from 'react';

interface Props {
  config: Record<string, unknown> | undefined;
  onReplace: (next: Record<string, unknown>) => void;
}

// The Advanced tab's JSON escape hatch (Phase 3, D3-5): a direct editor
// over the node's OWN config object — not the profile-merged view
// (profiles apply at publish; the merged view lives on the Profile
// tab). Commit-on-blur; invalid JSON keeps the draft and shows an
// inline error. Internal keys (mappingRows, mappingRefs, …) are
// deliberately allowed — this replaces their normal-mode presence.
export function NodeRawConfigEditor({ config, onReplace }: Props) {
  const [text, setText] = useState(() => JSON.stringify(config ?? {}, null, 2));
  const [error, setError] = useState('');

  const serialized = JSON.stringify(config ?? {}, null, 2);
  useEffect(() => {
    setText(serialized);
    setError('');
  }, [serialized]);

  return (
    <section className="inspector-section" data-testid="node-raw-config-section">
      <h3>Raw node config</h3>
      <label className="field" htmlFor="node-raw-config">
        <span className="field-label">JSON</span>
        <textarea
          id="node-raw-config"
          className={`text-input code ${error ? 'invalid' : ''}`}
          rows={8}
          spellCheck={false}
          value={text}
          onChange={(event) => setText(event.target.value)}
          onBlur={() => {
            try {
              const parsed: unknown = JSON.parse(text);
              if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
                setError('Expected a JSON object.');
                return;
              }
              setError('');
              if (JSON.stringify(parsed) === serialized) return;
              onReplace(parsed as Record<string, unknown>);
            } catch {
              setError('Not valid JSON.');
            }
          }}
        />
        {error && <p className="field-error">{error}</p>}
        <p className="field-hint">
          Edits this node&rsquo;s own config directly, including internal keys such as mappingRows. Profile
          values are not merged here. Changes apply when the field loses focus.
        </p>
      </label>
    </section>
  );
}
